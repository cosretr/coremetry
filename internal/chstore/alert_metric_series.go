package chstore

// alert_metric_series.go — v0.10.1064 (operatör, prod alarm problemi detayı
// "HTTP P99 latency >3s (sustained 10 min)": "grafik olmadığı için de anlamak
// çok zor artışları").
//
// Alarm kuralının ölçtüğü metriği ZAMAN İÇİNDE, değerlendiricinin gördüğü
// gibi verir: her nokta = kuralın penceresi boyunca değerlendiricinin
// hesapladığı değer (kayan pencere), aynı kaynak tablo, aynı süzgeç, aynı
// birleştirme işlevi. Böylece grafikteki çizgi eşiği tam problemin açıldığı
// yerde keser; kova başına ham değer çizilseydi tek bir sivri kova eşiği
// "aşmış" görünürdü ama kural (10 dk penceresi) aşmamış olurdu.
//
// Kaynak seçimi measureAllServicesPlan'ın BİREBİR ikizi (tek servis, zaman
// kovalı): temel RED → service_summary_5m (error_rate: spanmetrics_1m giriş
// span'leri, v0.10.783), mq_* → spanmetrics_1m, http_/db_/rpc_ → ham spans
// (süzgeçleri — http_method / http_status / db_system / rpc_system — hiçbir
// MV'nin boyutu değil; değerlendirici de aynı ham okumayı yapıyor). Ham spans
// okuması tek servisin (service_name, time) önekiyle, en çok 6 saatlik
// pencereyle, LIMIT + max_execution_time ile sınırlı.
//
// 5 dk altı pencereler değerlendiricide servis başına ham spans okur
// (UseSummaryMV false); dizi de öyle: 1 dk kova, ham spans.
//
// Kayan pencere SQL'de: kova başına ara durum (count / sum / quantile state),
// üstünde `RANGE BETWEEN <çerçeve> PRECEDING AND CURRENT ROW` ile birleştirme
// (-Merge pencere işlevi). Boş kovalar WITH FILL ile doldurulur — veri
// gelmeyen bir kovada da değerlendiricinin penceresindeki önceki kovalar
// değer üretir. Penceresi tamamen boş nokta NaN döner → boşluk (null).

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// AlertMetricPoint — dizinin bir noktası. T değerlendirme anı (kova sonu,
// şimdiyle kırpık), V o andaki pencere değeri; nil = pencerede veri yok.
type AlertMetricPoint struct {
	T int64    `json:"t"`
	V *float64 `json:"v"`
}

// AlertSeriesSpec — bir (metrik, pencere) çiftinin dizi ızgarası. SAF.
type AlertSeriesSpec struct {
	StepSec  int  // kova genişliği: 300 (MV ızgarası) ya da 60 (5 dk altı pencere)
	FrameSec int  // RANGE çerçevesi (sn) — o kovaya kadar geriye bakış
	RawSpans bool // okuma ham spans'tan mı (pencere tavanı buna göre)
	MaxSpan  time.Duration
}

const (
	alertSeriesMaxSpanMV  = 24 * time.Hour
	alertSeriesMaxSpanRaw = 6 * time.Hour
)

// AlertMetricSeriesSpec — metriğin ve kural penceresinin ızgarası. Bilinmeyen
// metrik ya da sıfır pencere hata döner (dizi çizilemez — uydurma yok).
//
// Çerçeve değerlendiricinin kapsamasıyla eşleşir:
//   - MV ızgarası (pencere ≥ 5 dk): t ∈ (b, b+5dk) anında değerlendirici
//     time_bucket ≥ floor5(t − W) kovalarını okur → [b − ⌈W/5dk⌉·5dk, b].
//   - Ham ızgara (pencere < 5 dk): t = b + 1dk anında [t − W, t) → çerçeve
//     ⌈W/1dk⌉·1dk − 1dk.
func AlertMetricSeriesSpec(metric string, windowSec int) (AlertSeriesSpec, error) {
	if windowSec <= 0 {
		return AlertSeriesSpec{}, fmt.Errorf("alert series: pencere yok")
	}
	var sp AlertSeriesSpec
	if UseSummaryMV(time.Duration(windowSec) * time.Second) {
		sp.StepSec = int(summaryMVBucket / time.Second)
		sp.FrameSec = ceilDivInt(windowSec, sp.StepSec) * sp.StepSec
	} else {
		sp.StepSec = 60
		sp.FrameSec = ceilDivInt(windowSec, sp.StepSec)*sp.StepSec - sp.StepSec
	}
	p, err := alertMetricSeriesPlan(metric, !UseSummaryMV(time.Duration(windowSec)*time.Second), "spanmetrics_1m")
	if err != nil {
		return AlertSeriesSpec{}, err
	}
	sp.RawSpans = p.table == "spans"
	sp.MaxSpan = alertSeriesMaxSpanMV
	if sp.RawSpans {
		sp.MaxSpan = alertSeriesMaxSpanRaw
	}
	return sp, nil
}

