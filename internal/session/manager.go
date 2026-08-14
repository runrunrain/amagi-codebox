package session

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"amagi-codebox/internal/appmeta/claude"
	"amagi-codebox/internal/launchplan"
)

// extractFirstUserMessage is the jsonl first-user-message resolver used by the
// stopped-session title backfill. It defaults to the real claude parser and is
// overridable in tests (see manager_titlecache_test.go) to observe scan
// invocations for negative-result cache coverage.
var extractFirstUserMessage = claude.ExtractFirstUserMessage

// Manager is the sole owner of session identity, origin, launch mode,
// membership and host lifecycle. Remote projections consume authority snapshots
// and never own a second existence index.
type Manager struct {
	indexMu sync.RWMutex
	entries map[string]*authorityEntry
	usedIDs map[string]struct{}

	homeMu  sync.RWMutex
	homeDir string

	reservationSeq uint64
	receiptSeqMu   sync.Mutex
	receiptSeq     uint64
	lifecycleSeqMu sync.Mutex
	lifecycleSeq   uint64

	reclaimedMu sync.Mutex
	reclaimed   map[uint64]RemoveReceipt
}

func NewManager() *Manager {
	return &Manager{
		entries:   make(map[string]*authorityEntry),
		usedIDs:   make(map[string]struct{}),
		reclaimed: make(map[uint64]RemoveReceipt),
	}
}

func (m *Manager) SetHomeDir(homeDir string) {
	if homeDir == "" {
		return
	}
	m.homeMu.Lock()
	m.homeDir = homeDir
	m.homeMu.Unlock()
}

// Create preserves the legacy immediate-create API for local callers and tests.
// Production launch paths use ReserveCreate and commit only after process and
// control activation. The resulting record is nevertheless an Authority entry,
// not a second legacy store.
func (m *Manager) Create(appType AppType, provider, preset, model string, mode LaunchMode, workDir string) *Session {
	authorityMode, err := authorityModeFromLaunchMode(mode)
	if err != nil {
		return nil
	}
	if workDir == "" {
		workDir = "."
	}
	reservation, err := m.ReserveCreate(CreateSpec{
		AppType: appType,
		Origin:  launchplan.OriginDesktop,
		Mode:    authorityMode,
		Workdir: workDir,
		// Immediate legacy creation has no composite Control/H1 activation proof;
		// it is therefore never admitted to remote projection.
		RemoteEligible: false,
		Provider:       provider,
		Preset:         preset,
		Model:          model,
	})
	if err != nil {
		return nil
	}
	entry := reservation.entry
	entry.guard.Lock()
	entry.private.revisions = SessionRevisions{Membership: 1, Lifecycle: 1, Run: 1, Activity: 1}
	entry.private.state = AuthorityRunning
	entry.private.lastActivityAt = entry.session.StartedAt
	entry.activityAt.Store(entry.session.StartedAt.UnixNano())
	entry.activityRevision.Store(1)
	entry.phase = authorityPresent
	copy := entry.session
	entry.guard.Unlock()
	reservation.used.Store(true)
	return &copy
}

func (m *Manager) SetPID(id string, pid int) {
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	entry.session.PID = pid
	entry.guard.Unlock()
}

func (m *Manager) SetTitle(id string, text string) {
	if text == "" {
		return
	}
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	if entry.phase == authorityPresent {
		entry.session.Title = text
	}
	entry.guard.Unlock()
}

func (m *Manager) SetClaudeSessionID(id string, sessionID string) {
	if sessionID == "" {
		return
	}
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	if entry.phase != authorityTombstoned {
		entry.session.ClaudeSessionID = sessionID
	}
	entry.guard.Unlock()
}

func (m *Manager) GetClaudeSessionID(id string) string {
	entry := m.entryForID(id)
	if entry == nil {
		return ""
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent {
		return ""
	}
	return entry.session.ClaudeSessionID
}

func (m *Manager) GetStatus(id string) SessionStatus {
	entry := m.entryForID(id)
	if entry == nil {
		return ""
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent {
		return ""
	}
	return entry.session.Status
}

func (m *Manager) MarkStopping(id string) {
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase == authorityPresent && entry.session.Status == StatusRunning && entry.private.pendingRemoveID == 0 && entry.private.pendingLifecycleID == 0 {
		entry.session.Status = StatusStopping
		entry.private.state = AuthorityStopping
		advanceLegacyLifecycleLocked(entry, time.Now())
	}
}

func (m *Manager) MarkStopped(id string) {
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		return
	}
	now := time.Now()
	entry.session.Status = StatusStopped
	entry.session.StoppedAt = &now
	entry.private.state = AuthorityStopped
	advanceLegacyLifecycleLocked(entry, now)
}

func (m *Manager) MarkExited(id string) {
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent || entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		return
	}
	now := time.Now()
	switch entry.session.Status {
	case StatusRunning:
		entry.session.Status = StatusExited
		entry.session.StoppedAt = &now
		entry.private.state = AuthorityExited
		advanceLegacyLifecycleLocked(entry, now)
	case StatusStopping:
		entry.session.Status = StatusStopped
		entry.session.StoppedAt = &now
		entry.private.state = AuthorityStopped
		advanceLegacyLifecycleLocked(entry, now)
	}
}

