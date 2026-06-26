package blend

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
	"github.com/shopspring/decimal"
)

type normalizedPool struct {
	rateScalar            decimal.Decimal
	rateModifierScalar    decimal.Decimal
	scalarVersion         string
	version               string
	backstopTakeRaw       decimal.Decimal
	backstopTakeAvailable bool
}

type normalizedReserve struct {
	poolContract             string
	assetID                  string
	assetDecimals            int32
	bRateRaw                 decimal.Decimal
	dRateRaw                 decimal.Decimal
	cFactorRaw               decimal.Decimal
	lFactorRaw               decimal.Decimal
	utilTargetRaw            decimal.Decimal
	maxUtilRaw               decimal.Decimal
	rBaseRaw                 decimal.Decimal
	rOneRaw                  decimal.Decimal
	rTwoRaw                  decimal.Decimal
	rThreeRaw                decimal.Decimal
	rateModifierRaw          decimal.Decimal
	usdPrice                 decimal.Decimal
	priceAvailable           bool
	totalSuppliedRaw         decimal.Decimal
	totalBorrowedRaw         decimal.Decimal
	utilizationRaw           decimal.Decimal
	borrowAPRRaw             *decimal.Decimal
	supplyAPRRaw             *decimal.Decimal
	aprPartial               bool
	supplyCapRaw             string
	borrowCapRaw             decimal.Decimal
	remainingBorrowableRaw   decimal.Decimal
	utilizationSource        string
	rateModifierNormalized   decimal.Decimal
	cFactorNormalized        decimal.Decimal
	lFactorNormalized        decimal.Decimal
	utilTargetNormalized     decimal.Decimal
	maxUtilNormalized        decimal.Decimal
	rBaseNormalized          decimal.Decimal
	rOneNormalized           decimal.Decimal
	rTwoNormalized           decimal.Decimal
	rThreeNormalized         decimal.Decimal
	borrowAPRNormalized      decimal.Decimal
	supplyAPRNormalized      decimal.Decimal
	borrowAPRNormalizedValid bool
	supplyAPRNormalizedValid bool
	raw                      contractsv1.ReserveState
}

type poolSummaryAccumulator struct {
	poolContract              string
	depositedUSD              decimal.Decimal
	borrowedUSD               decimal.Decimal
	effectiveCollateralUSD    decimal.Decimal
	effectiveLiabilityUSD     decimal.Decimal
	netAPYWeightUSD           decimal.Decimal
	netAPYNumeratorUSD        decimal.Decimal
	lFactorZeroLiability      bool
	hasLiability              bool
	hasEffectiveCollateral    bool
	pricePartial              bool
	aprPartial                bool
	netAPYPartial             bool
	liquidationCollaterals    []liquidationCollateral
	liquidationPriceScenarios map[string]string
}

type liquidationCollateral struct {
	assetID                string
	units                  decimal.Decimal
	cFactor                decimal.Decimal
	effectiveCollateralUSD decimal.Decimal
}

type protocolAccumulator struct {
	depositedUSD       decimal.Decimal
	borrowedUSD        decimal.Decimal
	netAPYWeightUSD    decimal.Decimal
	netAPYNumeratorUSD decimal.Decimal
	netAPYPartial      bool
}

type poolBreakdownEntry struct {
	DepositedUSD              string            `json:"deposited_usd"`
	BorrowedUSD               string            `json:"borrowed_usd"`
	EffectiveCollateralUSD    string            `json:"effective_collateral_usd"`
	EffectiveLiabilityUSD     string            `json:"effective_liability_usd"`
	HealthFactor              string            `json:"health_factor,omitempty"`
	BorrowLimitPct            string            `json:"borrow_limit_pct,omitempty"`
	BorrowCapUSD              string            `json:"borrow_cap_usd,omitempty"`
	ShortfallUSD              string            `json:"shortfall_usd"`
	PricePartial              bool              `json:"price_partial"`
	APRPartial                bool              `json:"apr_partial"`
	LiquidationPriceScenarios map[string]string `json:"liquidation_price_scenarios,omitempty"`
}

