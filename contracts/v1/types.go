package v1

import "time"

type PositionType string

const (
	PositionTypeSupply     PositionType = "supply"
	PositionTypeCollateral PositionType = "collateral"
	PositionTypeLiability  PositionType = "liability"
	PositionTypeBackstop   PositionType = "backstop"
)

type ActivityType string

const (
	ActivityTypeDeposit      ActivityType = "deposit"
	ActivityTypeWithdraw     ActivityType = "withdraw"
	ActivityTypeBorrow       ActivityType = "borrow"
	ActivityTypeRepay        ActivityType = "repay"
	ActivityTypeLiquidation  ActivityType = "liquidation"
	ActivityTypeClaimRewards ActivityType = "claim_rewards"
	ActivityTypeFlashLoan    ActivityType = "flash_loan"
	ActivityTypeBadDebt      ActivityType = "bad_debt"
	ActivityTypeStatusChange ActivityType = "contract_status_change"
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

type PoolState struct {
	ContractID       string
	BackstopContract string
	OracleContract   string
	WasmHash         string
	PoolStatus       string
	BackstopTakeRate string
	Reserves         []ReserveState
}

type ReserveState struct {
	ReserveIndex    int32
	AssetID         string
	AssetDecimals   int32
	BRateRaw        string
	DRateRaw        string
	BSupplyRaw      string
	DSupplyRaw      string
	PoolBalanceRaw  string
	CFactorRaw      string
	LFactorRaw      string
	UtilTargetRaw   string
	MaxUtilRaw      string
	RBaseRaw        string
	ROneRaw         string
	RTwoRaw         string
	RThreeRaw       string
	RateModifierRaw string
	SupplyCapRaw    string
	OraclePriceRaw  string
	OracleDecimals  int32
	// Normalized APR fractions, not percentages. Empty means unavailable.
	SupplyEmissionsAPR string
	BorrowEmissionsAPR string
}

type UserReservePosition struct {
	Address        string
	PoolContractID string
	AssetID        string
	PositionType   PositionType
	BTokensRaw     string
	DTokensRaw     string
}

type Q4WEntry struct {
	SharesRaw string
	UnlockAt  time.Time
}

type BackstopPosition struct {
	Address               string
	PoolContractID        string
	UserSharesRaw         string
	PoolSharesRaw         string
	PoolTokensRaw         string
	Q4W                   []Q4WEntry
	UnclaimedEmissionsRaw string
	LPTokenSupplyRaw      string
	LPBLNDReserveRaw      string
	LPUSDCReserveRaw      string
	BLNDDecimals          int32
	USDCDecimals          int32
	BLNDPriceUSD          string
	USDCPriceUSD          string
	BackstopInterestAPY   string
	BackstopEmissionsAPY  string
}

type LedgerState struct {
	Pools     []PoolState
	Users     []UserReservePosition
	Backstops []BackstopPosition
	// PendingUserPositions carries raw decode scratch across ledgers so a
	// protocol's DecodeState can stay a stateless pure reducer (D-20). See
	// PendingUserPosition. Carried state only — never emitted to gold.
	PendingUserPositions []PendingUserPosition
}

// PendingUserPosition retains a Blend user's raw, not-yet-resolved positions
// ScVal so DecodeState can stay a stateless pure reducer (D-20): user positions
// are keyed by reserve index in the raw blob and only resolve to assets against
// a pool's reserve map at build time, so the blob must survive prior->next
// round-trips to be re-resolved when reserves change. This is the one piece of
// TypedMirror builder state that does not otherwise round-trip through the typed
// slices above, so per D-20 it is represented here rather than via an opaque
// handle.
type PendingUserPosition struct {
	Address        string
	PoolContractID string
	PositionsXDR   string // base64 ScVal of the user's positions map
}

// ContractDataChange is the shared seam vocabulary between the relay's
// protocol-agnostic projector edge (which extracts these from raw ledger meta,
// per D-18) and a protocol adapter's DecodeState (which folds them into typed
// state). It is the contract_data delta for one ledger entry. Relocated here
// from the relay's internal blendstate.ContractDataChange; the silver-only
// hash/JSON debug fields are dropped (their only consumer, the retiring debug
// writer, goes away).
type ContractDataChange struct {
	ContractID string
	KeyXDR     string  // base64 ScVal
	ValueXDR   *string // base64 ScVal; nil when removed/evicted
	Durability string
	ChangeType string // Created/Updated/Restored/Removed
	Live       bool
	// LiveUntilLedgerSeq is the TTL-liveness signal (D-09): the ledger up to
	// which this entry is live. The relay extract (plan V) populates it from the
	// close-meta TtlEntry fold; DecodeState treats *LiveUntilLedgerSeq < ledgerSeq
	// as expired. nil means no TTL signal was attached. The prior relay struct
	// could not carry TTL, which is the D-09 root cause this field fixes.
	LiveUntilLedgerSeq *uint32
	LastModifiedLedger uint32
}

// ProtocolAdapter is the seam the relay's protocol-agnostic projector consumes
// and each lidapters protocol implements (D-18). It extends the prior
// ID/Protocol/Transform interface (which lived in the closed relay) with the
// decode half, so a protocol becomes self-contained: event decode, state
// decode, and transform all live in the OSS adapter, not the closed relay.
type ProtocolAdapter interface {
	ID() string
	Protocol() string

	// OwnsContract reports whether a contract_data change / event for contractID
	// belongs to this protocol. It subsumes the relay router contract-match +
	// blendstate discovery (ADR-6: classification is consumer-side).
	OwnsContract(contractID string) bool

	// DecodeState folds this protocol's contract_data changes into typed state.
	//
	// D-18: decode is adapter-owned — it is the moat boundary (protocol decode is
	// OSS adapter code, not the closed relay) and keeps each protocol
	// self-contained (event decode + state decode + transform in one place).
	//
	// D-20: it is a stateless PURE reducer — (prior, changes, ledgerSeq) -> next,
	// with no DB/network/clock/random and no hidden accumulator retained on the
	// adapter. A stateless reducer is trivially run-twice byte-identical (the I4
	// determinism gate) because there is no per-ledger scratch to leak map
	// iteration order or time.Now between calls; all carry-over threads through
	// *LedgerState (PendingUserPositions carries the one piece of raw scratch that
	// does not otherwise round-trip).
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
	ActivityType ActivityType
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
	PositionType PositionType
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
