package api

import "testing"

// v0.8.241 — operator-requested: nodes matching the hidden glob list
// (e.g. kafka:noise*, kafka:chatter*) must never render in any topology view.
// Pins the matcher semantics: queue:-prefix stripping, glob * and ?,
// exact-match otherwise, empty/blank patterns inert, and the digest
// distinguishing distinct sets (cache-key correctness, v0.5.187 rule).
func TestHiddenNodeMatcher(t *testing.T) {
	hidden := hiddenNodeMatcher([]string{"kafka:noise*", "kafka:chatter*", "exact-svc", "q?"})
	cases := []struct {
		id   string
		want bool
	}{
		{"queue:kafka:noise.orders", true}, // queue: stripped, prefix glob
		{"kafka:noise-shipper", true},      // raw id form
		{"queue:kafka:chatter.callcenter", true},
		{"kafka:business.payments", false}, // kafka but not noise*/chatter*
		{"queue:kafka", false},             // system-level node stays
		{"exact-svc", true},                // exact pattern
		{"exact-svc-2", false},             // exact means exact
		{"q1", true},                       // ? = single char
		{"q12", false},
		{"payment-service", false},
	}
	for _, c := range cases {
		if got := hidden(c.id); got != c.want {
			t.Errorf("hidden(%q) = %v, want %v", c.id, got, c.want)
		}
	}

	// Empty / blank-only lists must hide nothing.
	if none := hiddenNodeMatcher(nil); none("kafka:noise.x") {
		t.Error("nil patterns must hide nothing")
	}
	if none := hiddenNodeMatcher([]string{" ", ""}); none("kafka:noise.x") {
		t.Error("blank patterns must hide nothing")
	}

	// Distinct sets → distinct cache digests; same set → stable.
	a := hiddenDigest([]string{"kafka:noise*", "kafka:chatter*"})
	b := hiddenDigest([]string{"kafka:noise*"})
	if a == b {
		t.Error("different pattern sets must produce different digests")
	}
	if a != hiddenDigest([]string{"kafka:chatter*", "kafka:noise*"}) {
		t.Error("digest must be order-independent (set semantics)")
	}
}