func (a *Adapter) computeState(input contractsv1.TransformInput, output *contractsv1.TransformOutput) error {
	if input.State == nil {
		return nil
	}

	pools := map[string]normalizedPool{}
	reserves := map[string]normalizedReserve{}
	for _, pool := range input.State.Pools {
		pool, wasmHashSource := a.enrichPoolIdentity(pool)
		nPool, ok := a.resolvePool(pool)
		if !ok {
			output.Quarantine = append(output.Quarantine, contractsv1.QuarantineEvent{
				ID:         stableID(a.cfg.AdapterID, "pool", pool.ContractID, "unknown_wasm_hash"),
				AdapterID:  a.cfg.AdapterID,
				LedgerSeq:  input.LedgerSeq,
				ContractID: pool.ContractID,
				Reason:     "unknown_wasm_hash",
				Metadata: map[string]string{
					"wasm_hash": pool.WasmHash,
				},
			})
			continue
		}
		pools[pool.ContractID] = nPool
		output.Contracts = append(output.Contracts, contractsv1.Contract{
			ID:              stableID(a.cfg.Protocol, pool.ContractID),
			Address:         pool.ContractID,
			Protocol:        a.cfg.Protocol,
			ContractType:    "pool",
			Status:          pool.PoolStatus,
			WasmHash:        pool.WasmHash,
			FirstSeenLedger: input.LedgerSeq,
			LastSeenLedger:  input.LedgerSeq,
			Metadata: map[string]string{
				"scalar_version":   nPool.scalarVersion,
				"wasm_hash_source": wasmHashSource,
			},
		})

		for _, reserve := range pool.Reserves {
			nReserve, err := normalizeReserve(pool.ContractID, nPool, reserve)
			if err != nil {
				return err
			}
			key := reserveKey(pool.ContractID, reserve.AssetID)
			reserves[key] = nReserve

			borrowAPY := ""
			if nReserve.borrowAPRNormalizedValid {
				borrowAPY = numString(nReserve.borrowAPRNormalized)
			}
			supplyAPY := ""
			if nReserve.supplyAPRNormalizedValid {
				supplyAPY = numString(nReserve.supplyAPRNormalized)
			}

			output.Reserves = append(output.Reserves, contractsv1.Reserve{
				ID:             stableID(a.cfg.Protocol, pool.ContractID, reserve.AssetID),
				Protocol:       a.cfg.Protocol,
				ContractID:     pool.ContractID,
				AssetID:        reserve.AssetID,
				TotalSupplied:  numString(nReserve.totalSuppliedRaw),
				TotalBorrowed:  numString(nReserve.totalBorrowedRaw),
				Utilization:    numString(nReserve.utilizationRaw.Div(factorScaleDecimal)),
				SupplyAPY:      supplyAPY,
				BorrowAPY:      borrowAPY,
				SupplyCap:      reserve.SupplyCapRaw,
				BorrowCap:      numString(nReserve.borrowCapRaw),
				CFactor:        numString(nReserve.cFactorNormalized),
				LFactor:        numString(nReserve.lFactorNormalized),
				OracleContract: pool.OracleContract,
				LedgerSeq:      input.LedgerSeq,
				Timestamp:      input.CloseTime,
				Metadata: map[string]string{
					"scalar_version":           nPool.scalarVersion,
					"asset_decimals":           parseDecimalsInt(nReserve.assetDecimals),
					"oracle_price_usd":         numString(nReserve.usdPrice),
					"oracle_price":             numString(nReserve.usdPrice),
					"b_rate":                   numString(nReserve.bRateRaw.Div(nPool.rateScalar)),
					"d_rate":                   numString(nReserve.dRateRaw.Div(nPool.rateScalar)),
					"util_target":              numString(nReserve.utilTargetNormalized),
					"max_util":                 numString(nReserve.maxUtilNormalized),
					"r_base":                   numString(nReserve.rBaseNormalized),
					"r_one":                    numString(nReserve.rOneNormalized),
					"r_two":                    numString(nReserve.rTwoNormalized),
					"r_three":                  numString(nReserve.rThreeNormalized),
					"rate_modifier":            numString(nReserve.rateModifierNormalized),
					"apr_partial":              boolString(nReserve.aprPartial),
					"pool_balance_raw":         nReserve.raw.PoolBalanceRaw,
					"remaining_borrowable_raw": numString(nReserve.remainingBorrowableRaw),
					"rate_scalar":              numString(nPool.rateScalar),
					"rate_modifier_scalar":     numString(nPool.rateModifierScalar),
					"utilization_source":       nReserve.utilizationSource,
				},
			})
		}
	}

	for i := range output.Activities {
		activity := &output.Activities[i]
		if activity.AssetID == "" || activity.AmountRaw == "" {
			continue
		}
		if activity.Metadata == nil {
			activity.Metadata = map[string]string{}
		}
		reserve, ok := reserves[reserveKey(activity.ContractID, activity.AssetID)]
		price, priceOK := activityLedgerPrice(activity.Metadata)
		if !priceOK {
			activity.Metadata["event_price_unavailable"] = "true"
			continue
		}
		assetDecimals, decimalsOK := activityAssetDecimals(activity.Metadata)
		if ok {
			assetDecimals = reserve.assetDecimals
			decimalsOK = true
		}
		if !decimalsOK {
			activity.Metadata["asset_decimals_unavailable"] = "true"
			continue
		}
		amountRaw := parseDecimalOrZero(activity.AmountRaw)
		units := amountRaw.Div(decimal.New(1, assetDecimals))
		activity.USDValue = numString(units.Mul(price))
		activity.Metadata["usd_value_source"] = "event_ledger_price"
	}

	poolSummaries := map[string]map[string]*poolSummaryAccumulator{}
	protocolSummaries := map[string]*protocolAccumulator{}

	ensurePoolSummary := func(address, poolContract string) *poolSummaryAccumulator {
		byPool, ok := poolSummaries[address]
		if !ok {
			byPool = map[string]*poolSummaryAccumulator{}
			poolSummaries[address] = byPool
		}
		s, ok := byPool[poolContract]
		if !ok {
			s = &poolSummaryAccumulator{poolContract: poolContract, liquidationPriceScenarios: map[string]string{}}
			byPool[poolContract] = s
		}
		return s
	}

	ensureProtocolSummary := func(address string) *protocolAccumulator {
		s, ok := protocolSummaries[address]
		if !ok {
			s = &protocolAccumulator{}
			protocolSummaries[address] = s
		}
		return s
	}

	for _, userPos := range input.State.Users {
		pool, ok := pools[userPos.PoolContractID]
		if !ok {
			continue
		}
		reserve, ok := reserves[reserveKey(userPos.PoolContractID, userPos.AssetID)]
		if !ok {
			continue
		}

		share := parseDecimalOrZero(userPos.BTokensRaw)
		assetAmountRaw := fixedMulFloor(share, reserve.bRateRaw, pool.rateScalar)
		if userPos.PositionType == contractsv1.PositionTypeLiability {
			share = parseDecimalOrZero(userPos.DTokensRaw)
			assetAmountRaw = fixedMulCeil(share, reserve.dRateRaw, pool.rateScalar)
		}

		usdValue := decZero
		usdValueStr := ""
		if reserve.priceAvailable {
			divisor := decimal.New(1, reserve.assetDecimals)
			usdValue = assetAmountRaw.Div(divisor).Mul(reserve.usdPrice)
			usdValueStr = numString(usdValue)
		}

		positionMeta := map[string]string{
			"scalar_version":   pool.scalarVersion,
			"c_factor":         numString(reserve.cFactorNormalized),
			"l_factor":         numString(reserve.lFactorNormalized),
			"oracle_price_usd": numString(reserve.usdPrice),
			"oracle_price":     numString(reserve.usdPrice),
			"b_rate":           numString(reserve.bRateRaw.Div(pool.rateScalar)),
			"d_rate":           numString(reserve.dRateRaw.Div(pool.rateScalar)),
		}

		apy := ""
		aprPartial := false
		signedContribution := decZero
		if userPos.PositionType == contractsv1.PositionTypeSupply || userPos.PositionType == contractsv1.PositionTypeCollateral {
			if reserve.supplyAPRNormalizedValid {
				positionMeta["supply_apr"] = numString(reserve.supplyAPRNormalized)
			}
			if emissionsAPR, ok := normalizedAPRInput(reserve.raw.SupplyEmissionsAPR); ok && reserve.supplyAPRNormalizedValid {
				apy = numString(reserve.supplyAPRNormalized.Add(emissionsAPR))
				positionMeta["supply_emissions_apr"] = numString(emissionsAPR)
				positionMeta["net_supply_apr"] = apy
			} else {
				if emissionsAPR, ok := normalizedAPRInput(reserve.raw.SupplyEmissionsAPR); ok {
					// Base APR is invalid but the raw emissions APR parses: surface
					// the emission independently of net APR (metadata-served, D-08).
					positionMeta["supply_emissions_apr"] = numString(emissionsAPR)
				} else {
					positionMeta["emissions_apr_unavailable"] = "true"
				}
				positionMeta["apr_partial"] = "true"
				aprPartial = true
			}
		} else if userPos.PositionType == contractsv1.PositionTypeLiability {
			if reserve.borrowAPRNormalizedValid {
				positionMeta["borrow_apr"] = numString(reserve.borrowAPRNormalized)
			}
			if emissionsAPR, ok := normalizedAPRInput(reserve.raw.BorrowEmissionsAPR); ok && reserve.borrowAPRNormalizedValid {
				apy = numString(reserve.borrowAPRNormalized.Sub(emissionsAPR))
				positionMeta["borrow_emissions_apr"] = numString(emissionsAPR)
				positionMeta["net_borrow_apr"] = apy
			} else {
				if emissionsAPR, ok := normalizedAPRInput(reserve.raw.BorrowEmissionsAPR); ok {
					// Base APR is invalid but the raw emissions APR parses: surface
					// the emission independently of net APR (metadata-served, D-08).
					positionMeta["borrow_emissions_apr"] = numString(emissionsAPR)
				} else {
					positionMeta["emissions_apr_unavailable"] = "true"
				}
				positionMeta["apr_partial"] = "true"
				aprPartial = true
			}
		}
		if !reserve.priceAvailable {
			positionMeta["price_unavailable"] = "true"
		}
		if apy != "" && reserve.priceAvailable {
			r, _ := decimal.NewFromString(apy)
			signedContribution = usdValue.Mul(r)
			if userPos.PositionType == contractsv1.PositionTypeLiability {
				signedContribution = signedContribution.Neg()
			}
		}

		pos := contractsv1.Position{
			ID:           stableID(a.cfg.Protocol, userPos.Address, userPos.PoolContractID, userPos.AssetID, string(userPos.PositionType)),
			Address:      userPos.Address,
			Protocol:     a.cfg.Protocol,
			ContractID:   userPos.PoolContractID,
			AssetID:      userPos.AssetID,
			PositionType: userPos.PositionType,
			ShareAmount:  numString(share),
			AssetAmount:  numString(assetAmountRaw),
			USDValue:     usdValueStr,
			APY:          apy,
			LedgerSeq:    input.LedgerSeq,
			Timestamp:    input.CloseTime,
			Metadata:     positionMeta,
		}
		output.Positions = append(output.Positions, pos)

		poolSummary := ensurePoolSummary(userPos.Address, userPos.PoolContractID)
		protocolSummary := ensureProtocolSummary(userPos.Address)
		if !reserve.priceAvailable {
			poolSummary.pricePartial = true
			continue
		}

		switch userPos.PositionType {
		case contractsv1.PositionTypeSupply:
			poolSummary.depositedUSD = poolSummary.depositedUSD.Add(usdValue)
			protocolSummary.depositedUSD = protocolSummary.depositedUSD.Add(usdValue)
		case contractsv1.PositionTypeCollateral:
			poolSummary.depositedUSD = poolSummary.depositedUSD.Add(usdValue)
			protocolSummary.depositedUSD = protocolSummary.depositedUSD.Add(usdValue)
			effectiveCollateralUSD := usdValue.Mul(reserve.cFactorNormalized).Truncate(18)
			poolSummary.effectiveCollateralUSD = poolSummary.effectiveCollateralUSD.Add(effectiveCollateralUSD)
			poolSummary.hasEffectiveCollateral = true
			if !assetAmountRaw.IsZero() && !reserve.cFactorRaw.IsZero() {
				poolSummary.liquidationCollaterals = append(poolSummary.liquidationCollaterals, liquidationCollateral{
					assetID:                userPos.AssetID,
					units:                  assetAmountRaw.Div(decimal.New(1, reserve.assetDecimals)),
					cFactor:                reserve.cFactorNormalized,
					effectiveCollateralUSD: effectiveCollateralUSD,
				})
			}
		case contractsv1.PositionTypeLiability:
			poolSummary.borrowedUSD = poolSummary.borrowedUSD.Add(usdValue)
			protocolSummary.borrowedUSD = protocolSummary.borrowedUSD.Add(usdValue)
			poolSummary.hasLiability = true
			if reserve.lFactorRaw.IsZero() && !usdValue.IsZero() {
				poolSummary.lFactorZeroLiability = true
			} else if !reserve.lFactorRaw.IsZero() {
				poolSummary.effectiveLiabilityUSD = poolSummary.effectiveLiabilityUSD.Add(usdValue.Div(reserve.lFactorNormalized).RoundCeil(18))
			}
		}

		poolSummary.netAPYWeightUSD = poolSummary.netAPYWeightUSD.Add(usdValue.Abs())
		protocolSummary.netAPYWeightUSD = protocolSummary.netAPYWeightUSD.Add(usdValue.Abs())
		if aprPartial {
			poolSummary.aprPartial = true
			poolSummary.netAPYPartial = true
			protocolSummary.netAPYPartial = true
		} else {
			poolSummary.netAPYNumeratorUSD = poolSummary.netAPYNumeratorUSD.Add(signedContribution)
			protocolSummary.netAPYNumeratorUSD = protocolSummary.netAPYNumeratorUSD.Add(signedContribution)
		}
	}

	for _, backstop := range input.State.Backstops {
		_, ok := pools[backstop.PoolContractID]
		if !ok {
			continue
		}

		activeShares := parseDecimalOrZero(backstop.UserSharesRaw)
		queuedShares := decZero
		q4wEntries := make([]contractsv1.BackstopQueueEntry, 0, len(backstop.Q4W))
		var q4wUnlockAt *time.Time
		for _, q := range backstop.Q4W {
			share := parseDecimalOrZero(q.SharesRaw)
			queuedShares = queuedShares.Add(share)
			q4wEntries = append(q4wEntries, contractsv1.BackstopQueueEntry{
				Amount:   numString(share),
				UnlockAt: q.UnlockAt.UTC(),
			})
			if q4wUnlockAt == nil || q.UnlockAt.Before(*q4wUnlockAt) {
				u := q.UnlockAt.UTC()
				q4wUnlockAt = &u
			}
		}
		totalShares := activeShares.Add(queuedShares)

		poolShares := parseDecimalOrZero(backstop.PoolSharesRaw)
		poolTokens := parseDecimalOrZero(backstop.PoolTokensRaw)
		activeTokens := convertBackstopSharesToTokens(activeShares, poolShares, poolTokens)
		queuedTokens := convertBackstopSharesToTokens(queuedShares, poolShares, poolTokens)
		totalTokens := convertBackstopSharesToTokens(totalShares, poolShares, poolTokens)

		lpSupply := parseDecimalOrZero(backstop.LPTokenSupplyRaw)
		blndReserve := parseDecimalOrZero(backstop.LPBLNDReserveRaw)
		usdcReserve := parseDecimalOrZero(backstop.LPUSDCReserveRaw)
		blndComponentRaw := decZero
		usdcComponentRaw := decZero
		if !lpSupply.IsZero() {
			blndComponentRaw = fixedMulFloor(totalTokens, blndReserve, lpSupply)
			usdcComponentRaw = fixedMulFloor(totalTokens, usdcReserve, lpSupply)
		}

		blndPriceKnown := strings.TrimSpace(backstop.BLNDPriceUSD) != ""
		usdcPriceKnown := strings.TrimSpace(backstop.USDCPriceUSD) != ""
		backstopUSD := decZero
		backstopUSDStr := ""
		if blndPriceKnown && usdcPriceKnown {
			blndPrice := parseDecimalOrZero(backstop.BLNDPriceUSD)
			usdcPrice := parseDecimalOrZero(backstop.USDCPriceUSD)
			blndUnits := blndComponentRaw.Div(decimal.New(1, backstop.BLNDDecimals))
			usdcUnits := usdcComponentRaw.Div(decimal.New(1, backstop.USDCDecimals))
			backstopUSD = blndUnits.Mul(blndPrice).Add(usdcUnits.Mul(usdcPrice))
			backstopUSDStr = numString(backstopUSD)
		}

		interestKnown := strings.TrimSpace(backstop.BackstopInterestAPY) != ""
		emissionsKnown := strings.TrimSpace(backstop.BackstopEmissionsAPY) != ""
		apy := ""
		metadata := map[string]string{
			"active_shares":       numString(activeShares),
			"queued_shares":       numString(queuedShares),
			"total_shares":        numString(totalShares),
			"active_lp_tokens":    numString(activeTokens),
			"queued_lp_tokens":    numString(queuedTokens),
			"total_lp_tokens":     numString(totalTokens),
			"blnd_component":      numString(blndComponentRaw),
			"usdc_component":      numString(usdcComponentRaw),
			"blnd_component_raw":  numString(blndComponentRaw),
			"usdc_component_raw":  numString(usdcComponentRaw),
			"unclaimed_emissions": backstop.UnclaimedEmissionsRaw,
		}
		if !interestKnown || !emissionsKnown {
			metadata["apr_partial"] = "true"
			if emissionsKnown {
				// Net APR stays partial (interest missing) but the raw emissions
				// APY parses: surface it independently (metadata-served, D-08).
				metadata["backstop_emissions_apr"] = numString(parseDecimalOrZero(backstop.BackstopEmissionsAPY))
			} else {
				metadata["emissions_apr_unavailable"] = "true"
			}
		} else {
			interestAPY := parseDecimalOrZero(backstop.BackstopInterestAPY)
			emissionsAPY := parseDecimalOrZero(backstop.BackstopEmissionsAPY)
			apy = numString(interestAPY.Add(emissionsAPY))
			metadata["backstop_interest_apr"] = numString(interestAPY)
			metadata["backstop_emissions_apr"] = numString(emissionsAPY)
			metadata["net_backstop_apr"] = apy
		}
		if backstopUSDStr == "" {
			metadata["price_unavailable"] = "true"
		}

		pos := contractsv1.Position{
			ID:           stableID(a.cfg.Protocol, backstop.Address, backstop.PoolContractID, "backstop"),
			Address:      backstop.Address,
			Protocol:     a.cfg.Protocol,
			ContractID:   backstop.PoolContractID,
			AssetID:      "blend_backstop_lp",
			PositionType: contractsv1.PositionTypeBackstop,
			ShareAmount:  numString(totalShares),
			AssetAmount:  numString(totalTokens),
			USDValue:     backstopUSDStr,
			APY:          apy,
			LedgerSeq:    input.LedgerSeq,
			Timestamp:    input.CloseTime,
			Metadata:     metadata,
			Q4WEntries:   q4wEntries,
		}
		if q4wUnlockAt != nil {
			pos.Metadata["q4w_unlock_at"] = q4wUnlockAt.Format(time.RFC3339)
		}
		output.Positions = append(output.Positions, pos)

		poolSummary := ensurePoolSummary(backstop.Address, backstop.PoolContractID)
		protocolSummary := ensureProtocolSummary(backstop.Address)
		if backstopUSDStr == "" {
			poolSummary.pricePartial = true
			continue
		}
		poolSummary.depositedUSD = poolSummary.depositedUSD.Add(backstopUSD)
		protocolSummary.depositedUSD = protocolSummary.depositedUSD.Add(backstopUSD)
		poolSummary.netAPYWeightUSD = poolSummary.netAPYWeightUSD.Add(backstopUSD.Abs())
		protocolSummary.netAPYWeightUSD = protocolSummary.netAPYWeightUSD.Add(backstopUSD.Abs())
		if apy == "" {
			poolSummary.aprPartial = true
			poolSummary.netAPYPartial = true
			protocolSummary.netAPYPartial = true
		} else {
			r, _ := decimal.NewFromString(apy)
			contribution := backstopUSD.Mul(r)
			poolSummary.netAPYNumeratorUSD = poolSummary.netAPYNumeratorUSD.Add(contribution)
			protocolSummary.netAPYNumeratorUSD = protocolSummary.netAPYNumeratorUSD.Add(contribution)
		}
	}

	addresses := make([]string, 0, len(poolSummaries))
	for address := range poolSummaries {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)

	for _, address := range addresses {
		poolsForAddress := poolSummaries[address]
		protocol := ensureProtocolSummary(address)

		healthFactor := ""
		borrowLimitPct := ""
		borrowCapUSD := ""
		effectiveCollateralUSD := decZero
		effectiveLiabilityUSD := decZero
		hasCollateralInAnyPool := false
		hasLiabilityInAnyPool := false

		var minHealth *decimal.Decimal
		var maxBorrowLimit *decimal.Decimal
		borrowCapTotal := decZero
		hasBorrowCap := false
		var worstPool *poolSummaryAccumulator

		poolBreakdown := map[string]poolBreakdownEntry{}
		poolContracts := make([]string, 0, len(poolsForAddress))
		for poolContract := range poolsForAddress {
			poolContracts = append(poolContracts, poolContract)
		}
		sort.Strings(poolContracts)

		for _, poolContract := range poolContracts {
			pool := poolsForAddress[poolContract]
			if pool.hasEffectiveCollateral {
				hasCollateralInAnyPool = true
			}
			if pool.hasLiability {
				hasLiabilityInAnyPool = true
			}

			poolHealth := ""
			poolBorrowLimit := ""
			poolBorrowCap := ""
			poolEffectiveLiability := pool.effectiveLiabilityUSD
			shortfallUSD := decZero
			if pool.lFactorZeroLiability {
				poolEffectiveLiability = pool.borrowedUSD
				if poolEffectiveLiability.IsZero() {
					poolEffectiveLiability = decimal.New(1, 18)
				}
				poolHealth = "0"
				poolBorrowLimit = "1"
				poolBorrowCap = "0"
				shortfallUSD = maxDecimal(poolEffectiveLiability.Sub(pool.effectiveCollateralUSD), decZero)
			} else {
				if !pool.effectiveLiabilityUSD.IsZero() {
					h := pool.effectiveCollateralUSD.Div(pool.effectiveLiabilityUSD).Truncate(18)
					poolHealth = numString(h)
				}
				if pool.effectiveCollateralUSD.IsZero() {
					poolBorrowLimit = ""
					poolBorrowCap = ""
				} else {
					limit := pool.effectiveLiabilityUSD.Div(pool.effectiveCollateralUSD).RoundCeil(18)
					cap := maxDecimal(pool.effectiveCollateralUSD.Sub(pool.effectiveLiabilityUSD), decZero)
					poolBorrowLimit = numString(limit)
					poolBorrowCap = numString(cap)
					shortfallUSD = maxDecimal(pool.effectiveLiabilityUSD.Sub(pool.effectiveCollateralUSD), decZero)
				}
			}
			pool.liquidationPriceScenarios = liquidationScenarios(pool, poolEffectiveLiability)

			if poolHealth != "" {
				h, _ := decimal.NewFromString(poolHealth)
				if minHealth == nil || h.LessThan(*minHealth) {
					minHealth = &h
					worstPool = pool
				}
			}
			if poolBorrowLimit != "" {
				l, _ := decimal.NewFromString(poolBorrowLimit)
				if maxBorrowLimit == nil || l.GreaterThan(*maxBorrowLimit) {
					maxBorrowLimit = &l
				}
			}
			if poolBorrowCap != "" {
				hasBorrowCap = true
				c, _ := decimal.NewFromString(poolBorrowCap)
				borrowCapTotal = borrowCapTotal.Add(c)
			}

			poolBreakdown[poolContract] = poolBreakdownEntry{
				DepositedUSD:              numString(pool.depositedUSD),
				BorrowedUSD:               numString(pool.borrowedUSD),
				EffectiveCollateralUSD:    numString(pool.effectiveCollateralUSD),
				EffectiveLiabilityUSD:     numString(poolEffectiveLiability),
				HealthFactor:              poolHealth,
				BorrowLimitPct:            poolBorrowLimit,
				BorrowCapUSD:              poolBorrowCap,
				ShortfallUSD:              numString(shortfallUSD),
				PricePartial:              pool.pricePartial,
				APRPartial:                pool.aprPartial,
				LiquidationPriceScenarios: pool.liquidationPriceScenarios,
			}
		}

		if minHealth != nil {
			healthFactor = numString(*minHealth)
		}
		if maxBorrowLimit != nil {
			borrowLimitPct = numString(*maxBorrowLimit)
		}
		if hasBorrowCap {
			borrowCapUSD = numString(borrowCapTotal)
		}
		if worstPool != nil {
			effectiveCollateralUSD = worstPool.effectiveCollateralUSD
			if worstPool.lFactorZeroLiability {
				effectiveLiabilityUSD = maxDecimal(worstPool.borrowedUSD, decimal.New(1, 18))
			} else {
				effectiveLiabilityUSD = worstPool.effectiveLiabilityUSD
			}
		}

		netAPY := decZero
		structuredMeta := map[string]any{
			"risk_semantics":                  "blend_pool_isolated",
			"summary_health_factor_semantics": "worst_pool",
			"liquidation_price_unavailable":   "pool_or_scenario_required",
			"pool_breakdown":                  poolBreakdown,
		}
		meta := map[string]string{
			"risk_semantics":                  "blend_pool_isolated",
			"summary_health_factor_semantics": "worst_pool",
			"liquidation_price_unavailable":   "pool_or_scenario_required",
			"pool_breakdown":                  marshalPoolBreakdown(poolBreakdown),
		}
		if !hasLiabilityInAnyPool {
			healthFactor = ""
		}
		if !hasCollateralInAnyPool {
			borrowLimitPct = ""
			borrowCapUSD = ""
		}

		if protocol.netAPYPartial {
			meta["net_apy_partial"] = "true"
			meta["net_apy_unavailable_reason"] = "missing_row_apr"
			structuredMeta["net_apy_partial"] = true
			structuredMeta["net_apy_unavailable_reason"] = "missing_row_apr"
		} else if !protocol.netAPYWeightUSD.IsZero() {
			netAPY = protocol.netAPYNumeratorUSD.Div(protocol.netAPYWeightUSD)
		}

		output.Summaries = append(output.Summaries, contractsv1.PositionSummary{
			ID:                     stableID(a.cfg.Protocol, address, "summary"),
			Address:                address,
			Protocol:               a.cfg.Protocol,
			HealthFactor:           healthFactor,
			BorrowLimitPct:         borrowLimitPct,
			BorrowCapUSD:           borrowCapUSD,
			DepositedUSD:           numString(protocol.depositedUSD),
			BorrowedUSD:            numString(protocol.borrowedUSD),
			EffectiveCollateralUSD: numString(effectiveCollateralUSD),
			EffectiveLiabilityUSD:  numString(effectiveLiabilityUSD),
			NetAPY:                 numString(netAPY),
			NetAPYWeightUSD:        numString(protocol.netAPYWeightUSD),
			LiquidationPrice:       "",
			LedgerSeq:              input.LedgerSeq,
			Timestamp:              input.CloseTime,
			Metadata:               meta,
			StructuredMetadata:     structuredMeta,
		})
	}

	return nil
}

