package chstore

// trace_error_histogram.go — v0.10.1082 — /traces hacim şeridi, Errors +
// nitelik çipinde LİSTEYLE AYNI kümeyi sayar.
//
// Operator-reported (prod, 2026-10-03): "Error seçildiğinde histogram
// gelmiyor." `hasError=true` + `function_code = …` (ya da `k8s.pod.name = …`)
// çipiyle liste dolu, şerit "0 SPANS · 0 ERROR SPANS · 0.00% ERR RATE".
//
// Kök neden — iki yüzey hatayı FARKLI yerde arıyordu:
//   - Liste (GetTraces → trace_error_tracelevel.go, v0.10.1010) iki basamaklı:
//     ① kapsamda çipe uyan hatalı span varsa "çipe uyan hatalı span"; ② yoksa
//     TRACE düzeyi (çip bir span'de, hata aynı trace'in başka span'inde).
//   - Şerit (/api/spans/metric-batch) Errors'u ÇİP olarak ekliyordu
//     (span_metric_batch.go `status = error`) → sayılan span'in KENDİSİ hatalı
//     olmalı. ② kipinde bu kesin sıfırdır (v0.10.1011 bunu boş-durum cümlesiyle
//     "açıkladı", çizmedi). Giriş kapsamındaki çiplerde (k8s.*, http.*) şerit
//     bir de `kind IN (server, consumer)` AND'liyordu → hata istemci / iç
//     span'deyse ① kipinde bile sıfır ("No traces in view to bucket").
//
// Çözüm: şerit bu sınıfta metric-batch'i DEĞİL bu okumayı çağırır; girdi
// listenin KENDİ TraceFilter'ı (api: parseTraceFilter — aynı ayrıştırıcı) ve
// kip kararı listenin kendi probu (traceLevelErrorCandidates — aynı fonksiyon):
//   ① span kipi  — traceErrBothWhere (listenin probuyla AYNI WHERE): çipe uyan
//      hatalı span'ler kovalanır. Tavansız; maliyet eski şeritle aynı sınıf
//      (aynı yüklemler, aynı terfi kolonu / kvh / idx_status budaması).
//   ② trace kipi — listenin aday kümesi (≤ traceStage2MaxIDs id) trace
//      BAŞLANGICINA göre kovalanır: PREWHERE trace_id IN (…) (idx_trace bloom),
//      id başına min(time) + süre. Tam pencere GROUP BY trace_id YOK. Küme
//      tavana çarptıysa Capped — liste aynı şeyi RankedWithin ile söyler.
//
// Neden trace_summary_5m değil: MV nitelik taşımaz (function_code / pod yok),
// çip yüklemi ona karşı onurlandırılamaz. Ham `spans` burada zorunlu ve
// sınırlı: zaman sınırı + LIMIT + max_execution_time her sorguda.

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// TraceErrorHistogram — şerit cevabı. Seri şekli metric-batch'inkiyle aynı
// (SpanMetricPoint, ns kova başı); Mode "span" | "trace".
type TraceErrorHistogram struct {
	Mode   string
	Step   int
	Capped bool
	Count  []SpanMetricPoint
	Errors []SpanMetricPoint
	RT     []SpanMetricPoint
}

// TraceErrorHistogramEligible — SAF: liste bu süzgeçte iki basamaklı hata
// kararını (traceLevelErrorEligible) veriyor VE ek bir trace-düzeyi daraltma
// (süre / RequireServices) taşımıyor. Uygun değilse şerit metric-batch'te
// kalır (frontend errorStripEligible aynası).
func TraceErrorHistogramEligible(f TraceFilter) bool {
	return traceLevelErrorEligible(f) && f.MinMs == 0 && f.MaxMs == 0 && len(f.RequireServices) == 0
}

// traceErrHistBucketExpr — kova ifadesi: pencere başından önceki satırlar
// (liste WHERE'i 5 dk hizalı başlar, buildGetTracesWhere) İLK kovaya katlanır;
// x ekseni pencereye sabit, aksi hâlde eksen dışında kalıp toplamdan düşerlerdi.
// Bind sırası: pencere başı (ns), step (ns), step (ns).
const traceErrHistBucketExpr = "intDiv(greatest(toUnixTimestamp64Nano(time), ?), ?) * ?"