func ceilDivInt(a, b int) int { return (a + b - 1) / b }

// alertSeriesScan — pencere değerinin Go tarafında nasıl son hâline geldiği;
// değerlendiricinin measureAllScan'ıyla aynı anlam (v0.8.315 matematiği).
type alertSeriesScan int

const (
	seriesScanFloat       alertSeriesScan = iota // oran / yüzdelik — olduğu gibi
	seriesScanCountScaled                        // error_count: n · W / kapsanan
	seriesScanCountRate                          // request_rate: n / kapsanan
	seriesScanCountRaw                           // taşıma `count`: ölçeklenmez (değerlendirici de ölçeklemiyor)
)

// alertSeriesPlan — bir metriğin kovalı okuması. value ifadesi `w`
// penceresi üstünde, inner kolonlarını birleştirir; NaN = boş pencere.
type alertSeriesPlan struct {
	table  string // service_summary_5m | spanmetrics_1m | spans (mantıksal ad; from'dan bağımsız)
	from   string // FROM kaynağı (spanmetrics için smSource)
	tcol   string // zaman kolonu
	where  string // ek süzgeç ("" = yok)
	inner  string // kova başına ara kolonlar
	value  string // pencere değeri
	scan   alertSeriesScan
	bucket func(step int) string
}

func mvBucket(int) string { return "time_bucket" }
func smBucket(step int) string {
	return fmt.Sprintf("toStartOfInterval(time_bucket, INTERVAL %d SECOND)", step)
}
func rawBucket(step int) string {
	return fmt.Sprintf("toStartOfInterval(toDateTime(time), INTERVAL %d SECOND)", step)
}

const (
	seriesRatioValue = `ifNull(sum(e) OVER w / nullIf(sum(c) OVER w, 0) * 100, nan)`
	seriesAvgValue   = `ifNull(toFloat64(sum(d) OVER w) / nullIf(toFloat64(sum(c) OVER w), 0) / 1e6, nan)`
	seriesSumC       = `toFloat64(sum(c) OVER w)`
	seriesSumE       = `toFloat64(sum(e) OVER w)`
)