func (a *Adapter) enrichPoolIdentity(pool contractsv1.PoolState) (contractsv1.PoolState, string) {
	if strings.TrimSpace(pool.WasmHash) != "" {
		return pool, "contract_instance"
	}
	if !poolHasBlendState(pool) || len(a.cfg.V2WasmHashes) != 1 {
		return pool, ""
	}
	for wasmHash := range a.cfg.V2WasmHashes {
		pool.WasmHash = wasmHash
		return pool, "configured_single_v2_hash"
	}
	return pool, ""
}

func poolHasBlendState(pool contractsv1.PoolState) bool {
	if pool.ContractID == "" || len(pool.Reserves) == 0 {
		return false
	}
	for _, reserve := range pool.Reserves {
		if reserve.AssetID != "" && reserve.BRateRaw != "" && reserve.DRateRaw != "" {
			return true
		}
	}
	return false
}

func (a *Adapter) resolvePool(pool contractsv1.PoolState) (normalizedPool, bool) {
	if pool.WasmHash == "" {
		if a.cfg.AllowUnknownV2 {
			return newV2Pool(a.cfg.V2Scalar, pool.BackstopTakeRate), true
		}
		return normalizedPool{}, false
	}
	if _, ok := a.cfg.V2WasmHashes[pool.WasmHash]; ok {
		return newV2Pool(a.cfg.V2Scalar, pool.BackstopTakeRate), true
	}
	if a.cfg.AllowUnknownV2 {
		return newV2Pool(a.cfg.V2Scalar, pool.BackstopTakeRate), true
	}
	return normalizedPool{}, false
}

