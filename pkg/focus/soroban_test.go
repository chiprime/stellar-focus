package focus_test

import (
	"strings"
	"testing"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/internal/faketb"
	"github.com/stellar-optics/stellar-focus/pkg/focus"
	"github.com/stellar-optics/stellar-focus/pkg/focus/require"
)

// storageErrorVal is the payload of a host error for a contract that read a
// key that is not there: the storage category with the host's missing-value
// code.
func storageErrorVal() xdr.ScVal {
	code := xdr.ScErrorCodeScecMissingValue
	err := xdr.ScError{Type: xdr.ScErrorTypeSceStorage, Code: &code}
	return xdr.ScVal{Type: xdr.ScValTypeScvError, Error: &err}
}

// errorReturnMeta wraps an error return value in the version 3 meta a
// Soroban transaction produces.
func errorReturnMeta(t *testing.T, value xdr.ScVal) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 3,
		V3: &xdr.TransactionMetaV3{
			SorobanMeta: &xdr.SorobanTransactionMeta{ReturnValue: value},
		},
	})
}

// errorReturnMetaV4 wraps an error return value in the version 4 meta newer
// protocols produce, where the return value is optional.
func errorReturnMetaV4(t *testing.T, value xdr.ScVal) string {
	t.Helper()
	return mustMarshal(t, xdr.TransactionMeta{
		V: 4,
		V4: &xdr.TransactionMetaV4{
			SorobanMeta: &xdr.SorobanTransactionMetaV2{ReturnValue: &value},
		},
	})
}

// errorResponse is the RPC getTransaction response for a call that died with
// the given error, with the host's account of it flattened into
// diagnosticEventsXdr as the endpoint returns it.
func errorResponse(t *testing.T, value xdr.ScVal) rpcproto.GetTransactionResponse {
	t.Helper()

	return rpcproto.GetTransactionResponse{
		TransactionDetails: rpcproto.TransactionDetails{
			Status:              "FAILED",
			ResultXDR:           fixture(t, "tx_failed.res"),
			DiagnosticEventsXDR: []string{mustMarshal(t, errorEvent(value, false))},
		},
	}
}

// TestAssertContractErrorPasses covers the carriers an error can arrive in
// and the two places a meta records it: the Soroban return value and the
// host's error diagnostic.
func TestAssertContractErrorPasses(t *testing.T) {
	t.Parallel()

	contractError := errorReturnMeta(t, hostErrorVal(101))
	storageError := errorReturnMeta(t, storageErrorVal())

	trappedMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("transfer", false),
		errorEvent(hostErrorVal(4), false))

	trappedStorageMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		fnCall("withdraw", false),
		errorEvent(storageErrorVal(), false))

	response := errorResponse(t, hostErrorVal(4))

	tests := []struct {
		name  string
		value focus.Value
		want  xdr.ScErrorType
		code  uint32
	}{
		{
			name:  "contract error in the return value",
			value: contractError,
			want:  xdr.ScErrorTypeSceContract,
			code:  101,
		},
		{
			name:  "host error in the return value",
			value: storageError,
			want:  xdr.ScErrorTypeSceStorage,
			code:  uint32(xdr.ScErrorCodeScecMissingValue),
		},
		{
			name:  "contract error in the diagnostics",
			value: trappedMeta,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
		{
			name:  "host error in the diagnostics",
			value: trappedStorageMeta,
			want:  xdr.ScErrorTypeSceStorage,
			code:  uint32(xdr.ScErrorCodeScecMissingValue),
		},
		{
			name:  "contract error in a version 4 meta",
			value: errorReturnMetaV4(t, hostErrorVal(101)),
			want:  xdr.ScErrorTypeSceContract,
			code:  101,
		},
		{
			name: "contract error in a version 4 meta's diagnostics",
			value: diagnosticMetaV4(t, pinChanges(t, testDiagnosticLedger),
				errorEvent(hostErrorVal(4), false)),
			want: xdr.ScErrorTypeSceContract,
			code: 4,
		},
		{
			name: "Soroban section on its own",
			value: xdr.SorobanTransactionMeta{
				ReturnValue: hostErrorVal(101),
			},
			want: xdr.ScErrorTypeSceContract,
			code: 101,
		},
		{
			name: "trap evidence",
			value: focus.TrapEvidence{
				Result: fixture(t, "tx_failed.res"),
				Meta:   trappedMeta,
			},
			want: xdr.ScErrorTypeSceContract,
			code: 4,
		},
		{
			name:  "RPC response",
			value: response,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
		{
			name:  "pointer to an RPC response",
			value: &response,
			want:  xdr.ScErrorTypeSceContract,
			code:  4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if !focus.AssertContractError(tb, tc.value, tc.want, tc.code) {
				t.Errorf("assertion failed unexpectedly:\n%s", tb.Output())
			}
			if tb.Failed() {
				t.Errorf("assertion reported a failure while returning true:\n%s", tb.Output())
			}
		})
	}
}

