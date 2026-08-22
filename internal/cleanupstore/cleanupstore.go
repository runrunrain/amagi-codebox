// Package cleanupstore implements the durable external cleanup ownership
// registry. It was mechanically extracted unchanged from the root package
// (external_cleanup_store.go); the design anchors below still hold.
//
// File: ~/.amagi-codebox/external-cleanup-claims.log
//   - directory 0700, file 0600, regular/no-symlink;
//   - append-only canonical NDJSON, fsync before ownership handoff returns;
//   - no provider, path, argv/env, credentials, or process output;
//   - pre-start reservation, then exact register/complete keyed by session +
//     PID + boot-aware OS start identity;
//   - corrupt/non-regular/oversized state is fail-closed.
//
// The registry is intentionally separate from remote session-operations.log:
// these records describe local process cleanup ownership, not a user dangerous
// operation, but follow the same durability and privacy discipline.
package cleanupstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"amagi-codebox/internal/remote"
)

const (
	// JournalVersion is the canonical on-disk schema version of every journal
	// event, reservation, and record.
	JournalVersion uint8 = 1
	// JournalName is the journal file name inside the config directory; the
	// ".1" suffix names its rotation archive.
	JournalName = "external-cleanup-claims.log"
	// JournalFilePerm is the required regular-file mode of the journal.
	JournalFilePerm = 0o600

	externalCleanupJournalMaxBytes     = 1 << 20
	externalCleanupJournalMaxLineBytes = 1 << 10
	externalCleanupJournalDirPerm      = 0o700
)

var (
	// ErrStoreNotReady reports an uninitialized store or indeterminate
	// journal/rotation state; callers must treat it as fail-closed.
	ErrStoreNotReady = errors.New("external cleanup store: not ready")
	// ErrInvalidRecord reports a record/event that violates the canonical
	// journal schema or conflicts with replayed state.
	ErrInvalidRecord = errors.New("external cleanup store: invalid record")
	// ErrStoreFull reports the journal exceeding its 1 MiB bound even after
	// compaction; the store latches not-ready.
	ErrStoreFull = errors.New("external cleanup store: journal exceeds 1 MiB")
)

// Reservation is fsynced before OS Start. If PID/identity
// registration later fails, this fixed-schema authority survives host exit and
// makes the next App fail closed instead of assuming there is no live owner.
type Reservation struct {
	Version    uint8                    `json:"version"`
	SessionID  string                   `json:"sessionId"`
	Kind       remote.SharedServiceKind `json:"kind"`
	ReservedAt string                   `json:"reservedAt"`
}

// Record is the durable registration of an owned external process: the exact
// session + PID + boot-aware OS start identity handed off after OS Start,
// fsynced by Register and exactly retired by Complete.
type Record struct {
	Version         uint8                    `json:"version"`
	SessionID       string                   `json:"sessionId"`
	PID             int                      `json:"pid"`
	ProcessIdentity string                   `json:"processIdentity"`
	Kind            remote.SharedServiceKind `json:"kind"`
	RegisteredAt    string                   `json:"registeredAt"`
}

type journalAction string

// journalAction enumerates the canonical journal event actions.
const (
	// ActionReserved records a pre-start reservation.
	ActionReserved journalAction = "reserved"
	// ActionReservationCompleted exactly retires a reservation whose raw start
	// never happened.
	ActionReservationCompleted journalAction = "reservation_completed"
	// ActionRegistered records the exact post-start ownership upgrade.
	ActionRegistered journalAction = "registered"
	// ActionCompleted exactly retires an active record at terminal observation.
	ActionCompleted journalAction = "completed"
)

// JournalEvent is one canonical NDJSON line of the ownership journal; replay
// is exact-match and any inconsistency fails the whole store closed.
type JournalEvent struct {
	Version     uint8         `json:"version"`
	Action      journalAction `json:"action"`
	Reservation Reservation   `json:"reservation,omitempty"`
	Record      Record        `json:"record,omitempty"`
	OccurredAt  string        `json:"occurredAt"`
}

// Store is the durable external cleanup ownership registry consumed by the
// App: reserve-before-start, exact register/complete, and fail-closed loads.
type Store interface {
	IsReady() bool
	Reserve(Reservation) error
	Register(Record) error
	CompleteReservation(Reservation) error
	Complete(Record) error
	LoadPending() ([]Reservation, error)
	LoadActive() ([]Record, error)
}

type fileStore struct {
	mu           sync.Mutex
	path         string
	archive      string
	ready        bool
	active       map[string]Record
	reservations map[string]Reservation
	rename       func(string, string) error
}

