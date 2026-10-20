package focus

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
	lensformat "github.com/stellar-optics/stellar-xdr-lens/pkg/lens/format"
)

// Option configures an assertion.
type Option func(*config)

// config holds resolved assertion settings.
type config struct {
	// ignore lists path patterns whose differences are not failures.
	ignore []string
	// sortPatterns are path patterns whose arrays are treated as unordered
	// sets. When enabled, arrays at matching paths are sorted before the
	// comparison so that [sigA, sigB] and [sigB, sigA] are equal.
	sortPatterns []string
	// normalize maps a path pattern to a function rewriting matching values
	// before comparison.
	normalize []normalizer
	// colorMode is "auto", "always" or "never", matching lens.
	colorMode string
	// maxChanges caps how many differences are printed before the rest are
	// summarised.
	maxChanges int
}

type normalizer struct {
	pattern string
	fn      func(any) any
}

func newConfig(opts ...Option) *config {
	c := &config{colorMode: "never", maxChanges: 20}
	for _, o := range opts {
		o(c)
	}
	return c
}

// IgnorePaths excludes differences at matching paths from the comparison.
//
// Patterns match the dotted paths lens reports, such as
// "V1.Tx.Operations[0].Body.PaymentOp.Amount".
//
// A "*" segment matches exactly one path segment, and a "**" segment matches
// any number of segments at any depth. An index may also be wildcarded, as in
// "Operations[*]". Within a segment, "*", "?" and character classes work like
// globs. Matching is case-insensitive, and a pattern is anchored to the tail
// of a path as well as its whole, so none of these need a "V1.Tx." prefix:
//
//	focus.IgnorePaths("**.Signatures")                  // signatures at any depth
//	focus.IgnorePaths("operations[*].sourceaccount")    // per-operation source
//	focus.IgnorePaths("V1.Tx.Operations[*].SourceAccount")
//
// Ignored paths are always named in the failure output. A silently discarded
// difference is how an assertion library stops being trustworthy.
func IgnorePaths(patterns ...string) Option {
	return func(c *config) { c.ignore = append(c.ignore, patterns...) }
}

// IgnoreSequence ignores the transaction sequence number, which changes on
// every submission and is almost never what a test is asserting.
func IgnoreSequence() Option {
	return IgnorePaths("**.SeqNum")
}

// IgnoreSignatures ignores signatures, which differ whenever a test signs
// with a freshly generated key.
func IgnoreSignatures() Option {
	return IgnorePaths("**.Signatures")
}

// IgnoreTimeBounds ignores time bounds, which move with wall-clock time.
func IgnoreTimeBounds() Option {
	return IgnorePaths("**.TimeBounds", "**.MinTime", "**.MaxTime")
}

// IgnoreFee ignores the fee bid and the fee charged.
func IgnoreFee() Option {
	return IgnorePaths("**.Fee", "**.FeeCharged")
}

// IgnoreSourceAccount ignores the transaction and operation source accounts,
// for tests that generate a funding account per run.
func IgnoreSourceAccount() Option {
	return IgnorePaths("**.SourceAccount")
}

// IgnoreVolatile is the common combination: sequence numbers, signatures and
// time bounds. It is what most tests comparing a rebuilt transaction want.
func IgnoreVolatile() Option {
	return func(c *config) {
		IgnoreSequence()(c)
		IgnoreSignatures()(c)
		IgnoreTimeBounds()(c)
	}
}

// NormalizePath rewrites values at matching paths before they are compared,
// for cases an ignore rule is too blunt for.
//
// The function receives the decoded scalar and returns the value to compare,
// so a timestamp can be rounded or an address mapped to a placeholder rather
// than dropped entirely.
func NormalizePath(pattern string, fn func(any) any) Option {
	return func(c *config) {
		c.normalize = append(c.normalize, normalizer{pattern: pattern, fn: fn})
	}
}

