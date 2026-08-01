# Aquarius fixture registry

Every fixture is raw on-chain bytes: the `key`/`val` ScVal slices of a real
mainnet `ContractDataEntry`, cut byte-for-byte out of Hubble's
`contract_data_xdr` column (`crypto-stellar.crypto_stellar_dbt.contract_data_current`,
queried 2026-08-01). Nothing is re-encoded: the slice boundaries were found by
walking the XDR framing, so the fixture bytes are exactly what the ledger
stores. The same capture set backs the relay's Aquarius wing
(`relay.rs/crates/protocols/aquarius/testdata/`), so both decoders are pinned
to identical bytes.

File shape: `<base>-key.xdr` (the entry's ScVal key) and `<base>-val.xdr`
(the entry's ScVal value), named per the testdata naming law.

## Handles

| handle | address | role |
|---|---|---|
| `ccy2` | `CCY2PXGMKNQHO7WNYXEWX76L2C5BH3JUW3RCATGUYKY7QQTRILBZIFWV` | XLM/AQUA constant-product pool — [stellar.expert](https://stellar.expert/explorer/public/contract/CCY2PXGMKNQHO7WNYXEWX76L2C5BH3JUW3RCATGUYKY7QQTRILBZIFWV) |
| `cce5` | `CCE5SYJ4EJDVN2ZNB5A3DE7UOLLHI2I3J5FOFO6U6BFSXRGMYQ6GOTH7` | EURx/EURC stableswap pool — [stellar.expert](https://stellar.expert/explorer/public/contract/CCE5SYJ4EJDVN2ZNB5A3DE7UOLLHI2I3J5FOFO6U6BFSXRGMYQ6GOTH7) |
| `cbbm` | `CBBMQBNHB2FYVZYV7VNHOJHUMTFJLR4PUMRVQYNW6RHIKZO2NQMIBUCV` | XLM/USDC concentrated pool — [stellar.expert](https://stellar.expert/explorer/public/contract/CBBMQBNHB2FYVZYV7VNHOJHUMTFJLR4PUMRVQYNW6RHIKZO2NQMIBUCV) |
| `cboh` | `CBOHAVUYKQD4C7FIVXEDJCVLUZYUO6RN3VIKEDOTIJGDDV3QN33Y4T4D` | share token of `ccy2` — [stellar.expert](https://stellar.expert/explorer/public/contract/CBOHAVUYKQD4C7FIVXEDJCVLUZYUO6RN3VIKEDOTIJGDDV3QN33Y4T4D) |
| `cc4q` | `CC4QKBXXYJSGTZKE2VQCHORCOW2TD5EOHQROMF5T7FZOKATAVCXCVHTZ` | share token of `cce5` — [stellar.expert](https://stellar.expert/explorer/public/contract/CC4QKBXXYJSGTZKE2VQCHORCOW2TD5EOHQROMF5T7FZOKATAVCXCVHTZ) |
| `gc7i` | `GC7IUIQ7R6NOIFNB4PYFNVYVNHSLJIULSWQTXG7UK33UTIC6NSZIW2BC` | golden CP LP (largest `cboh` holder) |
| `gaca` | `GACAI36BCRAIFTXW7NTH7BQD3UQ7SLFCENNJGICPETTRE7VPOQ6GUWPM` | golden stable LP on `cc4q` |
| `gaoa` | `GAOAZ7K5ZCG4Y2FIPTELRE5WVR2NGI6KG6OTE2LZEJI2PBZ6NT3JDCMC` | golden in-range position owner on `cbbm` |
| `ga5k` | `GA5KKLGWKXTOVSF7SI3J3FYFFNK35MIVCW4SSUK5GURJTCJDAMXM63YB` | golden out-of-range position owner on `cbbm` |
| `gbya` | `GBYA2DXFVWHC6ITSXC6IZ2Q65DATOAQ6ZRWJTUUDXGSWTRUPKD5FJDUH` | golden full-range position owner on `cbbm` |

## Fixtures

All entries are live (`deleted = false`) as of the query date; `L…` in the
file name is the entry's `last_modified_ledger` — the ledger whose write
these exact bytes are.

| base | contract | entry key |
|---|---|---|
| `pubnet-L063750870-aquarius-pool-instance-ccy2-layoutcp` | `ccy2` | contract instance |
| `pubnet-L063718535-aquarius-pool-instance-cce5-layoutstable` | `cce5` | contract instance |
| `pubnet-L063751196-aquarius-pool-instance-cbbm-layoutconc` | `cbbm` | contract instance |
| `pubnet-L057586946-aquarius-sharetoken-balance-cboh-gc7i` | `cboh` | `Balance(gc7i)` |
| `pubnet-L060462775-aquarius-sharetoken-balance-cc4q-gaca` | `cc4q` | `Balance(gaca)` |
| `pubnet-L063066122-aquarius-pool-position-cbbm-gaoa-inrange` | `cbbm` | `Position(gaoa, -21880, -7980)` |
| `pubnet-L063422562-aquarius-pool-position-cbbm-ga5k-outofrange` | `cbbm` | `Position(ga5k, -16560, -16480)` |
| `pubnet-L063685926-aquarius-pool-position-cbbm-gbya-fullrange` | `cbbm` | `Position(gbya, -887260, 887260)` |
| `pubnet-L063066122-aquarius-pool-user-cbbm-gaoa` | `cbbm` | `User(gaoa)` |
| `pubnet-L058422172-aquarius-pool-userrewarddata-ccy2-gc7i` | `ccy2` | `UserRewardData(gc7i)` |
| `pubnet-L058422172-aquarius-pool-workingbalance-ccy2-gc7i` | `ccy2` | `WorkingBalance(gc7i)` |

## Hand derivation

Expectations in `state_mainnet_test.go` were read out of the raw XDR by an
independent stdlib byte walk (Python, no stellar SDK — the decoder under test
never produces its own expectations). ScVal discriminants and fixed widths
follow the XDR spec (`SCV_U128`/`SCV_I128` = 16 big-endian bytes,
`SCV_U256` = 32, `SCV_I32` = 4 signed).

Worked example — `gc7i`'s share balance. The `Balance` entry's val bytes are
`0000000a 0000000000000000 0001277976cf7dee`: discriminant 10 (`SCV_I128`),
hi = 0, lo = `0x0001277976cf7dee` = **324,877,614,546,414** shares.

Derived anchors (floor integer arithmetic, checked in Python bignum):

| anchor | inputs (all from the fixtures above) | value |
|---|---|---|
| CP principal, XLM leg | 324877614546414 × 43048952672876 / 920330396116230 | 15,196,326,354,214 |
| CP principal, AQUA leg | 324877614546414 × 21837045747626409 / 920330396116230 | 7,708,500,513,693,587 |
| stable principal, EURx leg | 7672285 × 146555080 / 135495039 | 8,298,549 |
| stable principal, EURC leg | 7672285 × 652942 / 135495039 | 36,972 |
| CP pending AQUA at t = last_time | 0 + 405464567278873 × (2226439666559120 − 903562252135547) / 1107920893109695 | 484,131,964,419,179 |
| `gaoa` range amounts | L=23911740265087, [−21880, −7980), sp=32779403528916036142219842285 | (22,159,167,791,450; 1,885,240,058,815) |
| `ga5k` range amounts | L=5782465360699, [−16560, −16480), same sp | (52,827,604,008; 0) |
| `gbya` range amounts | L=14612333, [−887260, 887260), same sp | (35,318,162; 6,045,622) |

Range amounts use the X96 sqrt-price decomposition (tick → sqrt ratio via
the standard Q128.96 bit-ladder; burn/withdraw rounding, i.e. down). Because
the concentrated wasm's source is unfetchable (repo 404; SE-verified @
`a22139a`), the formulas are pinned empirically: summing the decomposition
over all 116 live positions of `cbbm` at ledger 63,751,196 reproduces the
pool's own instance reserves to within unattributed global fee growth
(99.28% / 98.51%). Sanity: the derived `sqrt_ratio(−17652)` =
32778602836627082880087502758 brackets the observed `Slot0.sqrt_price_x96` =
32779403528916036142219842285 from below, as it must for a price sitting
inside tick −17652.

Fixtures at different ledgers are not one chain-consistent state; combined
anchors (pro-rata legs, pending reward) pin the decode + math pipeline over
the exact fixture inputs, not a point-in-time chain statement.
