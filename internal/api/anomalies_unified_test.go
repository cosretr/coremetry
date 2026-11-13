package api

// v0.10.2051 — /api/anomalies/unified: anahtar tüm girdileri taşır
// (v0.5.187 sınıfı), girdiler anahtardan önce sınırlanır, sayfa grubu
// bölmez ve bölüm toplamları dürüst. Eski sorun: istemci birleştirmesi
// sayfalamasızdı, kaynak başına 200 satırda sessizce kesiyordu.

import (
	"fmt"
	"net/url"
	"slices"
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
		// v0.10.2083 — iki farklı sıra aynı anahtarı paylaşmaz
		{Section: "open", Limit: 50, Sort: "service", Dir: "asc"},
		{Section: "open", Limit: 50, Sort: "service", Dir: "desc"},
		{Section: "open", Limit: 50, Sort: "started", Dir: "desc"},
		{Section: "open", Limit: 50, Sort: "signals", Dir: "desc"},
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
		// v0.10.2083 — sort/dir beyaz listesi; varsayılana eşit sıra boş anahtara iner
		{"sort=service&dir=asc", unifiedParams{Section: "open", Limit: 50, Sort: "service", Dir: "asc"}},
		{"sort=service&dir=bogus", unifiedParams{Section: "open", Limit: 50, Sort: "service", Dir: "desc"}},
		{"sort=headline&dir=asc", unifiedParams{Section: "open", Limit: 50}},
		{"sort=status&dir=desc", unifiedParams{Section: "open", Limit: 50}},
		{"section=done&sort=started&dir=desc", unifiedParams{Section: "done", Limit: 50}},
		{"section=done&sort=status&dir=asc", unifiedParams{Section: "done", Limit: 50, Sort: "status", Dir: "asc"}},
		{"event=a1&sort=service&dir=asc", unifiedParams{Section: "open", Limit: 50, Event: "a1"}},
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

// v0.10.2083 — sıra × yön × sayfalama: sayfalar art arda eklenince tüm bölümün
// sıralı listesi çıkar (2. sayfa aynı sıranın devamı), her satır (= grup)
// tam bir kez görünür ve üyeleri bölünmez.
func TestPageUnifiedSortContinuity(t *testing.T) {
	var rows []ml.UnifiedRow
	for i := 0; i < 130; i++ {
		st := ml.UnifiedWatching
		if i%4 == 0 {
			st = ml.UnifiedRunning
		}
		sig := []string{"log"}
		if i%3 == 0 {
			sig = []string{"trace", "log"}
		}
		rows = append(rows, ml.UnifiedRow{
			Key: fmt.Sprintf("g%03d", i), Service: fmt.Sprintf("svc-%d", i%7), Start: int64(i % 11),
			Status: st, Signals: sig, MLLead: 0, LegLead: -1, ML: []int{i, i + 1000}})
	}
	for _, col := range append([]string{""}, ml.UnifiedSortCols...) {
		for _, dir := range []string{"asc", "desc"} {
			want := slices.Clone(rows)
			ml.SortUnified(want, "open", col, dir)
			var got []ml.UnifiedRow
			for off := 0; off < 200; off += 50 {
				p := unifiedParams{Section: "open", Limit: 50, Offset: off, Sort: col, Dir: dir}
				page, total, _, _ := pageUnified(slices.Clone(rows), p)
				if total != 130 {
					t.Fatalf("%s.%s total=%d", col, dir, total)
				}
				got = append(got, page...)
			}
			if len(got) != len(want) {
				t.Fatalf("%s.%s: %d satır, istenen %d", col, dir, len(got), len(want))
			}
			seen := map[string]bool{}
			for i := range got {
				if got[i].Key != want[i].Key {
					t.Fatalf("%s.%s: %d. satır %s, istenen %s (sayfa sınırında sıra kopuyor)", col, dir, i, got[i].Key, want[i].Key)
				}
				if seen[got[i].Key] {
					t.Fatalf("%s.%s: %s iki kez", col, dir, got[i].Key)
				}
				seen[got[i].Key] = true
				if len(got[i].ML) != 2 || got[i].ML[1] != got[i].ML[0]+1000 {
					t.Fatalf("%s.%s: %s grubu bölündü: %v", col, dir, got[i].Key, got[i].ML)
				}
			}
		}
	}
}