func newV2Pool(v2Scalar, backstopTakeRate string) normalizedPool {
	backstopTakeRaw, available := parseFactorRaw(backstopTakeRate)
	backstopTakeRaw = minDecimal(maxDecimal(backstopTakeRaw, decZero), factorScaleDecimal)
	return normalizedPool{
		rateScalar:            parseDecimalOrZero(v2Scalar),
		rateModifierScalar:    parseDecimalOrZero(rateModifierScaleV2),
		scalarVersion:         "v2",
		version:               "v2",
		backstopTakeRaw:       backstopTakeRaw,
		backstopTakeAvailable: available,
	}
}

func normalizeReserve(poolContract string, pool normalizedPool, reserve contractsv1.ReserveState) (normalizedReserve, error) {
	var out normalizedReserve
	out.poolContract = poolContract
	out.assetID = reserve.AssetID
	out.assetDecimals = reserve.AssetDecimals
	out.raw = reserve
	out.utilizationSource = "contract_parity"

	if pool.rateScalar.IsZero() {
		return out, fmt.Errorf("scalar cannot be zero for pool %s", poolContract)
	}

	var err error
	if out.bRateRaw, err = mustParseDecimal(reserve.BRateRaw); err != nil {
		return out, err
	}
	if out.dRateRaw, err = mustParseDecimal(reserve.DRateRaw); err != nil {
		return out, err
	}
	if out.cFactorRaw, err = mustParseDecimal(reserve.CFactorRaw); err != nil {
		return out, err
	}
	if out.lFactorRaw, err = mustParseDecimal(reserve.LFactorRaw); err != nil {
		return out, err
	}
	if out.utilTargetRaw, err = mustParseDecimal(reserve.UtilTargetRaw); err != nil {
		return out, err
	}
	if out.maxUtilRaw, err = mustParseDecimal(reserve.MaxUtilRaw); err != nil {
		return out, err
	}
	if out.rBaseRaw, err = mustParseDecimal(reserve.RBaseRaw); err != nil {
		return out, err
	}
	if out.rOneRaw, err = mustParseDecimal(reserve.ROneRaw); err != nil {
		return out, err
	}
	if out.rTwoRaw, err = mustParseDecimal(reserve.RTwoRaw); err != nil {
		return out, err
	}
	if out.rThreeRaw, err = mustParseDecimal(reserve.RThreeRaw); err != nil {
		return out, err
	}
	if out.rateModifierRaw, err = mustParseDecimal(reserve.RateModifierRaw); err != nil {
		return out, err
	}

	out.cFactorNormalized = out.cFactorRaw.Div(factorScaleDecimal)
	out.lFactorNormalized = out.lFactorRaw.Div(factorScaleDecimal)
	out.utilTargetNormalized = out.utilTargetRaw.Div(factorScaleDecimal)
	out.maxUtilNormalized = out.maxUtilRaw.Div(factorScaleDecimal)
	out.rBaseNormalized = out.rBaseRaw.Div(factorScaleDecimal)
	out.rOneNormalized = out.rOneRaw.Div(factorScaleDecimal)
	out.rTwoNormalized = out.rTwoRaw.Div(factorScaleDecimal)
	out.rThreeNormalized = out.rThreeRaw.Div(factorScaleDecimal)
	out.rateModifierNormalized, err = normalizedRateModifier(reserve.RateModifierRaw, pool.rateModifierScalar)
	if err != nil {
		return out, err
	}

	priceRaw := parseDecimalOrZero(reserve.OraclePriceRaw)
	if !priceRaw.IsZero() {
		divisor := decimal.New(1, reserve.OracleDecimals)
		out.usdPrice = priceRaw.Div(divisor)
		out.priceAvailable = true
	}

	bSupplyRaw := parseDecimalOrZero(reserve.BSupplyRaw)
	dSupplyRaw := parseDecimalOrZero(reserve.DSupplyRaw)
	out.totalSuppliedRaw = fixedMulFloor(bSupplyRaw, out.bRateRaw, pool.rateScalar)
	out.totalBorrowedRaw = fixedMulCeil(dSupplyRaw, out.dRateRaw, pool.rateScalar)

	switch {
	case out.totalBorrowedRaw.IsZero():
		out.utilizationRaw = decZero
	case out.totalSuppliedRaw.IsZero():
		out.utilizationRaw = factorScaleDecimal
	default:
		out.utilizationRaw = fixedDivCeil(out.totalBorrowedRaw, out.totalSuppliedRaw, factorScaleDecimal)
	}
	if pool.version == "v2" && out.utilizationRaw.GreaterThan(factorScaleDecimal) {
		out.utilizationRaw = factorScaleDecimal
	}

	borrowAPRRaw, borrowAPRValid, aprPartial := computeBorrowAPRRaw(out.utilizationRaw, out.utilTargetRaw, out.rateModifierRaw, pool.rateModifierScalar, out.rBaseRaw, out.rOneRaw, out.rTwoRaw, out.rThreeRaw, dSupplyRaw)
	out.aprPartial = aprPartial
	if borrowAPRValid {
		v := borrowAPRRaw
		out.borrowAPRRaw = &v
		out.borrowAPRNormalized = borrowAPRRaw.Div(factorScaleDecimal)
		out.borrowAPRNormalizedValid = true
	}

	if !pool.backstopTakeAvailable || !borrowAPRValid {
		out.supplyAPRRaw = nil
		out.supplyAPRNormalizedValid = false
		out.aprPartial = true
	} else {
		tmp := fixedMulFloor(borrowAPRRaw, out.utilizationRaw, factorScaleDecimal)
		supplyAPRRaw := fixedMulFloor(tmp, factorScaleDecimal.Sub(pool.backstopTakeRaw), factorScaleDecimal)
		v := supplyAPRRaw
		out.supplyAPRRaw = &v
		out.supplyAPRNormalized = supplyAPRRaw.Div(factorScaleDecimal)
		out.supplyAPRNormalizedValid = true
	}

	out.supplyCapRaw = reserve.SupplyCapRaw
	out.borrowCapRaw = fixedMulFloor(out.totalSuppliedRaw, out.maxUtilRaw, factorScaleDecimal)
	out.remainingBorrowableRaw = maxDecimal(out.borrowCapRaw.Sub(out.totalBorrowedRaw), decZero)

	return out, nil
}

