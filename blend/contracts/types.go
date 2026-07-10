// Package contracts holds the Blend-domain state and vocabulary types: the
// typed ledger-state slices the adapter's DecodeState folds contract_data into,
// and the position/activity enums Blend emits. The protocol-agnostic seam types
// (ProtocolAdapter, TransformInput/Output, RawEventEnvelope, config records)
// live in the module's bindings package.
package contracts

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

type PoolState struct {
	ContractID       string
	BackstopContract string
	OracleContract   string
	WasmHash         string
	PoolStatus       string
	BackstopTakeRate string
	Reserves         []ReserveState
	// Backstop pool-level balance: the totals from this pool's PoolBalance entry
	// in the backstop contract (total LP shares/tokens deposited against the
	// pool, and the aggregate shares currently queued for withdrawal). These ride
	// on PoolState rather than only on BackstopPosition so they round-trip via
	// prior.Pools independent of whether the pool currently has any individual
	// backstop depositors.
	BackstopSharesRaw    string
	BackstopTokensRaw    string
	BackstopQ4WSharesRaw string
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
	// Enabled is ResConfig's per-reserve "can act right now" flag — distinct from
	// the pool-level status. ReactivityRaw is the IR-curve reactivity constant
	// (u32, 7-dp), governing how fast ir_mod moves.
	Enabled       bool
	ReactivityRaw string
	// Normalized APR fractions, not percentages. Empty means unavailable.
	SupplyEmissionsAPR string
	BorrowEmissionsAPR string
	// Per-side (supply/b-token, borrow/d-token) emission config + accrual data,
	// decoded from the pool's EmisConfig/EmisData(res_token_id) storage, where
	// res_token_id = ReserveIndex*2 + (1 for supply, 0 for borrow). An empty
	// EPSRaw means no active emission config exists for that side — absence,
	// never a fabricated "0". Expiration/LastTime are raw unix-second strings.
	SupplyEmisEPSRaw        string
	SupplyEmisExpirationRaw string
	SupplyEmisIndexRaw      string
	SupplyEmisLastTimeRaw   string
	BorrowEmisEPSRaw        string
	BorrowEmisExpirationRaw string
	BorrowEmisIndexRaw      string
	BorrowEmisLastTimeRaw   string
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

// OracleState is one price oracle's carried decode state: the shared price
// decimals, the asset->index map decoded from its instance storage, and the
// latest raw price per index. It rides in LedgerState so the decoder stays a
// stateless pure reducer — see bindings.LedgerState.Oracles.
type OracleState struct {
	ContractID string
	Decimals   int32
	Assets     []OracleAssetIndex
	Prices     []OracleIndexPrice
}

// OracleAssetIndex binds one asset to its index in the oracle's asset list. The
// oracle keys each price by this index, so this map is what ties a stored price
// back to a pool reserve.
type OracleAssetIndex struct {
	AssetID string
	Index   int64
}

// OracleIndexPrice is one asset's latest raw oracle price, keyed by the asset's
// index. The raw i128 price is resolved to a reserve at build time once the
// asset list is known.
type OracleIndexPrice struct {
	Index    int64
	PriceRaw string
}

// PriceFeedState is one registered Reflector price feed's carried decode state:
// the ordered asset list from the feed's instance storage, the feed's shared
// price decimals, the newest round timestamp, and the most recent rounds' raw
// prices. Reflector writes one round per resolution period (temporary entries;
// protocol 1 keyed u128(ts_ms<<64|asset_index) with a bare i128 value, protocol
// 2 keyed u64(ts_ms) with a sparse {mask, prices} batch), so the rounds an
// aggregator's staleness window can still reach must ride in LedgerState for a
// price-less ledger to resolve anything — the feed analog of OracleState.
type PriceFeedState struct {
	ContractID  string
	Decimals    int32
	LastRoundMs int64
	Assets      []FeedAssetIndex
	Rounds      []FeedRound
}

// FeedAssetIndex binds one feed asset to its index in the feed's ordered asset
// list. AssetKey is the canonical form of the SEP-40 Asset enum:
// "stellar:<contract-id>" for Asset::Stellar, "other:<symbol>" for Asset::Other.
type FeedAssetIndex struct {
	AssetKey string
	Index    int64
}

// FeedRound is one Reflector publication round: the normalized round timestamp
// (milliseconds, on the feed's resolution grid) and the raw feed-decimal price
// per asset index present in that round.
type FeedRound struct {
	TimestampMs int64
	Prices      []FeedRoundPrice
}

// FeedRoundPrice is one asset's raw price within a round, keyed by the asset's
// index in the feed's asset list.
type FeedRoundPrice struct {
	Index    int64
	PriceRaw string
}

// OracleAggregatorState is one Blend oracle-aggregator's carried configuration,
// decoded from its instance storage (Admin/Base/BaseAssets/Assets/Oracles/
// Decimals/MaxAge — blend-capital/oracle-aggregator storage.rs). The aggregator
// itself never writes prices; at fold time its per-asset prices are synthesized
// from the registered feeds' rounds using exactly this configuration. The
// config is assembled across several admin transactions after deploy, then
// rarely touched, so it must ride in LedgerState — the same carry requirement
// as OracleState.
type OracleAggregatorState struct {
	ContractID string
	Decimals   int32
	MaxAgeS    int64
	// BaseKey is the canonical asset key of the aggregator's Base — the unit its
	// answers are quoted in ("other:USD" fiat for Fixed, "stellar:<USDC id>" for
	// YieldBlox). Carried, not converted: downstream valuations are denominated
	// exactly as the pool's own solvency math sees them.
	BaseKey string
	// BaseAssets are canonical asset keys hardcoded to price 1.0 at the
	// aggregator's decimals, with no feed lookup at all.
	BaseAssets []string
	Feeds      []AggregatorFeedRef
	Assets     []AggregatorAssetConfig
}

// AggregatorFeedRef is one entry of the aggregator's Oracles vec: the Reflector
// feed answering for the assets whose OracleIndex points here, plus the feed
// decimals and resolution the aggregator's own price math uses.
type AggregatorFeedRef struct {
	Index       int64
	ContractID  string
	Decimals    int32
	ResolutionS int64
}

// AggregatorAssetConfig maps one pool-side asset (a Stellar token contract) to
// its feed-side identity: the canonical feed asset key, the feed to ask
// (OracleIndex into Feeds), and the max_dev deviation guard (percent, 0/100 =
// disabled) the aggregator applies before serving a price.
type AggregatorAssetConfig struct {
	AssetID      string
	FeedAssetKey string
	OracleIndex  int64
	MaxDev       int64
}

// AssetMetadata is one registered token contract's decoded human-readable
// identity — a Stellar Asset Contract's AssetInfo instance entry or a SEP-41
// token's METADATA instance entry, decoded to {symbol, name, decimals}. It
// rides in LedgerState so the decoder stays a stateless pure reducer, the same
// carry requirement as OracleState: the instance is written once at deploy and
// not re-emitted on later ledgers.
type AssetMetadata struct {
	ContractID string
	Symbol     string
	Name       string
	Decimals   int32
}

// PendingUserPosition retains a Blend user's raw, not-yet-resolved positions
// ScVal so the decoder can stay a stateless pure reducer: user positions are
// keyed by reserve index in the raw blob and only resolve to assets against a
// pool's reserve map when the state is built, so the blob must survive the
// prior->next round-trip to be re-resolved when reserves change. It is the one
// piece of builder scratch the typed slices above cannot represent, so it is
// carried here explicitly rather than hidden behind an opaque handle.
type PendingUserPosition struct {
	Address        string
	PoolContractID string
	PositionsXDR   string // base64 ScVal of the user's positions map
}
