// Package fuzz provides seed corpora for fuzzing XDR decoders.
//
// The corpus is the XDR stellar-focus has been reading all along: real
// mainnet transactions, captured and kept as fixtures. A project that wants
// to fuzz its own XDR decoder can seed it with the same payloads, rather
// than capturing the same transactions again or writing a corpus by hand.
//
// The corpus is embedded, so it is available wherever the module is
// imported, with no filesystem path to get wrong:
//
//	func FuzzDecodeEnvelope(f *testing.F) {
//	    for _, seed := range fuzz.CorpusFor("TransactionEnvelope") {
//	        f.Add(seed)
//	    }
//	    f.Fuzz(func(t *testing.T, data []byte) {
//	        var env xdr.TransactionEnvelope
//	        _ = xdr.SafeUnmarshal(data, &env)
//	    })
//	}
//
// The seeds are raw XDR bytes. A fuzzer that wants the base64 form — the
// one the Stellar APIs speak — can encode them itself, or read the fixture
// files from the package's testdata directory.
//
// A corpus is only useful if it covers the types a decoder accepts, so
// Types lists what is here:
//
//	fuzz.Types() // TransactionEnvelope, TransactionMeta, TransactionResult
package fuzz

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

//go:embed testdata
var files embed.FS

// manifestName is the file that describes the embedded corpus.
const manifestName = "testdata/manifest.json"

// Seed is one payload, ready to be handed to a fuzzer.
type Seed struct {
	// Name identifies the seed, e.g. "tx_failed.env".
	Name string
	// Type is the XDR type the payload holds, e.g. "TransactionEnvelope".
	Type string
	// Bytes is the raw XDR.
	Bytes []byte
	// Note says where the payload came from, for a reader wondering why
	// this particular one is in the corpus.
	Note string
}

// manifest is the description of the embedded corpus.
type manifest struct {
	Seeds []struct {
		File string `json:"file"`
		Name string `json:"name"`
		Type string `json:"type"`
		Note string `json:"note"`
	} `json:"seeds"`
}

// seeds is the corpus, read once.
var seeds = loadSeeds()

// loadSeeds reads the embedded corpus.
//
// A corpus that cannot be read is a broken package rather than a broken
// test, so a malformed manifest panics here: the failure happens at import
// time, where it is obvious, rather than in whichever test happened to ask
// for a seed.
func loadSeeds() []Seed {
	raw, err := files.ReadFile(manifestName)
	if err != nil {
		panic(fmt.Sprintf("fuzz: reading the corpus manifest: %v", err))
	}

	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(fmt.Sprintf("fuzz: parsing the corpus manifest: %v", err))
	}

	out := make([]Seed, 0, len(m.Seeds)+1)
	for _, entry := range m.Seeds {
		payload, err := files.ReadFile("testdata/" + entry.File)
		if err != nil {
			panic(fmt.Sprintf("fuzz: reading the corpus file %s: %v", entry.File, err))
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload)))
		if err != nil {
			panic(fmt.Sprintf("fuzz: decoding the corpus file %s: %v", entry.File, err))
		}
		out = append(out, Seed{
			Name:  entry.Name,
			Type:  entry.Type,
			Bytes: decoded,
			Note:  entry.Note,
		})
	}

	// The captured transactions have no captured meta, so one is built here
	// to give a meta decoder a seed that carries the structures only a
	// Soroban meta carries.
	if meta, err := sorobanMetaSeed(); err == nil {
		out = append(out, meta)
	}
	return out
}

// Seeds returns every seed in the corpus, with the names and types that
// describe them.
func Seeds() []Seed {
	out := make([]Seed, len(seeds))
	copy(out, seeds)
	return out
}

// Corpus returns the raw XDR bytes of every seed, suitable for use as a
// fuzz seed corpus.
func Corpus() [][]byte {
	out := make([][]byte, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, seed.Bytes)
	}
	return out
}

// CorpusFor returns the raw XDR bytes of the seeds of one XDR type.
//
// The type name is matched loosely, so "TransactionEnvelope",
// "transaction_envelope" and "Transaction Envelope" all find the same
// seeds. A type the corpus does not hold yields an empty slice rather than
// an error: a fuzzer that asks for seeds it does not need should not fail.
func CorpusFor(typeName string) [][]byte {
	wanted := normalizeType(typeName)
	out := make([][]byte, 0, len(seeds))
	for _, seed := range seeds {
		if normalizeType(seed.Type) == wanted {
			out = append(out, seed.Bytes)
		}
	}
	return out
}

// Types lists the XDR types the corpus covers, in alphabetical order.
func Types() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if seen[seed.Type] {
			continue
		}
		seen[seed.Type] = true
		out = append(out, seed.Type)
	}
	sort.Strings(out)
	return out
}

// normalizeType folds a type name to the form used for comparison.
func normalizeType(name string) string {
	return strings.ToLower(strings.NewReplacer("_", "", " ", "", "-", "").Replace(strings.TrimSpace(name)))
}

// sorobanMetaSeed builds a Soroban transaction meta.
//
// The captured transactions in the corpus have no captured meta, and a meta
// is where the interesting structures live: contract events, ScVal
// payloads and the ledger changes a contract invocation makes. The seed is
// built rather than captured, and it is deterministic, so the corpus does
// not change from run to run.
func sorobanMetaSeed() (Seed, error) {
	symbol := xdr.ScSymbol("transfer")
	fromKey := xdr.ScSymbol("from")
	amountKey := xdr.ScSymbol("amount")

	var contractID xdr.ContractId
	copy(contractID[:], []byte("stellar-focus-fuzz-corpus-id!!"))

	accountID, err := xdr.AddressToAccountId("GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6")
	if err != nil {
		return Seed{}, err
	}
	address := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &accountID}

	amount := xdr.UInt128Parts{Hi: 0, Lo: 1_000_000}
	returned := xdr.Uint32(7)

	entries := xdr.ScMap{
		{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &fromKey},
			Val: xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address},
		},
		{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &amountKey},
			Val: xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &amount},
		},
	}
	mapPtr := &entries

	meta := xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{
				Events: []xdr.ContractEvent{
					{
						Type:       xdr.ContractEventTypeContract,
						ContractId: &contractID,
						Body: xdr.ContractEventBody{
							V: 0,
							V0: &xdr.ContractEventV0{
								Topics: []xdr.ScVal{{Type: xdr.ScValTypeScvSymbol, Sym: &symbol}},
								Data:   xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr},
							},
						},
					},
				},
				ReturnValue: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &returned},
			},
		},
	}

	encoded, err := xdr.MarshalBase64(meta)
	if err != nil {
		return Seed{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Seed{}, err
	}
	return Seed{
		Name:  "soroban.meta",
		Type:  "TransactionMeta",
		Bytes: raw,
		Note:  "a Soroban meta, built from the captured transaction rather than captured with it",
	}, nil
}
