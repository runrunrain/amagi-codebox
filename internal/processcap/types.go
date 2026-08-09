package processcap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// BackendKind identifies the process owner without importing a business package.
type BackendKind uint8

const (
	BackendPTY BackendKind = iota + 1
	BackendExternalLauncher
)

// BindingID is a process-local exact backend identity. Generation is allocated
// by one backend owner and is never reused for that owner.
type BindingID struct {
	Kind       BackendKind
	Owner      uint64
	Generation uint64
}

func (id BindingID) Validate(expected BackendKind) error {
	if id.Kind == 0 || id.Owner == 0 || id.Generation == 0 {
		return ErrInvalidBinding
	}
	if expected != 0 && id.Kind != expected {
		return fmt.Errorf("%w: backend kind mismatch", ErrInvalidBinding)
	}
	return nil
}

// RegistryKey binds a concrete capability to the authority run generation.
// Neither PID nor SessionID participates in lookup.
type RegistryKey struct {
	BindingID     BindingID
	RunGeneration uint64
}

func NewRegistryKey(id BindingID, runGeneration uint64) (RegistryKey, error) {
	if err := id.Validate(0); err != nil || runGeneration == 0 {
		return RegistryKey{}, ErrInvalidRegistryKey
	}
	return RegistryKey{BindingID: id, RunGeneration: runGeneration}, nil
}

// CloseWaiter retains exact close completion without exposing a backend handle.
type CloseWaiter interface {
	Wait(context.Context) error
	Confirmed() bool
}

// CloseDisposition classifies exact close evidence.
type CloseDisposition uint8

const (
	CloseConfirmed CloseDisposition = iota + 1
	CloseAlreadyAbsent
	CloseIndeterminate
)

// ExactCloseEvidence is immutable process-local close evidence. Its fields are
// private so it cannot accidentally become a wire or persistence shape.
type ExactCloseEvidence struct {
	bindingID   BindingID
	receiptID   uint64
	disposition CloseDisposition
	waiter      CloseWaiter
}

func NewExactCloseEvidence(binding BindingID, receiptID uint64, disposition CloseDisposition, waiter CloseWaiter) (ExactCloseEvidence, error) {
	if err := binding.Validate(0); err != nil || receiptID == 0 {
		return ExactCloseEvidence{}, ErrInvalidCloseEvidence
	}
	switch disposition {
	case CloseConfirmed, CloseAlreadyAbsent:
		if waiter != nil && !waiter.Confirmed() {
			return ExactCloseEvidence{}, ErrInvalidCloseEvidence
		}
	case CloseIndeterminate:
		if waiter == nil {
			return ExactCloseEvidence{}, ErrInvalidCloseEvidence
		}
	default:
		return ExactCloseEvidence{}, ErrInvalidCloseEvidence
	}
	return ExactCloseEvidence{bindingID: binding, receiptID: receiptID, disposition: disposition, waiter: waiter}, nil
}

func (e ExactCloseEvidence) BindingID() BindingID          { return e.bindingID }
func (e ExactCloseEvidence) ReceiptID() uint64             { return e.receiptID }
func (e ExactCloseEvidence) Disposition() CloseDisposition { return e.disposition }
func (e ExactCloseEvidence) Waiter() CloseWaiter           { return e.waiter }
func (e ExactCloseEvidence) Confirmed() bool {
	if e.disposition == CloseConfirmed || e.disposition == CloseAlreadyAbsent {
		return true
	}
	return e.waiter != nil && e.waiter.Confirmed()
}

// Binding is the concrete long-lived process capability. CloseExact must act on
// the captured backend, never by resolving a PID or SessionID again.
type Binding interface {
	BindingID() BindingID
	CloseExact(context.Context) ExactCloseEvidence
}

// StartEvidence is minted at the same point a backend enters its owner map.
type StartEvidence struct {
	PID     int
	Binding Binding
}

