package api

// v0.10.2051 — /api/anomalies/unified: anahtar tüm girdileri taşır
// (v0.5.187 sınıfı), girdiler anahtardan önce sınırlanır, sayfa grubu
// bölmez ve bölüm toplamları dürüst. Eski sorun: istemci birleştirmesi
// sayfalamasızdı, kaynak başına 200 satırda sessizce kesiyordu.

import (
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/ml"
)

func TestUnifiedKeyDistinct(t *testing.T) {
	base := unifiedParams{Section: "open", Limit: 50}
	variants := []unifiedParams{
		base,
		{Section: "done", Limit: 50},
		{Section: "open", Signal: "log", Limit: 50},
		{Section: "open", Limit: 50, Offset: 50},
		{Section: "open", Limit: 100},
		{Section: "open", Limit: 50, Event: "a1"},
		{Section: "open", Limit: 50, Event: "a2"},
	}
	seen := map[string]int{}
	for i, v := range variants {
		k := unifiedKey(v, "UTC")
		if j, ok := seen[k]; ok {
			t.Fatalf("anahtar çakışması: %d ve %d → %s", i, j, k)
		}
		seen[k] = i
	}
	if unifiedKey(base, "UTC") == unifiedKey(base, "Europe/Istanbul") {
		t.Fatal("saat dilimi anahtarda değil")
	}
	same := unifiedParams{Section: "open", Limit: 50}
	if unifiedKey(base, "UTC") != unifiedKey(same, "UTC") {
		t.Fatal("anahtar kararsız")
	}
}

func TestParseUnifiedParams(t *testing.T) {
	cases := []struct {
		q    string
		want unifiedParams
	}{
		{"", unifiedParams{Section: "open", Limit: 50}},
		{"section=done&signal=trace&offset=100&limit=25", unifiedParams{Section: "done", Signal: "trace", Offset: 100, Limit: 25}},
		{"section=x&signal=group&offset=-5&limit=1000", unifiedParams{Section: "open", Limit: 50}},
		{"offset=999999", unifiedParams{Section: "open", Limit: 50, Offset: unifiedOffsetMax}},
		{"event=a1&section=done&offset=50&signal=log", unifiedParams{Section: "open", Limit: 50, Event: "a1"}},
	}
	for _, c := range cases {
		v, _ := url.ParseQuery(c.q)
		if got := parseUnifiedParams(v.Get); got != c.want {
			t.Errorf("%q → %+v, istenen %+v", c.q, got, c.want)
		}
	}
}

func TestPageUnified(t *testing.T) {
	var rows []ml.UnifiedRow
	for i := 0; i < 120; i++ {
		st := ml.UnifiedDone
		if i%3 == 0 {
			st = ml.UnifiedWatching
		}
		sig := []string{"log"}
		if i%2 == 0 {
			sig = []string{"trace", "log"}
		}
		rows = append(rows, ml.UnifiedRow{Key: string(rune('a'+i%26)) + string(rune('0'+i/26)), Start: int64(i), Status: st, Signals: sig, MLLead: -1, LegLead: 0})
	}
	page, total, openN, doneN := pageUnified(rows, unifiedParams{Section: "done", Limit: 50, Offset: 50})
	if total != 80 || openN != 40 || doneN != 80 || len(page) != 30 {
		t.Fatalf("total=%d open=%d done=%d sayfa=%d", total, openN, doneN, len(page))
	}
	if page[0].Start <= page[1].Start {
		t.Fatal("biten bölüm başlangıca göre yeni önce sıralı değil")
	}
	_, total, openN, doneN = pageUnified(rows, unifiedParams{Section: "open", Signal: "trace", Limit: 50})
	if total != 20 || openN != 20 || doneN != 40 {
		t.Fatalf("süzgeçli: total=%d open=%d done=%d", total, openN, doneN)
	}
	page, total, _, _ = pageUnified(rows, unifiedParams{Section: "open", Limit: 50, Offset: 500})
	if len(page) != 0 || total != 40 {
		t.Fatalf("sondan ötesi: sayfa=%d total=%d", len(page), total)
	}
}
