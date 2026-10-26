package focus

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Sponsorship assertions identify *who* pays the reserve for a ledger entry.
//
// The protocol records sponsorship in the entry itself: a sponsored entry
// carries the sponsor's account id in its LedgerEntryExtensionV1.SponsoringID,
// and a transaction that creates, transfers or revokes a sponsorship leaves
// that transition in its meta as ordinary ledger entry changes. Reading the
// transition rather than the operation matters, because a revoke-sponsorship
// operation can transfer, establish or revoke a sponsorship depending on the
// entry's state, and the result code says only that the operation succeeded.
//
// Everything here is offline: the meta is the evidence, so no RPC call, ledger
// snapshot or clock is involved, and the same meta gives the same answer every
// run.

// AssertEntrySponsoredBy checks that, after the transaction, the ledger entry
// identified by targetEntry is sponsored by sponsorAccount.
//
// targetEntry accepts the same forms as the entry assertions: an xdr.LedgerKey,
// an xdr.LedgerEntry, or either as base64 XDR, raw bytes or a decoded
// *lens.Value. sponsorAccount accepts a strkey account address, an
// xdr.AccountId or an xdr.MuxedAccount. The sponsor identity is strict — an
// empty or malformed address is reported as an input error, not as a
// mismatch — because "sponsored by nobody in particular" is not a property a
// test should be able to assert by accident.
//
// The entry must have been created, modified or restored by the transaction.
// A removed entry carries no sponsorship to inspect, and a transaction that
// never touched the entry says nothing about who sponsors it; both are
// reported as such rather than treated as a missing sponsor.
//
// A failure names the sponsorship transition the transaction made — for
// example "sponsorship transferred from GA… to GB…" — so "wrong sponsor" and
// "no sponsor at all" never read the same.
func AssertEntrySponsoredBy(t TB, meta Value, targetEntry, sponsorAccount Value) bool {
	t.Helper()
	const assertion = "AssertEntrySponsoredBy"

	want, ok := resolveAccount(t, assertion, "sponsorAccount", sponsorAccount)
	if !ok {
		return false
	}
	key, ok := resolveLedgerKey(t, assertion, targetEntry)
	if !ok {
		return false
	}
	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}

	var (
		entry   *xdr.LedgerEntry
		before  sponsorshipState
		seen    bool
		removed bool
	)
	if !guard(t, assertion, func() {
		for _, c := range changes {
			if c.keyID != key.keyID {
				continue
			}
			seen = true
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if state, has := entrySponsorship(c.entry); has {
					before = state
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated,
				xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				entry = c.entry
				removed = false
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				removed = true
			}
		}
	}) {
		return false
	}

	if !seen {
		t.Errorf("%s: the transaction made no ledger change to %s\n\n%s",
			assertion, key.label, indent(describeChanges(changes)))
		return false
	}
	if removed || entry == nil {
		t.Errorf("%s: the transaction removed %s, which carries no sponsorship to inspect\n\n%s",
			assertion, key.label, indent(describeEntryChanges(changes, key.keyID)))
		return false
	}

	var after sponsorshipState
	if !guard(t, assertion, func() { after, _ = entrySponsorship(entry) }) {
		return false
	}
	if after.sponsored && after.sponsor == want {
		return true
	}

	t.Errorf("%s: %s is %s, want sponsored by %s\n\n  %s\n\n%s",
		assertion, key.label, after.describe(), shorten(want),
		describeSponsorshipTransition(before, after),
		indent(describeEntryChanges(changes, key.keyID)))
	return false
}

// sponsorshipState is the sponsorship an entry carries at one point in the
// meta: whether it is sponsored, and by whom.
type sponsorshipState struct {
	sponsored bool
	sponsor   string
}

// describe names the state for a failure message.
func (s sponsorshipState) describe() string {
	if !s.sponsored {
		return "not sponsored"
	}
	return "sponsored by " + shorten(s.sponsor)
}

// entrySponsorship reads an entry's sponsor from its extension. It calls
// generated accessors, so it must run behind guard.
//
// The V1 extension with a nil SponsoringID is the protocol's way of spelling
// "this entry is not sponsored", and is distinct from an entry carrying no V1
// extension at all; both are reported as unsponsored, because no test can act
// on the difference.
func entrySponsorship(entry *xdr.LedgerEntry) (sponsorshipState, bool) {
	if entry == nil {
		return sponsorshipState{}, false
	}
	ext, ok := entry.Ext.GetV1()
	if !ok || ext.SponsoringId == nil {
		return sponsorshipState{}, true
	}
	addr, err := ext.SponsoringId.GetAddress()
	if err != nil {
		return sponsorshipState{}, true
	}
	return sponsorshipState{sponsored: true, sponsor: addr}, true
}

// describeSponsorshipTransition names what the transaction did to the
// sponsorship, so the failure distinguishes a transfer from an establishment
// or a revocation.
func describeSponsorshipTransition(before, after sponsorshipState) string {
	switch {
	case before.sponsored && after.sponsored && before.sponsor != after.sponsor:
		return fmt.Sprintf("sponsorship transferred from %s to %s",
			shorten(before.sponsor), shorten(after.sponsor))
	case !before.sponsored && after.sponsored:
		return "sponsorship established by " + shorten(after.sponsor)
	case before.sponsored && !after.sponsored:
		return "sponsorship revoked from " + shorten(before.sponsor)
	case before.sponsored && after.sponsored:
		return "sponsorship unchanged (" + after.describe() + ")"
	default:
		return "the entry remains unsponsored"
	}
}

// describeEntryChanges renders only the changes touching one entry, so a
// sponsorship failure shows the state transitions for the entry in question
// rather than the whole meta.
func describeEntryChanges(changes []ledgerChange, keyID string) string {
	matching := make([]ledgerChange, 0, len(changes))
	for _, c := range changes {
		if c.keyID == keyID {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return "no ledger entry change in this meta touches that entry"
	}
	return describeChanges(matching)
}
