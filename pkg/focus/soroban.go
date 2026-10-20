package focus

import (
	"fmt"
	"math/big"
	"strings"

	rpcproto "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/stellar-optics/stellar-focus/pkg/simulate"
)

// contractIDString renders a contract id as its strkey "C…" form.
//
// ContractId is a bare 32-byte hash in the generated code, so the strkey
// encoding has to be applied here rather than read off a helper.
func contractIDString(id xdr.ContractId) (string, error) {
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

// Event describes a contract event to look for.
//
// A zero field matches anything, so asserting only that a contract emitted
// something is as easy as asserting on the exact payload. Topics and Data
// accept the same forms as any other value in this package, plus a plain
// string for the common case of a symbol topic:
//
//	focus.AssertEventEmitted(t, meta, focus.Event{
//	    ContractID: "CBGSB…",
//	    Topics:     []any{"transfer", from, to},
//	})
type Event struct {
	// ContractID is the strkey contract address. Empty matches any contract.
	ContractID string
	// Topics are the event topics, in order. Nil matches any topics. A nil
	// element matches any single topic, so []any{"transfer", nil} matches a
	// two-topic event whose first topic is the symbol "transfer".
	Topics []any
	// Data is the event payload. Nil matches any payload.
	Data any
}

// AssertEventEmitted checks that a transaction meta contains a matching
// contract event.
func AssertEventEmitted(t TB, meta Value, want Event) bool {
	t.Helper()

	events, ok := contractEvents(t, "AssertEventEmitted", meta)
	if !ok {
		return false
	}

	for _, e := range events {
		if eventMatches(e, want) {
			return true
		}
	}

	t.Errorf("AssertEventEmitted: no event matched %s\n\n%s",
		describeWantedEvent(want), indent(describeEvents(events), "  "))
	return false
}

// AssertEventCount checks how many contract events a meta carries.
func AssertEventCount(t TB, meta Value, want int) bool {
	t.Helper()

	events, ok := contractEvents(t, "AssertEventCount", meta)
	if !ok {
		return false
	}
	if len(events) != want {
		t.Errorf("AssertEventCount: meta has %d event(s), want %d\n\n%s",
			len(events), want, indent(describeEvents(events), "  "))
		return false
	}
	return true
}

// AssertNoEvents checks that a transaction emitted no contract events.
func AssertNoEvents(t TB, meta Value) bool {
	t.Helper()
	return AssertEventCount(t, meta, 0)
}

// AssertReturnValue checks the value a contract invocation returned.
func AssertReturnValue(t TB, meta Value, want Value) bool {
	t.Helper()

	soroban, ok := sorobanMeta(t, "AssertReturnValue", meta)
	if !ok {
		return false
	}

	wantVal, ok := scVal(want)
	if !ok {
		t.Errorf("AssertReturnValue: want value could not be read as an ScVal")
		return false
	}

	if soroban.ReturnValue.Equals(wantVal) {
		return true
	}

	t.Errorf("AssertReturnValue: contract returned %s, want %s",
		describeScVal(soroban.ReturnValue), describeScVal(wantVal))
	return false
}

// AssertContractTrapped checks that a transaction failed because the contract
// itself trapped, rather than being rejected by the protocol.
//
// This is the Soroban failure developers hit most, and distinguishing it from
// a malformed transaction is usually the first question worth answering.
func AssertContractTrapped(t TB, result Value) bool {
	t.Helper()
	return contractTrapped(t, "AssertContractTrapped", result)
}

// AssertSimulationSucceeded checks that a simulation ran without the host
// rejecting it.
//
// The host rejects a simulation when the transaction would not run: a failing
// precondition, a missing contract, a trap in the invocation. The rejection
// message is the host's own, so it says what went wrong.
//
// The result comes from simulate.Simulate, which never talks to the network
// itself — this package holds no network code.
func AssertSimulationSucceeded(t TB, result *simulate.SimulateResult) bool {
	t.Helper()

	if result == nil {
		t.Errorf("AssertSimulationSucceeded: there is no simulation result")
		return false
	}
	if result.Error != "" {
		t.Errorf("AssertSimulationSucceeded: the simulation was rejected: %s", result.Error)
		return false
	}
	return true
}

// AssertSimulatedReturnValue checks the value a simulated invocation would
// return.
//
// The simulation ran against the ledger as it was when it ran, so this is a
// check on the shape of the answer rather than on a stored fixture.
func AssertSimulatedReturnValue(t TB, result *simulate.SimulateResult, want Value) bool {
	t.Helper()

	if result == nil {
		t.Errorf("AssertSimulatedReturnValue: there is no simulation result")
		return false
	}
	if result.Error != "" {
		t.Errorf("AssertSimulatedReturnValue: the simulation was rejected: %s", result.Error)
		return false
	}
	if result.ReturnValue == nil {
		t.Errorf("AssertSimulatedReturnValue: the simulation returned no value")
		return false
	}

	wantVal, ok := scVal(want)
	if !ok {
		t.Errorf("AssertSimulatedReturnValue: want value could not be read as an ScVal")
		return false
	}

	if result.ReturnValue.Equals(wantVal) {
		return true
	}

	t.Errorf("AssertSimulatedReturnValue: the simulation returned %s, want %s",
		describeScVal(*result.ReturnValue), describeScVal(wantVal))
	return false
}

// AssertInstructionsBelow checks that a simulation stayed under an
// instruction budget.
//
// The budget is exclusive: a transaction that uses exactly the limit is over
// it. Instructions are counted against the ledger state of the moment, so the
// number moves with the chain — this is a ceiling to catch a regression, not
// an exact figure to pin.
func AssertInstructionsBelow(t TB, result *simulate.SimulateResult, limit uint32) bool {
	t.Helper()

	if result == nil {
		t.Errorf("AssertInstructionsBelow: there is no simulation result")
		return false
	}
	if result.Error != "" {
		t.Errorf("AssertInstructionsBelow: the simulation was rejected: %s", result.Error)
		return false
	}
	if result.Instructions >= limit {
		t.Errorf("AssertInstructionsBelow: the simulation used %d instruction(s), want fewer than %d",
			result.Instructions, limit)
		return false
	}
	return true
// AssertContractError checks that a contract invocation failed with a
// particular error, and that the error is the one the test expects.
//
// A Soroban contract does not fail with a bare "the contract trapped". It
// fails with an error the host records in full: a category and a code. A
// contract that panics with a code of its own fails with ScErrorTypeContract
// and the number it chose; a contract that reads a key that is not there fails
// with ScErrorTypeStorage and the host's ScErrorCodeMissingValue.
// AssertContractTrapped says that the invocation trapped; this assertion says
// which error it trapped with.
//
// metaOrResult accepts:
//
//	TransactionMeta                  the meta, whose Soroban section holds the
//	                                  return value and the diagnostic events;
//	SorobanTransactionMeta           the Soroban section on its own;
//	TrapEvidence                     a result paired with the meta that holds
//	                                  the diagnostics;
//	protocol.GetTransactionResponse  the Soroban RPC response, whose
//	                                  diagnosticEventsXdr carries the error.
//
// The error is read from the Soroban return value when that is an error, and
// from the host's error event otherwise — the error event is where the error
// of a trapped invocation lives.
//
// wantCode is the number the error carries: a contract's own code for
// ScErrorTypeContract, and the numeric value of an xdr.ScErrorCode for the
// host's categories, so a missing value is
// uint32(xdr.ScErrorCodeScecMissingValue).
func AssertContractError(t TB, metaOrResult Value, wantType xdr.ScErrorType, wantCode uint32) bool {
	t.Helper()
	const assertion = "AssertContractError"

	got, found, ok := contractError(t, assertion, metaOrResult)
	if !ok {
		return false
	}
	if !found {
		t.Errorf("%s: the invocation recorded no error, so there is nothing to compare with %s.\n"+
			"A contract error is recorded in the transaction meta — as the Soroban return value or as "+
			"the host's error diagnostic — so pass the meta, or the response that carries it.",
			assertion, describeScErrorCode(wantType, wantCode))
		return false
	}

	gotType, gotCode, gotHasCode := scErrorParts(got)
	if gotType == wantType && gotHasCode && gotCode == wantCode {
		return true
	}

	t.Errorf("%s: got %s, want %s", assertion,
		describeScErrorParts(gotType, gotCode, gotHasCode),
		describeScErrorCode(wantType, wantCode))
	return false
}

// contractError finds the error an invocation recorded, and reports whether
// one was found.
//
// The switch is the abstraction seam: another carrier — a Horizon response, a
// bundle record — is one more case here, and the assertion above never needs
// to know which one was used.
func contractError(t TB, assertion string, v Value) (xdr.ScError, bool, bool) {
	t.Helper()

	switch value := v.(type) {
	case rpcproto.GetTransactionResponse:
		return contractErrorFromResponse(t, assertion, &value)

	case *rpcproto.GetTransactionResponse:
		if value == nil {
			t.Errorf("%s: value is a nil *protocol.GetTransactionResponse", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromResponse(t, assertion, value)

	case TrapEvidence:
		if value.Meta == nil {
			t.Errorf("%s: the trap evidence carries no meta, and the error of a failed invocation "+
				"lives in the meta's return value or diagnostics", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromMetaValue(t, assertion, value.Meta)

	case *TrapEvidence:
		if value == nil {
			t.Errorf("%s: value is a nil *TrapEvidence", assertion)
			return xdr.ScError{}, false, false
		}
		if value.Meta == nil {
			t.Errorf("%s: the trap evidence carries no meta, and the error of a failed invocation "+
				"lives in the meta's return value or diagnostics", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromMetaValue(t, assertion, value.Meta)

	case xdr.SorobanTransactionMeta:
		meta := value
		return contractErrorFromSorobanMeta(t, assertion, &meta)

	case *xdr.SorobanTransactionMeta:
		if value == nil {
			t.Errorf("%s: value is a nil *SorobanTransactionMeta", assertion)
			return xdr.ScError{}, false, false
		}
		return contractErrorFromSorobanMeta(t, assertion, value)

	default:
		return contractErrorFromMetaValue(t, assertion, v)
	}
}

// contractErrorFromMetaValue reads the error out of a transaction meta, in
// any of the forms an assertion accepts.
func contractErrorFromMetaValue(t TB, assertion string, v Value) (xdr.ScError, bool, bool) {
	t.Helper()

	if v == nil {
		t.Errorf("%s: value is nil, so there is no error to check", assertion)
		return xdr.ScError{}, false, false
	}

	decoded, err := coerce(v, "TransactionMeta")
	if err != nil {
		// A transaction result records that the invocation failed but not
		// which error it was, so it is called out rather than reported as an
		// unreadable value.
		if _, resultErr := coerce(v, "TransactionResult"); resultErr == nil {
			t.Errorf("%s: a transaction result records that an operation failed but not which error "+
				"it was.\nThe error is in the transaction meta, so pass the meta, or the getTransaction "+
				"response whose diagnosticEventsXdr carries it.", assertion)
			return xdr.ScError{}, false, false
		}
		t.Errorf("%s: meta value could not be read: %v", assertion, err)
		return xdr.ScError{}, false, false
	}

	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta", assertion, decoded.Type)
		return xdr.ScError{}, false, false
	}
	return contractErrorFromMeta(t, assertion, m)
}

// contractErrorFromMeta reads the error a transaction meta records.
//
// The return value comes first: it is the value the invocation itself
// produced, where the diagnostic stream is the host's account of it. The
// error event is the fallback, because a trapped invocation leaves an error
// there and nowhere else.
func contractErrorFromMeta(t TB, assertion string, m *xdr.TransactionMeta) (xdr.ScError, bool, bool) {
	t.Helper()

	if ret, ok := metaReturnValue(m); ok {
		if e, ok := scValError(ret); ok {
			return e, true, true
		}
	}

	var (
		found bool
		err   xdr.ScError
	)
	if !guard(t, assertion, func() {
		raw, readErr := m.GetDiagnosticEvents()
		if readErr != nil {
			t.Errorf("%s: reading the diagnostic events: %v", assertion, readErr)
			return
		}
		for _, e := range decorateDiagnostics(raw) {
			if e.Kind != DiagnosticError {
				continue
			}
			scErr, isError := scValError(e.Data)
			if !isError {
				continue
			}
			err, found = scErr, true
			return
		}
	}) {
		return xdr.ScError{}, false, false
	}
	if !found {
		return xdr.ScError{}, false, true
	}
	return err, true, true
}

// contractErrorFromSorobanMeta reads the error out of a Soroban section.
func contractErrorFromSorobanMeta(t TB, assertion string, meta *xdr.SorobanTransactionMeta) (xdr.ScError, bool, bool) {
	t.Helper()

	if e, ok := scValError(meta.ReturnValue); ok {
		return e, true, true
	}
	return xdr.ScError{}, false, true
}

// contractErrorFromResponse reads the error out of a Soroban RPC
// getTransaction response, whose diagnostic events carry it.
func contractErrorFromResponse(t TB, assertion string, resp *rpcproto.GetTransactionResponse) (xdr.ScError, bool, bool) {
	t.Helper()

	if len(resp.DiagnosticEventsXDR) == 0 {
		t.Errorf("%s: the transaction response carries no diagnostic events, so there is no error "+
			"to check.\nA Soroban RPC reports a failed contract call's error in diagnosticEventsXdr; "+
			"without it this check cannot be made.", assertion)
		return xdr.ScError{}, false, false
	}

	var (
		events []xdr.DiagnosticEvent
		err    error
	)
	if !guard(t, assertion, func() {
		events, err = decodeDiagnosticEvents(resp.DiagnosticEventsXDR)
	}) {
		return xdr.ScError{}, false, false
	}
	if err != nil {
		t.Errorf("%s: reading the response's diagnostic events: %v", assertion, err)
		return xdr.ScError{}, false, false
	}

	for _, e := range decorateDiagnostics(events) {
		if e.Kind != DiagnosticError {
			continue
		}
		scErr, isError := scValError(e.Data)
		if !isError {
			continue
		}
		return scErr, true, true
	}
	return xdr.ScError{}, false, true
}

// metaReturnValue returns the Soroban return value a transaction meta
// records, and whether the meta has one at all.
//
// A version 4 meta keeps the return value in the version 2 Soroban section,
// where it is optional; a version 3 meta keeps it in the version 1 section,
// where it is always present.
func metaReturnValue(m *xdr.TransactionMeta) (xdr.ScVal, bool) {
	switch m.V {
	case 3:
		v3, ok := m.GetV3()
		if !ok || v3.SorobanMeta == nil {
			return xdr.ScVal{}, false
		}
		return v3.SorobanMeta.ReturnValue, true
	case 4:
		v4, ok := m.GetV4()
		if !ok || v4.SorobanMeta == nil || v4.SorobanMeta.ReturnValue == nil {
			return xdr.ScVal{}, false
		}
		return *v4.SorobanMeta.ReturnValue, true
	default:
		return xdr.ScVal{}, false
	}
}

// scValError reads the error an ScVal carries, and reports whether it is one.
func scValError(v xdr.ScVal) (xdr.ScError, bool) {
	if v.Type != xdr.ScValTypeScvError {
		return xdr.ScError{}, false
	}
	e, ok := v.GetError()
	if !ok {
		return xdr.ScError{}, false
	}
	return e, true
}

// scErrorParts splits an error into the category and the number it carries.
//
// A contract error carries the number the contract chose; the host's
// categories carry one of the protocol's named codes. An error that carries
// neither is malformed, which the third return value reports.
func scErrorParts(e xdr.ScError) (xdr.ScErrorType, uint32, bool) {
	if e.Type == xdr.ScErrorTypeSceContract {
		if e.ContractCode == nil {
			return e.Type, 0, false
		}
		return e.Type, uint32(*e.ContractCode), true
	}
	if e.Code == nil {
		return e.Type, 0, false
	}
	return e.Type, uint32(int32(*e.Code)), true
}

// describeScErrorCode renders a wanted error as "ScErrorTypeContract(101)".
//
// The host's categories carry a named code, so those are named rather than
// numbered: "ScErrorTypeStorage(MissingValue)" says what went wrong, where a
// bare 3 would not.
func describeScErrorCode(t xdr.ScErrorType, code uint32) string {
	return describeScErrorParts(t, code, true)
}

// describeScErrorParts renders an error as "ScErrorTypeContract(101)", naming
// the category and the code the error carries.
func describeScErrorParts(t xdr.ScErrorType, code uint32, hasCode bool) string {
	name := scErrorTypeName(t)
	if !hasCode {
		return name + "(no code)"
	}
	if t == xdr.ScErrorTypeSceContract {
		return fmt.Sprintf("%s(%d)", name, code)
	}
	// A category the protocol does not define has no named codes either, so
	// the number is all there is to show.
	if t.String() == "" {
		return fmt.Sprintf("%s(%d)", name, code)
	}
	if named := scErrorCodeName(code); named != "" {
		return fmt.Sprintf("%s(%s)", name, named)
	}
	return fmt.Sprintf("%s(%d)", name, code)
}

// scErrorTypeName names an error category in plain English: the contract's
// own category is ScErrorTypeContract, not ScErrorTypeSceContract. A value
// outside the enum is named by number rather than left blank.
func scErrorTypeName(t xdr.ScErrorType) string {
	name := strings.TrimPrefix(t.String(), "ScErrorTypeSce")
	if name == "" {
		return fmt.Sprintf("ScErrorType(%d)", int32(t))
	}
	return "ScErrorType" + name
}

// scErrorCodeName names a host error code, and returns "" for a number the
// protocol does not define.
func scErrorCodeName(code uint32) string {
	name := strings.TrimPrefix(xdr.ScErrorCode(int32(code)).String(), "ScErrorCodeScec")
	if name == "" {
		return ""
	}
	return name
}

// Events returns the contract events in a meta, as an escape hatch for checks
// this package does not provide. It reports a test failure and returns nil
// when the meta cannot be read.
func Events(t TB, meta Value) []Event {
	t.Helper()

	raw, ok := contractEvents(t, "Events", meta)
	if !ok {
		return nil
	}
	out := make([]Event, 0, len(raw))
	for _, e := range raw {
		out = append(out, toEvent(e))
	}
	return out
}

// sorobanMeta extracts the Soroban section of a transaction meta.
func sorobanMeta(t TB, assertion string, meta Value) (*xdr.SorobanTransactionMeta, bool) {
	t.Helper()

	decoded, ok := resolve(t, assertion, "", meta, "TransactionMeta")
	if !ok {
		return nil, false
	}

	m, ok := decoded.Raw.(*xdr.TransactionMeta)
	if !ok {
		t.Errorf("%s: value is a %s, want a TransactionMeta", assertion, decoded.Type)
		return nil, false
	}

	var soroban *xdr.SorobanTransactionMeta
	if !guard(t, assertion, func() {
		if v3, has := m.GetV3(); has {
			soroban = v3.SorobanMeta
		}
	}) {
		return nil, false
	}
	if soroban == nil {
		t.Errorf("%s: this transaction meta carries no Soroban section, so it has no "+
			"contract events or return value (was the transaction a contract invocation?)", assertion)
		return nil, false
	}
	return soroban, true
}

// contractEvents returns the contract events in a meta.
func contractEvents(t TB, assertion string, meta Value) ([]xdr.ContractEvent, bool) {
	t.Helper()
	soroban, ok := sorobanMeta(t, assertion, meta)
	if !ok {
		return nil, false
	}
	return soroban.Events, true
}

// eventMatches reports whether an event satisfies a matcher.
func eventMatches(e xdr.ContractEvent, want Event) bool {
	if want.ContractID != "" {
		if e.ContractId == nil {
			return false
		}
		id, err := contractIDString(*e.ContractId)
		if err != nil || id != want.ContractID {
			return false
		}
	}

	body, ok := e.Body.GetV0()
	if !ok {
		return false
	}

	if want.Topics != nil {
		if len(want.Topics) != len(body.Topics) {
			return false
		}
		for i, wt := range want.Topics {
			if wt == nil {
				continue
			}
			wv, ok := scVal(wt)
			if !ok || !body.Topics[i].Equals(wv) {
				return false
			}
		}
	}

	if want.Data != nil {
		wv, ok := scVal(want.Data)
		if !ok || !body.Data.Equals(wv) {
			return false
		}
	}
	return true
}

// scVal converts an accepted value into an ScVal.
//
// A plain string is treated as a symbol, because a symbol topic is by far the
// most common thing a test writes, and requiring the base64 of
// ScVal{Sym:"transfer"} would make the common case unreadable.
func scVal(v Value) (xdr.ScVal, bool) {
	switch val := v.(type) {
	case xdr.ScVal:
		return val, true
	case *xdr.ScVal:
		if val == nil {
			return xdr.ScVal{}, false
		}
		return *val, true
	case string:
		// Try base64 XDR first; fall back to treating it as a symbol.
		decoded, err := coerce(val, "ScVal")
		if err == nil {
			if sv, ok := decoded.Raw.(*xdr.ScVal); ok {
				return *sv, true
			}
		}
		sym := xdr.ScSymbol(val)
		return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}, true
	default:
		decoded, err := coerce(v, "ScVal")
		if err != nil {
			return xdr.ScVal{}, false
		}
		sv, ok := decoded.Raw.(*xdr.ScVal)
		if !ok {
			return xdr.ScVal{}, false
		}
		return *sv, true
	}
}

// toEvent converts an XDR event into the matcher-shaped form returned by
// Events.
func toEvent(e xdr.ContractEvent) Event {
	out := Event{}
	if e.ContractId != nil {
		if id, err := contractIDString(*e.ContractId); err == nil {
			out.ContractID = id
		}
	}
	if body, ok := e.Body.GetV0(); ok {
		for _, tpc := range body.Topics {
			out.Topics = append(out.Topics, tpc)
		}
		out.Data = body.Data
	}
	return out
}

// describeEvents renders the events actually present, so a non-match shows
// what was there instead of only what was missing.
func describeEvents(events []xdr.ContractEvent) string {
	if len(events) == 0 {
		return "the transaction emitted no contract events"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "events present (%d):\n", len(events))
	for i, e := range events {
		contract := "(no contract)"
		if e.ContractId != nil {
			if id, err := contractIDString(*e.ContractId); err == nil {
				contract = shorten(id)
			}
		}
		fmt.Fprintf(&b, "  [%d] %s", i, contract)

		if body, ok := e.Body.GetV0(); ok {
			topics := make([]string, 0, len(body.Topics))
			for _, tpc := range body.Topics {
				topics = append(topics, describeScVal(tpc))
			}
			fmt.Fprintf(&b, "  topics[%s]  data %s", strings.Join(topics, ", "), describeScVal(body.Data))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeWantedEvent renders a matcher, naming which fields were wildcards
// so a failing match is diagnosable.
func describeWantedEvent(want Event) string {
	var parts []string
	if want.ContractID != "" {
		parts = append(parts, "contract "+shorten(want.ContractID))
	}
	if want.Topics != nil {
		topics := make([]string, 0, len(want.Topics))
		for _, tpc := range want.Topics {
			if tpc == nil {
				topics = append(topics, "*")
				continue
			}
			if sv, ok := scVal(tpc); ok {
				topics = append(topics, describeScVal(sv))
				continue
			}
			topics = append(topics, fmt.Sprintf("%v", tpc))
		}
		parts = append(parts, "topics["+strings.Join(topics, ", ")+"]")
	}
	if want.Data != nil {
		if sv, ok := scVal(want.Data); ok {
			parts = append(parts, "data "+describeScVal(sv))
		}
	}
	if len(parts) == 0 {
		return "any event"
	}
	return strings.Join(parts, "  ")
}

// describeScVal renders an ScVal compactly for a one-line event listing.
func describeScVal(v xdr.ScVal) string {
	switch v.Type {
	case xdr.ScValTypeScvSymbol:
		if s, ok := v.GetSym(); ok {
			return fmt.Sprintf("%q", string(s))
		}
	case xdr.ScValTypeScvString:
		if s, ok := v.GetStr(); ok {
			return fmt.Sprintf("%q", string(s))
		}
	case xdr.ScValTypeScvU32:
		if n, ok := v.GetU32(); ok {
			return fmt.Sprintf("u32(%d)", n)
		}
	case xdr.ScValTypeScvI32:
		if n, ok := v.GetI32(); ok {
			return fmt.Sprintf("i32(%d)", n)
		}
	case xdr.ScValTypeScvU64:
		if n, ok := v.GetU64(); ok {
			return fmt.Sprintf("u64(%d)", n)
		}
	case xdr.ScValTypeScvI64:
		if n, ok := v.GetI64(); ok {
			return fmt.Sprintf("i64(%d)", n)
		}
	case xdr.ScValTypeScvBool:
		if b, ok := v.GetB(); ok {
			return fmt.Sprintf("%t", b)
		}
	case xdr.ScValTypeScvVoid:
		return "void"
	case xdr.ScValTypeScvAddress:
		if a, ok := v.GetAddress(); ok {
			if s, err := a.String(); err == nil {
				return shorten(s)
			}
		}
	case xdr.ScValTypeScvU128:
		if n, ok := v.GetU128(); ok {
			return fmt.Sprintf("u128(%s)", u128String(n))
		}
	case xdr.ScValTypeScvI128:
		if n, ok := v.GetI128(); ok {
			return fmt.Sprintf("i128(%s)", i128String(n))
		}
	case xdr.ScValTypeScvBytes:
		if b, ok := v.GetBytes(); ok {
			return describeBytes(b)
		}
	case xdr.ScValTypeScvVec:
		if vec, ok := v.GetVec(); ok {
			return describeScVec(vec)
		}
	case xdr.ScValTypeScvMap:
		if m, ok := v.GetMap(); ok {
			return describeScMap(m)
		}
	case xdr.ScValTypeScvContractInstance:
		return "contract instance"
	case xdr.ScValTypeScvLedgerKeyContractInstance:
		return "contract instance key"
	case xdr.ScValTypeScvLedgerKeyNonce:
		return "nonce key"
	case xdr.ScValTypeScvError:
		if e, ok := v.GetError(); ok {
			return describeScError(e)
		}
	case xdr.ScValTypeScvTimepoint:
		if t, ok := v.GetTimepoint(); ok {
			return fmt.Sprintf("timepoint(%d)", t)
		}
	case xdr.ScValTypeScvDuration:
		if d, ok := v.GetDuration(); ok {
			return fmt.Sprintf("duration(%d)", d)
		}
	}
	return strings.TrimPrefix(v.Type.String(), "ScValTypeScv")
}

// describeScVec renders a vector compactly, truncating long ones so a large
// payload does not dominate a failure message.
func describeScVec(vec *xdr.ScVec) string {
	if vec == nil || len(*vec) == 0 {
		return "vec[]"
	}
	const maxShown = 3
	parts := make([]string, 0, maxShown+1)
	for i, element := range *vec {
		if i == maxShown {
			parts = append(parts, fmt.Sprintf("… +%d", len(*vec)-maxShown))
			break
		}
		parts = append(parts, describeScVal(element))
	}
	return "vec[" + strings.Join(parts, ", ") + "]"
}

// describeScMap renders a map compactly, truncating long ones.
func describeScMap(m *xdr.ScMap) string {
	if m == nil || len(*m) == 0 {
		return "map{}"
	}
	const maxShown = 3
	parts := make([]string, 0, maxShown+1)
	for i, entry := range *m {
		if i == maxShown {
			parts = append(parts, fmt.Sprintf("… +%d", len(*m)-maxShown))
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s",
			describeScVal(entry.Key), describeScVal(entry.Val)))
	}
	return "map{" + strings.Join(parts, ", ") + "}"
}

// describeBytes renders a byte array as hex, truncated so a large payload does
// not dominate a failure message.
func describeBytes(b xdr.ScBytes) string {
	const maxShown = 8
	if len(b) == 0 {
		return "bytes(0x)"
	}
	if len(b) > maxShown {
		return fmt.Sprintf("bytes(0x%x…, %d bytes)", b[:maxShown], len(b))
	}
	return fmt.Sprintf("bytes(0x%x)", b)
}

// u128String renders the two-word unsigned representation as one decimal.
func u128String(parts xdr.UInt128Parts) string {
	value := new(big.Int).SetUint64(uint64(parts.Hi))
	value.Lsh(value, 64)
	value.Or(value, new(big.Int).SetUint64(uint64(parts.Lo)))
	return value.String()
}

// i128String renders the two-word signed representation as one decimal. Lsh on
// the signed high word sign-extends, so adding the low word gives the value.
func i128String(parts xdr.Int128Parts) string {
	value := new(big.Int).SetInt64(int64(parts.Hi))
	value.Lsh(value, 64)
	value.Add(value, new(big.Int).SetUint64(uint64(parts.Lo)))
	return value.String()
}

// shorten abbreviates a strkey for inline display, matching how lens and
// prism abbreviate addresses.
func shorten(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:5] + "…" + s[len(s)-4:]
}