// NewFileStore returns a file-backed Store rooted at configDir. An empty
// configDir, or any indeterminate journal/rotation state, leaves the store
// not ready; callers must treat that as fail-closed.
func NewFileStore(configDir string) Store {
	return NewFileStoreWithRename(configDir, os.Rename)
}

// NewFileStoreWithRename is NewFileStore with an injected rename used to
// exercise rotation crash windows; nil falls back to os.Rename.
func NewFileStoreWithRename(configDir string, rename func(string, string) error) Store {
	if rename == nil {
		rename = os.Rename
	}
	store := &fileStore{
		path:         filepath.Join(configDir, JournalName),
		archive:      filepath.Join(configDir, JournalName+".1"),
		active:       make(map[string]Record),
		reservations: make(map[string]Reservation),
		rename:       rename,
	}
	store.init(configDir)
	return store
}

func (s *fileStore) init(configDir string) {
	if configDir == "" {
		return
	}
	if err := os.MkdirAll(configDir, externalCleanupJournalDirPerm); err != nil {
		return
	}
	_ = os.Chmod(configDir, externalCleanupJournalDirPerm)

	mainInfo, mainErr := os.Lstat(s.path)
	if os.IsNotExist(mainErr) {
		// Missing main is a rotation authority window. Archive has three states:
		// absent permits a fresh file; valid permits exact recovery; every other
		// state is indeterminate and MUST remain untouched/not-ready.
		archiveInfo, archiveErr := os.Lstat(s.archive)
		switch {
		case os.IsNotExist(archiveErr):
			file, createErr := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, JournalFilePerm)
			if createErr != nil {
				return
			}
			if syncErr := file.Sync(); syncErr != nil {
				_ = file.Close()
				return
			}
			if closeErr := file.Close(); closeErr != nil {
				return
			}
			_ = syncExternalCleanupDirectory(configDir)
		case archiveErr != nil:
			return
		default:
			archiveData, err := readExternalCleanupJournal(s.archive, archiveInfo)
			if err != nil || validateExternalCleanupJournal(archiveData) != nil {
				return // preserve corrupt/nonregular/unreadable archive as evidence
			}
			if err := s.rename(s.archive, s.path); err != nil {
				return
			}
			_ = syncExternalCleanupDirectory(configDir)
		}
		mainInfo, mainErr = os.Lstat(s.path)
	}
	if mainErr != nil {
		return
	}
	data, err := readExternalCleanupJournal(s.path, mainInfo)
	if err != nil {
		return
	}
	_ = os.Chmod(s.path, JournalFilePerm)

	// If both files exist (crash after compacted main installation but before
	// archive removal), the main remains authoritative, but an abnormal archive
	// still makes the rotation state indeterminate. Validate without merging it.
	if archiveInfo, archiveErr := os.Lstat(s.archive); archiveErr == nil {
		archiveData, readErr := readExternalCleanupJournal(s.archive, archiveInfo)
		if readErr != nil || validateExternalCleanupJournal(archiveData) != nil {
			return
		}
	} else if !os.IsNotExist(archiveErr) {
		return
	}

	if err := s.replay(data); err != nil {
		return
	}
	s.ready = true
}

func readExternalCleanupJournal(path string, info os.FileInfo) ([]byte, error) {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > externalCleanupJournalMaxBytes {
		return nil, ErrStoreNotReady
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > externalCleanupJournalMaxBytes {
		return nil, ErrStoreNotReady
	}
	return data, nil
}

func validateExternalCleanupJournal(data []byte) error {
	probe := &fileStore{
		active:       make(map[string]Record),
		reservations: make(map[string]Reservation),
	}
	return probe.replay(data)
}

func syncExternalCleanupDirectory(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *fileStore) replay(data []byte) error {
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		line := data[start:end]
		if len(line) > externalCleanupJournalMaxLineBytes {
			return ErrInvalidRecord
		}
		if len(line) > 0 {
			var event JournalEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("%w: decode event", ErrInvalidRecord)
			}
			if err := validateExternalCleanupEvent(event); err != nil {
				return err
			}
			if err := s.applyEvent(event); err != nil {
				return err
			}
		}
		if end == len(data) {
			break
		}
		start = end + 1
	}
	return nil
}

func validateExternalCleanupEvent(event JournalEvent) error {
	if event.Version != JournalVersion || event.OccurredAt == "" {
		return ErrInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
		return ErrInvalidRecord
	}
	switch event.Action {
	case ActionReserved, ActionReservationCompleted:
		return validateExternalCleanupReservation(event.Reservation)
	case ActionRegistered, ActionCompleted:
		return validateExternalCleanupRecord(event.Record)
	default:
		return ErrInvalidRecord
	}
}

