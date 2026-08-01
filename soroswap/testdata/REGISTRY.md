# Soroswap fixture registry

Every fixture is raw on-chain bytes: the `key`/`val` ScVal slices of a real
pubnet `ContractDataEntry`, cut byte-for-byte out of Hubble's
`contract_data_xdr` column (`crypto-stellar.crypto_stellar_dbt.
contract_data_current`, queried 2026-08-01). Nothing is re-encoded: slice
boundaries follow the XDR framing, so the fixture bytes are exactly what the
ledger stores. The same capture set anchors the Rust engine's Soroswap
fixtures; both engines decode the shared raw bytes independently.

File shape: `<base>-key.xdr` (the entry's ScVal key) and `<base>-val.xdr`
(the entry's ScVal value), named per the testdata naming law. The discovery
registry is one `.jsonl` (raw hex key/val per line — see below).

## Handles

| handle | address | role |
|---|---|---|
| `fact` | `CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2` | Soroswap factory — [stellar.expert](https://stellar.expert/explorer/public/contract/CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2) |
| `cdlm` | `CDLMAKG5TSJA6FGP7LLC2FKJRQW6DQYMEPP6FURFVULDEQMP3PRZ4ISI` | XLM/LIBRE pair — [stellar.expert](https://stellar.expert/explorer/public/contract/CDLMAKG5TSJA6FGP7LLC2FKJRQW6DQYMEPP6FURFVULDEQMP3PRZ4ISI) |
| `cam7` | `CAM7DY53G63XA4AJRS24Z6VFYAFSSF76C3RZ45BE5YU3FQS5255OOABP` | XLM/USDC pair — [stellar.expert](https://stellar.expert/explorer/public/contract/CAM7DY53G63XA4AJRS24Z6VFYAFSSF76C3RZ45BE5YU3FQS5255OOABP) |
| `cccd` | `CCCDU62TWI744KFK6COAW2PARPVPXKKE3DBVBUZCFWZOGGD7HZ5YEY3X` | USDC/BLND pair — [stellar.expert](https://stellar.expert/explorer/public/contract/CCCDU62TWI744KFK6COAW2PARPVPXKKE3DBVBUZCFWZOGGD7HZ5YEY3X) |
| `gb77` | `GB77C7CHJQGWNMDPWRXXN5KMS55K5SSERBGND4GGCYDDBLC52FEKHUOR` | golden LP on `cdlm` (the ledger-63,415,332 deposit) |
| `gacx` | `GACXANRYOSGYKYI3CRTSIOFLTCCO3AWIN5IFJEF4VO4OU4IWR4WAQ4ON` | golden LP on `cam7` (largest holder) |
| `gc7i` | `GC7IUIQ7R6NOIFNB4PYFNVYVNHSLJIULSWQTXG7UK33UTIC6NSZIW2BC` | golden LP on `cccd` (largest holder) |

Token identities named by the pair instances (u32 keys 0/1):
XLM SAC `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA`,
LIBRE `CBEM2CAIYLM3HBOPU5HLQL7V5BUAKM3N77DYQKX4FNHTQLQUUD2ZFBOX`,
USDC `CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75`,
BLND `CD25MNVTZDL4Y3XBCPCJXGXATV5WUHHOWMYFF4YBEGU5FCPGMYTVG5JY`.

## Fixtures

All entries live (`deleted = false`) at the query date; `L…` is the entry's
`last_modified_ledger` — the ledger whose write these exact bytes are.

| base | contract | entry key |
|---|---|---|
| `pubnet-L063750862-soroswap-pair-instance-cdlm-layoutu32` | `cdlm` | contract instance (u32 keys 0–4, `METADATA`, `Vec[TotalSupply]`; `KLast=5` absent) |
| `pubnet-L063751402-soroswap-pair-instance-cam7-layoutu32` | `cam7` | contract instance (same shape) |
| `pubnet-L063750872-soroswap-pair-instance-cccd-layoutu32` | `cccd` | contract instance (same shape) |
| `pubnet-L063415332-soroswap-pair-balance-cdlm-gb77` | `cdlm` | `Balance(gb77)` — written by the golden deposit |
| `pubnet-L063491372-soroswap-pair-balance-cam7-gacx` | `cam7` | `Balance(gacx)` |
| `pubnet-L058465535-soroswap-pair-balance-cccd-gc7i` | `cccd` | `Balance(gc7i)` |
| `pubnet-L055467506-soroswap-pair-balance-cccd-cccd-minlock` | `cccd` | `Balance(cccd)` — the pair's own MINIMUM_LIQUIDITY lock (contract holder) |
| `pubnet-L063675481-soroswap-factory-instance-fact` | `fact` | contract instance (`FeeTo`, `FeeToSetter`, `TotalPairs`; `FeesEnabled` absent) |
| `pubnet-L062344317-soroswap-factory-pairindex-fact-n0` | `fact` | `PairAddressesNIndexed(0)` |
| `pubnet-L063675481-soroswap-factory-pairindex-registry-fact.jsonl` | `fact` | ALL 209 live `PairAddressesNIndexed(u32)` entries |

The `.jsonl` discovery registry carries one object per line —
`{"index", "key_xdr_hex", "val_xdr_hex", "last_modified_ledger"}` — the raw
ScVal key/val slices of every live `PairAddressesNIndexed` entry, ordered by
index (dense 0..=208). The discovery test folds these raw bytes and requires
≥ 200 distinct pair addresses registered.

## Hand derivation

Every expected constant in the tests was read out of the raw XDR with an
independent stdlib byte walk (Python `struct.unpack`, big-endian; strkey
re-encoded from the 32-byte payload + CRC16-XMODEM) — the decoder under test
never produces its own expectations. `SCV_I128` (discriminant 10) is 16
big-endian bytes, hi then lo.

Worked example — `gb77`'s LP balance. The `Balance` entry's val bytes are
`0000000a 0000000000000000 0000000fb80d7aa1`: discriminant 10 (`SCV_I128`),
hi = 0, lo = `0xfb80d7aa1` = **67,512,400,545** LP shares, written at ledger
63,415,332 by the golden deposit.

Pro-rata anchors (floor division, `shares · reserve_i / TotalSupply`, all
inputs read from the raw instance/balance bytes above):

| pair | Reserve0 | Reserve1 | TotalSupply | holder shares | lp₀ | lp₁ |
|---|---|---|---|---|---|---|
| `cdlm` | 6,911,037,650 | 1,174,050,007,304 | 87,628,791,895 | `gb77` 67,512,400,545 | **5,324,514,145** | **904,530,721,454** |
| `cam7` | 3,379,689,401,562 | 578,116,831,925 | 1,219,283,143,019 | `gacx` 1,192,466,439,370 | **3,305,357,094,397** | **565,401,829,798** |
| `cccd` | 257,958,218,761 | 6,238,333,411,519 | 1,209,342,926,092 | `gc7i` 1,191,193,505,547 | **254,086,866,728** | **6,144,710,557,204** |

Factory instance: `TotalPairs = 209` (u32); `FeeTo = FeeToSetter =
GAYPUMZFDKUEUJ4LPTHVXVG2GD5B6AV5GGLYDMSZXCSI4QILQKSY25JI`; `FeesEnabled`
ABSENT (the contract reads it with a `false` default — the decode reports
absence, never a fabricated false). `PairAddressesNIndexed(0)` →
`CB46LMGJC7SYSH4C7SBNLV635OX5BSNQDGRR32NRXAV7N2AVNZMQUJ3A`.

`cccd` min-liquidity lock: `Balance(cccd) = 1000` — the pair contract holds
its own first-mint lock (`contracts/pair/src/lib.rs:31,193` in
soroswap/core @ bb90a65), so a CONTRACT address is a legitimate LP holder
the census must enroll.
