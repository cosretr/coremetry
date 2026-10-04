package chstore

// oracle_sample_traces.go — v0.10.1104 (operatör: "Exceptions sayfasındaki
// traceidler mouse orta clickle yeni sekmede açmıyorum. Bu arada bazı traceidler
// de aslında coremetry üzerinde olmayabilir").
//
// `ora:` grubunun örnekleri Oracle hata satırlarından gelir (oracleGroupSamples);
// trace id kolonu Oracle'ın taşıdığı değerdir ve o trace Coremetry'ye hiç
// ulaşmamış olabilir (enstrümansız çağıran, örnekleme, retention). Örnek
// listesi bunu bilmeden link basıyordu → boş /trace sayfası. Burada örneklerin
// ayrık trace id'leri TEK TraceFactsByIDs sorgusuyla (spans, bloom index,
// max_execution_time 5) aranır: pencere örnek satırlarının [en eski, en yeni]
// aralığı ±5 dk (v0.10.1100 Oracle explain bağlamıyla aynı pay — Oracle saat
// damgası ile span zamanı arasındaki kayma), ≤100 id, 6 sn zaman aşımı. Hata /
// zaman aşımı → alan boş kalır (bilinmiyor, ön yüz link basar); örnek çağrısı
// ASLA düşmez. Span grupları bu yoldan geçmez: örnek span'den doğar.

import (
	"context"
	"strings"
	"time"
)

const (
	// oracleSampleTraceMaxIDs — örnek tavanıyla (GetExceptionGroupSamples ≤100) aynı.
	oracleSampleTraceMaxIDs = 100
	// oracleSampleTraceSlack — satır penceresinin iki yanına pay (explain ile aynı).
	oracleSampleTraceSlack = 5 * time.Minute
	// oracleSampleTraceTimeout — tek sorgunun bütçesi; aşılırsa alan bilinmiyor.
	oracleSampleTraceTimeout = 6 * time.Second
)

// traceFactsLookup — Store.TraceFactsByIDs'in imzası (test dikişi).
type traceFactsLookup func(ctx context.Context, ids []string, from, to time.Time) (map[string]TraceFact, error)

// oracleSampleTraceIDs — SAF: örneklerdeki ayrık, boş olmayan trace id'ler
// (sıra korunur), ≤ oracleSampleTraceMaxIDs.
func oracleSampleTraceIDs(samples []ExceptionSample) []string {
	seen := map[string]bool{}
	var out []string
	for _, sm := range samples {
		id := strings.TrimSpace(sm.TraceID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= oracleSampleTraceMaxIDs {
			break
		}
	}
	return out
}

// oracleSampleTraceWindow — SAF: trace id taşıyan örneklerin zaman aralığı ±pay.
// ok=false → aranacak örnek yok.
func oracleSampleTraceWindow(samples []ExceptionSample) (from, to time.Time, ok bool) {
	var lo, hi int64
	for _, sm := range samples {
		if strings.TrimSpace(sm.TraceID) == "" || sm.Time <= 0 {
			continue
		}
		if !ok || sm.Time < lo {
			lo = sm.Time
		}
		if !ok || sm.Time > hi {
			hi = sm.Time
		}
		ok = true
	}
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(0, lo).UTC().Add(-oracleSampleTraceSlack), time.Unix(0, hi).UTC().Add(oracleSampleTraceSlack), true
}

// markSampleTraces — SAF: bulunan kümeye göre TraceInCoremetry. found nil →
// sorgu yapılamadı, hiçbir alan dokunulmaz (bilinmiyor). Yalnız SORULAN id'ler
// işaretlenir (tavan dışı kalan "yok" sayılmaz); trace id'siz örnek
// işaretlenmez (link zaten yok).
func markSampleTraces(samples []ExceptionSample, asked []string, found map[string]TraceFact) {
	if found == nil {
		return
	}
	askedSet := make(map[string]bool, len(asked))
	for _, id := range asked {
		askedSet[id] = true
	}
	for i := range samples {
		id := strings.TrimSpace(samples[i].TraceID)
		if id == "" || !askedSet[id] {
			continue
		}
		_, ok := found[id]
		samples[i].TraceInCoremetry = &ok
	}
}

// markOracleSampleTraces — tek sınırlı arama + işaretleme. Hata → dokunmaz.
func markOracleSampleTraces(ctx context.Context, samples []ExceptionSample, lookup traceFactsLookup) {
	ids := oracleSampleTraceIDs(samples)
	if len(ids) == 0 || lookup == nil {
		return
	}
	from, to, ok := oracleSampleTraceWindow(samples)
	if !ok {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, oracleSampleTraceTimeout)
	defer cancel()
	found, err := lookup(tctx, ids, from, to)
	if err != nil {
		return
	}
	if found == nil {
		found = map[string]TraceFact{}
	}
	markSampleTraces(samples, ids, found)
}