func validateExternalCleanupReservation(reservation Reservation) error {
	if reservation.Version != JournalVersion || reservation.SessionID == "" || len(reservation.SessionID) > 128 || reservation.ReservedAt == "" {
		return ErrInvalidRecord
	}
	if reservation.Kind != remote.SharedServiceClaudeHeadroom && reservation.Kind != remote.SharedServiceCodexHeadroom {
		return ErrInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, reservation.ReservedAt); err != nil {
		return ErrInvalidRecord
	}
	return nil
}

func validateExternalCleanupRecord(record Record) error {
	if record.Version != JournalVersion || record.SessionID == "" || len(record.SessionID) > 128 ||
		record.PID <= 0 || record.ProcessIdentity == "" || len(record.ProcessIdentity) > 256 || record.RegisteredAt == "" {
		return ErrInvalidRecord
	}
	if record.Kind != remote.SharedServiceClaudeHeadroom && record.Kind != remote.SharedServiceCodexHeadroom {
		return ErrInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, record.RegisteredAt); err != nil {
		return ErrInvalidRecord
	}
	return nil
}

func (s *fileStore) applyEvent(event JournalEvent) error {
	switch event.Action {
	case ActionReserved:
		if _, active := s.active[event.Reservation.SessionID]; active {
			return ErrInvalidRecord
		}
		if current, exists := s.reservations[event.Reservation.SessionID]; exists && !sameExternalCleanupReservation(current, event.Reservation) {
			return ErrInvalidRecord
		}
		s.reservations[event.Reservation.SessionID] = event.Reservation
	case ActionReservationCompleted:
		if current, ok := s.reservations[event.Reservation.SessionID]; ok {
			if !sameExternalCleanupReservation(current, event.Reservation) {
				return ErrInvalidRecord
			}
			delete(s.reservations, event.Reservation.SessionID)
		}
	case ActionRegistered:
		if reservation, ok := s.reservations[event.Record.SessionID]; ok {
			if reservation.Kind != event.Record.Kind {
				return ErrInvalidRecord
			}
			delete(s.reservations, event.Record.SessionID)
		}
		// No reservation is accepted only for backward-compatible replay of
		// pre-Round9 registered records. A conflicting active identity is corrupt.
		if current, exists := s.active[event.Record.SessionID]; exists && !sameExternalCleanupProcess(current, event.Record) {
			return ErrInvalidRecord
		}
		s.active[event.Record.SessionID] = event.Record
	case ActionCompleted:
		if current, ok := s.active[event.Record.SessionID]; ok {
			if !sameExternalCleanupProcess(current, event.Record) {
				return ErrInvalidRecord
			}
			delete(s.active, event.Record.SessionID)
		}
	}
	return nil
}

func sameExternalCleanupReservation(a, b Reservation) bool {
	return a.SessionID == b.SessionID && a.Kind == b.Kind && a.ReservedAt == b.ReservedAt
}

func sameExternalCleanupProcess(a, b Record) bool {
	return a.SessionID == b.SessionID && a.PID == b.PID && a.ProcessIdentity == b.ProcessIdentity && a.Kind == b.Kind
}

