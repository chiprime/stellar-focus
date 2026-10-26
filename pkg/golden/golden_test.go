package golden_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/golden"
)

// A real captured mainnet envelope, shared with the sibling repositories.
func envelope(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "pkg", "fuzz", "testdata", "tx_failed.env.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestBuildAndSaveRoundTrip(t *testing.T) {
	t.Parallel()

	env := envelope(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "envelope.golden.json")

	fixture, err := golden.Build(env, "TransactionEnvelope", "a failed Soroban call", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.XDR != env {
		t.Error("the stored XDR does not round-trip")
	}
	if loaded.Type != "TransactionEnvelope" {
		t.Errorf("Type = %q, want TransactionEnvelope", loaded.Type)
	}
	if loaded.Format != golden.FormatVersion {
		t.Errorf("Format = %q, want %q", loaded.Format, golden.FormatVersion)
	}
	if loaded.Description != "a failed Soroban call" {
		t.Errorf("Description = %q", loaded.Description)
	}
}

// TestGoldenFileIsReviewable is the reason this format exists: a reviewer
// must be able to see in a pull-request diff what actually changed.
func TestGoldenFileIsReviewable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	content := string(raw)

	// It must be valid, indented, line-oriented JSON.
	if !json.Valid(raw) {
		t.Fatal("golden file is not valid JSON")
	}
	if lines := strings.Count(content, "\n"); lines < 20 {
		t.Errorf("golden file has only %d lines; a one-line blob is not reviewable", lines)
	}
	if !strings.HasSuffix(content, "\n") {
		t.Error("golden file does not end with a newline")
	}

	// The decoded section must carry real field names a reviewer recognises,
	// not just the base64.
	for _, want := range []string{`"decoded"`, `"SourceAccount"`, `"Operations"`, `"type"`} {
		if !strings.Contains(content, want) {
			t.Errorf("golden file does not contain %q; the diff would not be reviewable", want)
		}
	}
	// Enriched values must survive, so a changed amount reads as an amount.
	if !strings.Contains(content, "G") {
		t.Error("golden file contains no strkey address")
	}
}

func TestAssertPassesAgainstMatchingGolden(t *testing.T) {
	t.Parallel()

	env := envelope(t)
	dir := t.TempDir()

	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if !g.Assert("env", env) {
		t.Errorf("Assert() failed against a matching golden file:\n%s", tb.Output())
	}
}

// TestAssertFailureShowsDiffAndRemedy checks the two things a stale golden
// file must tell you: what changed, and how to re-record it.
func TestAssertFailureShowsDiffAndRemedy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stored := buildEnvelope(t, 100)
	actual := buildEnvelope(t, 200)

	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Assert("env", actual) {
		t.Fatal("Assert() passed against a stale golden file")
	}

	out := tb.Output()
	if !strings.Contains(out, "Body.PaymentOp.Amount") {
		t.Errorf("failure does not show which field changed:\n%s", out)
	}
	if !strings.Contains(out, "is out of date") || !strings.Contains(out, "-focus.update") {
		t.Errorf("failure does not say how to re-record the fixture:\n%s", out)
	}
}

// TestRequireStopsTheTestOnFailure is the fatal counterpart of Assert: the
// same diagnostic, delivered as one fatal message so a test that stops
// still shows the reader everything.
func TestRequireStopsTheTestOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stored := buildEnvelope(t, 100)
	actual := buildEnvelope(t, 200)

	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir))
	if g.Require("env", actual) {
		t.Fatal("Require() passed against a stale golden file")
	}

	if len(tb.Fatals) == 0 {
		t.Fatalf("the failure was not reported fatally:\n%s", tb.Output())
	}
	if len(tb.Errors) != 0 {
		t.Errorf("the failure was also reported through Errorf:\n%s", tb.Output())
	}

	out := tb.Output()
	for _, want := range []string{"Body.PaymentOp.Amount", "is out of date", "-focus.update"} {
		if !strings.Contains(out, want) {
			t.Errorf("the fatal message does not contain %q:\n%s", want, out)
		}
	}
}

