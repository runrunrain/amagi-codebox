package main

// Durable external cleanup ownership registry.
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
	externalCleanupJournalVersion      uint8 = 1
	externalCleanupJournalName               = "external-cleanup-claims.log"
	externalCleanupJournalMaxBytes           = 1 << 20
	externalCleanupJournalMaxLineBytes       = 1 << 10
	externalCleanupJournalDirPerm            = 0o700
	externalCleanupJournalFilePerm           = 0o600
)

var (
	errExternalCleanupStoreNotReady = errors.New("external cleanup store: not ready")
	errExternalCleanupInvalidRecord = errors.New("external cleanup store: invalid record")
	errExternalCleanupStoreFull     = errors.New("external cleanup store: journal exceeds 1 MiB")
)

// externalCleanupReservation is fsynced before OS Start. If PID/identity
// registration later fails, this fixed-schema authority survives host exit and
// makes the next App fail closed instead of assuming there is no live owner.
type externalCleanupReservation struct {
	Version    uint8                    `json:"version"`
	SessionID  string                   `json:"sessionId"`
	Kind       remote.SharedServiceKind `json:"kind"`
	ReservedAt string                   `json:"reservedAt"`
}

type externalCleanupRecord struct {
	Version         uint8                    `json:"version"`
	SessionID       string                   `json:"sessionId"`
	PID             int                      `json:"pid"`
	ProcessIdentity string                   `json:"processIdentity"`
	Kind            remote.SharedServiceKind `json:"kind"`
	RegisteredAt    string                   `json:"registeredAt"`
}

type externalCleanupJournalAction string

const (
	externalCleanupReserved             externalCleanupJournalAction = "reserved"
	externalCleanupReservationCompleted externalCleanupJournalAction = "reservation_completed"
	externalCleanupRegistered           externalCleanupJournalAction = "registered"
	externalCleanupCompleted            externalCleanupJournalAction = "completed"
)

type externalCleanupJournalEvent struct {
	Version     uint8                        `json:"version"`
	Action      externalCleanupJournalAction `json:"action"`
	Reservation externalCleanupReservation   `json:"reservation,omitempty"`
	Record      externalCleanupRecord        `json:"record,omitempty"`
	OccurredAt  string                       `json:"occurredAt"`
}

type externalCleanupStore interface {
	IsReady() bool
	Reserve(externalCleanupReservation) error
	Register(externalCleanupRecord) error
	CompleteReservation(externalCleanupReservation) error
	Complete(externalCleanupRecord) error
	LoadPending() ([]externalCleanupReservation, error)
	LoadActive() ([]externalCleanupRecord, error)
}

type fileExternalCleanupStore struct {
	mu           sync.Mutex
	path         string
	archive      string
	ready        bool
	active       map[string]externalCleanupRecord
	reservations map[string]externalCleanupReservation
	rename       func(string, string) error
}

func newFileExternalCleanupStore(configDir string) externalCleanupStore {
	return newFileExternalCleanupStoreWithRename(configDir, os.Rename)
}

func newFileExternalCleanupStoreWithRename(configDir string, rename func(string, string) error) externalCleanupStore {
	if rename == nil {
		rename = os.Rename
	}
	store := &fileExternalCleanupStore{
		path:         filepath.Join(configDir, externalCleanupJournalName),
		archive:      filepath.Join(configDir, externalCleanupJournalName+".1"),
		active:       make(map[string]externalCleanupRecord),
		reservations: make(map[string]externalCleanupReservation),
		rename:       rename,
	}
	store.init(configDir)
	return store
}

func (s *fileExternalCleanupStore) init(configDir string) {
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
			file, createErr := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, externalCleanupJournalFilePerm)
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
	_ = os.Chmod(s.path, externalCleanupJournalFilePerm)

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
		return nil, errExternalCleanupStoreNotReady
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > externalCleanupJournalMaxBytes {
		return nil, errExternalCleanupStoreNotReady
	}
	return data, nil
}