func computeBorrowAPRRaw(utilizationRaw, utilTargetRaw, rateModifierRaw, rateModifierScalar, rBaseRaw, rOneRaw, rTwoRaw, rThreeRaw, dSupplyRaw decimal.Decimal) (decimal.Decimal, bool, bool) {
	if utilizationRaw.IsZero() || dSupplyRaw.IsZero() {
		return decZero, true, false
	}
	if utilTargetRaw.IsZero() {
		return decZero, false, true
	}

	u95 := decimal.RequireFromString("9500000")
	if utilizationRaw.LessThanOrEqual(utilTargetRaw) {
		utilScalar := fixedDivCeil(utilizationRaw, utilTargetRaw, factorScaleDecimal)
		baseRate := rBaseRaw.Add(fixedMulCeil(utilScalar, rOneRaw, factorScaleDecimal))
		return fixedMulCeil(baseRate, rateModifierRaw, rateModifierScalar), true, false
	}
	if utilizationRaw.LessThanOrEqual(u95) {
		den := u95.Sub(utilTargetRaw)
		if den.IsZero() {
			baseRate := rBaseRaw.Add(rOneRaw).Add(rTwoRaw)
			return fixedMulCeil(baseRate, rateModifierRaw, rateModifierScalar), true, false
		}
		utilScalar := fixedDivCeil(utilizationRaw.Sub(utilTargetRaw), den, factorScaleDecimal)
		baseRate := rBaseRaw.Add(rOneRaw).Add(fixedMulCeil(utilScalar, rTwoRaw, factorScaleDecimal))
		return fixedMulCeil(baseRate, rateModifierRaw, rateModifierScalar), true, false
	}

	utilScalar := fixedDivCeil(utilizationRaw.Sub(u95), decimal.RequireFromString("500000"), factorScaleDecimal)
	extraRate := fixedMulCeil(utilScalar, rThreeRaw, factorScaleDecimal)
	intersection := fixedMulCeil(rBaseRaw.Add(rOneRaw).Add(rTwoRaw), rateModifierRaw, rateModifierScalar)
	return intersection.Add(extraRate), true, false
}

