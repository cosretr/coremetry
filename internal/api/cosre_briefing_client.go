package api

// cosre_briefing_client.go — v0.10.1712: Login Briefing kalem 2
// (client_metrics_diff; briefing-audit "Tasarım önerisi", Q9 + Q11).
//
// Faz 1 kapsamı (Q11): giden HTTP istemci metrikleri YOK (Faz 2); okunan:
//   - HTTP / gRPC SUNUCU ailesi — v0.10.1719: takımın TÜM servisleri (≤ 20,
//     span azalan) by (svc) (cosre_briefing_http_group.go); v0.10.1727: tür
//     (HTTP / gRPC) servis başına katalogdan, var olan tür başına bir küme ×
//     2 sorgu. R9: takım × env ≤ 6 VM (2 tür × 2 + Kafka 2). Range sorgusu
//     [prev başı, to) — noktalar pencere sınırında ikiye bölünür.
//   - Kafka istemcisi — runKafkaQuestions (Infra Kafka panelinin Go yolu, Q9)
//     iki soru: gönderim hatası/sn ve tüketici lag maks., takım servis kümesi
//     TEK sorguda, service.name × topic kırılımı.
// Okuma hatası → partial; kaynak yok / seri yok → bulgu yok (uydurma yok).

import (
	"context"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// briefingKafkaQuestionKeys — kalem 2'nin Kafka soruları (KafkaServiceQuestions alt kümesi).
var briefingKafkaQuestionKeys = map[string]string{
	"producer_error_rate": briefing.ClientSendErr,
	"consumer_lag_max":    briefing.ClientLag,
}

// briefingKafkaQuestions — SAF: katalog sorularından alt küme, kırılım servis × topic.
func briefingKafkaQuestions() []vmetrics.KafkaQuestion {
	var out []vmetrics.KafkaQuestion
	for _, q := range vmetrics.KafkaServiceQuestions() {
		if _, ok := briefingKafkaQuestionKeys[q.Key]; ok {
			q.GroupBy = []string{"service.name", "topic"}
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// briefingHTTPCandidates — SAF: kur penceresi RED satırlarından en yoğun k servis (span azalan, ad).
func briefingHTTPCandidates(cur []chstore.ServiceSummary, k int) []string {
	rows := append([]chstore.ServiceSummary(nil), cur...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SpanCount != rows[j].SpanCount {
			return rows[i].SpanCount > rows[j].SpanCount
		}
		return rows[i].Name < rows[j].Name
	})
	var out []string
	for _, r := range rows {
		if len(out) >= k {
			break
		}
		if r.Name != "" && r.SpanCount > 0 {
			out = append(out, r.Name)
		}
	}
	return out
}

// briefingKafkaRows — SAF: runKafkaQuestions blokları → kalem 2 satırları
// (GroupKey = service.name, topic). Blok hatası → failed.
func briefingKafkaRows(blocks map[string]kafkaMetricBlock, fromMs int64) (rows []briefing.ClientRow, failed bool) {
	keys := make([]string, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		metric, ok := briefingKafkaQuestionKeys[k]
		if !ok {
			continue
		}
		b := blocks[k]
		if b.Error != "" {
			failed = true
			continue
		}
		for _, s := range b.Series {
			if len(s.GroupKey) == 0 || s.GroupKey[0] == "" {
				continue
			}
			pts := make([][2]float64, 0, len(s.Points))
			for _, p := range s.Points {
				pts = append(pts, [2]float64{float64(p.Time / 1e6), p.Value})
			}
			n, p, hn, hp := briefing.SplitMeans(pts, fromMs)
			label := ""
			if len(s.GroupKey) > 1 && s.GroupKey[1] != "" {
				label = "topic=" + s.GroupKey[1]
			}
			rows = append(rows, briefing.ClientRow{Service: s.GroupKey[0], Family: briefing.FamilyKafka, Metric: metric, Label: label,
				Now: n, Prev: p, HasNow: hn, HasPrev: hp})
		}
	}
	return rows, failed
}

// readBriefingClient — kalem 2 okuması (takım × env). state: ok | skipped | partial.
func (s *Server) readBriefingClient(ctx context.Context, svcs []string, cur []chstore.ServiceSummary, env string, pFrom, from, to time.Time) ([]briefing.ClientRow, string) {
	var rows []briefing.ClientRow
	failed, read := false, false
	fromMs := from.UnixMilli()

	if rd0, isVM := s.metricChartReaderFor(); isVM {
		if rd, ok := rd0.(redFamilyReader); ok {
			read = true
			r, f := s.readBriefingHTTPGroup(ctx, rd, briefingHTTPCandidates(cur, len(cur)), env, pFrom, from, to)
			rows, failed = append(rows, r...), failed || f
		}
	}

	if src := s.metricSource(); src != nil && len(svcs) > 0 {
		read = true
		scopeFor := func(vmetrics.KafkaMetric) *vmetrics.KafkaScope {
			return &vmetrics.KafkaScope{Services: svcs, From: pFrom, To: to}
		}
		blocks, _, _ := runKafkaQuestions(ctx, src, briefingKafkaQuestions(), env, scopeFor)
		r, f := briefingKafkaRows(blocks, fromMs)
		rows, failed = append(rows, r...), failed || f
	}

	switch {
	case !read:
		return nil, "skipped"
	case failed:
		return rows, "partial"
	}
	return rows, "ok"
}