// SortPath tells the comparison to treat the array at matching paths as an
// unordered set.
//
// XDR arrays are ordered for a reason — operations run in the order they
// appear — but a few are not: the signatures on a multi-signed transaction
// arrive in whichever order the signers signed in. Comparing those
// positionally reports one difference per element for what is really the
// same envelope.
//
//	focus.SortPath("**.Signatures")
//
// The arrays at the matching paths are sorted before the two values are
// diffed, so [sigA, sigB] and [sigB, sigA] compare equal. Everything else
// about the envelope is still compared the usual way: a missing signature is
// still a difference.
//
// The paths that were treated as unordered sets are named in the failure
// message, and a pattern that matches no array at all is reported as
// probably stale, because a rule that quietly does nothing is how a test
// starts passing for the wrong reason.
func SortPath(patterns ...string) Option {
	return func(c *config) {
		c.sortPatterns = append(c.sortPatterns, patterns...)
	}
}

// sortResult records what the sort rules did, so the failure message can say
// how the two values were compared.
type sortResult struct {
	// unordered names the array paths that were compared as unordered sets.
	unordered []string
	// stale names the patterns that matched no array in either value.
	stale []string
}

// sortValues returns the values to compare, with the arrays at the matching
// paths sorted in both of them.
//
// The values are copied rather than sorted in place. A caller may hand in a
// decoded value it still holds — a *lens.Value is an accepted input — and an
// assertion must not reorder somebody else's data.
func (c *config) sortValues(want, got *lens.Value) (*lens.Value, *lens.Value, sortResult) {
	if len(c.sortPatterns) == 0 {
		return want, got, sortResult{}
	}

	wantCopy := &lens.Value{Type: want.Type, Node: cloneNode(want.Node), Raw: want.Raw}
	gotCopy := &lens.Value{Type: got.Type, Node: cloneNode(got.Node), Raw: got.Raw}

	var unordered []string
	unordered = append(unordered, sortNode(wantCopy.Node, "", c.sortPatterns)...)
	unordered = append(unordered, sortNode(gotCopy.Node, "", c.sortPatterns)...)

	paths := listPaths(wantCopy.Node, "", nil)
	paths = listPaths(gotCopy.Node, "", paths)

	return wantCopy, gotCopy, sortResult{
		unordered: dedupe(unordered),
		stale:     c.unusedSorts(paths),
	}
}

// sortNode sorts the children of every list at a path matching one of the
// patterns, and returns the paths it sorted.
//
// The order is by canonical rendering rather than by any one field, because
// an unordered array has no field that identifies its elements: the elements
// are compared as wholes.
func sortNode(n *lens.Node, path string, patterns []string) []string {
	if n == nil {
		return nil
	}

	var sorted []string
	switch n.Kind {
	case lens.KindList:
		if matchesAnyPattern(patterns, path) {
			sort.SliceStable(n.Children, func(i, j int) bool {
				return canonicalKey(n.Children[i]) < canonicalKey(n.Children[j])
			})
			sorted = append(sorted, path)
		}
		for i, child := range n.Children {
			sorted = append(sorted, sortNode(child, fmt.Sprintf("%s[%d]", path, i), patterns)...)
		}
	case lens.KindStruct:
		for _, child := range n.Children {
			sorted = append(sorted, sortNode(child, joinPath(path, child.Name), patterns)...)
		}
	}
	return sorted
}

// listPaths collects the paths of every list in a tree.
//
// This is what the stale-pattern check looks at: a sort pattern that matches
// no list matched nothing.
func listPaths(n *lens.Node, path string, out []string) []string {
	if n == nil {
		return out
	}

	switch n.Kind {
	case lens.KindList:
		out = append(out, path)
		for i, child := range n.Children {
			out = listPaths(child, fmt.Sprintf("%s[%d]", path, i), out)
		}
	case lens.KindStruct:
		for _, child := range n.Children {
			out = listPaths(child, joinPath(path, child.Name), out)
		}
	}
	return out
}

// unusedSorts reports sort patterns that matched no array.
//
// A pattern that never matches is usually a typo — a stale field name, or a
// path with the wrong prefix — and silently doing nothing is exactly how a
// test starts passing for the wrong reason.
func (c *config) unusedSorts(paths []string) []string {
	var unused []string
	for _, pattern := range c.sortPatterns {
		var hit bool
		for _, path := range paths {
			if matchPath(pattern, path) {
				hit = true
				break
			}
		}
		if !hit {
			unused = append(unused, pattern)
		}
	}
	return unused
}

