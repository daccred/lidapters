package discovery

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Local copies of the ScVal test constructors the deploy-event tests use; they
// previously lived in the flat adapter package's behavior tests.

func contractAddressVal(t *testing.T, seed byte) xdr.ScVal {
	t.Helper()
	var hash xdr.Hash
	hash[31] = seed
	contractID := xdr.ContractId(hash)
	address, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, contractID)
	if err != nil {
		t.Fatalf("contract address: %v", err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}

func accountAddressVal(t *testing.T, seed byte) xdr.ScVal {
	t.Helper()
	var raw xdr.Uint256
	raw[31] = seed
	account, err := xdr.NewAccountId(xdr.PublicKeyTypePublicKeyTypeEd25519, raw)
	if err != nil {
		t.Fatalf("account id: %v", err)
	}
	address, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeAccount, account)
	if err != nil {
		t.Fatalf("account address: %v", err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}

func symbolVal(t *testing.T, raw string) xdr.ScVal {
	t.Helper()
	sym := xdr.ScSymbol(raw)
	value, err := xdr.NewScVal(xdr.ScValTypeScvSymbol, sym)
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	return value
}
