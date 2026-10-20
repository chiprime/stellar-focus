# stellar-focus

A Go testing toolkit for Stellar and Soroban: assert on XDR, manage golden fixtures, and capture real chain data as test data.

[![CI](https://github.com/stellar-optics/stellar-focus/actions/workflows/ci.yml/badge.svg)](https://github.com/stellar-optics/stellar-focus/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/stellar-optics/stellar-focus.svg)](https://pkg.go.dev/github.com/stellar-optics/stellar-focus)
[![Go Report Card](https://goreportcard.com/badge/github.com/stellar-optics/stellar-focus)](https://goreportcard.com/report/github.com/stellar-optics/stellar-focus)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Part of [stellar-optics](https://github.com/stellar-optics) — Go tools that make Stellar and Soroban data legible. Siblings: [stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens) (decode, explain, diff) and [stellar-prism](https://github.com/stellar-optics/stellar-prism) (tail live data).

## Why this exists

Testing Go code that produces Stellar XDR means comparing two base64 strings.
When they differ, `assert.Equal` tells you this:

```
    Error:  Not equal:
            expected: "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2YkUIwQLxzJkMlvAwp/AAAAGQAAAAAAAAAbwAAAAAAAAAAAAAAAQAAAAAAAAABAAAAAB4f..."
            actual  : "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2YkUIwQLxzJkMlvAwp/AAAAGQAAAAAAAAA3gAAAAAAAAAAAAAAAQAAAAAAAAABAAAAAB4f..."
```

That is technically correct and completely useless. focus tells you which
field changed:

```
--- FAIL: TestPaymentEnvelope (0.00s)
    payment_test.go:29: AssertEnvelopeEqual: envelopes differ — 1 difference
        (ignored: **.SeqNum)

          TransactionEnvelope

          ~ V1.Tx.Operations[0].Body.PaymentOp.Amount
              - 1000000000 100.0000000 (stroops: 1000000000)
              + 2000000000 200.0000000 (stroops: 2000000000)

        want  AAAAAgAAAACS05bpJAmO8a9V…  (172 bytes base64)
        got   AAAAAgAAAACS05bpJAmO8a9V…  (172 bytes base64)
    payment_test.go:30: AssertFee: fee is 100 (0.0000100 XLM), want 200 (0.0000200 XLM)
    payment_test.go:31: AssertOperationTypes: operations are [payment], want [payment, change_trust]
          [0] payment — pay 200.0000000 XLM to GAPB7…WYXZ
```

That is real output from a real `go test` run, not a mock-up. Note what it
does: names the field path, shows the amount in XLM as well as stroops, says
which ignore rule fired, keeps the base64 truncated but available, and reports
all three failures in one run.

Diffs are rendered by [stellar-xdr-lens](https://github.com/stellar-optics/stellar-xdr-lens),
so the diff in a failing test is the same one `lens diff` prints at the
command line.

## Install

```sh
go get github.com/stellar-optics/stellar-focus
```

Requires Go 1.25 or later.

**The library carries no assertion framework, no diff library, and no CLI
framework.** Test helpers get pulled into other people's projects, so
`pkg/focus` depends only on the standard library, the Stellar SDK and lens.
[A test enforces this](pkg/focus/deps_test.go). The `focus` command lives in a
separate package the library never imports.

## Assertions

```go
func TestBuildPayment(t *testing.T) {
	got := BuildPayment(dest, "100")   // whatever your code returns

	focus.AssertEnvelopeEqual(t, wantXDR, got, focus.IgnoreVolatile())
	focus.AssertOperationTypes(t, got, "payment")
	focus.AssertFee(t, got, 100)
	focus.AssertMemo(t, got, "text: invoice-42")
}
```

Every assertion accepts XDR in whatever form your code produced it — a base64
string, raw `[]byte`, any `xdr.*` value, or an already-decoded `*lens.Value`.

### Results, in plain English

```go
focus.AssertOperationFailed(t, result, 1, "payment_underfunded")
```

```
    submit_test.go:31: AssertOperationFailed: operation 1 failed with
        "payment_no_trust", want "payment_underfunded"

          got:  payment_no_trust — The destination has no trustline for this asset.
                → The destination must establish a trustline before it can receive a non-native asset.
          want: payment_underfunded
```

Codes are the short forms Horizon returns, and every one of the protocol's 208
result codes carries lens's plain-English explanation and hint.

### Soroban

```go
focus.AssertEventEmitted(t, meta, focus.Event{
	ContractID: "CBGSB…",
	Topics:     []any{"transfer", nil},   // nil matches any topic
})
focus.AssertReturnValue(t, meta, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &seven})
focus.AssertContractTrapped(t, result)

// A trap, with the host's own explanation of it from the diagnostic stream.
focus.AssertContractTrappedWith(t, tx, "Error(Contract, #4)")

// The error itself: which category, and which code.
focus.AssertContractError(t, meta, xdr.ScErrorTypeSceContract, 101)
```

A non-match lists the events that *were* emitted, so you are not left guessing.
`AssertContractTrappedWith` takes a Soroban RPC `getTransaction` response, or a
`focus.TrapEvidence{Result, Meta}` pair when the result and meta are separate
fixtures, and searches the rendered host diagnostics.

### Preflighting a transaction

A simulation runs a transaction against the current ledger state without
submitting it, and reports what would have happened: the return value, the
ledger entries it would touch, and the resources it would consume. That makes
a resource regression visible before submission rather than after it has
failed on chain.

```go
result, err := simulate.Simulate(ctx, src, envelopeXDR)
if err != nil {
	t.Fatalf("simulation failed: %v", err)
}

focus.AssertSimulationSucceeded(t, result)
focus.AssertSimulatedReturnValue(t, result, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &seven})
focus.AssertInstructionsBelow(t, result, 100_000)
```

The network call lives in `pkg/simulate`, never in `pkg/focus` or
`pkg/golden`: the assertions take the result and nothing else. The seam is
`simulate.Source`, which `capture.RPCSource` implements against a real
endpoint and a test implements with a canned response, so a suite stays
offline.

**A simulation is not a fixture.** It runs against the ledger as it is right
now, so the same transaction simulated twice can report a different footprint
and a different instruction count. Assert on a budget and a shape, not on an
exact snapshot.
`AssertContractError` answers the question a trap leaves open: not *that* the
contract failed but *how*. It reads the Soroban return value and the host's
error diagnostic, and names both sides of the comparison:

```
    contract_test.go:42: AssertContractError: got ScErrorTypeContract(101), want ScErrorTypeContract(404)
```

The code is the number the error carries: a contract's own code for
`ScErrorTypeContract`, and the numeric value of an `xdr.ScErrorCode` for the
host's categories — `uint32(xdr.ScErrorCodeScecMissingValue)` for a missing
storage entry.

### Typed memos, sources and signers

The display forms (`"text: hello"`, `"id: 42"`) make a wrong memo *type* read
like a wrong memo *value*, so the typed assertions name the value the way the
caller holds it:

```go
focus.AssertMemoText(t, env, "invoice-42")
focus.AssertMemoID(t, env, 42)
focus.AssertMemoHash(t, env, hash)     // a hash memo or a return memo
focus.AssertNoMemo(t, env)

focus.AssertOperationSourceAccount(t, env, 0, "GABC…")   // empty want: no override

focus.AssertSignedBy(t, env, "GABC…")                     // by public key
focus.AssertSignatureHints(t, env, [4]byte{0xde, 0xad, 0xbe, 0xef})
```

An operation may name a source account of its own, which replaces the
transaction's for that operation alone; an empty want asserts that it names
none, and the failure says which account the operation falls back to.

A Stellar signature carries a four-byte hint rather than a public key, so a
signature is attributed to a signer by the hint the protocol derives from that
signer's key — the last four bytes of it.

### Ignoring what legitimately varies

Without this, the library is unusable on real suites: a rebuilt transaction
differs in its sequence number, signatures and time bounds every run.

```go
focus.IgnoreVolatile()      // sequence + signatures + time bounds
focus.IgnoreSequence()
focus.IgnoreSignatures()
focus.IgnorePaths("**.Signatures", "operations[*].sourceaccount")
focus.NormalizePath("**.SeqNum", func(v any) any { return v.(int64) / 100 * 100 })
```

Patterns match the dotted paths lens reports: `*` is one segment, `**` is any
depth, and indexes accept `[*]`. Within a segment, `*`, `?` and `[abc]`/`[!abc]`
classes work like globs; matching is case-insensitive and patterns are anchored
to the tail of a path too, so `operations[*].sourceaccount` needs no prefix.

Two deliberate behaviours:

- **Ignored paths are always named in the failure output.** A silently
  discarded difference is how an assertion library stops being trustworthy.
- **An ignore pattern that matches nothing is reported as a failure**, even
  when the values are otherwise equal. A stale pattern is a test quietly
  weakening, and this is the only moment anyone would notice.

### Comparing arrays as sets

Some XDR arrays are ordered and some are not. The signatures on a multi-signed
transaction arrive in signing order, which is not part of what the transaction
means, and a positional comparison reports one difference per element for what
is really the same envelope.

```go
focus.AssertEnvelopeEqualUnordered(t, want, got)   // signatures compared as a set
focus.AssertEnvelopeEqual(t, want, got, focus.SortPath("**.Signatures"))
```

The arrays at the matching paths are sorted before the diff, so `[sigA, sigB]`
and `[sigB, sigA]` compare equal. Everything else is still compared the usual
way: a missing signature is still a difference.

The same two deliberate behaviours apply. The paths that were compared as
unordered sets are named in the failure output, and a sort pattern that
matches no array is reported as a failure, because a rule that quietly does
nothing is the same problem as a stale ignore pattern.

### `require` for fatal assertions

```go
import "github.com/stellar-optics/stellar-focus/pkg/focus/require"

require.EnvelopeEqual(t, want, got)   // stops the test; same message
```

Same split, same naming as testify's `assert` and `require`.

### Assertions never panic

Malformed XDR, a nil value, a wrong type — all reported as ordinary test
failures. A panic inside your test binary would destroy the output of every
other test in the package, so [a test feeds hostile input through every
exported assertion](pkg/focus/assertions_test.go).

## Golden files

```go
func TestEnvelopeGolden(t *testing.T) {
	g := golden.New(t, golden.WithType("TransactionEnvelope"))
	g.Assert("payment", BuildPayment(dest, "100"))
}
```

```console
$ go test ./...
    payment_test.go:15: golden: testdata/payment.golden.json does not exist yet.
        Create it by running:

            go test ./... -focus.update

$ go test ./... -focus.update
ok      demo    0.012s
```

**Fixtures are reviewable JSON, not opaque blobs.** The base64 XDR is the
source of truth for comparison; the decoded form sits beside it so a
pull-request diff shows which field changed:

```json
{
  "format": "focus/v1",
  "type": "TransactionEnvelope",
  "description": "a successful Soroban invocation",
  "xdr": "AAAAAgAAAACS05bpJAmO8a9VODCyfbOCmh2Yk…",
  "captured": {
    "network": "testnet",
    "ledger": 3966820,
    "txHash": "2b2e276a…",
    "at": "2026-08-05T12:00:00Z"
  },
  "decoded": {
    "type": "TransactionEnvelope",
    "value": { "V1": { "Tx": { "SourceAccount": "GCJNH…ZST6", "Fee": 58002, … } } }
  }
}
```

A golden test nobody can review is a golden test nobody updates carefully.

The flag is namespaced `-focus.update` so it never collides with your own
`-update`.

## Capture

```sh
go install github.com/stellar-optics/stellar-focus/cmd/focus@latest

# Record a real transaction from testnet as fixtures
focus capture tx 2b2e276a8f99a292… --out testdata/

# Later: check they still match the chain
focus verify testdata/
```

```console
$ focus verify testdata/
testdata/envelope.golden.json            ok
testdata/result.golden.json              ok
testdata/handwritten.golden.json         SKIP    no capture provenance, so there is nothing to re-fetch

3 checked: 2 ok, 0 drifted, 1 skipped
```

`verify` exits `2` on drift and prints the structural diff, `1` when a fixture
could not be checked at all, and `0` when everything checked still matches. A
hand-written fixture is reported as skipped rather than quietly passing a
check it was never eligible for.

Defaults to **testnet**; use `--network mainnet` or `--rpc-url`.

### Configuration

Flags, `FOCUS_*` environment variables and a config file are three spellings
of the same settings, resolved in that order:

```sh
focus verify testdata/ --network mainnet
FOCUS_NETWORK=mainnet focus verify testdata/
focus verify testdata/ --config focus.yaml   # or .focus.yaml in the working directory
```

```yaml
# focus.yaml
network: mainnet
rpc-url: https://mainnet.sorobanrpc.com
timeout: 45s
color: never
out: testdata
prefix: nightly
force: false
```

An explicit `--config` must exist; a missing `.focus.yaml` is simply ignored.
Invalid settings fail before any request is made.

## Snapshots

Failure-message formatting is snapshotted so a layout change is a reviewable
diff rather than a silent change in what users see:

```go
focus.AssertFailureSnapshot(t, "envelope_amount_mismatch", func(tb focus.TB) {
	focus.AssertEnvelopeEqual(tb, want, got)
})
```

Create or update snapshots with `FOCUS_UPDATE_SNAPSHOTS=1 go test ./...`. They
live under `testdata/snapshots` as plain text, `.snap` files.

## Testing offline

`go test ./...` makes **no network calls**. The RPC client sits behind an
interface with an in-memory fake, so the whole suite runs offline and
deterministically — including the tests of `focus capture` and `focus verify`.

## Examples

### Full Lifecycle (TypeScript)

`examples/lifecycle.ts` demonstrates the complete Stellar transaction lifecycle
on testnet using `@stellar/stellar-sdk`:

1. **FUND** — Create a claimable balance with predicates
2. **CLAIM** — Claim the balance by the designated recipient
3. **ATTEST** — Record the claim on a Soroban contract
4. **SETTLE** — Finalize via contract call

```bash
cd examples
npm install
export FUNDER_SECRET="S..."      # testnet account with XLM
export CLAIMANT_SECRET="S..."    # testnet recipient
# Optional for attest/settle:
export ATTESTOR_SECRET="S..."
export ATTESTATION_CONTRACT_ID="C..."
npm run dev
```

The script prints transaction hashes at each step. View them on
[Stellar Expert](https://stellar.expert/explorer/testnet) to verify.

**CI** runs `npm run typecheck` in `examples/` to keep the example compiling.

See [examples/README.md](examples/README.md) for full setup and a sample
Soroban contract for the attest/settle steps.

## Project status

**Early but real.** v0.1 does what this README says. The library API may shift
before v1.0.

- Assertions for envelopes, results, fields and Soroban events.
- Golden files with `-focus.update`; capture and verify against a live network.
- Failure-message snapshots, a unified flag/env/config file, documented exit
  codes, and an offline end-to-end suite for the command.
- Soroban values render as the XDR wire shape — there is no contract-spec
  decoding yet. See [docs/roadmap.md](docs/roadmap.md).

## Contributing

Adding an assertion or a fixture source is designed to be a one-file change.
See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/architecture.md](docs/architecture.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
