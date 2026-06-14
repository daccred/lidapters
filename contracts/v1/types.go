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
