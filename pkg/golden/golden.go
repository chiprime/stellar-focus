// Package golden provides golden-file testing for Stellar XDR.
//
// Golden files here are readable JSON rather than opaque blobs. A fixture
// stores the base64 XDR as the source of truth alongside its decoded form, so
// when a golden file changes, the pull-request diff shows which field changed
// rather than one unreadable string being replaced by another. That is the
// whole point: a golden test nobody can review is a golden test nobody
// updates carefully.
//
// Files are rewritten by running the test suite with -focus.update:
//
//	go test ./... -focus.update
//
// The flag is namespaced so it never collides with a project's own -update.
//
// Fixtures may be stored gzip-compressed (a .golden.json.gz suffix writes
// them that way), and reads are transparent about it; content is compared
// after line-ending normalization, so a checkout on Windows asserts the same
// as one on Linux.
package golden

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"
	"github.com/stellar/go-stellar-sdk/network"

	"github.com/stellar-optics/stellar-focus/pkg/focus"
)

// updateFlag is registered on the default command line so any test binary
// importing this package accepts -focus.update.
var updateFlag = flag.Bool("focus.update", false,
	"rewrite stellar-focus golden files instead of comparing against them")

// Update reports whether the suite was run with -focus.update.
//
// Tests rarely need this directly — Assert already honours the flag — but it
// is exported for suites that generate fixtures in some other way and want to
// follow the same convention.
func Update() bool { return *updateFlag }

// FormatVersion identifies the golden file layout, so a future change can be
// detected rather than silently misread.
const FormatVersion = "focus/v1"

// Fixture is the on-disk form of a golden file.
//
// Field order in the struct is the order in the file, chosen so a diff shows
// the identifying information first and the large decoded tree last.
type Fixture struct {
	// Format identifies the layout version.
	Format string `json:"format"`
	// Type is the XDR type, e.g. "TransactionEnvelope".
	Type string `json:"type"`
	// Description is a free-text note explaining what this fixture is for.
	Description string `json:"description,omitempty"`
	// XDR is the base64 payload and the source of truth for comparisons.
	XDR string `json:"xdr"`
	// Captured records where the fixture came from, when it was captured
	// from a live network rather than written by hand.
	Captured *Provenance `json:"captured,omitempty"`
	// Decoded is the human-readable form, present so a reviewer can see in a
	// pull request what actually changed. It is never used for comparison.
	Decoded json.RawMessage `json:"decoded,omitempty"`
}

// Provenance records where a captured fixture came from, so a stale fixture
// can be traced and re-verified.
//
// NetworkPassphrase is recorded alongside the network name because the name
// alone is ambiguous: a transaction hash and its signatures are bound to the
// passphrase, and two private networks can share a name but not a
// passphrase.
type Provenance struct {
	Network           string `json:"network"`
	NetworkPassphrase string `json:"networkPassphrase,omitempty"`
	Ledger            uint32 `json:"ledger,omitempty"`
	TxHash            string `json:"txHash,omitempty"`
	At                string `json:"at"`
	Endpoint          string `json:"endpoint,omitempty"`
	Anonymized        bool   `json:"anonymized,omitempty"`
	Sanitized         bool   `json:"sanitized,omitempty"`
}

// Golden compares values against golden files on disk.
type Golden struct {
	t                focus.TB
	dir              string
	suffix           string
	format           Format
	opts             []focus.Option
	noiseSuppression bool
	desc             string
	typeHnt          string
	vars             any
}

// Opt configures a Golden.
type Opt func(*Golden)

// WithDir sets the directory holding golden files. The default is "testdata".
func WithDir(dir string) Opt {
	return func(g *Golden) { g.dir = dir }
}

// WithSuffix sets the file suffix. The default follows the format, so it is
// ".golden.json" unless WithFormat says otherwise.
func WithSuffix(s string) Opt {
	return func(g *Golden) { g.suffix = s }
}