// traceErrHistSpanSQL — SAF: ① span kipi. Yer tutucu sırası: kova (3), WHERE.
// q bind değil, SQL metnine sayı olarak girer (handler beyaz listesinden).
func traceErrHistSpanSQL(whereSQL string, q float64) string {
	return fmt.Sprintf(`
		SELECT %s AS bucket,
		       count() AS n,
		       toFloat64(quantileTDigest(%g)(duration / 1e6)) AS rt
		FROM spans %s
		GROUP BY bucket
		ORDER BY bucket
		LIMIT 50000
		SETTINGS max_execution_time = 25`, traceErrHistBucketExpr, q, whereSQL)
}

// traceErrHistTraceSQL — SAF: ② trace kipi; adayların başlangıç + süresi.
// Liste satırının ifadeleriyle (buildGetTracesListSQLWith) aynı trace_start /
// dur_ms. Yer tutucu sırası: n id, WHERE, LIMIT.
func traceErrHistTraceSQL(n int, whereSQL string) string {
	return `
		SELECT trace_id,
		       toUnixTimestamp64Nano(min(time)) AS t0,
		       (max(toUnixTimestamp64Nano(time) + duration) -
		        toUnixTimestamp64Nano(min(time))) / 1e6 AS dur_ms
		FROM spans ` + stage2PrewhereSQL(n) + whereSQL + `
		GROUP BY trace_id
		LIMIT ?
		SETTINGS max_execution_time = 25, ` + stage2Settings
}

// traceStartRow — ② kipinde bir aday trace: başlangıç (ns) ve süre (ms).
type traceStartRow struct {
	id      string
	startNs int64
	durMs   float64
}

// bucketTraceStarts — SAF (tablo testli): trace'leri başlangıç kovasına sayar;
// pencere başından önce başlayanlar ilk kovaya katlanır (span kipinin
// greatest() kuralıyla aynı). rt = kovadaki trace sürelerinin q-kuantili
// (doğrusal ara değer). Boş kova üretilmez (metric-batch ham yoluyla aynı).
func bucketTraceStarts(rows []traceStartRow, fromNs int64, stepSec int, q float64) (count, rt []SpanMetricPoint) {
	if stepSec <= 0 || len(rows) == 0 {
		return nil, nil
	}
	stepNs := int64(stepSec) * int64(time.Second)
	byBucket := map[int64][]float64{}
	for _, r := range rows {
		t := r.startNs
		if t < fromNs {
			t = fromNs
		}
		b := (t / stepNs) * stepNs
		byBucket[b] = append(byBucket[b], r.durMs)
	}
	keys := make([]int64, 0, len(byBucket))
	for k := range byBucket {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		ds := byBucket[k]
		count = append(count, SpanMetricPoint{Time: k, Value: float64(len(ds))})
		rt = append(rt, SpanMetricPoint{Time: k, Value: quantileLinear(ds, q)})
	}
	return count, rt
}

// quantileLinear — SAF: sıralı örnekte doğrusal ara değerli kuantil.
func quantileLinear(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if q <= 0 {
		return s[0]
	}
	if q >= 1 {
		return s[len(s)-1]
	}
	pos := q * float64(len(s)-1)
	lo := int(pos)
	if lo+1 >= len(s) {
		return s[lo]
	}
	frac := pos - float64(lo)
	return s[lo] + (s[lo+1]-s[lo])*frac
}