func reserveKey(poolContract, assetID string) string {
	return poolContract + "|" + assetID
}

func parseDecimalsInt(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func activityLedgerPrice(metadata map[string]string) (decimal.Decimal, bool) {
	for _, key := range []string{"event_ledger_usd_price", "ledger_usd_price", "event_usd_price"} {
		if price, ok := normalizedDecimalInput(metadata[key]); ok {
			return price, true
		}
	}
	return decZero, false
}

func activityAssetDecimals(metadata map[string]string) (int32, bool) {
	for _, key := range []string{"asset_decimals", "event_asset_decimals"} {
		raw := strings.TrimSpace(metadata[key])
		if raw == "" {
			continue
		}
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err == nil {
			return int32(parsed), true
		}
	}
	return 0, false
}

func normalizedAPRInput(raw string) (decimal.Decimal, bool) {
	return normalizedDecimalInput(raw)
}

func normalizedDecimalInput(raw string) (decimal.Decimal, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return decZero, false
	}
	d, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decZero, false
	}
	return d, true
}

func liquidationScenarios(pool *poolSummaryAccumulator, effectiveLiabilityUSD decimal.Decimal) map[string]string {
	if len(pool.liquidationCollaterals) == 0 || effectiveLiabilityUSD.LessThanOrEqual(decZero) {
		return map[string]string{}
	}
	scenarios := map[string]string{}
	for _, collateral := range pool.liquidationCollaterals {
		denominator := collateral.units.Mul(collateral.cFactor)
		if denominator.LessThanOrEqual(decZero) {
			continue
		}
		otherEffectiveCollateral := pool.effectiveCollateralUSD.Sub(collateral.effectiveCollateralUSD)
		numerator := effectiveLiabilityUSD.Sub(otherEffectiveCollateral)
		if numerator.LessThanOrEqual(decZero) {
			continue
		}
		price := numerator.Div(denominator)
		if price.GreaterThan(decZero) {
			scenarios[collateral.assetID] = numString(price)
		}
	}
	return scenarios
}

func parseFactorRaw(raw string) (decimal.Decimal, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return decZero, false
	}
	d := parseDecimalOrZero(trimmed)
	if d.IsZero() {
		return decZero, true
	}
	if d.Abs().LessThanOrEqual(decOne) {
		return d.Mul(factorScaleDecimal).Floor(), true
	}
	return d, true
}

func convertBackstopSharesToTokens(shares, poolShares, poolTokens decimal.Decimal) decimal.Decimal {
	if poolShares.IsZero() {
		return decZero
	}
	if shares.Equal(poolShares) {
		return poolTokens
	}
	return fixedMulFloor(shares, poolTokens, poolShares)
}

func marshalPoolBreakdown(breakdown map[string]poolBreakdownEntry) string {
	raw, err := json.Marshal(breakdown)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
