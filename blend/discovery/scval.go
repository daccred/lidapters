package discovery

import (
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Local copies of the tiny ScVal accessors the deploy-event decoder needs, so
// discovery stays a free-standing package with no dependency on the blend
// adapter internals.

func validContractAddress(address string) bool {
	return strkey.IsValidContractAddress(address)
}

func scValSymbol(val xdr.ScVal) string {
	switch val.Type {
	case xdr.ScValTypeScvSymbol:
		if val.Sym != nil {
			return string(*val.Sym)
		}
	case xdr.ScValTypeScvString:
		if val.Str != nil {
			return string(*val.Str)
		}
	}
	return ""
}

func scValAddress(val xdr.ScVal) string {
	if val.Type != xdr.ScValTypeScvAddress || val.Address == nil {
		return ""
	}
	addr := *val.Address
	switch addr.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if addr.AccountId == nil {
			return ""
		}
		encoded, err := strkey.Encode(strkey.VersionByteAccountID, addr.AccountId.Ed25519[:])
		if err != nil {
			return ""
		}
		return encoded
	case xdr.ScAddressTypeScAddressTypeContract:
		if addr.ContractId == nil {
			return ""
		}
		encoded, err := strkey.Encode(strkey.VersionByteContract, addr.ContractId[:])
		if err != nil {
			return ""
		}
		return encoded
	default:
		return ""
	}
}