// TraceErrorHistogram — /traces şeridi, Errors + span-düzeyi çip (bkz. başlık).
// q = yanıt-süresi istatistiği (0.5 / 0.95 / 0.99); step clampSpanMetricStep'ten
// geçer (nokta bütçesi metric-batch ile aynı).
func (s *Store) TraceErrorHistogram(ctx context.Context, f TraceFilter, stepSec int, q float64) (TraceErrorHistogram, error) {
	out := TraceErrorHistogram{Step: clampSpanMetricStep(stepSec, f.From, f.To, 0)}
	if !TraceErrorHistogramEligible(f) {
		return out, fmt.Errorf("trace error histogram: süzgeç uygun değil (Errors + span-düzeyi çip, arama / süre / services yok)")
	}
	// Liste ile AYNI kip kararı (aynı fonksiyon, aynı prob).
	ids, mode, capped, err := s.traceLevelErrorCandidates(ctx, f)
	if err != nil {
		return out, err
	}
	out.Mode = mode
	stepNs := int64(out.Step) * int64(time.Second)
	fromNs := f.From.UnixNano()
	if mode == traceErrModeSpan {
		// ① — listenin probuyla AYNI WHERE. Root bayrağı span kipinde
		// uygulanmaz (v0.10.1008 kararı: kök yüklemi çipli span'de AND'lenince
		// sıfır; liste kökü trace düzeyinde ayrıca doğrular).
		wc := traceErrBothWhere(f, s.clusterExpr())
		args := append([]any{fromNs, stepNs, stepNs}, wc.args...)
		rows, err := s.telemetryReadConn().Query(ctx, traceErrHistSpanSQL(wc.sql(), q), args...)
		if err != nil {
			return out, fmt.Errorf("trace error histogram (span): %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b int64
			var n uint64
			var rt float64
			if err := rows.Scan(&b, &n, &rt); err != nil {
				return out, err
			}
			out.Count = append(out.Count, SpanMetricPoint{Time: b, Value: float64(n)})
			out.RT = append(out.RT, SpanMetricPoint{Time: b, Value: rt})
		}
		if err := rows.Err(); err != nil {
			return out, err
		}
		// Her sayılan span hatalıdır (yüklem bunu şart koşuyor).
		out.Errors = out.Count
		return out, nil
	}
	// ② — listenin aday kümesi; boşsa liste de boş.
	out.Capped = capped
	if len(ids) == 0 {
		return out, nil
	}
	tr, err := s.traceErrHistTraceRows(ctx, f, ids)
	if err != nil {
		return out, err
	}
	if f.RootOnly && len(tr) > 0 {
		// Liste ② kipinde kökü adaylar üstünde doğrular (rootPostFilter →
		// filterRootTracesAt); şerit aynı çağrıyla aynı kümeyi bırakır.
		cands := make([]stage1Cand, len(tr))
		for i, r := range tr {
			cands[i] = stage1Cand{id: r.id, t0: r.startNs, t1: r.startNs + int64(r.durMs*1e6)}
		}
		hasRoot, err := s.filterRootTracesAt(ctx, cands, f.From, f.To, s.TraceMVGap(ctx, f.From, f.To))
		if err != nil {
			return out, err
		}
		kept := tr[:0]
		for _, r := range tr {
			if hasRoot[r.id] {
				kept = append(kept, r)
			}
		}
		tr = kept
	}
	out.Count, out.RT = bucketTraceStarts(tr, fromNs, out.Step, q)
	// ② kümesindeki her trace hatalıdır (aday = hata-doğrulanmış).
	out.Errors = out.Count
	return out, nil
}

// traceErrHistTraceRows — adayların başlangıç + süresi; kapsam listenin satır
// onarımıyla aynı (withoutChips: servis / ortam / küme / pencere, çipsiz).
func (s *Store) traceErrHistTraceRows(ctx context.Context, f TraceFilter, ids []string) ([]traceStartRow, error) {
	lf := withoutChips(f)
	lf.CandidateIDs, lf.RootOnly = nil, false
	wc := buildGetTracesWhere(lf, s.clusterExpr())
	args := make([]any, 0, len(ids)+len(wc.args)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, wc.args...)
	args = append(args, len(ids))
	rows, err := s.telemetryReadConn().Query(ctx, traceErrHistTraceSQL(len(ids), wc.sql()), args...)
	if err != nil {
		return nil, fmt.Errorf("trace error histogram (trace): %w", err)
	}
	defer rows.Close()
	out := make([]traceStartRow, 0, len(ids))
	for rows.Next() {
		var r traceStartRow
		if err := rows.Scan(&r.id, &r.startNs, &r.durMs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
