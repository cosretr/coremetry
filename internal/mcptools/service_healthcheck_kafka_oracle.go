package mcptools

// service_healthcheck_kafka_oracle.go — v0.10.2046 (servis sağlık kontrolü
// dilim 4): kontrol 12 Kafka consumer lag + kontrol 13 Oracle hataları.
//
//	12  kafka.consumer.records_lag_max (vmetrics katalog satırı, servis
//	    kapsamlı, topic kırılımı, ≤16 nokta) — metrik yönlendiricisi
//	    (prod: VictoriaMetrics; CH metrik yoluna emek yok). 1 sorgu.
//	    Topic başına: pencere boyunca her nokta ≥1000 ve son ≥ ilk (eriyip
//	    düşmedi) → Sorun; son − ilk ≥1000 ve son ≥1.5×ilk (artıyor) → Dikkat.
//	    1000 kayıt tabanı Kafka lag olayının etki tabanıyla aynı (v0.10.2041).
//	13  Oracle kökenli exception grupları (exception_groups FINAL, `ora:`,
//	    servis, son 7 gün, ≤50; ListExceptionGroups — yeni SQL yok). 1 sorgu.
//	    Pencerede görülen ve son 24 saatte DOĞAN kod → Dikkat; o kod ≥10
//	    oluşumda → Sorun. Bilinen (eski) kodların sürmesi bilgi (İyi) — onların
//	    önceliği Exception kontrolünde (Inbox P1/P2 kuralı) zaten hükümde.
//	    Pencere-içi oluşum sayısı tabloda yok; "≥10 olay" yeni kodun toplam
//	    oluşumuyla ölçülür (yeni kodda toplam ≈ yakın geçmiş).
//
// Serviste hiç veri yoksa (Kafka tüketicisi değil / Oracle hatası hiç yok)
// kontrol UYGULANMADI: satır olmaz, dipnotta adı yazılır. İyi değildir;
// Ölçülemedi de değildir (okuma başarılı, konu yok).

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// hcStateNA — iç işaret: kontrol bu servise uygulanmaz (runner ayıklar).
const hcStateNA = "n/a"

const (
	hcKafkaLagMetric    = "kafka.consumer.records_lag_max"
	hcKafkaLagFloor     = 1000.0
	hcKafkaGrowRatio    = 1.5
	hcKafkaMinWindow    = 15 * time.Minute
	hcKafkaMaxPoints    = 16
	hcKafkaMinPoints    = 3
	hcOracleLookback    = 7 * 24 * time.Hour
	hcOracleNewWithin   = 24 * time.Hour
	hcOracleCritOccur   = 10
	hcOracleReadMax     = 50
	hcOracleCodesInSeen = 3
)

// hcKafkaLagFilter — SAF: katalog satırı + servis kapsamı + topic kırılımı.
func hcKafkaLagFilter(service string, from, to time.Time) (chstore.MetricQueryFilter, error) {
	m, ok := vmetrics.KafkaMetricByName(hcKafkaLagMetric)
	if !ok {
		return chstore.MetricQueryFilter{}, fmt.Errorf("%s katalogda yok", hcKafkaLagMetric)
	}
	return vmetrics.KafkaQuery(m, vmetrics.KafkaScope{Services: []string{service}, From: from, To: to, MaxDataPoints: hcKafkaMaxPoints}, []string{"topic"})
}

// hcSplitNotApplicable — SAF: uygulanmayan satırları ayıkla (başlıkları sırayla).
func hcSplitNotApplicable(rows []HealthcheckRow) ([]HealthcheckRow, []string) {
	out := rows[:0:0]
	var na []string
	for _, r := range rows {
		if r.State == hcStateNA {
			na = append(na, r.Title)
			continue
		}
		out = append(out, r)
	}
	if out == nil {
		out = []HealthcheckRow{}
	}
	return out, na
}

