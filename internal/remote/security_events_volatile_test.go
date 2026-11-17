package remote

// security_events_volatile_test.go — 仅测试消费的构造口与内存替身收纳
// （原散落在 security_events.go / server_security.go / session_journal.go
// 产品文件中，code-sweep 第二轮移入 _test 编译集）：
//   - VolatileSecurityEventSink 簇（M1-A 有界内存事件汇；M1-B 生产用 durable）
//   - newSecurityOptions（确定性测试依赖的私有构造器）
//   - noopSessionOperationJournal 簇（无需持久化的内存日志）

import (
	"amagi-codebox/internal/remote/contract"
	"context"
	"sync"
	"time"
)

const (
	// MaxVolatileSecurityEvents bounds the number of accepted events.
	MaxVolatileSecurityEvents = 256
	// MaxVolatileSecurityEventBytes bounds the aggregate accepted payload.
	MaxVolatileSecurityEventBytes = 256 << 10
)

// acceptedVolatileEvent is one accepted buffer entry. It stores the canonical
// bytes (for integrity re-check) and the accept time.
type acceptedVolatileEvent struct {
	id         SecurityEventID
	canonical  []byte
	acceptedAt time.Time
}

// VolatileSecurityEventSink is the M1-A bounded in-memory sink (used by tests;
// M1-B production uses the durable sink).
type VolatileSecurityEventSink struct {
	mu        sync.Mutex
	events    []acceptedVolatileEvent
	byID      map[SecurityEventID]int
	usedBytes int
}

// NewVolatileSecurityEventSink returns a ready volatile sink.
func NewVolatileSecurityEventSink() *VolatileSecurityEventSink {
	return &VolatileSecurityEventSink{
		events: make([]acceptedVolatileEvent, 0, MaxVolatileSecurityEvents),
		byID:   make(map[SecurityEventID]int, MaxVolatileSecurityEvents),
	}
}

// Durability reports EventSinkVolatile.
func (s *VolatileSecurityEventSink) Durability() EventSinkDurability { return EventSinkVolatile }

// AppendSecurityEvent implements the closed append protocol.
func (s *VolatileSecurityEventSink) AppendSecurityEvent(e SecurityEvent) (EventAppendResult, error) {
	canonical, err := canonicalizeSecurityEvent(e)
	if err != nil {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	if len(canonical) > MaxSecurityEventCanonicalBytes {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureInvalid}, errPreAcceptFailed
	}
	id := e.EventIDOf()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Duplicate-before-cap: an already-accepted same-ID entry is consulted
	// BEFORE any new-entry capacity check.
	if idx, ok := s.byID[id]; ok {
		if equalCanonical(s.events[idx].canonical, canonical) {
			return EventAppendResult{State: EventDuplicateAcceptedBySink}, nil
		}
		// Same ID, different canonical payload: integrity failure.
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureIntegrity}, errIntegrityFailed
	}

	// New event: entry + byte capacity gate. No drop-oldest; on failure the map
	// is NOT touched and no payload is retained.
	if len(s.events) >= MaxVolatileSecurityEvents {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}
	if s.usedBytes+len(canonical) > MaxVolatileSecurityEventBytes {
		return EventAppendResult{State: EventPreAcceptFailed, Failure: EventFailureCapacity}, errPreAcceptFailed
	}

	cp := make([]byte, len(canonical))
	copy(cp, canonical)
	s.events = append(s.events, acceptedVolatileEvent{
		id:         id,
		canonical:  cp,
		acceptedAt: e.OccurredAtOf(),
	})
	s.byID[id] = len(s.events) - 1
	s.usedBytes += len(cp)
	return EventAppendResult{State: EventAcceptedBySink}, nil
}

// AcceptedCount returns the number of accepted entries (test/diagnostic only).
func (s *VolatileSecurityEventSink) AcceptedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// UsedBytes returns the aggregate accepted canonical bytes (test/diagnostic).
func (s *VolatileSecurityEventSink) UsedBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usedBytes
}

// newSecurityOptions is the private constructor for deterministic test deps.
func newSecurityOptions(configDir string, hostSummary HostSummaryFunc, clock Clock, random interface{ Read([]byte) (int, error) }, sink SecurityEventSink) SecurityOptions {
	if configDir == "" || hostSummary == nil || clock == nil || random == nil || sink == nil {
		panic("security: all options are required")
	}
	return SecurityOptions{configDir: configDir, hostSummary: hostSummary, clock: clock, random: random, sinkFactory: func(_ *securityHealthRegister) SecurityEventSink {
		return sink
	}}
}

// noopSessionOperationJournal is an in-memory journal that always succeeds
// (for tests that don't need durability).
type noopSessionOperationJournal struct {
	mu      sync.Mutex
	records []SessionOperationRecord
	ready   bool
}

// NewNoopSessionOperationJournal creates an in-memory journal.
func NewNoopSessionOperationJournal(ready bool) SessionOperationJournal {
	return &noopSessionOperationJournal{ready: ready}
}

func (j *noopSessionOperationJournal) BeginIntent(ctx context.Context, intent SessionOperationIntent) (*OperationRecordPermit, error) {
	if !j.ready {
		return nil, errJournalNotReady
	}
	if intent.OperationID == "" || intent.SessionID == "" || intent.CLIType == "" || intent.Operation == "" || intent.Actor == "" {
		return nil, errJournalInvalidIntent
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, SessionOperationRecord{
		Version:     journalVersion,
		OperationID: intent.OperationID,
		SessionID:   intent.SessionID,
		CLIType:     intent.CLIType,
		Operation:   intent.Operation,
		Phase:       SessionPhaseIntent,
		Outcome:     SessionOutcomePending,
		Actor:       intent.Actor,
		OccurredAt:  nowUTCNano(),
	})
	return &OperationRecordPermit{intent: intent}, nil
}

func (j *noopSessionOperationJournal) Complete(ctx context.Context, permit *OperationRecordPermit, outcome SessionOperationOutcome, failureCode contract.ErrorCode) error {
	if permit == nil {
		return errJournalInvalidIntent
	}
	if !j.ready {
		return errJournalNotReady
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	rec := SessionOperationRecord{
		Version:     journalVersion,
		OperationID: permit.intent.OperationID,
		SessionID:   permit.intent.SessionID,
		CLIType:     permit.intent.CLIType,
		Operation:   permit.intent.Operation,
		Phase:       SessionPhaseResult,
		Outcome:     outcome,
		Actor:       permit.intent.Actor,
		OccurredAt:  nowUTCNano(),
	}
	if outcome == SessionOutcomeFailed && failureCode != "" {
		fc := failureCode
		rec.FailureCode = &fc
	}
	j.records = append(j.records, rec)
	return nil
}

func (j *noopSessionOperationJournal) ListRecent(ctx context.Context, limit uint16) ([]SessionOperationRecord, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > journalQueryMaxLimit {
		limit = journalQueryMaxLimit
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]SessionOperationRecord, len(j.records))
	copy(out, j.records)
	sortRecordsNewestFirst(out)
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (j *noopSessionOperationJournal) IsReady() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.ready
}
