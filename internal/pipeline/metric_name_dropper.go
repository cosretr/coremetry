package pipeline

// metric_name_dropper.go — v0.10.1809: metrik DROP kurallarının OKUMA
// tarafı.
//
// DropsMetric / AcceptMetric yazma anında karar verir (CH ingest, VM
// forward — forward_filter.go). Kuraldan ÖNCE yazılmış adlar store'da
// kalır ve metrik listelerinde (picker, katalog, etiket keşfi) görünmeye
// devam ederdi. MetricNameDropper aynı kural kataloğundan, YALNIZ ADLA
// karar verilebilen kuralları derler:
//
//   - etkin, Signal=metrics, Kind=drop;
//   - When ve And'in HER koşulu ad anahtarında (metric / name /
//     metric.name — matchMetric'in ad dalıyla aynı küme).
//
// Etiket/öznitelik/servis koşullu kural adı GİZLEMEZ: "healthcheck'in
// route=/health serisi" düşer ama adın öbür serileri hâlâ yazılıyor;
// adı saklamak canlı veriyi listeden silerdi.
//
// Neden açık sorgu (query/PromQL/dashboard) filtrelenmiyor: operatör
// eski veriyi açıkça adıyla incelemeye devam edebilmeli — gizlemek
// keşif yüzeyinin işi, veriye erişimi kesmek değil (DECISIONS 2026-11-05).

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// isMetricNameKey — matchMetric'in `case "metric", "name", "metric.name"`
// dalının aynası. Burada ayrı bir liste tutmak yerine ortak bir yardımcı
// olsaydı daha iyiydi ama matchMetric sıcak yolda switch'le çalışıyor;
// metric_name_dropper_test.go alias'ların üçünü de pinler.
func isMetricNameKey(k string) bool {
	return k == "metric" || k == "name" || k == "metric.name"
}

// MetricNameDropper — ad tek başına düşürülüyor mu? fn nil = uygulanabilir
// kural yok (çağıran hiç süzmez, cevap bayt-bayt eski). digest kural
// kümesinin TAMAMINI hash'ler (sıralı + FNV, v0.5.187) ve cache anahtarına
// girer: kural PUT'undan sonra TTL boyunca eski liste servis edilmesin.
// Kural yokken digest "" — anahtar sabit kalır, gereksiz soğuk okuma yok.
//
// nil-alıcı güvenli (testlerdeki &Server{} pipeline taşımaz).
func (e *Engine) MetricNameDropper() (func(name string) bool, string) {
	if e == nil {
		return nil, ""
	}
	e.mu.RLock()
	rules := e.rules
	e.mu.RUnlock()

	var sets [][]Condition
	var canon []string
	for i := range rules {
		r := &rules[i]
		if !r.Enabled || r.Signal != SignalMetrics || r.Kind != KindDrop {
			continue
		}
		conds := make([]Condition, 0, 1+len(r.And))
		conds = append(conds, r.When)
		conds = append(conds, r.And...)
		nameOnly := true
		parts := make([]string, 0, len(conds))
		for _, c := range conds {
			if !isMetricNameKey(c.Key) {
				nameOnly = false
				break
			}
			parts = append(parts, fmt.Sprintf("%s\x1f%s\x1f%s", c.Key, c.Op, c.Value))
		}
		if !nameOnly {
			continue
		}
		sort.Strings(parts)
		canon = append(canon, strings.Join(parts, "\x1e"))
		sets = append(sets, conds)
	}
	if len(sets) == 0 {
		return nil, ""
	}
	sort.Strings(canon)
	h := fnv.New64a()
	for _, c := range canon {
		_, _ = h.Write([]byte(c))
		_, _ = h.Write([]byte{0})
	}
	digest := fmt.Sprintf("%x", h.Sum64())

	fn := func(name string) bool {
		for _, conds := range sets {
			ok := true
			for _, c := range conds {
				if !matchOp(c.Op, name, c.Value) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}
	return fn, digest
}
