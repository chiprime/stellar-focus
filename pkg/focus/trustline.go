package focus

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// AssertTrustlineModified checks that a transaction modified an account's
// trustline for an asset and left it with expectedLimit as its limit.
//
// account and asset accept the same forms as AssertBalanceChanged; the native
// asset is rejected because it has no trustline. Creating a trustline counts
// as modifying it, since the entry is new to the ledger with that limit.
//
// The limit is in stroops, the raw unit the protocol stores; treating it as a
// human amount is the caller's business. Later modifications win, so a
// transaction that changes the limit twice is checked against its final,
// as-committed state.
func AssertTrustlineModified(t TB, meta Value, account Value, asset Value, expectedLimit int64) bool {
	t.Helper()
	const assertion = "AssertTrustlineModified"

	changes, ok := ledgerChanges(t, assertion, meta)
	if !ok {
		return false
	}
	addr, ok := resolveAccount(t, assertion, "account", account)
	if !ok {
		return false
	}
	spec, ok := resolveAsset(t, assertion, "asset", asset)
	if !ok {
		return false
	}
	if spec.native {
		t.Errorf("AssertTrustlineModified: the native asset has no trustline; " +
			"use AssertBalanceChanged to assert on an account's native balance")
		return false
	}

	mod, ok := findTrustlineModification(t, assertion, changes, addr, spec)
	if !ok {
		return false
	}
	if !mod.modified {
		t.Errorf("AssertTrustlineModified: the transaction did not modify trustline %s on account %s\n\n%s",
			spec.id, shorten(addr), indent(describeAccountChanges(changes, addr)))
		return false
	}
	if int64(mod.limit) == expectedLimit {
		return true
	}

	t.Errorf("AssertTrustlineModified: trustline %s on account %s has limit %s, want %s\n\n  %s",
		spec.id, shorten(addr),
		amountStroops(mod.limit, ""), amountStroops(xdr.Int64(expectedLimit), ""),
		describeTrustlineModification(mod))
	return false
}

// trustlineModification is the outcome of walking the changes for one
// trustline: whether it was touched, and its limit before and after.
type trustlineModification struct {
	modified  bool
	created   bool
	before    xdr.Int64
	hasBefore bool
	limit     xdr.Int64
}

// findTrustlineModification finds the last change to a trustline and the
// state preceding it.
func findTrustlineModification(
	t TB, assertion string, changes []ledgerChange, account string, spec assetSpec,
) (trustlineModification, bool) {
	var mod trustlineModification
	ok := guard(t, assertion, func() {
		for _, c := range changes {
			if c.entryType != xdr.LedgerEntryTypeTrustline || c.account != account || c.asset != spec.id {
				continue
			}
			switch c.kind {
			case xdr.LedgerEntryChangeTypeLedgerEntryState:
				if limit, has := trustlineLimit(c.entry); has {
					mod.before = limit
					mod.hasBefore = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				if limit, has := trustlineLimit(c.entry); has {
					mod.limit = limit
					mod.modified = true
					mod.created = true
				}
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
				xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				if limit, has := trustlineLimit(c.entry); has {
					mod.limit = limit
					mod.modified = true
				}
			}
		}
	})
	if !ok {
		return trustlineModification{}, false
	}
	return mod, true
}

// trustlineLimit reads a trustline entry's limit.
func trustlineLimit(entry *xdr.LedgerEntry) (xdr.Int64, bool) {
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeTrustline || entry.Data.TrustLine == nil {
		return 0, false
	}
	return entry.Data.TrustLine.Limit, true
}

// describeTrustlineModification renders the limit movement, so a mismatch
// shows what the limit moved from as well as to.
func describeTrustlineModification(mod trustlineModification) string {
	switch {
	case mod.created:
		return "new trustline, limit " + amountStroops(mod.limit, "")
	case mod.hasBefore:
		return fmt.Sprintf("limit %s → %s",
			amountStroops(mod.before, ""), amountStroops(mod.limit, ""))
	default:
		return "limit " + amountStroops(mod.limit, "")
	}
}