// canonicalKey renders a subtree into a stable string, so two elements of an
// unordered array can be ordered by something other than their position.
//
// Every part of the element takes part — the field names, the values and the
// enriched notes — because an element is compared as a whole.
func canonicalKey(n *lens.Node) string {
	var b strings.Builder
	writeCanonicalKey(&b, n)
	return b.String()
}

func writeCanonicalKey(b *strings.Builder, n *lens.Node) {
	if n == nil {
		b.WriteString("nil;")
		return
	}

	switch n.Kind {
	case lens.KindScalar:
		fmt.Fprintf(b, "s(%v|%s);", n.Value, n.Note)
	case lens.KindList:
		b.WriteString("l[")
		for _, child := range n.Children {
			writeCanonicalKey(b, child)
		}
		b.WriteString("];")
	default:
		b.WriteString("t{")
		for _, child := range n.Children {
			fmt.Fprintf(b, "%s=", child.Name)
			writeCanonicalKey(b, child)
		}
		b.WriteString("};")
	}
}

// cloneNode copies a tree, so sorting a copy leaves the caller's value alone.
func cloneNode(n *lens.Node) *lens.Node {
	if n == nil {
		return nil
	}

	clone := &lens.Node{
		Name:     n.Name,
		Kind:     n.Kind,
		TypeName: n.TypeName,
		Value:    n.Value,
		Note:     n.Note,
	}
	if len(n.Children) > 0 {
		clone.Children = make([]*lens.Node, len(n.Children))
		for i, child := range n.Children {
			clone.Children[i] = cloneNode(child)
		}
	}
	return clone
}

// joinPath appends a field name to a path.
func joinPath(path, name string) string {
	switch {
	case name == "":
		return path
	case path == "":
		return name
	default:
		return path + "." + name
	}
}

// matchesAnyPattern reports whether a path matches any of the patterns.
func matchesAnyPattern(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if matchPath(pattern, path) {
			return true
		}
	}
	return false
}

// dedupe removes repeated entries, keeping the order they came in.
func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// WithColor sets whether the failure diff is colourised: "auto", "always" or
// "never", matching lens. The default is "never", because test output is
// usually captured rather than read on a terminal.
func WithColor(mode string) Option {
	return func(c *config) { c.colorMode = mode }
}

// WithMaxChanges caps how many individual differences appear in a failure
// message before the remainder are summarised as a count. A value of 0 or
// less prints all of them.
func WithMaxChanges(n int) Option {
	return func(c *config) { c.maxChanges = n }
}

// palette resolves the colour palette for failure output.
func (c *config) palette() lensformat.Palette {
	switch c.colorMode {
	case "always":
		return lensformat.ColorPalette
	default:
		// "auto" behaves as "never": an assertion writes through TB.Errorf,
		// which is captured by the test framework rather than written to a
		// terminal, so there is nothing to detect.
		return lensformat.NoColor
	}
}

// filter removes ignored changes from a diff and reports which patterns
// actually matched something.
//
// Returning the matched patterns rather than just the filtered list is what
// lets the failure message say what was ignored, and lets a caller notice
// that an ignore rule matched nothing.
func (c *config) filter(changes []lens.Change) (kept []lens.Change, applied []string) {
	if len(c.ignore) == 0 {
		return changes, nil
	}

	matched := make(map[string]bool, len(c.ignore))
	kept = make([]lens.Change, 0, len(changes))

	for _, ch := range changes {
		var skip bool
		for _, pattern := range c.ignore {
			if matchPath(pattern, ch.Path) {
				skip = true
				matched[pattern] = true
				break
			}
		}
		if !skip {
			kept = append(kept, ch)
		}
	}

	for _, pattern := range c.ignore {
		if matched[pattern] {
			applied = append(applied, pattern)
		}
	}
	return kept, applied
}

// unusedIgnores reports ignore patterns that matched nothing.
//
// A pattern that never matches is usually a typo — a stale field name, or a
// path with the wrong prefix — and silently doing nothing is exactly how a
// test starts passing for the wrong reason.
func (c *config) unusedIgnores(changes []lens.Change) []string {
	var unused []string
	for _, pattern := range c.ignore {
		var hit bool
		for _, ch := range changes {
			if matchPath(pattern, ch.Path) {
				hit = true
				break
			}
		}
		if !hit {
			unused = append(unused, pattern)
		}
	}
	return unused
}