// WithFormat stores fixtures in the named format instead of JSON, and moves the
// default suffix with it — ".golden.yaml" for YAML and ".golden.txt" for text —
// so a name needs no extension. A WithSuffix given after it still wins.
func WithFormat(f Format) Opt {
	return func(g *Golden) {
		g.format = f
		g.suffix = f.DefaultSuffix()
	}
}

// WithOptions applies focus assertion options — ignore and normalize rules —
// to every comparison this Golden makes.
func WithOptions(opts ...focus.Option) Opt {
	return func(g *Golden) { g.opts = append(g.opts, opts...) }
}

// WithNoiseSuppression controls automatic normalization of volatile XDR fields.
// It is enabled by default; disable it when a test intentionally checks them.
func WithNoiseSuppression(enabled bool) Opt {
	return func(g *Golden) { g.noiseSuppression = enabled }
}

// WithDescription sets the description written into new golden files.
func WithDescription(d string) Opt {
	return func(g *Golden) { g.desc = d }
}

// WithType forces the XDR type rather than detecting it, which is worth doing
// for short payloads where detection is ambiguous.
func WithType(typeName string) Opt {
	return func(g *Golden) { g.typeHnt = typeName }
}

// WithVariables supplies data used to interpolate templates in loaded fixtures.
func WithVariables(variables any) Opt {
	return func(g *Golden) { g.vars = variables }
}