// alertMetricSeriesPlan — SAF; tablo testli. raw = 5 dk altı pencere
// (değerlendiricinin servis başına ham yolu). Yönlendirme
// measureAllServicesPlan ile aynı (parite testi pinler).
func alertMetricSeriesPlan(metric string, raw bool, smSource string) (alertSeriesPlan, error) {
	sm := func(where, inner, value string, scan alertSeriesScan) alertSeriesPlan {
		return alertSeriesPlan{table: "spanmetrics_1m", from: smSource, tcol: "time_bucket",
			where: where, inner: inner, value: value, scan: scan, bucket: smBucket}
	}
	mv := func(inner, value string, scan alertSeriesScan) alertSeriesPlan {
		return alertSeriesPlan{table: "service_summary_5m", from: "service_summary_5m", tcol: "time_bucket",
			inner: inner, value: value, scan: scan, bucket: mvBucket}
	}
	sp := func(where, inner, value string, scan alertSeriesScan) alertSeriesPlan {
		return alertSeriesPlan{table: "spans", from: "spans", tcol: "time",
			where: where, inner: inner, value: value, scan: scan, bucket: rawBucket}
	}
	rawQuantile := func(where, op string) alertSeriesPlan {
		q := op[1 : len(op)-3] // "50" / "95" / "99"
		return sp(where, `quantileState(0.`+q+`)(duration) AS q`,
			`quantileMerge(0.`+q+`)(q) OVER w / 1e6`, seriesScanFloat)
	}

	// Temel RED metrikleri.
	switch metric {
	case "error_rate":
		if raw {
			return sp(EntrySpanKindsWhere, `countIf(status_code='error') AS e, count() AS c`, seriesRatioValue, seriesScanFloat), nil
		}
		return sm(EntrySpanKindsWhere, `countMerge(error_state) AS e, countMerge(calls_state) AS c`, seriesRatioValue, seriesScanFloat), nil
	case "error_count":
		if raw {
			return sp("", `countIf(status_code='error') AS e`, seriesSumE, seriesScanCountScaled), nil
		}
		return mv(`countMerge(error_count_state) AS e`, seriesSumE, seriesScanCountScaled), nil
	case "request_rate":
		if raw {
			return sp("", `count() AS c`, seriesSumC, seriesScanCountRate), nil
		}
		return mv(`countMerge(span_count_state) AS c`, seriesSumC, seriesScanCountRate), nil
	case "avg_ms":
		if raw {
			return sp("", `sum(duration) AS d, count() AS c`, seriesAvgValue, seriesScanFloat), nil
		}
		return mv(`sumMerge(duration_sum_state) AS d, countMerge(span_count_state) AS c`, seriesAvgValue, seriesScanFloat), nil
	case "p50_ms", "p95_ms", "p99_ms":
		if raw {
			return rawQuantile("", metric), nil
		}
		// service_summary_5m quantilesTDigestState(0.5, 0.95, 0.99): 1/2/3.
		idx := map[string]int{"p50_ms": 1, "p95_ms": 2, "p99_ms": 3}[metric]
		return mv(`quantilesTDigestMergeState(0.5,0.95,0.99)(duration_q_state) AS q`,
			fmt.Sprintf(`arrayElement(quantilesTDigestMerge(0.5,0.95,0.99)(q) OVER w, %d) / 1e6`, idx), seriesScanFloat), nil
	}

	// Hedefli kural metrikleri (DB ifadesi / http_route / Kafka istemcisi)
	// span yolundan ölçülmez — db_/http_ önekleri TransportFilter'a düşüp
	// YANLIŞ popülasyonu çizerdi; dizi yok.
	for _, pre := range []string{"db_stmt_", "http_route_", "kafka_"} {
		if strings.HasPrefix(metric, pre) {
			return alertSeriesPlan{}, fmt.Errorf("alert series: hedefli kural metriği %q", metric)
		}
	}

	// Taşıma metrikleri.
	where, numerator, ok := TransportFilter(metric)
	if !ok {
		return alertSeriesPlan{}, fmt.Errorf("alert series: bilinmeyen metrik %q", metric)
	}
	op := TransportOp(metric)
	if op == "" {
		return alertSeriesPlan{}, fmt.Errorf("alert series: bilinmeyen taşıma işlemi %q", metric)
	}
	if mvWhere, mvOK := spanmetricsTransportWhere(metric); !raw && mvOK &&
		(op != "error_rate" || numerator == "status_code='error'") {
		switch op {
		case "error_rate":
			return sm(mvWhere, `countMerge(error_state) AS e, countMerge(calls_state) AS c`, seriesRatioValue, seriesScanFloat), nil
		case "p50_ms", "p95_ms", "p99_ms":
			// spanmetrics_1m quantilesTDigestState(0.5, 0.9, 0.95, 0.99): 1/3/4.
			idx := map[string]int{"p50_ms": 1, "p95_ms": 3, "p99_ms": 4}[op]
			return sm(mvWhere, `quantilesTDigestMergeState(0.5,0.9,0.95,0.99)(duration_q_state) AS q`,
				fmt.Sprintf(`arrayElement(quantilesTDigestMerge(0.5,0.9,0.95,0.99)(q) OVER w, %d) / 1e6`, idx), seriesScanFloat), nil
		case "avg_ms":
			return sm(mvWhere, `sumMerge(duration_sum_state) AS d, countMerge(calls_state) AS c`, seriesAvgValue, seriesScanFloat), nil
		case "count":
			return sm(mvWhere, `countMerge(calls_state) AS c`, seriesSumC, seriesScanCountRaw), nil
		}
	}
	switch op {
	case "error_rate":
		return sp(where, `countIf(`+numerator+`) AS e, count() AS c`, seriesRatioValue, seriesScanFloat), nil
	case "p50_ms", "p95_ms", "p99_ms":
		return rawQuantile(where, op), nil
	case "avg_ms":
		return sp(where, `sum(duration) AS d, count() AS c`, seriesAvgValue, seriesScanFloat), nil
	case "count":
		return sp(where, `count() AS c`, seriesSumC, seriesScanCountRaw), nil
	}
	return alertSeriesPlan{}, fmt.Errorf("alert series: bilinmeyen taşıma işlemi %q", metric)
}

