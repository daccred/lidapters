package soroswap

import (
	"fmt"
	"math/big"
)

func parseUint(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid unsigned integer %q", s)
	}
	return n, nil
}

// proRata is the LP principal decomposition: shares * reserve / totalSupply,
// floor division — exactly the burn-side rounding the pair applies when a
// holder withdraws (amounts round down in the holder's disfavor). All
// arithmetic is exact big.Int.
func proRata(shares, reserve, total string) (string, error) {
	s, err := parseUint(shares)
	if err != nil {
		return "", err
	}
	r, err := parseUint(reserve)
	if err != nil {
		return "", err
	}
	t, err := parseUint(total)
	if err != nil {
		return "", err
	}
	if t.Sign() == 0 {
		return "", fmt.Errorf("zero total shares")
	}
	return new(big.Int).Quo(new(big.Int).Mul(s, r), t).String(), nil
}