func validateExternalCleanupJournal(data []byte) error {
	probe := &fileExternalCleanupStore{
		active:       make(map[string]externalCleanupRecord),
		reservations: make(map[string]externalCleanupReservation),
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

func (s *fileExternalCleanupStore) replay(data []byte) error {
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		line := data[start:end]
		if len(line) > externalCleanupJournalMaxLineBytes {
			return errExternalCleanupInvalidRecord
		}
		if len(line) > 0 {
			var event externalCleanupJournalEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("%w: decode event", errExternalCleanupInvalidRecord)
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

func validateExternalCleanupEvent(event externalCleanupJournalEvent) error {
	if event.Version != externalCleanupJournalVersion || event.OccurredAt == "" {
		return errExternalCleanupInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
		return errExternalCleanupInvalidRecord
	}
	switch event.Action {
	case externalCleanupReserved, externalCleanupReservationCompleted:
		return validateExternalCleanupReservation(event.Reservation)
	case externalCleanupRegistered, externalCleanupCompleted:
		return validateExternalCleanupRecord(event.Record)
	default:
		return errExternalCleanupInvalidRecord
	}
}

func validateExternalCleanupReservation(reservation externalCleanupReservation) error {
	if reservation.Version != externalCleanupJournalVersion || reservation.SessionID == "" || len(reservation.SessionID) > 128 || reservation.ReservedAt == "" {
		return errExternalCleanupInvalidRecord
	}
	if reservation.Kind != remote.SharedServiceClaudeHeadroom && reservation.Kind != remote.SharedServiceCodexHeadroom {
		return errExternalCleanupInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, reservation.ReservedAt); err != nil {
		return errExternalCleanupInvalidRecord
	}
	return nil
}

func validateExternalCleanupRecord(record externalCleanupRecord) error {
	if record.Version != externalCleanupJournalVersion || record.SessionID == "" || len(record.SessionID) > 128 ||
		record.PID <= 0 || record.ProcessIdentity == "" || len(record.ProcessIdentity) > 256 || record.RegisteredAt == "" {
		return errExternalCleanupInvalidRecord
	}
	if record.Kind != remote.SharedServiceClaudeHeadroom && record.Kind != remote.SharedServiceCodexHeadroom {
		return errExternalCleanupInvalidRecord
	}
	if _, err := time.Parse(time.RFC3339Nano, record.RegisteredAt); err != nil {
		return errExternalCleanupInvalidRecord
	}
	return nil
}

func (s *fileExternalCleanupStore) applyEvent(event externalCleanupJournalEvent) error {
	switch event.Action {
	case externalCleanupReserved:
		if _, active := s.active[event.Reservation.SessionID]; active {
			return errExternalCleanupInvalidRecord
		}
		if current, exists := s.reservations[event.Reservation.SessionID]; exists && !sameExternalCleanupReservation(current, event.Reservation) {
			return errExternalCleanupInvalidRecord
		}
		s.reservations[event.Reservation.SessionID] = event.Reservation
	case externalCleanupReservationCompleted:
		if current, ok := s.reservations[event.Reservation.SessionID]; ok {
			if !sameExternalCleanupReservation(current, event.Reservation) {
				return errExternalCleanupInvalidRecord
			}
			delete(s.reservations, event.Reservation.SessionID)
		}
	case externalCleanupRegistered:
		if reservation, ok := s.reservations[event.Record.SessionID]; ok {
			if reservation.Kind != event.Record.Kind {
				return errExternalCleanupInvalidRecord
			}
			delete(s.reservations, event.Record.SessionID)
		}
		// No reservation is accepted only for backward-compatible replay of
		// pre-Round9 registered records. A conflicting active identity is corrupt.
		if current, exists := s.active[event.Record.SessionID]; exists && !sameExternalCleanupProcess(current, event.Record) {
			return errExternalCleanupInvalidRecord
		}
		s.active[event.Record.SessionID] = event.Record
	case externalCleanupCompleted:
		if current, ok := s.active[event.Record.SessionID]; ok {
			if !sameExternalCleanupProcess(current, event.Record) {
				return errExternalCleanupInvalidRecord
			}
			delete(s.active, event.Record.SessionID)
		}
	}
	return nil
}

func sameExternalCleanupReservation(a, b externalCleanupReservation) bool {
	return a.SessionID == b.SessionID && a.Kind == b.Kind && a.ReservedAt == b.ReservedAt
}

func sameExternalCleanupProcess(a, b externalCleanupRecord) bool {
	return a.SessionID == b.SessionID && a.PID == b.PID && a.ProcessIdentity == b.ProcessIdentity && a.Kind == b.Kind
}

func (s *fileExternalCleanupStore) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *fileExternalCleanupStore) Reserve(reservation externalCleanupReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return errExternalCleanupStoreNotReady
	}
	if reservation.Version == 0 {
		reservation.Version = externalCleanupJournalVersion
	}
	if reservation.ReservedAt == "" {
		reservation.ReservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateExternalCleanupReservation(reservation); err != nil {
		return err
	}
	if _, exists := s.active[reservation.SessionID]; exists {
		return errExternalCleanupInvalidRecord
	}
	if current, exists := s.reservations[reservation.SessionID]; exists {
		if sameExternalCleanupReservation(current, reservation) {
			return nil
		}
		return errExternalCleanupInvalidRecord
	}
	event := externalCleanupJournalEvent{
		Version:     externalCleanupJournalVersion,
		Action:      externalCleanupReserved,
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

func (s *fileExternalCleanupStore) Register(record externalCleanupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return errExternalCleanupStoreNotReady
	}
	if record.Version == 0 {
		record.Version = externalCleanupJournalVersion
	}
	if record.RegisteredAt == "" {
		record.RegisteredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := validateExternalCleanupRecord(record); err != nil {
		return err
	}
	reservation, ok := s.reservations[record.SessionID]
	if !ok || reservation.Kind != record.Kind {
		return errExternalCleanupInvalidRecord
	}
	event := externalCleanupJournalEvent{
		Version:    externalCleanupJournalVersion,
		Action:     externalCleanupRegistered,
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

func (s *fileExternalCleanupStore) CompleteReservation(reservation externalCleanupReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return errExternalCleanupStoreNotReady
	}
	current, ok := s.reservations[reservation.SessionID]
	if !ok {
		return nil
	}
	if !sameExternalCleanupReservation(current, reservation) {
		return errExternalCleanupInvalidRecord
	}
	event := externalCleanupJournalEvent{
		Version:     externalCleanupJournalVersion,
		Action:      externalCleanupReservationCompleted,
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

func (s *fileExternalCleanupStore) Complete(record externalCleanupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return errExternalCleanupStoreNotReady
	}
	current, ok := s.active[record.SessionID]
	if !ok {
		return nil
	}
	if !sameExternalCleanupProcess(current, record) {
		return errExternalCleanupInvalidRecord
	}
	event := externalCleanupJournalEvent{
		Version:    externalCleanupJournalVersion,
		Action:     externalCleanupCompleted,
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

func (s *fileExternalCleanupStore) LoadPending() ([]externalCleanupReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, errExternalCleanupStoreNotReady
	}
	out := make([]externalCleanupReservation, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		out = append(out, reservation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *fileExternalCleanupStore) LoadActive() ([]externalCleanupRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return nil, errExternalCleanupStoreNotReady
	}
	out := make([]externalCleanupRecord, 0, len(s.active))
	for _, record := range s.active {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *fileExternalCleanupStore) appendLocked(event externalCleanupJournalEvent) error {
	line, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("external cleanup store: marshal: %w", err)
	}
	if len(line) > externalCleanupJournalMaxLineBytes {
		return errExternalCleanupInvalidRecord
	}
	line = append(line, '\n')
	info, err := os.Lstat(s.path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		s.ready = false
		return errExternalCleanupStoreNotReady
	}
	if info.Size() > externalCleanupJournalMaxBytes {
		s.ready = false
		return errExternalCleanupStoreFull
	}
	if info.Size()+int64(len(line)) > externalCleanupJournalMaxBytes {
		if err := s.compactLocked(); err != nil {
			s.ready = false
			return err
		}
		info, err = os.Lstat(s.path)
		if err != nil || info.Size()+int64(len(line)) > externalCleanupJournalMaxBytes {
			s.ready = false
			return errExternalCleanupStoreFull
		}
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, externalCleanupJournalFilePerm)
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
func (s *fileExternalCleanupStore) compactLocked() error {
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
	if err := temp.Chmod(externalCleanupJournalFilePerm); err != nil {
		return err
	}

	reservations := make([]externalCleanupReservation, 0, len(s.reservations))
	for _, reservation := range s.reservations {
		reservations = append(reservations, reservation)
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].SessionID < reservations[j].SessionID })
	records := make([]externalCleanupRecord, 0, len(s.active))
	for _, record := range s.active {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID < records[j].SessionID })
	total := 0
	appendEvent := func(event externalCleanupJournalEvent) error {
		line, err := json.Marshal(event)
		if err != nil || len(line) > externalCleanupJournalMaxLineBytes {
			return errExternalCleanupInvalidRecord
		}
		line = append(line, '\n')
		total += len(line)
		if total > externalCleanupJournalMaxBytes {
			return errExternalCleanupStoreFull
		}
		_, err = temp.Write(line)
		return err
	}
	for _, reservation := range reservations {
		if err := appendEvent(externalCleanupJournalEvent{
			Version:     externalCleanupJournalVersion,
			Action:      externalCleanupReserved,
			Reservation: reservation,
			OccurredAt:  time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return err
		}
	}
	for _, record := range records {
		if err := appendEvent(externalCleanupJournalEvent{
			Version:    externalCleanupJournalVersion,
			Action:     externalCleanupRegistered,
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