func (s *fileStore) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *fileStore) Reserve(reservation Reservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrStoreNotReady
	}
	if reservation.Version == 0 {
		reservation.Version = JournalVersion
	}
	if reservation.ReservedAt == "" {
		reservation.ReservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateExternalCleanupReservation(reservation); err != nil {
		return err
	}
	if _, exists := s.active[reservation.SessionID]; exists {
		return ErrInvalidRecord
	}
	if current, exists := s.reservations[reservation.SessionID]; exists {
		if sameExternalCleanupReservation(current, reservation) {
			return nil
		}
		return ErrInvalidRecord
	}
	event := JournalEvent{
		Version:     JournalVersion,
		Action:      ActionReserved,
		Reservation: reservation,
		OccurredAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.appendLocked(event); err != nil {
		return err
	}
	if err := s.applyEvent(event); err != nil {
		return err
	}
	return nil
}

func (s *fileStore) Register(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrStoreNotReady
	}
	if record.Version == 0 {
		record.Version = JournalVersion
	}
	if record.RegisteredAt == "" {
		record.RegisteredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateExternalCleanupRecord(record); err != nil {
		return err
	}
	reservation, ok := s.reservations[record.SessionID]
	if !ok || reservation.Kind != record.Kind {
		return ErrInvalidRecord
	}
	event := JournalEvent{
		Version:    JournalVersion,
		Action:     ActionRegistered,
		Record:     record,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.appendLocked(event); err != nil {
		return err
	}
	if err := s.applyEvent(event); err != nil {
		return err
	}
	return nil
}

func (s *fileStore) CompleteReservation(reservation Reservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrStoreNotReady
	}
	current, ok := s.reservations[reservation.SessionID]
	if !ok {
		return nil
	}
	if !sameExternalCleanupReservation(current, reservation) {
		return ErrInvalidRecord
	}
	event := JournalEvent{
		Version:     JournalVersion,
		Action:      ActionReservationCompleted,
		Reservation: current,
		OccurredAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.appendLocked(event); err != nil {
		return err
	}
	if err := s.applyEvent(event); err != nil {
		return err
	}
	return nil
}

func (s *fileStore) Complete(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrStoreNotReady
	}
	current, ok := s.active[record.SessionID]
	if !ok {
		return nil
	}
	if !sameExternalCleanupProcess(current, record) {
		return ErrInvalidRecord
	}
	event := JournalEvent{
		Version:    JournalVersion,
		Action:     ActionCompleted,
		Record:     current,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.appendLocked(event); err != nil {
		return err
	}
	if err := s.applyEvent(event); err != nil {
		return err
	}
	return nil
}

func (s *fileStore) LoadPending() ([]Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, ErrStoreNotReady
	}
	out := make([]Reservation, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		out = append(out, reservation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *fileStore) LoadActive() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, ErrStoreNotReady
	}
	out := make([]Record, 0, len(s.active))
	for _, record := range s.active {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *fileStore) appendLocked(event JournalEvent) error {
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("external cleanup store: marshal: %w", err)
	}
	if len(line) > externalCleanupJournalMaxLineBytes {
		return ErrInvalidRecord
	}
	line = append(line, '\n')
	info, err := os.Lstat(s.path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		s.ready = false
		return ErrStoreNotReady
	}
	if info.Size() > externalCleanupJournalMaxBytes {
		s.ready = false
		return ErrStoreFull
	}
	if info.Size()+int64(len(line)) > externalCleanupJournalMaxBytes {
		if err := s.compactLocked(); err != nil {
			s.ready = false
			return err
		}
		info, err = os.Lstat(s.path)
		if err != nil || info.Size()+int64(len(line)) > externalCleanupJournalMaxBytes {
			s.ready = false
			return ErrStoreFull
		}
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, JournalFilePerm)
	if err != nil {
		s.ready = false
		return fmt.Errorf("external cleanup store: open: %w", err)
	}
	if _, err := file.Write(line); err != nil {
		_ = file.Close()
		s.ready = false
		return fmt.Errorf("external cleanup store: write: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		s.ready = false
		return fmt.Errorf("external cleanup store: sync: %w", err)
	}
	if err := file.Close(); err != nil {
		s.ready = false
		return fmt.Errorf("external cleanup store: close: %w", err)
	}
	return nil
}

// compactLocked rewrites only currently active registrations. It follows the
// session-operation journal's whole-record/one-archive discipline and preserves
// a recoverable old main across the two renames (including on Windows where a
// rename cannot replace an existing destination).
func (s *fileStore) compactLocked() error {
	dir := filepath.Dir(s.path)
	temp, err := os.CreateTemp(dir, ".external-cleanup-*.tmp")
	if err != nil {
		return fmt.Errorf("external cleanup store: create compact temp: %w", err)
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(JournalFilePerm); err != nil {
		return err
	}

	reservations := make([]Reservation, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		reservations = append(reservations, reservation)
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].SessionID < reservations[j].SessionID })
	records := make([]Record, 0, len(s.active))
	for _, record := range s.active {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID < records[j].SessionID })
	total := 0
	appendEvent := func(event JournalEvent) error {
		line, err := json.Marshal(event)
		if err != nil || len(line) > externalCleanupJournalMaxLineBytes {
			return ErrInvalidRecord
		}
		line = append(line, '\n')
		total += len(line)
		if total > externalCleanupJournalMaxBytes {
			return ErrStoreFull
		}
		_, err = temp.Write(line)
		return err
	}
	for _, reservation := range reservations {
		if err := appendEvent(JournalEvent{
			Version:     JournalVersion,
			Action:      ActionReserved,
			Reservation: reservation,
			OccurredAt:  time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
	}
	for _, record := range records {
		if err := appendEvent(JournalEvent{
			Version:    JournalVersion,
			Action:     ActionRegistered,
			Record:     record,
			OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}

	_ = os.Remove(s.archive)
	if err := s.rename(s.path, s.archive); err != nil {
		return fmt.Errorf("external cleanup store: archive main: %w", err)
	}
	if err := s.rename(tempPath, s.path); err != nil {
		_ = s.rename(s.archive, s.path)
		return fmt.Errorf("external cleanup store: install compact main: %w", err)
	}
	removeTemp = false
	_ = os.Remove(s.archive)
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