func (e StartEvidence) Validate(expected BackendKind) error {
	if e.PID <= 0 || e.Binding == nil {
		return ErrInvalidStartEvidence
	}
	if err := e.Binding.BindingID().Validate(expected); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStartEvidence, err)
	}
	return nil
}

// Registry is the process-lifetime owner of concrete bindings. Exact resolve is
// possible only with BindingID and the matching authority run generation.
type registeredBinding struct {
	binding   Binding
	closeOnce sync.Once
	evidence  ExactCloseEvidence
}

type Registry struct {
	mu       sync.RWMutex
	bindings map[RegistryKey]*registeredBinding
}

func NewRegistry() *Registry {
	return &Registry{bindings: make(map[RegistryKey]*registeredBinding)}
}

func (r *Registry) Register(binding Binding, runGeneration uint64) (RegistryKey, error) {
	if r == nil || binding == nil {
		return RegistryKey{}, ErrInvalidRegistryKey
	}
	key, err := NewRegistryKey(binding.BindingID(), runGeneration)
	if err != nil {
		return RegistryKey{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bindings[key]; exists {
		return RegistryKey{}, ErrBindingAlreadyRegistered
	}
	r.bindings[key] = &registeredBinding{binding: binding}
	return key, nil
}

func (r *Registry) ResolveExact(id BindingID, runGeneration uint64) (Binding, bool) {
	if r == nil {
		return nil, false
	}
	key, err := NewRegistryKey(id, runGeneration)
	if err != nil {
		return nil, false
	}
	r.mu.RLock()
	registered := r.bindings[key]
	r.mu.RUnlock()
	if registered == nil {
		return nil, false
	}
	return registered.binding, true
}

// ReleaseExact drops registry ownership only when the exact key and concrete
// binding still match. It is idempotent for an already released key.
func (r *Registry) ReleaseExact(id BindingID, runGeneration uint64, binding Binding) error {
	if r == nil {
		return ErrInvalidRegistryKey
	}
	key, err := NewRegistryKey(id, runGeneration)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.bindings[key]
	if !exists {
		return nil
	}
	if binding != nil && current.binding != binding {
		return ErrBindingMismatch
	}
	delete(r.bindings, key)
	return nil
}

// CloseExact invokes the concrete capability at most once for this exact
// BindingID+run generation and retains its evidence/Waiter until ReleaseExact.
func (r *Registry) CloseExact(ctx context.Context, id BindingID, runGeneration uint64) (ExactCloseEvidence, bool) {
	if r == nil {
		return ExactCloseEvidence{}, false
	}
	key, err := NewRegistryKey(id, runGeneration)
	if err != nil {
		return ExactCloseEvidence{}, false
	}
	r.mu.RLock()
	registered := r.bindings[key]
	r.mu.RUnlock()
	if registered == nil {
		return ExactCloseEvidence{}, false
	}
	registered.closeOnce.Do(func() {
		registered.evidence = registered.binding.CloseExact(ctx)
	})
	return registered.evidence, registered.evidence.ReceiptID() != 0
}

func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.bindings)
}

// NewOwnerID returns process-random non-zero owner entropy. Callers must fail
// process start if entropy cannot be obtained.
func NewOwnerID() (uint64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	owner := binary.LittleEndian.Uint64(buf[:])
	if owner == 0 {
		return 0, errors.New("processcap: zero owner identity")
	}
	return owner, nil
}

var (
	ErrInvalidBinding           = errors.New("processcap: invalid binding")
	ErrInvalidRegistryKey       = errors.New("processcap: invalid registry key")
	ErrInvalidStartEvidence     = errors.New("processcap: invalid start evidence")
	ErrInvalidCloseEvidence     = errors.New("processcap: invalid close evidence")
	ErrBindingAlreadyRegistered = errors.New("processcap: binding already registered")
	ErrBindingMismatch          = errors.New("processcap: binding mismatch")
)