// hcClassifyKafkaLag — kontrol 12.
func hcClassifyKafkaLag(service string, series []chstore.SpanMetricSeries, err error) HealthcheckRow {
	row := hcBase(service, HCCheckKafkaLag, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	type topicLag struct {
		topic             string
		first, last, peak float64
		state             string
		why               string
	}
	var ts []topicLag
	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		pts := append([]chstore.SpanMetricPoint(nil), s.Points...)
		sort.Slice(pts, func(i, j int) bool { return pts[i].Time < pts[j].Time })
		t := topicLag{topic: strings.Join(s.GroupKey, " · "), first: pts[0].Value, last: pts[len(pts)-1].Value, state: HCOK}
		low := pts[0].Value
		for _, p := range pts {
			t.peak = max(t.peak, p.Value)
			low = min(low, p.Value)
		}
		if t.topic == "" {
			t.topic = "(topic yok)"
		}
		switch {
		case len(pts) >= hcKafkaMinPoints && low >= hcKafkaLagFloor && t.last >= t.first:
			t.state = HCCrit
			t.why = fmt.Sprintf("lag pencere boyunca ≥%.0f, düşmedi (%.0f → %.0f)", hcKafkaLagFloor, t.first, t.last)
		case len(pts) >= 2 && t.last-t.first >= hcKafkaLagFloor && t.last >= hcKafkaGrowRatio*t.first:
			t.state = HCWarn
			t.why = fmt.Sprintf("lag artıyor (%.0f → %.0f)", t.first, t.last)
		}
		ts = append(ts, t)
	}
	if len(ts) == 0 {
		row.State = hcStateNA
		return row
	}
	rank := map[string]int{HCCrit: 0, HCWarn: 1, HCOK: 2}
	sort.SliceStable(ts, func(i, j int) bool {
		if rank[ts[i].state] != rank[ts[j].state] {
			return rank[ts[i].state] < rank[ts[j].state]
		}
		return ts[i].peak > ts[j].peak
	})
	w := ts[0]
	row.State = w.state
	if w.state == HCOK {
		row.Seen = fmt.Sprintf("%d topic; en yüksek lag %.0f (%s), birikme yok", len(ts), w.peak, w.topic)
		return row
	}
	row.Seen = "topic " + w.topic + ": " + w.why
	if n := len(ts) - 1; n > 0 {
		row.Seen += fmt.Sprintf(" · %d topic daha", n)
	}
	row.Short = "Kafka lag (" + w.topic + ")"
	return row
}

// hcClassifyOracle — kontrol 13. groups: servisin son 7 günde görülen
// Oracle grupları (ignored hariç).
func hcClassifyOracle(service string, groups []chstore.ExceptionGroup, from, now time.Time, err error) HealthcheckRow {
	row := hcBase(service, HCCheckOracle, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	if len(groups) == 0 {
		row.State = hcStateNA
		return row
	}
	var active, fresh []chstore.ExceptionGroup
	for _, g := range groups {
		if time.Unix(0, g.LastSeen).Before(from) {
			continue
		}
		active = append(active, g)
		if !time.Unix(0, g.FirstSeen).Before(now.Add(-hcOracleNewWithin)) {
			fresh = append(fresh, g)
		}
	}
	if len(active) == 0 {
		row.State = HCOK
		row.Seen = fmt.Sprintf("pencerede Oracle hatası yok (son 7 günde %d kod)", len(groups))
		return row
	}
	codes := func(gs []chstore.ExceptionGroup) string {
		var parts []string
		for i, g := range gs {
			if i >= hcOracleCodesInSeen {
				parts = append(parts, fmt.Sprintf("+%d", len(gs)-i))
				break
			}
			parts = append(parts, fmt.Sprintf("%s %s (%d)", g.Type, truncateRunes(g.Message, 40), g.Occurrences))
		}
		return strings.Join(parts, ", ")
	}
	if len(fresh) == 0 {
		row.State = HCOK
		row.Seen = fmt.Sprintf("%d bilinen kod sürüyor, yeni kod yok: %s", len(active), codes(active))
		return row
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Occurrences > fresh[j].Occurrences })
	row.Link, row.LinkLabel = "/problems?exc="+url.QueryEscape(fresh[0].Fingerprint), "Exception"
	row.Seen = fmt.Sprintf("%d yeni kod (son 24 sa): %s", len(fresh), codes(fresh))
	if fresh[0].Occurrences >= hcOracleCritOccur {
		row.State, row.Short = HCCrit, "yeni "+fresh[0].Type
		return row
	}
	row.State, row.Short = HCWarn, "yeni "+fresh[0].Type
	return row
}