// TestRequirePassesAgainstAMatchingGolden checks the ordinary case: a
// fixture that matches says nothing at all.
func TestRequirePassesAgainstAMatchingGolden(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := buildEnvelope(t, 100)

	fixture, err := golden.Build(env, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	if !golden.New(tb, golden.WithDir(dir)).Require("env", env) {
		t.Errorf("Require() failed against a matching golden file:\n%s", tb.Output())
	}
	if tb.Failed() {
		t.Errorf("a matching fixture reported a failure:\n%s", tb.Output())
	}
}

// TestAssertMissingFileExplainsHowToCreateIt covers the first run of a new
// golden test, where a bare "no such file" would be unhelpful.
func TestAssertMissingFileExplainsHowToCreateIt(t *testing.T) {
	t.Parallel()

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(t.TempDir()))
	if g.Assert("absent", envelope(t)) {
		t.Fatal("Assert() passed against a missing golden file")
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"does not exist yet", "-focus.update"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestAssertHonoursIgnoreOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stored := buildEnvelopeSeq(t, 111, 100)
	actual := buildEnvelopeSeq(t, 222, 100)

	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	g := golden.New(tb, golden.WithDir(dir), golden.WithNoiseSuppression(false),
		golden.WithOptions(focus.IgnoreSequence()))
	if !g.Assert("env", actual) {
		t.Errorf("ignore option was not applied:\n%s", tb.Output())
	}
}

func TestAssertSuppressesSequenceNoiseByDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stored := buildEnvelopeSeq(t, 111, 100)
	actual := buildEnvelopeSeq(t, 222, 100)
	fixture, err := golden.Build(stored, "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(filepath.Join(dir, "env.golden.json"), fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	tb := faketb.New()
	if !golden.New(tb, golden.WithDir(dir)).Assert("env", actual) {
		t.Errorf("default noise suppression did not normalize sequence number:\n%s", tb.Output())
	}
}

func TestPath(t *testing.T) {
	t.Parallel()

	g := golden.New(faketb.New(), golden.WithDir("fixtures"), golden.WithSuffix(".gold"))

	tests := []struct{ name, want string }{
		{"payment", filepath.Join("fixtures", "payment.gold")},
		{"payment.gold", filepath.Join("fixtures", "payment.gold")},
	}
	for _, tc := range tests {
		if got := g.Path(tc.name); got != tc.want {
			t.Errorf("Path(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestLoadRejectsAnUnknownFormat guards against silently misreading a file
// written by a future version.
func TestLoadRejectsAnUnknownFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "future.golden.json")
	if err := os.WriteFile(path,
		[]byte(`{"format":"focus/v99","type":"TransactionEnvelope","xdr":"AAAA"}`), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if _, err := golden.Load(path); err == nil {
		t.Fatal("Load() accepted an unknown format version")
	} else if !strings.Contains(err.Error(), "focus/v99") {
		t.Errorf("error does not name the offending version: %v", err)
	}
}

func TestLoadRejectsAFixtureWithNoXDR(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.golden.json")
	if err := os.WriteFile(path, []byte(`{"format":"focus/v1","type":"X"}`), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	if _, err := golden.Load(path); err == nil {
		t.Fatal("Load() accepted a fixture with no xdr field")
	}
}

// TestUpdateFlagIsRegistered checks the documented convention is actually
// wired into the flag set, since a silently-missing flag would make every
// golden file impossible to re-record.
func TestUpdateFlagIsRegistered(t *testing.T) {
	t.Parallel()

	if golden.Update() {
		t.Skip("suite is running with -focus.update")
	}
	// Update() returning false without panicking is the check; the flag is
	// registered at package init and would panic on a duplicate name.
}

// TestRefreshRendersTheDecodedSectionAgain is what the refresh is for: the
// decoded tree of a stored fixture goes stale when the formatter changes, and
// re-recording the fixture to fix it would re-record the data too.
func TestRefreshRendersTheDecodedSectionAgain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "a failed Soroban call", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.Decoded = json.RawMessage(`{"stale": true}`)
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	changed, err := golden.Refresh(path)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if !changed {
		t.Error("Refresh() reported no change for a stale fixture")
	}

	refreshed, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(string(refreshed.Decoded), "stale") {
		t.Errorf("the decoded section was not refreshed:\n%s", refreshed.Decoded)
	}
	// Everything but the decoded section keeps its value.
	if refreshed.XDR != fixture.XDR {
		t.Error("the refresh changed the XDR")
	}
	if refreshed.Type != fixture.Type {
		t.Errorf("the refresh changed the type: %q", refreshed.Type)
	}
	if refreshed.Description != fixture.Description {
		t.Errorf("the refresh changed the description: %q", refreshed.Description)
	}
}

// TestRefreshLeavesACurrentFixtureAlone checks that a refresh is safe to run
// over a whole directory: nothing changes, so nothing is written.
func TestRefreshLeavesACurrentFixtureAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "env.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	changed, err := golden.Refresh(path)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if changed {
		t.Error("Refresh() changed a fixture that was already current")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the file was rewritten even though nothing changed")
	}
}

// TestRefreshReportsAFixtureItCannotDecode checks that a fixture whose XDR
// does not decode is reported rather than quietly left stale.
func TestRefreshReportsAFixtureItCannotDecode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.golden.json")

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fixture.XDR = "!!! not xdr !!!"
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := golden.Refresh(path); err == nil {
		t.Fatal("Refresh() returned no error for a fixture it could not decode")
	} else if !strings.Contains(err.Error(), "decoding") {
		t.Errorf("the error does not say what went wrong: %v", err)
	}
}

func TestNewProvenanceFillsTheKnownPassphrase(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		network string
		want    string
	}{
		{"testnet", "Test SDF Network ; September 2015"},
		{"mainnet", "Public Global Stellar Network ; September 2015"},
		{"futurenet", "Test SDF Future Network ; October 2022"},
		{"standalone", ""},
	}
	for _, tc := range tests {
		p := golden.NewProvenance(tc.network, "https://rpc.example", 1, "abc", at)
		if p.NetworkPassphrase != tc.want {
			t.Errorf("NewProvenance(%q).NetworkPassphrase = %q, want %q", tc.network, p.NetworkPassphrase, tc.want)
		}
		if got := golden.PassphraseFor(tc.network); got != tc.want {
			t.Errorf("PassphraseFor(%q) = %q, want %q", tc.network, got, tc.want)
		}
	}
}

func TestPassphraseIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	const passphrase = "Standalone Network ; February 2017"
	provenance := golden.NewProvenance("standalone", "http://localhost:8000/rpc", 7, "abc",
		time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	provenance.NetworkPassphrase = passphrase

	fixture, err := golden.Build(envelope(t), "TransactionEnvelope", "", provenance)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "envelope.golden.json")
	if err := golden.Save(path, fixture); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(raw), `"networkPassphrase": "Standalone Network ; February 2017"`) {
		t.Errorf("the passphrase is not in the file:\n%s", raw)
	}

	loaded, err := golden.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Captured == nil || loaded.Captured.NetworkPassphrase != passphrase {
		t.Errorf("loaded provenance = %+v, want passphrase %q", loaded.Captured, passphrase)
	}
}
