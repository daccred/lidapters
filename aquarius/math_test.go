package aquarius

import (
	"math/big"
	"testing"
)

func TestProRataFloors(t *testing.T) {
	got, err := proRata("2", "10", "3")
	if err != nil || got != "6" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err = proRata("1", "1", "0"); err == nil {
		t.Fatal("zero total shares accepted")
	}
}
func TestRangePrincipalBranches(t *testing.T) {
	q := q96.String()
	two := new(big.Int).Lsh(big.NewInt(1), 97).String()
	three := new(big.Int).Mul(big.NewInt(3), q96).String()
	a0, a1, err := rangePrincipal("100", q, q, two)
	if err != nil || a0 != "50" || a1 != "0" {
		t.Fatalf("below: %s %s %v", a0, a1, err)
	}
	a0, a1, err = rangePrincipal("100", three, q, two)
	if err != nil || a0 != "0" || a1 != "100" {
		t.Fatalf("above: %s %s %v", a0, a1, err)
	}
}
