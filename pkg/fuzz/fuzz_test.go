package fuzz_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/fuzz"
)

// TestCorpusDecodes checks that every seed is real XDR of the type the
// corpus says it is. A seed that does not decode is worse than no seed: it
// teaches a fuzzer nothing.
func TestCorpusDecodes(t *testing.T) {
	t.Parallel()

	for _, seed := range fuzz.Seeds() {
		t.Run(seed.Name, func(t *testing.T) {
			t.Parallel()

			switch seed.Type {
			case "TransactionEnvelope":
				var env xdr.TransactionEnvelope
				if err := xdr.SafeUnmarshal(seed.Bytes, &env); err != nil {
					t.Fatalf("the seed does not decode as an envelope: %v", err)
				}
			case "TransactionResult":
				var res xdr.TransactionResult
				if err := xdr.SafeUnmarshal(seed.Bytes, &res); err != nil {
					t.Fatalf("the seed does not decode as a result: %v", err)
				}
			case "TransactionMeta":
				var meta xdr.TransactionMeta
				if err := xdr.SafeUnmarshal(seed.Bytes, &meta); err != nil {
					t.Fatalf("the seed does not decode as a meta: %v", err)
				}
			default:
				t.Fatalf("the corpus holds an unknown type %q", seed.Type)
			}
		})
	}
}

func TestCorpusForSelectsByType(t *testing.T) {
	t.Parallel()

	envelopes := fuzz.CorpusFor("TransactionEnvelope")
	if len(envelopes) == 0 {
		t.Fatal("the corpus holds no envelope seeds")
	}
	for _, seed := range envelopes {
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshal(seed, &env); err != nil {
			t.Errorf("an envelope seed does not decode: %v", err)
		}
	}

	// The type name is matched loosely, so a caller who writes it in
	// snake_case still finds the seeds.
	if len(fuzz.CorpusFor("transaction_envelope")) != len(envelopes) {
		t.Errorf("the snake_case spelling found %d seeds, want %d",
			len(fuzz.CorpusFor("transaction_envelope")), len(envelopes))
	}

	if got := fuzz.CorpusFor("NoSuchType"); len(got) != 0 {
		t.Errorf("an unknown type returned %d seeds, want none", len(got))
	}
}

func TestCorpusHoldsEverySeed(t *testing.T) {
	t.Parallel()

	if len(fuzz.Corpus()) != len(fuzz.Seeds()) {
		t.Errorf("the corpus holds %d seeds and %d entries", len(fuzz.Corpus()), len(fuzz.Seeds()))
	}
}

func TestTypesCoverTheMajorOnes(t *testing.T) {
	t.Parallel()

	types := fuzz.Types()
	for _, want := range []string{"TransactionEnvelope", "TransactionMeta", "TransactionResult"} {
		if !contains(types, want) {
			t.Errorf("the corpus does not cover %s: %v", want, types)
		}
	}
}

// TestCorpusMatchesTheFixtures checks that the embedded corpus and the
// fixture files are the same payloads, so a corpus cannot drift away from
// the captures it came from.
func TestCorpusMatchesTheFixtures(t *testing.T) {
	t.Parallel()

	files, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading the corpus directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() || file.Name() == "manifest.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("testdata", file.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", file.Name(), err)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatalf("decoding %s: %v", file.Name(), err)
		}
		if !containsSeed(fuzz.Seeds(), decoded) {
			t.Errorf("%s is in the corpus directory but not in the corpus", file.Name())
		}
	}
}

// contains reports whether a list of strings holds a value.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// containsSeed reports whether the corpus holds a payload.
func containsSeed(seeds []fuzz.Seed, want []byte) bool {
	for _, seed := range seeds {
		if string(seed.Bytes) == string(want) {
			return true
		}
	}
	return false
}

// ExampleCorpus shows the pattern a downstream project follows to seed a
// fuzzer with the corpus.
func ExampleCorpus() {
	for _, seed := range fuzz.CorpusFor("TransactionEnvelope") {
		var env xdr.TransactionEnvelope
		if err := xdr.SafeUnmarshal(seed, &env); err != nil {
			continue
		}
	}
	// Output:
}
