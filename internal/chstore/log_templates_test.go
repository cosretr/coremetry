package chstore

// v0.10.310 — /logs Şablonlar sekmesi: ListLogTemplates WHERE/ORDER kurucusu.
// Servis filtresi has(services, ?) ile; bilinmeyen sort eski davranışı korur.

import (
	"strings"
	"testing"
	"time"
)

func TestLogTemplatesWhere(t *testing.T) {
	for _, tc := range []struct {
		n     string
		f     ListLogTemplatesFilter
		want  []string
		no    []string
		nargs int
	}{
		{"varsayılan", ListLogTemplatesFilter{}, []string{"WHERE 1", "ORDER BY total_count DESC"}, []string{"last_seen >= ?", "has(services"}, 0},
		{"since", ListLogTemplatesFilter{SinceNs: time.Now().UnixNano(), SortBy: "last_seen"}, []string{"last_seen >= ?", "ORDER BY last_seen DESC"}, []string{"has(services"}, 1},
		{"servis", ListLogTemplatesFilter{Service: "api", SortBy: "first_seen"}, []string{"has(services, ?)", "ORDER BY first_seen DESC"}, []string{"last_seen >= ?"}, 1},
		{"ikisi", ListLogTemplatesFilter{SinceNs: 1, Service: "api", SortBy: "count"}, []string{"last_seen >= ?", "has(services, ?)", "ORDER BY total_count DESC"}, nil, 2},
		{"bilinmeyen sort", ListLogTemplatesFilter{SortBy: "spike"}, []string{"ORDER BY total_count DESC"}, nil, 0},
		// v0.10.1030 — "yeni log şablonu" dedektörünün aday ve bilinen okumaları.
		{"aday okuması", ListLogTemplatesFilter{FirstSeenSinceNs: 2, MinTotalCount: 3, SortBy: "first_seen_asc"},
			[]string{"first_seen >= fromUnixTimestamp64Nano(?)", "total_count >= ?", "ORDER BY first_seen ASC"}, []string{"last_seen >= ?", "first_seen <", "hasAny", "empty(services)", "countSubstrings"}, 2},
		{"bilinen şablonlar", ListLogTemplatesFilter{SinceNs: 1, FirstSeenBeforeNs: 2, MinTotalCount: 3, AnyServices: []string{"payments-api", "checkout"}, TokenCounts: []int{5, 15}, SortBy: "last_seen"},
			[]string{"last_seen >= ?", "first_seen < fromUnixTimestamp64Nano(?)", "total_count >= ?", "AND hasAny(services, ?)", "(countSubstrings(template, ' ') + 1) IN (?,?)", "ORDER BY last_seen DESC"},
			[]string{"has(services, ?)", "empty(services)", "first_seen >="}, 6},
		{"servisli + servissiz", ListLogTemplatesFilter{AnyServices: []string{"payments-api"}, IncludeServiceless: true},
			[]string{"AND (hasAny(services, ?) OR empty(services))"}, nil, 1},
		{"yalnız servissiz", ListLogTemplatesFilter{IncludeServiceless: true},
			[]string{"AND empty(services)"}, []string{"hasAny"}, 0},
		{"boş AnyServices kısıt değil", ListLogTemplatesFilter{AnyServices: []string{}, TokenCounts: []int{}}, []string{"WHERE 1"}, []string{"hasAny", "empty(services)", "first_seen", "countSubstrings", "total_count >="}, 0},
	} {
		got, args := logTemplatesWhere(tc.f)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q yok:\n%s", tc.n, w, got)
			}
		}
		for _, w := range tc.no {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q olmamalı:\n%s", tc.n, w, got)
			}
		}
		if len(args) != tc.nargs || strings.Count(got, "?") != tc.nargs {
			t.Errorf("%s: args %d, ? %d; want %d", tc.n, len(args), strings.Count(got, "?"), tc.nargs)
		}
	}
	if _, args := logTemplatesWhere(ListLogTemplatesFilter{Service: "api"}); args[0] != "api" {
		t.Errorf("servis bind'i = %v", args[0])
	}
	// v0.10.1030 — first_seen sınırları NANOSANİYE kesin int64 bind'dir
	// (time.Time olsaydı clickhouse-go saniyeye keserdi ve aynı sınırla
	// kurulan aday/bilinen kümeleri tam tümleyen olmazdı); tam saniye
	// OLMAYAN değerle sınanır. AnyServices TEK bind argümanı (dizi);
	// belirteç sayıları tek tek.
	const edge = int64(1_790_000_000_123_456_789)
	_, args := logTemplatesWhere(ListLogTemplatesFilter{
		FirstSeenSinceNs: edge, FirstSeenBeforeNs: edge, MinTotalCount: 3,
		AnyServices: []string{"payments-api", "checkout"}, TokenCounts: []int{15},
	})
	if len(args) != 5 {
		t.Fatalf("args = %v", args)
	}
	for i := 0; i < 2; i++ {
		if ns, ok := args[i].(int64); !ok || ns != edge {
			t.Errorf("first_seen bind'i %d = %#v, want int64 %d", i, args[i], edge)
		}
	}
	if n, ok := args[2].(uint64); !ok || n != 3 {
		t.Errorf("total_count bind'i = %#v", args[2])
	}
	if svcs, ok := args[3].([]string); !ok || len(svcs) != 2 || svcs[0] != "payments-api" {
		t.Errorf("hasAny bind'i = %#v", args[3])
	}
	if n, ok := args[4].(int); !ok || n != 15 {
		t.Errorf("belirteç sayısı bind'i = %#v", args[4])
	}
}