// TestAssertContractErrorFailureMessages pins what a failing assertion says.
//
// The message has to name both the error that was found and the one that was
// wanted, in the plain form the issue asks for, because that is the whole
// point of the assertion: a test that fails says which error it got.
func TestAssertContractErrorFailureMessages(t *testing.T) {
	t.Parallel()

	contractError := errorReturnMeta(t, hostErrorVal(101))
	trappedMeta := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger),
		errorEvent(hostErrorVal(4), false))

	noError := diagnosticMeta(t, pinChanges(t, testDiagnosticLedger), fnCall("transfer", false))

	tests := []struct {
		name     string
		call     func(focus.TB) bool
		contains []string
	}{
		{
			name: "wrong code",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceContract, 404)
			},
			contains: []string{
				"AssertContractError: got ScErrorTypeContract(101), want ScErrorTypeContract(404)",
			},
		},
		{
			name: "wrong category",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceStorage, uint32(xdr.ScErrorCodeScecMissingValue))
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorTypeStorage(MissingValue)",
			},
		},
		{
			name: "wrong code in the diagnostics",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, trappedMeta,
					xdr.ScErrorTypeSceContract, 99)
			},
			contains: []string{
				"got ScErrorTypeContract(4), want ScErrorTypeContract(99)",
			},
		},
		{
			name: "no error recorded",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, noError,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"the invocation recorded no error",
				"ScErrorTypeContract(4)",
			},
		},
		{
			name: "a bare transaction result",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, fixture(t, "tx_failed.res"),
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"a transaction result records that an operation failed but not which error it was",
				"transaction meta",
			},
		},
		{
			name: "a value that cannot be read",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, 42,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"meta value could not be read",
				"unsupported type int",
			},
		},
		{
			name: "a nil value",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, nil,
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"value is nil"},
		},
		{
			name: "a response without diagnostics",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, rpcproto.GetTransactionResponse{},
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{
				"carries no diagnostic events",
				"diagnosticEventsXdr",
			},
		},
		{
			name: "a nil evidence pointer",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, (*focus.TrapEvidence)(nil),
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"nil *TrapEvidence"},
		},
		{
			name: "evidence without a meta",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, focus.TrapEvidence{},
					xdr.ScErrorTypeSceContract, 4)
			},
			contains: []string{"the trap evidence carries no meta"},
		},
		{
			name: "an error outside the protocol's categories",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorType(42), 4)
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorType(42)(4)",
			},
		},
		{
			name: "a code the protocol does not define",
			call: func(tb focus.TB) bool {
				return focus.AssertContractError(tb, contractError,
					xdr.ScErrorTypeSceStorage, 999)
			},
			contains: []string{
				"got ScErrorTypeContract(101), want ScErrorTypeStorage(999)",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tb := faketb.New()
			if tc.call(tb) {
				t.Fatal("assertion passed, want a failure")
			}
			msg, err := tb.Message()
			if err != nil {
				t.Fatalf("reading the failure message: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("failure message does not contain %q:\n%s", want, msg)
				}
			}
			if tb.Helpers == 0 {
				t.Error("assertion did not mark itself as a helper, so file:line points into the library")
			}
		})
	}
}

// TestContractErrorRequiresFatalf is the documented difference between the
// two packages: require stops the test, and its message is unchanged.
func TestContractErrorRequiresFatalf(t *testing.T) {
	t.Parallel()

	meta := errorReturnMeta(t, hostErrorVal(101))

	tb := faketb.New()
	require.ContractError(tb, meta, xdr.ScErrorTypeSceContract, 404)
	if len(tb.Fatals) != 1 || len(tb.Errors) != 0 {
		t.Fatalf("require reported through Errorf; want one Fatalf (errors=%d fatals=%d)",
			len(tb.Errors), len(tb.Fatals))
	}

	msg, err := tb.Message()
	if err != nil {
		t.Fatalf("reading the failure message: %v", err)
	}
	if !strings.Contains(msg, "got ScErrorTypeContract(101), want ScErrorTypeContract(404)") {
		t.Errorf("require lost the error comparison:\n%s", msg)
	}
}
