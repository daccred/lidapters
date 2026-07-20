package aquarius

import (
	"fmt"
	"math/big"
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
