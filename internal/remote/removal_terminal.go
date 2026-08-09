package remote

import (
	"errors"
	"time"

	"amagi-codebox/internal/remote/contract"
)

const removalNormalCloseCode = 1000

// PreparedRemovalTerminal owns already-validated payloads. Post-commit queue
// admission stores this concrete pointer; it performs no encoding.
type PreparedRemovalTerminal struct {
	bootstrap    *attachmentBootstrap
	basePayloads [][]byte
	materialized [3][]byte
	count        uint8
	closeCode    int
}

func (p *PreparedRemovalTerminal) PayloadsForWriter() [][]byte {
	if p == nil {
		return nil
	}
	count := 0
	if bootstrap := p.bootstrap.PendingPayload(); len(bootstrap) > 0 {
		p.materialized[count] = bootstrap
		count++
	}
	for _, payload := range p.basePayloads {
		if len(payload) == 0 || count == len(p.materialized) {
			continue
		}
		p.materialized[count] = payload
		count++
	}
	p.count = uint8(count)
	return p.materialized[:count]
}

type RemovalTerminalPort interface {
	AdmitRemovalTerminal(*PreparedRemovalTerminal) bool
}

type preparedRemovalDelivery struct {
	viewer     contract.DeviceID
	lease      *ControlConnectionLease
	generation uint64
	terminal   RemovalTerminalPort
	item       *PreparedRemovalTerminal
}

func (h *SessionEventHub) PrepareRemovalDeliveries(sessionID contract.SessionID, controlForViewer func(contract.DeviceID) (contract.ControlSnapshot, error), at time.Time) ([]preparedRemovalDelivery, error) {
	if h == nil {
		return nil, nil
	}
	h.mu.Lock()
	type candidate struct {
		viewer    contract.DeviceID
		lease     *ControlConnectionLease
		consumer  ControlEventConsumer
		bootstrap *attachmentBootstrap
	}
	candidates := make([]candidate, 0)
	for subscriber := range h.subscribers {
		if subscriber.sessionID != sessionID || !subscriber.active.Load() || subscriber.IsFenced() || (subscriber.lease != nil && !subscriber.lease.IsLive()) {
			continue
		}
		candidates = append(candidates, candidate{viewer: subscriber.viewerDeviceID, lease: subscriber.lease, consumer: subscriber.consumer, bootstrap: subscriber.bootstrap})
	}
	h.mu.Unlock()

	if at.IsZero() {
		at = time.Now()
	}
	occurredAt := formatUTC(at)
	deliveries := make([]preparedRemovalDelivery, 0, len(candidates))
	for _, candidate := range candidates {
		terminal, ok := candidate.consumer.(RemovalTerminalPort)
		if !ok || terminal == nil {
			return nil, errors.New("remote: active attachment lacks removal terminal capability")
		}
		payloads := make([][]byte, 0, 2)
		if controlForViewer != nil {
			controlSnapshot, err := controlForViewer(candidate.viewer)
			if err != nil {
				return nil, err
			}
			if controlSnapshot.State != contract.ControlStateNone {
				controlNone, err := contract.MarshalServerEvent(contract.ControlStateEvent{
					Type: contract.ServerEventTypeControlState, SessionID: sessionID,
					State: contract.ControlStateNone, Reason: string(reasonSessionRemoved), OccurredAt: occurredAt,
				})
				if err != nil {
					return nil, err
				}
				payloads = append(payloads, controlNone)
			}
		}
		removed, err := contract.MarshalServerEvent(contract.SessionStateEvent{
			Type: contract.ServerEventTypeSessionState, SessionID: sessionID,
			State: contract.SessionStateRemoved, OccurredAt: occurredAt,
		})
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, removed)
		generation := uint64(0)
		if candidate.lease != nil {
			generation = candidate.lease.AttachmentGeneration()
		}
		deliveries = append(deliveries, preparedRemovalDelivery{
			viewer: candidate.viewer, lease: candidate.lease, generation: generation,
			terminal: terminal, item: &PreparedRemovalTerminal{bootstrap: candidate.bootstrap, basePayloads: payloads, closeCode: removalNormalCloseCode},
		})
	}
	return deliveries, nil
}

func admitRemovalDeliveries(deliveries []preparedRemovalDelivery) bool {
	allAdmitted := true
	for _, delivery := range deliveries {
		if delivery.lease != nil && delivery.lease.AttachmentGeneration() != delivery.generation {
			allAdmitted = false
			continue
		}
		if !delivery.terminal.AdmitRemovalTerminal(delivery.item) {
			allAdmitted = false
		}
	}
	return allAdmitted
}