// alertMetricSeriesSQL — SAF. Bağ argümanları sırasıyla: servis, okuma
// başı (unix sn, çerçeve kadar önce), bitiş, doldurma başı, doldurma sonu,
// dizi başı. Hepsi unix saniye tamsayısı → toDateTime(?) (saat dilimsiz).
// Her ham spans okuması LIMIT + max_execution_time + zaman sınırlı WHERE
// taşır (CLAUDE.md CH kuralı).
func alertMetricSeriesSQL(p alertSeriesPlan, sp AlertSeriesSpec) string {
	extra := ""
	if p.where != "" {
		extra = " AND " + p.where
	}
	return fmt.Sprintf(`
		SELECT b, v FROM (
			SELECT b, %s AS v FROM (
				SELECT %s AS b, %s
				FROM %s
				WHERE service_name = ? AND %s >= toDateTime(?) AND %s < toDateTime(?)%s
				GROUP BY b
				ORDER BY b WITH FILL FROM toDateTime(?) TO toDateTime(?) STEP %d
				LIMIT 2000
			)
			WINDOW w AS (ORDER BY toUInt32(b) RANGE BETWEEN %d PRECEDING AND CURRENT ROW)
		)
		WHERE b >= toDateTime(?)
		ORDER BY b
		LIMIT 2000
		SETTINGS max_execution_time = 10`,
		p.value, p.bucket(sp.StepSec), p.inner, p.from, p.tcol, p.tcol, extra, sp.StepSec, sp.FrameSec)
}

// alertMetricSeriesArgs — SAF; alertMetricSeriesSQL'in bağ sırası. Okuma
// çerçeve kadar önce başlar: dizinin ilk noktasının penceresi de dolu olsun.
func alertMetricSeriesArgs(q AlertMetricSeriesQuery, sp AlertSeriesSpec) []any {
	readFrom := q.From.Unix() - int64(sp.FrameSec)
	return []any{q.Service, readFrom, q.To.Unix(), readFrom, q.To.Unix(), q.From.Unix()}
}

// AlertMetricSeriesQuery — From/To kovaya hizalı ([From, To)); Now gelecekteki
// kovaları keser ve son (dolmakta olan) kovanın kapsamını belirler.
type AlertMetricSeriesQuery struct {
	Metric    string
	Service   string
	WindowSec int
	From, To  time.Time
	Now       time.Time
}

// AlertMetricSeries — alarm kuralının metriğini değerlendiricinin kayan
// penceresiyle, kova kova okur (tek servis). Boş kova = null nokta.
func (s *Store) AlertMetricSeries(ctx context.Context, q AlertMetricSeriesQuery) ([]AlertMetricPoint, error) {
	sp, err := AlertMetricSeriesSpec(q.Metric, q.WindowSec)
	if err != nil {
		return nil, err
	}
	raw := !UseSummaryMV(time.Duration(q.WindowSec) * time.Second)
	p, err := alertMetricSeriesPlan(q.Metric, raw, s.spanmetricsSourceFor("spanmetrics_1m"))
	if err != nil {
		return nil, err
	}
	rows, err := s.telemetryReadConn().Query(ctx, alertMetricSeriesSQL(p, sp), alertMetricSeriesArgs(q, sp)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vals := make(map[int64]float64)
	for rows.Next() {
		var b time.Time
		var v float64
		if err := rows.Scan(&b, &v); err != nil {
			return nil, err
		}
		vals[b.Unix()] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildAlertSeriesPoints(vals, q, sp, p.scan), nil
}

// buildAlertSeriesPoints — SAF. [From, To) ızgarasının her kovası bir nokta
// (Now'dan sonra başlayan kova yok). T = min(kova sonu, Now); sayım türleri
// değerlendiricinin v0.8.315 matematiğiyle kapsanan süreye göre ölçeklenir.
// NaN / eksik satır = null (pencerede veri yok — sıfır DEĞİL; sayım türünde
// eksik satır 0'dır: WITH FILL zaten sıfır sayım üretir).
func buildAlertSeriesPoints(vals map[int64]float64, q AlertMetricSeriesQuery, sp AlertSeriesSpec, scan alertSeriesScan) []AlertMetricPoint {
	step := int64(sp.StepSec)
	if step <= 0 || !q.To.After(q.From) {
		return []AlertMetricPoint{}
	}
	now := q.Now.Unix()
	out := make([]AlertMetricPoint, 0, (q.To.Unix()-q.From.Unix())/step+1)
	for b := q.From.Unix(); b < q.To.Unix() && b < now; b += step {
		end := b + step
		if end > now {
			end = now
		}
		pt := AlertMetricPoint{T: end * int64(time.Second)}
		v, have := vals[b]
		covered := float64(end - (b - int64(sp.FrameSec)))
		switch scan {
		case seriesScanFloat:
			if have && !math.IsNaN(v) && !math.IsInf(v, 0) {
				x := v
				pt.V = &x
			}
		case seriesScanCountScaled:
			x := ScaleToWindow(v, float64(q.WindowSec), covered)
			pt.V = &x
		case seriesScanCountRate:
			x := 0.0
			if covered > 0 {
				x = v / covered
			}
			pt.V = &x
		case seriesScanCountRaw:
			x := v
			pt.V = &x
		}
		out = append(out, pt)
	}
	return out
}
