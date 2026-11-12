package api

// v0.10.2014 — operatör (2026-11-12): "Bir log satırını açtığımda hem runbook
// hem ask cosre olmasın." "Runbook ile açıkla" (6.4) kalktı; "CoSRE'ye sor"
// cevabının SONUNA eşleşen runbook'lar deterministik olarak eklenir.
// Eşleşme yok → hiçbir şey; en çok 2.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestRenderRunbookAppendix(t *testing.T) {
	m := func(id, title string) mcptools.RunbookMatch {
		return mcptools.RunbookMatch{ID: id, Title: title, Href: "/runbook?id=" + id, Score: 0.5}
	}
	cases := []struct {
		name string
		in   []mcptools.RunbookMatch
		want string
	}{
		{"yok", nil, ""},
		{"boş dilim", []mcptools.RunbookMatch{}, ""},
		{"bir", []mcptools.RunbookMatch{m("heap", "JVM heap tükenmesi")},
			"Runbook:\n- [JVM heap tükenmesi](/runbook?id=heap)"},
		{"ikiden fazla → iki", []mcptools.RunbookMatch{m("a", "A"), m("b", "B"), m("c", "C")},
			"Runbook:\n- [A](/runbook?id=a)\n- [B](/runbook?id=b)"},
		{"köşeli parantez + satır sonu", []mcptools.RunbookMatch{m("x", "[db]\nhavuz")},
			"Runbook:\n- [(db) havuz](/runbook?id=x)"},
		{"adsız atlanır", []mcptools.RunbookMatch{m("e", "  "), m("f", "F")},
			"Runbook:\n- [F](/runbook?id=f)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderRunbookAppendix(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestJoinExplainAppendix(t *testing.T) {
	if got := joinExplainAppendix("Ne diyor: x", ""); got != "Ne diyor: x" {
		t.Fatalf("ek yok: %q", got)
	}
	got := joinExplainAppendix("Ne diyor: x", "Runbook:\n- [A](/runbook?id=a)")
	if got != "Ne diyor: x\n\nRunbook:\n- [A](/runbook?id=a)" {
		t.Fatalf("ek: %q", got)
	}
	// Akan kipte son delta = explainAppendixSep + ek; birleşik metin önek + delta.
	if !strings.HasSuffix(got, explainAppendixSep+"Runbook:\n- [A](/runbook?id=a)") {
		t.Fatalf("delta uyumsuz: %q", got)
	}
}

func TestLogRunbookQuery(t *testing.T) {
	ex := &logstore.ExceptionSummary{Type: "java.lang.OutOfMemoryError", Message: "Java heap space", Cause: "java.io.IOException", Class: "resource"}
	q := logRunbookQuery("boom body", "Out of memory", ex)
	if !strings.HasPrefix(q, "OutOfMemoryError IOException resource Java heap space Out of memory") || !strings.HasSuffix(q, "boom body") {
		t.Fatalf("sıra: %q", q)
	}
	if q := logRunbookQuery("only body", "", nil); q != "only body" {
		t.Fatalf("exception yok: %q", q)
	}
	if q := logRunbookQuery("b", "", &logstore.ExceptionSummary{Type: "System.TimeoutException"}); !strings.HasPrefix(q, "TimeoutException") {
		t.Fatalf(".NET kısa ad: %q", q)
	}
}