// New returns a Golden bound to a test.
func New(t focus.TB, opts ...Opt) *Golden {
	g := &Golden{
		t: t, dir: "testdata", suffix: JSON.DefaultSuffix(), format: JSON,
		noiseSuppression: true,
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Path returns the file a given name maps to.
func (g *Golden) Path(name string) string {
	if !strings.HasSuffix(name, g.suffix) {
		name += g.suffix
	}
	return filepath.Join(g.dir, name)
}

// Assert compares a value against the named golden file.
//
// With -focus.update the file is written instead, and the assertion passes.
// A missing file is reported with the command to create it rather than as a
// bare "no such file" — the first run of a new golden test should tell you
// what to do next.
func (g *Golden) Assert(name string, actual focus.Value) bool {
	g.t.Helper()
	return g.compare(g.t, name, actual)
}

// Require compares a value against the named golden file and stops the test
// when it does not match.
//
// It is the fatal counterpart of Assert, for the tests where everything
// after the comparison would be meaningless: a fixture that does not exist,
// a decode that failed, a transaction that was never built.
//
// The diagnostic is the one Assert reports — the same diff, in the same
// layout, with the same -focus.update instructions — but it is delivered as
// a single fatal message, so a test that stops still shows the reader
// everything a test that continued would have shown.
func (g *Golden) Require(name string, actual focus.Value) bool {
	g.t.Helper()

	// The comparison reports through a TB, and the report has to survive
	// the failure, so the messages are collected and delivered together.
	buffer := &messageBuffer{TB: g.t}
	ok := g.compare(buffer, name, actual)
	if !ok {
		g.t.Fatalf("%s", buffer.text())
	}
	return ok
}

// compare does the work of an assertion, reporting through tb.
//
// It is shared by Assert and Require so the two cannot drift apart: the
// same comparison, the same messages, the same layout.
func (g *Golden) compare(tb focus.TB, name string, actual focus.Value) bool {
	tb.Helper()
	path := g.Path(name)

	if Update() {
		if err := g.write(path, actual); err != nil {
			tb.Errorf("golden: updating %s: %v", path, err)
			return false
		}
		return true
	}

	want, err := LoadAs(path, g.format)
	if errors.Is(err, os.ErrNotExist) {
		// A compressed twin counts: a repository that gzipped its fixtures
		// should not have to reconfigure every assertion to keep passing.
		if gz, gzErr := LoadAs(path+GzipSuffix, g.format); gzErr == nil {
			path, want, err = path+GzipSuffix, gz, nil
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			tb.Errorf("golden: %s does not exist yet.\n"+
				"Create it by running:\n\n    go test ./... -focus.update\n", path)
			return false
		}
		tb.Errorf("golden: reading %s: %v", path, err)
		return false
	}
	if g.vars != nil {
		want, err = InterpolateFixture(want, g.vars)
		if err != nil {
			tb.Errorf("golden: interpolating %s: %v", path, err)
			return false
		}
	}

	typeName := g.typeHnt
	if typeName == "" {
		typeName = want.Type
	}

	// The comparison is against the stored XDR, never the decoded rendering:
	// the decoded form exists for humans reading diffs, and treating it as
	// authoritative would make the assertion depend on lens's formatting.
	opts := g.opts
	if g.noiseSuppression {
		opts = append(noiseSuppressionOptions(), opts...)
	}
	ok := focus.AssertEqualAs(tb, typeName, want.XDR, actual, opts...)
	if !ok {
		tb.Errorf("golden: %s is out of date.\n"+
			"If the change above is intended, re-record it with:\n\n    go test ./... -focus.update\n", path)
	}
	return ok
}

// messageBuffer collects the messages an assertion reports instead of
// reporting them.
//
// It exists so that a fatal assertion can deliver its whole diagnostic as
// one message. The diff and the command that re-records the fixture have to
// reach the reader together: a reader who gets only the diff does not know
// what to do about it, and a reader who gets only the remedy does not know
// what changed.
type messageBuffer struct {
	focus.TB
	messages []string
}

// Errorf records the message rather than reporting it.
func (buf *messageBuffer) Errorf(format string, args ...any) {
	buf.messages = append(buf.messages, fmt.Sprintf(format, args...))
}

// Fatalf records the message rather than reporting it.
//
// A comparison never fails fatally, so nothing should reach this; recording
// keeps the whole diagnostic in one place if that ever changes.
func (buf *messageBuffer) Fatalf(format string, args ...any) {
	buf.messages = append(buf.messages, fmt.Sprintf(format, args...))
}

// text returns the collected messages, in the order they were reported and
// with the same layout they would have had on the terminal.
func (buf *messageBuffer) text() string {
	var b strings.Builder
	for _, m := range buf.messages {
		b.WriteString(m)
		if !strings.HasSuffix(m, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// write records a value as a golden file.
func (g *Golden) write(path string, actual focus.Value) error {
	fixture, err := Build(actual, g.typeHnt, g.desc, nil)
	if err != nil {
		return err
	}
	return SaveAs(path, fixture, g.format)
}

// Build turns a value into a Fixture, decoding it for the human-readable
// section.
func Build(v focus.Value, typeName, description string, provenance *Provenance) (*Fixture, error) {
	b64, decoded, err := focus.Encode(v, typeName)
	if err != nil {
		return nil, err
	}

	fixture := &Fixture{
		Format:      FormatVersion,
		Type:        decoded.Type,
		Description: description,
		XDR:         b64,
		Captured:    provenance,
	}

	// The decoded section is lens's own JSON, so it matches what
	// `lens decode --json` prints for the same payload.
	rendered, err := renderDecoded(decoded)
	if err != nil {
		return nil, err
	}
	fixture.Decoded = rendered

	return fixture, nil
}

// renderDecoded renders a decoded value as the JSON tree a fixture stores.
func renderDecoded(decoded *lens.Value) (json.RawMessage, error) {
	var buf bytes.Buffer
	f := lensformat.NewJSONFormatter(lensformat.WithIndent("  "))
	if err := f.Format(&buf, decoded); err != nil {
		return nil, fmt.Errorf("rendering decoded form: %w", err)
	}
	return json.RawMessage(bytes.TrimSpace(buf.Bytes())), nil
}

// Refresh re-renders the decoded section of a fixture from the XDR it stores.
//
// The XDR is the source of truth and the decoded tree beside it is what a
// human reads in a pull request. When the formatter improves, or the
// protocol gains a field, that tree goes stale — and re-recording the
// fixtures to fix it would re-record the data too. Refresh touches only the
// decoded section: the format, the type, the description, the XDR and the
// capture provenance keep their values.
//
// It reports whether the file on disk changed. A fixture whose decoded
// section is already current is left alone, so the command is safe to run
// over a whole testdata directory.
func Refresh(path string) (bool, error) {
	format := FormatForPath(path)

	fixture, err := LoadAs(path, format)
	if err != nil {
		return false, err
	}

	decoded, err := RefreshedDecoded(fixture)
	if err != nil {
		return false, err
	}

	refreshed := *fixture
	refreshed.Decoded = decoded

	serializer, err := SerializerFor(format)
	if err != nil {
		return false, err
	}
	data, err := serializer.Marshal(&refreshed)
	if err != nil {
		return false, fmt.Errorf("rendering %s: %w", path, err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(current, data) {
		return false, nil
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// RefreshedDecoded returns the decoded section a fixture would carry after a
// refresh, without writing anything.
//
// It is what a dry run shows: the tree the fixture would have, next to the
// one it has.
func RefreshedDecoded(f *Fixture) (json.RawMessage, error) {
	if f == nil {
		return nil, errors.New("fixture is nil")
	}
	if f.Type == "" {
		return nil, errors.New("fixture has no type, so there is nothing to decode")
	}
	if strings.TrimSpace(f.XDR) == "" {
		return nil, errors.New("fixture has no XDR, so there is nothing to decode")
	}

	decoded, err := lens.DecodeAs(f.XDR, f.Type)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", f.Type, err)
	}
	return renderDecoded(decoded)
}

// Load reads a golden file, choosing the format from its extension. An
// unrecognised extension is read as JSON, which is what every fixture written
// before the other formats existed is.
func Load(path string) (*Fixture, error) {
	raw, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	format := FormatForPath(path)
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		format = JSON
	}
	serializer, err := SerializerFor(format)
	if err != nil {
		return nil, err
	}
	f, err := serializer.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return f, nil
}

// LoadAs reads a golden file in an explicit format.
func LoadAs(path string, format Format) (*Fixture, error) {
	// The format is checked before the file is, so an unknown format is
	// reported as such even when the path does not exist.
	serializer, err := SerializerFor(format)
	if err != nil {
		return nil, err
	}

	raw, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := serializer.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return f, nil
}

// Save writes a golden file, choosing the format from its extension and
// creating the directory if needed.
func Save(path string, f *Fixture) error {
	return SaveAs(path, f, FormatForPath(path))
}

// SaveAs writes a golden file in an explicit format, creating the directory if
// needed.
//
// Output is indented and newline-terminated so that a diff is line-oriented
// and reviewable, which is the reason this format exists at all.
func SaveAs(path string, f *Fixture, format Format) error {
	serializer, err := SerializerFor(format)
	if err != nil {
		return err
	}
	data, err := serializer.Marshal(f)
	if err != nil {
		return err
	}
	return WriteFile(path, data)
}

// KnownPassphrases maps the network names a capture records to the
// passphrase those networks sign with.
//
// Only public networks are listed. A standalone or private network has a
// passphrase of its own, which the capture source reports or the caller
// supplies.
var KnownPassphrases = map[string]string{
	"testnet":   network.TestNetworkPassphrase,
	"mainnet":   network.PublicNetworkPassphrase,
	"futurenet": network.FutureNetworkPassphrase,
}

// PassphraseFor returns the passphrase of a known network, or an empty
// string for a network it does not recognise.
func PassphraseFor(networkName string) string {
	return KnownPassphrases[networkName]
}

// NewProvenance records a capture at the given time.
//
// The network passphrase is filled in when the network is one of
// KnownPassphrases; for any other network it is left empty for the caller to
// set from the source or from an explicit value.
func NewProvenance(networkName, endpoint string, ledger uint32, txHash string, at time.Time) *Provenance {
	return &Provenance{
		Network:           networkName,
		NetworkPassphrase: PassphraseFor(networkName),
		Ledger:            ledger,
		TxHash:            txHash,
		At:                at.UTC().Format(time.RFC3339),
		Endpoint:          endpoint,
	}
}
