package ml

// unified_sort.go — v0.10.2083 (operatör 2026-11-13: /anomalies başlık
// sıralaması geri). v0.10.2051 listeyi sunucu sayfalı yaptı; tablo standardı
// gereği sunucu sayfalı tablo istemcide sıralanmaz (bir sayfalık küme
// üzerinde sıra yalan söyler), başlık tıklaması kayboldu. Sıra artık BURADA,
// birleşik satırların tamamı üzerinde, sayfalamadan ÖNCE: 2. sayfa aynı
// sıranın devamı. Satır = grup, sıralama bir grubu bölmez.
//
// Neden unified.go'da DEĞİL: BuildUnified birleştirmedir ve başka işlerle
// değişiyor; sıra kuralı ayrı, saf ve kendi testinde (unified_sort_test.go).
// Sıralanan kolonlar yalnız sunucuda değeri olanlar: başlangıç, servis,
// sinyaller, durum. "Ne oldu" cümlesi tarayıcıda kurulur → sıralanmaz.

import (
	"cmp"
	"slices"
	"sort"
	"strings"
)

// Sıralama kolon kimlikleri — FE lib/unifiedAnomalySort.ts ile aynı dizgeler.
const (
	UnifiedSortStarted = "started"
	UnifiedSortService = "service"
	UnifiedSortSignals = "signals"
	UnifiedSortStatus  = "status"
)

// UnifiedSortCols — beyaz liste; dışındaki kimlik varsayılana iner.
var UnifiedSortCols = []string{UnifiedSortStarted, UnifiedSortService, UnifiedSortSignals, UnifiedSortStatus}

// unifiedSectionDefault — bölümün varsayılan sırası (SortUnifiedOpen /
// SortUnifiedDone'un kolon karşılığı).
func unifiedSectionDefault(section string) (string, string) {
	if section == "done" {
		return UnifiedSortStarted, "desc"
	}
	return UnifiedSortStatus, "desc"
}

// NormalizeUnifiedSort — SAF: (kolon, yön) beyaz listeye indirgenir.
// Tanınmayan kolon → ("", ""); geçersiz yön → "desc"; bölümün varsayılanına
// eşit sıra da ("", "") olur — varsayılan tek biçimde (aynı önbellek anahtarı,
// aynı eşitlik bozucu).
func NormalizeUnifiedSort(section, col, dir string) (string, string) {
	if !slices.Contains(UnifiedSortCols, col) {
		return "", ""
	}
	if dir != "asc" {
		dir = "desc"
	}
	if dc, dd := unifiedSectionDefault(section); col == dc && dir == dd {
		return "", ""
	}
	return col, dir
}

// unifiedSignalsRank — sinyal kümesi kanonik sıradaki indeksleriyle
// (trace=0, log=1, metric=2); aynı küme aynı dizi.
func unifiedSignalsRank(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if i := slices.Index(unifiedSignalOrder, s); i >= 0 {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

// SortUnified — SAF: bölüm satırlarını (kolon, yön) ile yerinde sıralar.
// Kolon boşsa bölümün varsayılanı (SortUnifiedOpen / SortUnifiedDone).
// Eşitlik bozucu yönden BAĞIMSIZ: başlangıç (yeni önce), sonra anahtar —
// sayfalama deterministik.
func SortUnified(rows []UnifiedRow, section, col, dir string) {
	col, dir = NormalizeUnifiedSort(section, col, dir)
	if col == "" {
		if section == "done" {
			SortUnifiedDone(rows)
		} else {
			SortUnifiedOpen(rows)
		}
		return
	}
	byCol := func(a, b UnifiedRow) int {
		switch col {
		case UnifiedSortStarted:
			return cmp.Compare(a.Start, b.Start)
		case UnifiedSortService:
			return strings.Compare(a.Service, b.Service)
		case UnifiedSortSignals:
			return slices.Compare(unifiedSignalsRank(a.Signals), unifiedSignalsRank(b.Signals))
		case UnifiedSortStatus:
			return UnifiedStatusRank(a.Status) - UnifiedStatusRank(b.Status)
		}
		return 0
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if c := byCol(a, b); c != 0 {
			if dir == "asc" {
				return c < 0
			}
			return c > 0
		}
		if a.Start != b.Start {
			return a.Start > b.Start
		}
		return a.Key < b.Key
	})
}
