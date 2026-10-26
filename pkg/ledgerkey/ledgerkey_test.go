package ledgerkey_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/ledgerkey"
)

const (
	testAccount = "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7"
	testIssuer  = "GA2JRQOF6EA3HQWDCEDBPPMLYPJCFLDDGYZLEQGMS5SOBQIB3BAFHVAW"
)

// bytesScVal builds an ScVal carrying a byte string.
func bytesScVal(b []byte) xdr.ScVal {
	bytes := xdr.ScBytes(b)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &bytes}
}

func decodeKey(t *testing.T, encoded string) xdr.LedgerKey {
	t.Helper()

	var key xdr.LedgerKey
	if err := xdr.SafeUnmarshalBase64(encoded, &key); err != nil {
		t.Fatalf("decoding ledger key %q: %v", encoded, err)
	}
	return key
}

func TestAccount(t *testing.T) {
	t.Parallel()

	encoded, err := ledgerkey.Account(testAccount)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	key := decodeKey(t, encoded)
	if key.Type != xdr.LedgerEntryTypeAccount {
		t.Fatalf("key type = %s, want Account", key.Type)
	}
	account, ok := key.GetAccount()
	if !ok {
		t.Fatal("account key carries no account arm")
	}
	if account.AccountId.Address() != testAccount {
		t.Errorf("account = %q, want %q", account.AccountId.Address(), testAccount)
	}
}

func TestTrustline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		asset xdr.Asset
	}{
		{"native", xdr.MustNewNativeAsset()},
		{"credit alphanum4", xdr.MustNewCreditAsset("USDC", testIssuer)},
		{"credit alphanum12", xdr.MustNewCreditAsset("LONGASSET", testIssuer)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := ledgerkey.Trustline(testAccount, tc.asset)
			if err != nil {
				t.Fatalf("Trustline: %v", err)
			}

			key := decodeKey(t, encoded)
			if key.Type != xdr.LedgerEntryTypeTrustline {
				t.Fatalf("key type = %s, want Trustline", key.Type)
			}
			line, ok := key.GetTrustLine()
			if !ok {
				t.Fatal("trustline key carries no trustline arm")
			}
			if line.AccountId.Address() != testAccount {
				t.Errorf("account = %q, want %q", line.AccountId.Address(), testAccount)
			}
			if !line.Asset.ToAsset().Equals(tc.asset) {
				t.Errorf("asset = %s, want %s", line.Asset.ToAsset(), tc.asset)
			}
		})
	}
}

func TestContractData(t *testing.T) {
	t.Parallel()

	contractID := strkey.MustEncode(strkey.VersionByteContract, make([]byte, 32))
	hash := xdr.Hash{1, 2, 3}
	contractKey := xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}

	tests := []struct {
		name string
		key  focus.Value
		want xdr.ScVal
	}{
		{
			name: "xdr ScVal",
			key:  contractKey,
			want: contractKey,
		},
		{
			name: "symbol string",
			key:  "counter",
			want: xdr.ScVal{
				Type: xdr.ScValTypeScvSymbol,
				Sym:  func() *xdr.ScSymbol { s := xdr.ScSymbol("counter"); return &s }(),
			},
		},
		{
			name: "base64 ScVal",
			key:  mustMarshalScVal(t, contractKey),
			want: contractKey,
		},
		{
			name: "bytes ScVal",
			key:  bytesScVal(hash[:]),
			want: bytesScVal(hash[:]),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := ledgerkey.ContractData(
				contractID, tc.key, xdr.ContractDataDurabilityPersistent)
			if err != nil {
				t.Fatalf("ContractData: %v", err)
			}

			key := decodeKey(t, encoded)
			if key.Type != xdr.LedgerEntryTypeContractData {
				t.Fatalf("key type = %s, want ContractData", key.Type)
			}
			data, ok := key.GetContractData()
			if !ok {
				t.Fatal("contract data key carries no contract data arm")
			}
			if data.Durability != xdr.ContractDataDurabilityPersistent {
				t.Errorf("durability = %s, want persistent", data.Durability)
			}
			if got := data.Contract.MustContractId(); got != (xdr.ContractId{}) {
				t.Errorf("contract id = %x, want the zero contract", got)
			}
			if !data.Key.Equals(tc.want) {
				t.Errorf("key = %v, want %v", data.Key, tc.want)
			}
		})
	}
}

func TestContractCode(t *testing.T) {
	t.Parallel()

	hash := [32]byte{1, 2, 3, 4}
	encoded, err := ledgerkey.ContractCode(hash)
	if err != nil {
		t.Fatalf("ContractCode: %v", err)
	}

	key := decodeKey(t, encoded)
	if key.Type != xdr.LedgerEntryTypeContractCode {
		t.Fatalf("key type = %s, want ContractCode", key.Type)
	}
	code, ok := key.GetContractCode()
	if !ok {
		t.Fatal("contract code key carries no code arm")
	}
	if code.Hash != xdr.Hash(hash) {
		t.Errorf("hash = %x, want %x", code.Hash, hash)
	}
}

func TestConstructorsProduceComparableCanonicalXDR(t *testing.T) {
	t.Parallel()

	encoded, err := ledgerkey.Account(testAccount)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	expected := xdr.LedgerKey{}
	if err := expected.SetAccount(xdr.MustAddress(testAccount)); err != nil {
		t.Fatalf("building expected key: %v", err)
	}

	tb := faketb.New()
	if !focus.AssertLedgerKeyEqual(tb, expected, encoded) {
		t.Errorf("built key does not equal the direct SDK key:\n%s", tb.Output())
	}
}

func TestInvalidInputs(t *testing.T) {
	t.Parallel()

	invalidAsset := xdr.Asset{Type: xdr.AssetType(9)}
	nilArmAsset := xdr.Asset{Type: xdr.AssetTypeAssetTypeCreditAlphanum4}

	tests := []struct {
		name string
		call func() (string, error)
	}{
		{"invalid account", func() (string, error) { return ledgerkey.Account("not-an-account") }},
		{"invalid trustline account", func() (string, error) {
			return ledgerkey.Trustline("not-an-account", xdr.MustNewNativeAsset())
		}},
		{"invalid trustline asset", func() (string, error) {
			return ledgerkey.Trustline(testAccount, invalidAsset)
		}},
		{"asset without its arm", func() (string, error) {
			return ledgerkey.Trustline(testAccount, nilArmAsset)
		}},
		{"invalid contract id", func() (string, error) {
			return ledgerkey.ContractData("not-a-contract", "counter", xdr.ContractDataDurabilityPersistent)
		}},
		{"invalid durability", func() (string, error) {
			return ledgerkey.ContractData(
				strkey.MustEncode(strkey.VersionByteContract, make([]byte, 32)),
				"counter", xdr.ContractDataDurability(9))
		}},
		{"nil key", func() (string, error) {
			return ledgerkey.ContractData(
				strkey.MustEncode(strkey.VersionByteContract, make([]byte, 32)),
				(*xdr.ScVal)(nil), xdr.ContractDataDurabilityPersistent)
		}},
		{"unsupported key", func() (string, error) {
			return ledgerkey.ContractData(
				strkey.MustEncode(strkey.VersionByteContract, make([]byte, 32)),
				42, xdr.ContractDataDurabilityPersistent)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tc.call(); err == nil {
				t.Fatal("constructor accepted invalid input")
			}
		})
	}
}

func mustMarshalScVal(tb testing.TB, v xdr.ScVal) string {
	tb.Helper()
	encoded, err := xdr.MarshalBase64(v)
	if err != nil {
		tb.Fatalf("marshalling ScVal: %v", err)
	}
	return encoded
}
