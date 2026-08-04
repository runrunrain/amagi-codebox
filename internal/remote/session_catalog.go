package remote

// session_catalog.go — M2 SessionCatalog: tracks public sessions for REST
// list/detail projection (design §5.3).
//
// The catalog holds public (visible to remote) sessions. Staging sessions
// (before ActivateRun) are NOT in the public index. Removed sessions are
// tombstoned (404 on detail). The catalog provides:
//   - list: sorted snapshot of public, non-removed sessions (§5.3);
//   - detail: single session projection;
//   - activation: staging → public transition;
//   - removal: tombstone.
//
// The catalog does NOT do authorization (that's the gate) and does NOT hold
// output/history (that's the stream store). It only holds the safe metadata
// needed for list/detail: id, title, cliType, canonical workdir, startedAt,
// lastActivityAt, and a reference to the session's control entry + stream.

import (
	"sort"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
)

// ---------------------------------------------------------------------------
// CatalogEntry — one public session's metadata
// ---------------------------------------------------------------------------

// catalogEntry is the per-session catalog record. It holds only safe metadata
// (no secrets, no output, no provider/model/key/path-beyond-workdir).
type catalogEntry struct {
	id             contract.SessionID
	title          string
	cliType        contract.CLIType
	workdir        string             // canonicalized host path
	recipe         RemoteLaunchRecipe // stable restart recipe (no secret/env) — M-004
	startedAt      time.Time
	lastActivityAt time.Time
	removed        bool
}

// SessionRecipe is the immutable frozen launch recipe (design §4.4, §5.4). It
// holds stable refs only (cliType + canonical workdir); no secret/env.
type SessionRecipe struct {
	CLIType contract.CLIType
	Workdir string // canonicalized host path
}

// ---------------------------------------------------------------------------
// SessionCatalog — public session index
// ---------------------------------------------------------------------------

// SessionCatalog tracks public sessions for REST list/detail (design §5.3).
// It is process-memory only. The catalog is independent of the control gate
// and the stream store; the adapter composes projections from all three.
type SessionCatalog struct {
	mu      sync.Mutex
	entries map[contract.SessionID]*catalogEntry
}

// NewSessionCatalog creates an empty catalog.
func NewSessionCatalog() *SessionCatalog {
	return &SessionCatalog{
		entries: make(map[contract.SessionID]*catalogEntry),
	}
}

// Activate publishes a session to the public index (design §4.4 ActivateRun).
// If the session already exists (restart), only lastActivityAt is updated.
// startedAt is preserved across restarts (design §5.3).
func (c *SessionCatalog) Activate(
	id contract.SessionID,
	title string,
	cliType contract.CLIType,
	workdir string,
	at time.Time,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.entries[id]
	if existing != nil {
		// Restart: preserve startedAt, update lastActivityAt.
		existing.lastActivityAt = at
		existing.removed = false
		// title/workdir/cliType may change on restart (new recipe).
		if title != "" {
			existing.title = title
		}
		if workdir != "" {
			existing.workdir = workdir
		}
		existing.cliType = cliType
		return
	}
	c.entries[id] = &catalogEntry{
		id:             id,
		title:          title,
		cliType:        cliType,
		workdir:        workdir,
		startedAt:      at,
		lastActivityAt: at,
	}
}

// StoreRecipe records the stable restart recipe for a public session (M-004).
// It is a no-op if the session is not public (must follow Activate). The recipe
// carries only stable refs (no secret/env).
func (c *SessionCatalog) StoreRecipe(id contract.SessionID, recipe RemoteLaunchRecipe) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil && !e.removed {
		e.recipe = recipe
	}
}

// Recipe returns the stored restart recipe for a session (M-004). Returns
// (zero, false) if the session is not public.
func (c *SessionCatalog) Recipe(id contract.SessionID) (RemoteLaunchRecipe, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e == nil || e.removed {
		return RemoteLaunchRecipe{}, false
	}
	return e.recipe, true
}

// TouchActivity updates lastActivityAt for a session (design §5.3: "monotonic
// update on activation, accepted input, output, stop/restart/exit").
func (c *SessionCatalog) TouchActivity(id contract.SessionID, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e == nil || e.removed {
		return
	}
	if at.After(e.lastActivityAt) {
		e.lastActivityAt = at
	}
}

// Remove tombstones a session (design §5.3: GET never returns removed).
func (c *SessionCatalog) Remove(id contract.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[id]; e != nil {
		e.removed = true
	}
}

// PhysicallyDelete removes the entry entirely (used by shutdown cleanup).
func (c *SessionCatalog) PhysicallyDelete(id contract.SessionID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// IsPublic reports whether a session is in the public index and not removed.
func (c *SessionCatalog) IsPublic(id contract.SessionID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	return e != nil && !e.removed
}

// Entry returns a copy of the catalog entry for a session, or (zero, false) if
// not found or removed.
func (c *SessionCatalog) Entry(id contract.SessionID) (catalogEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[id]
	if e == nil || e.removed {
		return catalogEntry{}, false
	}
	return *e, true
}

// ListEntries returns a sorted snapshot of all public, non-removed sessions
// (design §5.3: lastActivityAt desc, id asc tiebreak).
func (c *SessionCatalog) ListEntries() []catalogEntry {
	c.mu.Lock()
	entries := make([]catalogEntry, 0, len(c.entries))
	for _, e := range c.entries {
		if !e.removed {
			entries = append(entries, *e)
		}
	}
	c.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].lastActivityAt.Equal(entries[j].lastActivityAt) {
			return entries[i].lastActivityAt.After(entries[j].lastActivityAt)
		}
		return entries[i].id < entries[j].id
	})
	return entries
}

// Count returns the number of public, non-removed sessions.
func (c *SessionCatalog) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if !e.removed {
			n++
		}
	}
	return n
}

// Clear drops all entries (shutdown).
func (c *SessionCatalog) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[contract.SessionID]*catalogEntry)
}

// safeTitle builds a safe display title from CLI label + workdir basename
// (design §5.3: "CLI label + workdir basename, may deduplicate").
func safeTitle(cliType contract.CLIType, workdir string) string {
	label := cliLabel(cliType)
	base := workdirBasename(workdir)
	if base == "" {
		return label
	}
	return label + " · " + base
}

// cliLabel maps a CLI type to a human-readable label.
func cliLabel(cliType contract.CLIType) string {
	switch cliType {
	case contract.CLITypeClaudeCode:
		return "Claude Code"
	case contract.CLITypeOpenCode:
		return "OpenCode"
	case contract.CLITypeCodex:
		return "Codex"
	case contract.CLITypePi:
		return "Pi"
	default:
		return string(cliType)
	}
}

// workdirBasename extracts the last path component of a workdir.
func workdirBasename(workdir string) string {
	if workdir == "" {
		return ""
	}
	// Normalize separators.
	w := workdir
	for len(w) > 1 && (w[len(w)-1] == '/' || w[len(w)-1] == '\\') {
		w = w[:len(w)-1]
	}
	for i := len(w) - 1; i >= 0; i-- {
		if w[i] == '/' || w[i] == '\\' {
			return w[i+1:]
		}
	}
	return w
}
