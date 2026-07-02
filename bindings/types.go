// Package bindings holds the protocol-agnostic seam between the relay's
// projector edge and a protocol adapter: the ProtocolAdapter interface, the
// raw-input envelopes it consumes (RawEventEnvelope, ContractDataChange), the
// gold output rows it emits (TransformOutput and friends), and the
// config-persistence inversion-of-control types (config.go).
//
// One deliberate transitional coupling: LedgerState is the seam's state
// carrier (it is named in the ProtocolAdapter signatures), but its slices are
// still Blend-shaped, so this package imports blend/contracts for the member
// types and enums. Genericizing LedgerState is deferred until a second real
// protocol needs its own state shape.
package bindings

import (
	"time"

	"github.com/daccred/lidapters/blend/contracts"
)

type RawEventEnvelope struct {
	SchemaVersion string
	MessageType   string
	Subject       string
	LedgerSeq     int64
	TxHash        string
	EventIndex    int
	ContractID    string
	Topic         string
	RawEvent      []byte
	SourceName    string
	CloseTime     time.Time
	Metadata      map[string]string
}

type LedgerState struct {
	Pools     []contracts.PoolState
	Users     []contracts.UserReservePosition
	Backstops []contracts.BackstopPosition
	// PendingUserPositions carries raw decode scratch across ledgers so the
	// decoder can stay a stateless pure reducer: anything that must survive from
	// one ledger to the next rides in this returned value rather than on the
	// decoder, which is what keeps repeated runs byte-identical. See
	// contracts.PendingUserPosition. Carried state only — never emitted to gold.
	PendingUserPositions []contracts.PendingUserPosition
	// Oracles carries each price oracle's decoded asset->index map, decimals and
	// per-index raw prices across ledgers. The oracle's instance entry (which
	// holds the asset list + decimals) is written once at deploy, not on each
	// set_price, and a price entry only appears in the ledger it changes — so
	// without carrying this, any ledger after the deploy would rebuild an empty
	// oracle and reserves would lose their price (the index map would be empty
	// and price-only ledgers would map nothing). It is the oracle analog of
	// PendingUserPositions. Carried state only — never emitted to gold.
	Oracles []contracts.OracleState
}

// ContractDataChange is the shared vocabulary between the relay's
// protocol-agnostic projector edge (which extracts these from raw ledger meta)
// and a protocol adapter's DecodeState (which folds them into typed state). It
// is the contract_data delta for one ledger entry. The silver-only hash/JSON
// debug fields the relay's prior struct carried are dropped here, since their
// only consumer (a debug writer) is gone.
type ContractDataChange struct {
	ContractID string
	KeyXDR     string  // base64 ScVal
	ValueXDR   *string // base64 ScVal; nil when removed/evicted
	Durability string
	ChangeType string // Created/Updated/Restored/Removed
	Live       bool
	// LiveUntilLedgerSeq is the TTL-liveness signal: the ledger up to which this
	// entry is live. The relay extract populates it from the close-meta TtlEntry
	// fold; DecodeState treats *LiveUntilLedgerSeq < ledgerSeq as expired. nil
	// means no TTL signal was attached. On Soroban an entry's data and its TTL
	// are separate ledger entries, so without carrying the TTL here expired state
	// would read as live forever.
	LiveUntilLedgerSeq *uint32
	LastModifiedLedger uint32
}

// ProtocolAdapter is the seam the relay's protocol-agnostic projector consumes
// and each protocol adapter implements. It folds the decode half into the older
// ID/Protocol/Transform interface so a protocol is fully self-contained: event
// decode, state decode, and transform all live in the adapter rather than being
// split between the adapter and the relay core.
type ProtocolAdapter interface {
	ID() string
	Protocol() string

	// OwnsContract reports whether a contract_data change / event for contractID
	// belongs to this protocol. It subsumes the relay router contract-match +
	// protocol classification, which happens consumer-side, inside the adapter.
	OwnsContract(contractID string) bool

	// DecodeState folds this protocol's contract_data changes into typed state.
	//
	// Decode is adapter-owned: keeping protocol decode in the adapter (rather
	// than in the relay core) is what makes each protocol self-contained — event
	// decode, state decode, and transform in one place.
	//
	// It is a stateless PURE reducer — (prior, changes, ledgerSeq) -> next, with
	// no DB/network/clock/random and no hidden accumulator retained on the
	// adapter. With no per-ledger scratch, folding the same input twice yields
	// byte-identical output: there is nothing to leak map-iteration order or
	// time.Now between calls; all carry-over threads through *LedgerState
	// (PendingUserPositions carries the one piece of raw scratch that does not
	// otherwise round-trip).
	DecodeState(prior *LedgerState, changes []ContractDataChange, ledgerSeq int64) (*LedgerState, error)

	// Transform folds events + typed state into gold. Pure; unchanged by the fold.
	Transform(input TransformInput) (*TransformOutput, error)
}

type TransformInput struct {
	LedgerSeq int64
	CloseTime time.Time
	Events    []RawEventEnvelope
	State     *LedgerState
}

type Activity struct {
	ID           string
	LedgerSeq    int64
	TxHash       string
	EventIndex   int
	ContractID   string
	Address      string
	Protocol     string
	ActivityType contracts.ActivityType
	AssetID      string
	AmountRaw    string
	ShareAmount  string
	ShareType    string
	AssetSymbol  string
	Counterparty string
	USDValue     string
	Direction    string
	Timestamp    time.Time
	Metadata     map[string]string
}

type BackstopQueueEntry struct {
	Amount   string
	UnlockAt time.Time
}

type Position struct {
	ID           string
	Address      string
	Protocol     string
	ContractID   string
	AssetID      string
	PositionType contracts.PositionType
	ShareAmount  string
	AssetAmount  string
	USDValue     string
	APY          string
	LedgerSeq    int64
	Timestamp    time.Time
	Metadata     map[string]string
	Q4WEntries   []BackstopQueueEntry
}

type PositionSummary struct {
	ID                     string
	Address                string
	Protocol               string
	HealthFactor           string
	BorrowLimitPct         string
	BorrowCapUSD           string
	DepositedUSD           string
	BorrowedUSD            string
	EffectiveCollateralUSD string
	EffectiveLiabilityUSD  string
	NetAPY                 string
	NetAPYWeightUSD        string
	LiquidationPrice       string
	LedgerSeq              int64
	Timestamp              time.Time
	Metadata               map[string]string
	StructuredMetadata     map[string]any
}

type Reserve struct {
	ID             string
	Protocol       string
	ContractID     string
	AssetID        string
	TotalSupplied  string
	TotalBorrowed  string
	Utilization    string
	SupplyAPY      string
	BorrowAPY      string
	SupplyCap      string
	BorrowCap      string
	CFactor        string
	LFactor        string
	OracleContract string
	LedgerSeq      int64
	Timestamp      time.Time
	Metadata       map[string]string
}

type Contract struct {
	ID              string
	Address         string
	Protocol        string
	ContractType    string
	Status          string
	WasmHash        string
	FirstSeenLedger int64
	LastSeenLedger  int64
	Metadata        map[string]string
}

type QuarantineEvent struct {
	ID         string
	AdapterID  string
	LedgerSeq  int64
	TxHash     string
	EventIndex int
	ContractID string
	Reason     string
	RawEvent   []byte
	Metadata   map[string]string
}

type TransformOutput struct {
	LedgerSeq  int64
	Activities []Activity
	Positions  []Position
	Summaries  []PositionSummary
	Reserves   []Reserve
	Contracts  []Contract
	Quarantine []QuarantineEvent
}
