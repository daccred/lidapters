// Package aquarius is the scaffold for the Aquarius protocol adapter. It
// satisfies the bindings.ProtocolAdapter seam so the multi-protocol wiring can
// be exercised end to end, but no decode or transform logic is implemented yet:
// every fold entry point returns ErrNotImplemented.
package aquarius

import (
	"errors"

	"github.com/daccred/lidapters/bindings"
)

// ErrNotImplemented is returned by every adapter method that would need real
// Aquarius decode/transform logic.
var ErrNotImplemented = errors.New("aquarius: adapter not implemented")

var _ bindings.ProtocolAdapter = (*Adapter)(nil)

type Adapter struct{}

func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) ID() string {
	return "aquarius"
}

func (a *Adapter) Protocol() string {
	return "aquarius"
}

// OwnsContract reports false for everything: the scaffold owns no contracts
// until discovery and configuration are implemented.
func (a *Adapter) OwnsContract(contractID string) bool {
	return false
}

func (a *Adapter) DecodeState(prior *bindings.LedgerState, changes []bindings.ContractDataChange, ledgerSeq int64) (*bindings.LedgerState, error) {
	return nil, ErrNotImplemented
}

func (a *Adapter) Transform(input bindings.TransformInput) (*bindings.TransformOutput, error) {
	return nil, ErrNotImplemented
}
