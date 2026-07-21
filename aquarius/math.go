package aquarius

import (
	"fmt"
	"math/big"
	"time"

	"github.com/daccred/lidapters/bindings"
)

var q96 = new(big.Int).Lsh(big.NewInt(1), 96)

func parseUint(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid unsigned integer %q", s)
	}
	return n, nil
}
func mulDivFloor(a, b, d *big.Int) *big.Int { return new(big.Int).Quo(new(big.Int).Mul(a, b), d) }

// pendingReward reproduces the pool's get_user_reward getter from folded
// checkpoint state (classic pools): the contract checkpoints to_claim and the
// pool's accumulated total into UserRewardData on every user interaction, and
// accrues tps per second to the pool between interactions, capped at the
// emission expiry. Between the user's checkpoint and ledger close time T the
// user is owed working_balance * (accumulated(T) - pool_accumulated_user) /
// working_supply on top of the checkpointed to_claim. Verified against frozen
// mainnet checkpoints (205/223/233 raw AQUA stroops). All arithmetic is exact
// big.Int; when any checkpoint input is missing the checkpointed to_claim is
// returned unchanged (no fabricated accrual).
func pendingReward(pool bindings.AMMPoolState, pos bindings.AMMPositionState, closeTime time.Time) string {
	toClaim := pos.PendingRewardRaw
	if toClaim == "" {
		toClaim = "0"
	}
	wbRaw := pos.WorkingBalanceRaw
	if wbRaw == "" {
		wbRaw = pos.SharesRaw
	}
	if wbRaw == "" || wbRaw == "0" || pos.RewardPoolAccumulatedRaw == "" {
		return toClaim
	}
	if pool.RewardTpsRaw == "" || pool.RewardAccumulatedRaw == "" || pool.RewardLastTimeRaw == "" || pool.WorkingSupplyRaw == "" || pool.WorkingSupplyRaw == "0" {
		return toClaim
	}
	wb, err := parseUint(wbRaw)
	if err != nil {
		return toClaim
	}
	tps, err := parseUint(pool.RewardTpsRaw)
	if err != nil {
		return toClaim
	}
	accStored, err := parseUint(pool.RewardAccumulatedRaw)
	if err != nil {
		return toClaim
	}
	accUser, err := parseUint(pos.RewardPoolAccumulatedRaw)
	if err != nil {
		return toClaim
	}
	ws, err := parseUint(pool.WorkingSupplyRaw)
	if err != nil || ws.Sign() == 0 {
		return toClaim
	}
	lastTime, err := parseUint(pool.RewardLastTimeRaw)
	if err != nil {
		return toClaim
	}
	now := new(big.Int).SetInt64(closeTime.Unix())
	if pool.RewardExpiredAtRaw != "" {
		if exp, e := parseUint(pool.RewardExpiredAtRaw); e == nil && now.Cmp(exp) > 0 {
			now = exp
		}
	}
	dt := new(big.Int).Sub(now, lastTime)
	if dt.Sign() > 0 {
		accStored = new(big.Int).Add(accStored, new(big.Int).Mul(tps, dt))
	}
	deltaAcc := new(big.Int).Sub(accStored, accUser)
	if deltaAcc.Sign() <= 0 {
		return toClaim
	}
	claim, err := parseUint(toClaim)
	if err != nil {
		return toClaim
	}
	return new(big.Int).Add(claim, mulDivFloor(wb, deltaAcc, ws)).String()
}

func proRata(shares, reserve, total string) (string, error) {
	s, e := parseUint(shares)
	if e != nil {
		return "", e
	}
	r, e := parseUint(reserve)
	if e != nil {
		return "", e
	}
	t, e := parseUint(total)
	if e != nil {
		return "", e
	}
	if t.Sign() == 0 {
		return "", fmt.Errorf("zero total shares")
	}
	return mulDivFloor(s, r, t).String(), nil
}

// rangePrincipal applies burn/withdraw rounding (down) to Q96 square-root
// prices. Bounds are supplied by the audited tick-math decoder.
func rangePrincipal(liquidity, p, pa, pb string) (string, string, error) {
	L, e := parseUint(liquidity)
	if e != nil {
		return "", "", e
	}
	P, e := parseUint(p)
	if e != nil {
		return "", "", e
	}
	A, e := parseUint(pa)
	if e != nil {
		return "", "", e
	}
	B, e := parseUint(pb)
	if e != nil {
		return "", "", e
	}
	if A.Sign() <= 0 || A.Cmp(B) >= 0 {
		return "", "", fmt.Errorf("invalid sqrt-price bounds")
	}
	amount0 := func(x, y *big.Int) *big.Int {
		num := new(big.Int).Mul(new(big.Int).Lsh(new(big.Int).Set(L), 96), new(big.Int).Sub(y, x))
		return new(big.Int).Quo(new(big.Int).Quo(num, y), x)
	}
	amount1 := func(x, y *big.Int) *big.Int { return mulDivFloor(L, new(big.Int).Sub(y, x), q96) }
	if P.Cmp(A) <= 0 {
		return amount0(A, B).String(), "0", nil
	}
	if P.Cmp(B) < 0 {
		return amount0(P, B).String(), amount1(A, P).String(), nil
	}
	return "0", amount1(A, B).String(), nil
}