func (m *Manager) MarkFailed(id string, errMsg string) {
	entry := m.entryForID(id)
	if entry == nil {
		return
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase == authorityTombstoned || entry.private.pendingRemoveID != 0 || entry.private.pendingLifecycleID != 0 {
		return
	}
	now := time.Now()
	entry.session.Status = StatusFailed
	entry.session.StoppedAt = &now
	entry.session.ErrorMessage = errMsg
	entry.private.state = AuthorityUnavailable
	if entry.phase == authorityPresent {
		advanceLegacyLifecycleLocked(entry, now)
	}
}

func (m *Manager) Get(id string) (*Session, error) {
	entry := m.entryForID(id)
	if entry == nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	entry.guard.Lock()
	defer entry.guard.Unlock()
	if entry.phase != authorityPresent {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	copy := entry.session
	return &copy, nil
}

type legacyListSnapshot struct {
	entry              *authorityEntry
	session            Session
	membershipRevision uint64
}

// collectPresentSnapshots snapshots all presently-authoritative entries with a
// copy of their Session field and the membership revision captured under each
// entry's guard. It performs NO jsonl title backfill. Callers that only need
// counts or IDs (RunningCount, GetRunning, ClearStopped) use this to avoid the
// per-poll jsonl scan that List() performs for stopped claudecode sessions.
// Results are sorted newest-first (by StartedAt) for a deterministic order
// matching List()'s final sort.
func (m *Manager) collectPresentSnapshots() []legacyListSnapshot {
	m.indexMu.RLock()
	entries := make([]*authorityEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.indexMu.RUnlock()

	snapshots := make([]legacyListSnapshot, 0, len(entries))
	for _, entry := range entries {
		entry.guard.Lock()
		if entry.phase == authorityPresent {
			snapshots = append(snapshots, legacyListSnapshot{entry: entry, session: entry.session, membershipRevision: entry.private.revisions.Membership})
		}
		entry.guard.Unlock()
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].session.StartedAt.After(snapshots[j].session.StartedAt)
	})
	return snapshots
}

func (m *Manager) List() []SessionInfo {
	snapshots := m.collectPresentSnapshots()

	m.homeMu.RLock()
	homeDir := m.homeDir
	m.homeMu.RUnlock()
	result := make([]SessionInfo, 0, len(snapshots))
	for i := range snapshots {
		s := snapshots[i].session
		if homeDir != "" && s.Title == "" && s.ClaudeSessionID != "" && !isActiveSessionStatus(s.Status) && s.AppType == AppTypeClaudeCode {
			entry := snapshots[i].entry
			if title, ok := m.tryReadCLITitle(entry, s, homeDir); ok {
				entry.guard.Lock()
				if entry.phase == authorityPresent && entry.private.revisions.Membership == snapshots[i].membershipRevision && entry.session.Title == "" {
					entry.session.Title = title
					s.Title = title
					// 正结果：令任何陈旧的负结果指纹失效。
					entry.private.titleScanned = false
				}
				entry.guard.Unlock()
			}
		}
		info := SessionInfo{
			ID: s.ID, AppType: s.AppType, Provider: s.Provider, Preset: s.Preset,
			Model: s.Model, Mode: s.Mode, WorkDir: s.WorkDir, Status: s.Status,
			PID: s.PID, StartedAt: s.StartedAt.Format(time.RFC3339),
			Title: s.Title, ClaudeSessionID: s.ClaudeSessionID,
		}
		if isActiveSessionStatus(s.Status) {
			info.Duration = formatDuration(time.Since(s.StartedAt))
		} else if s.StoppedAt != nil {
			info.Duration = formatDuration(s.StoppedAt.Sub(s.StartedAt))
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt > result[j].StartedAt })
	return result
}

// tryReadCLITitle resolves a stopped claudecode session's title from its jsonl
// file with a per-entry negative-result cache. The desktop UI polls sessions
// (useSessionList, every 2s) and List() backfills titles for stopped
// claudecode sessions whose Title is still empty. Without a negative cache every
// poll re-scanned the jsonl to EOF whenever there was no extractable title. The
// cache stores the file's (mtime, size) fingerprint at the last negative scan;
// a subsequent poll whose os.Stat matches the fingerprint skips the rescan.
// Appending to the file (size/mtime change) invalidates the cache so a later
// title is still detected. Fingerprint reads/writes are guarded by entry.guard.
func (m *Manager) tryReadCLITitle(entry *authorityEntry, s Session, homeDir string) (string, bool) {
	jsonlPath := claude.SessionJSONLPath(homeDir, s.WorkDir, s.ClaudeSessionID)
	fi, err := os.Stat(jsonlPath)
	if err != nil {
		// 文件不存在或不可访问：无法生成指纹，下次轮询重新 stat（廉价），不缓存。
		return "", false
	}
	mtimeNs := fi.ModTime().UnixNano()
	size := fi.Size()

	entry.guard.Lock()
	cachedNegative := entry.private.titleScanned &&
		entry.private.titleScanMtime == mtimeNs &&
		entry.private.titleScanSize == size
	entry.guard.Unlock()
	if cachedNegative {
		return "", false
	}

	content, found, scanErr := extractFirstUserMessage(jsonlPath)
	if scanErr != nil {
		return "", false
	}
	if !found {
		// 负结果：记录指纹，文件未变时下次轮询跳过重扫。
		entry.guard.Lock()
		if entry.phase == authorityPresent {
			entry.private.titleScanMtime = mtimeNs
			entry.private.titleScanSize = size
			entry.private.titleScanned = true
		}
		entry.guard.Unlock()
		return "", false
	}
	return truncateFirstLine(content, titleMaxRunes, s.WorkDir), true
}

func (m *Manager) RunningCount() int {
	count := 0
	for _, snap := range m.collectPresentSnapshots() {
		if isActiveSessionStatus(snap.session.Status) {
			count++
		}
	}
	return count
}

// Remove is the legacy local removal wrapper. Authoritative embedded paths use
// PrepareRemove and CommitPreparedRemove; this wrapper still removes terminal
// legacy/external records from the same Authority index.
func (m *Manager) Remove(id string) error {
	entry := m.entryForID(id)
	if entry == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	entry.guard.Lock()
	if entry.phase != authorityPresent {
		entry.guard.Unlock()
		return fmt.Errorf("session not found: %s", id)
	}
	switch entry.session.Status {
	case StatusRunning:
		entry.guard.Unlock()
		return fmt.Errorf("cannot remove running session: %s: %w", id, ErrSessionRunning)
	case StatusStopping:
		entry.guard.Unlock()
		return fmt.Errorf("cannot remove stopping session: %s: %w", id, ErrSessionStopping)
	}
	entry.phase = authorityTombstoned
	entry.guard.Unlock()
	m.indexMu.Lock()
	if m.entries[id] == entry {
		delete(m.entries, id)
	}
	m.indexMu.Unlock()
	return nil
}

func (m *Manager) ClearStopped() int {
	// Use collectPresentSnapshots (no jsonl IO) instead of List(): this helper
	// only needs Status, and List()'s stopped-session title backfill would force
	// a per-call jsonl scan on every clear.
	snapshots := m.collectPresentSnapshots()
	ids := make([]string, 0, len(snapshots))
	for _, snap := range snapshots {
		if !isActiveSessionStatus(snap.session.Status) {
			ids = append(ids, snap.session.ID)
		}
	}
	count := 0
	for _, id := range ids {
		if m.Remove(id) == nil {
			count++
		}
	}
	return count
}

func (m *Manager) GetRunning() []string {
	snapshots := m.collectPresentSnapshots()
	result := make([]string, 0, len(snapshots))
	for _, snap := range snapshots {
		if isActiveSessionStatus(snap.session.Status) {
			result = append(result, snap.session.ID)
		}
	}
	return result
}

func advanceLegacyLifecycleLocked(entry *authorityEntry, at time.Time) {
	if entry.private.revisions.Lifecycle != ^uint64(0) {
		entry.private.revisions.Lifecycle++
	}
	if at.After(entry.private.lastActivityAt) {
		entry.private.lastActivityAt = at
	}
	incrementActivityLocked(entry)
}

func isActiveSessionStatus(status SessionStatus) bool {
	return status == StatusRunning || status == StatusStopping
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
