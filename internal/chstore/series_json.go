package chstore

import "encoding/json"

// series_json.go — v0.10.844: seri tipi nil dilimi JSON'a `null` değil `[]`
// yazar. FE sözleşmesi `groupKey: string[]`, `points: {…}[]` (null yok) ve
// ~35 guard'sız `.points.map` tüketicisi var; v0.10.842 tek üreticiyi
// (errorRatePercent) düzeltmişti ama üretici 20+ (spanmetric, metricrate,
// metricresolve, rollup_*, tracemetric, metrichist…). Garanti tek noktada:
// tel şekli. Değer alıcı → []SpanMetricSeries, *SpanMetricSeries ve harita
// değerleri aynı yoldan geçer. Kapsam dışı: serinin KENDİSİNİ taşıyan nil
// dilim (`series["rate"] = nil` → null) — FE tipi orada `| null` der.
//
// MarshalJSON — nil GroupKey/Points → []; dolu dilimler ve alan sırası aynı.
func (s SpanMetricSeries) MarshalJSON() ([]byte, error) {
	type plain SpanMetricSeries // yöntemsiz kopya — özyineleme yok
	p := plain(s)
	if p.GroupKey == nil {
		p.GroupKey = []string{}
	}
	if p.Points == nil {
		p.Points = []SpanMetricPoint{}
	}
	return json.Marshal(p)
}
