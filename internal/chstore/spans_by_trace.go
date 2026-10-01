package chstore

// spans_by_trace.go — verilen trace id listesi için span özetleri
// (v0.10.229, Influx D4 audit §4 adım 3).
//
// CH'de trace ARANMAZ: liste Influx'tan (SORGU 2) gelir, burada yalnız
// `trace_id IN (...)` ile okunur — bloom `idx_trace` + zaman sınırı +
// LIMIT + max_execution_time (CLAUDE.md CH bounds). Katlama SAF
// (foldTraceSummaries) ve tablo-testli.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	spansByTraceMaxIDs  = 50
	spansByTraceMaxRows = 5000
)

// TraceSpanSummary — trace başına tek satır kanıt.
type TraceSpanSummary struct {
	TraceID        string `json:"traceId"`
	StartNs        int64  `json:"startNs"`
	DurationNs     int64  `json:"durationNs"`
	Spans          int    `json:"spans"`
	ErrorSpans     int    `json:"errorSpans"`
	RootService    string `json:"rootService,omitempty"`
	RootOp         string `json:"rootOp,omitempty"`
	ErrorService   string `json:"errorService,omitempty"`
	ErrorOp        string `json:"errorOp,omitempty"`
	SlowestService string `json:"slowestService,omitempty"`
	SlowestOp      string `json:"slowestOp,omitempty"`
}

type traceSpanRow struct {
	TraceID, SpanID, ParentID, Service, Name, Status string
	Time                                             time.Time
	DurationNs                                       int64
	// v0.10.1004 — endpoint kimliği için (TraceEndpointHit).
	Kind, Route string
}

// TraceEndpointHit — v0.10.1004: kanıttaki trace'lerin geçtiği ENDPOINT
// (Oracle Problem'i → "hangi endpoint"). Kimlik /endpoints sayfasıyla aynı
// kural (endpoints.go): http_route doluysa yol = http_route (HTTP); boşsa ve
// span server/consumer ise yol = span adı (RPC).
type TraceEndpointHit struct {
	Service     string `json:"service"`
	Path        string `json:"path"`
	RPC         bool   `json:"rpc,omitempty"`
	Traces      int    `json:"traces"`      // bu endpoint'ten geçen kanıt trace'i
	ErrorTraces int    `json:"errorTraces"` // bunlardan endpoint span'i hatalı olan
}

const traceEndpointTopN = 5

// SpanSummariesForTraces — ids ≤50 (fazlası kırpılır), pencere
// [from, to]; çağıran ±5 dk pad'ler. Sonuç en yeni trace önce.
func (s *Store) SpanSummariesForTraces(ctx context.Context, ids []string, from, to time.Time) ([]TraceSpanSummary, error) {
	sums, _, err := s.SpanEvidenceForTraces(ctx, ids, from, to, "")
	return sums, err
}

// SpanEvidenceForTraces — v0.10.1004: aynı TEK okumadan trace özetleri +
// endpoint dökümü (foldTraceEndpoints). service: Problem'in öznesi (gerçek
// servis adı) — boşsa trace'in hata veren servisi esas alınır.
func (s *Store) SpanEvidenceForTraces(ctx context.Context, ids []string, from, to time.Time, service string) ([]TraceSpanSummary, []TraceEndpointHit, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	if len(ids) > spansByTraceMaxIDs {
		ids = ids[:spansByTraceMaxIDs]
	}
	holders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, from, to)
	for i, id := range ids {
		holders[i] = "?"
		args = append(args, id)
	}
	rows, err := s.telemetryReadConn().Query(ctx, fmt.Sprintf(`
		SELECT trace_id, span_id, parent_id, service_name, name, status_code, time, duration, kind, http_route
		FROM spans
		WHERE time >= ? AND time <= ?
		  AND trace_id IN (%s)
		ORDER BY trace_id, time
		LIMIT %d
		SETTINGS max_execution_time = 5`, strings.Join(holders, ","), spansByTraceMaxRows), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var all []traceSpanRow
	for rows.Next() {
		var r traceSpanRow
		if err := rows.Scan(&r.TraceID, &r.SpanID, &r.ParentID, &r.Service, &r.Name, &r.Status, &r.Time, &r.DurationNs, &r.Kind, &r.Route); err != nil {
			return nil, nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return foldTraceSummaries(all), foldTraceEndpoints(all, service, traceEndpointTopN), nil
}

// entryEndpoint — SAF: span bir endpoint'in giriş span'i mi (endpoints.go
// kuralı). HTTP: route dolu ve span giden çağrı değil; RPC: route boş ve
// server/consumer.
func entryEndpoint(r traceSpanRow) (path string, rpc, ok bool) {
	kind := strings.ToLower(r.Kind)
	if r.Route != "" {
		if kind == "client" || kind == "producer" {
			return "", false, false
		}
		return r.Route, false, true
	}
	if (kind == "server" || kind == "consumer") && r.Name != "" {
		return r.Name, true, true
	}
	return "", false, false
}

// foldTraceEndpoints — SAF (tablo testli): trace başına TEK endpoint seçilir,
// sonra (servis, yol) başına sayılır; en çok trace'li önce, ≤topN.
//
// Hangi servis: `service` (Problem'in öznesi) trace'te bir giriş span'i
// taşıyorsa o; taşımıyorsa trace'in en derin hata span'inin servisi; o da yoksa
// trace'in en erken giriş span'i (kök giriş). Seçilen serviste birden çok
// giriş span'i varsa en erkeni — servise GİRİŞ noktası.
func foldTraceEndpoints(rows []traceSpanRow, service string, topN int) []TraceEndpointHit {
	if len(rows) == 0 {
		return nil
	}
	byTrace := map[string][]traceSpanRow{}
	order := []string{}
	for _, r := range rows {
		if _, seen := byTrace[r.TraceID]; !seen {
			order = append(order, r.TraceID)
		}
		byTrace[r.TraceID] = append(byTrace[r.TraceID], r)
	}
	type key struct {
		svc, path string
		rpc       bool
	}
	agg := map[key]*TraceEndpointHit{}
	for _, id := range order {
		sp := byTrace[id]
		sort.SliceStable(sp, func(i, j int) bool { return sp[i].Time.Before(sp[j].Time) })
		// Hata servisi = EN GEÇ başlayan hata span'inin servisi (zincirde en
		// derin hata; v0.10.892 kuralı — ilk hata span'i hemen her zaman giriş
		// noktasıdır ve hep aynı gateway'i gösterirdi).
		errSvc := ""
		for _, r := range sp {
			if r.Status == "error" {
				errSvc = r.Service
			}
		}
		pick := func(svc string) (traceSpanRow, string, bool, bool) {
			for _, r := range sp {
				if svc != "" && r.Service != svc {
					continue
				}
				if path, rpc, ok := entryEndpoint(r); ok {
					return r, path, rpc, true
				}
			}
			return traceSpanRow{}, "", false, false
		}
		var (
			r    traceSpanRow
			path string
			rpc  bool
			ok   bool
		)
		candidates := make([]string, 0, 3)
		if service != "" {
			candidates = append(candidates, service)
		}
		if errSvc != "" && errSvc != service {
			candidates = append(candidates, errSvc)
		}
		candidates = append(candidates, "") // herhangi bir servis: en erken giriş span'i
		for _, svc := range candidates {
			if r, path, rpc, ok = pick(svc); ok {
				break
			}
		}
		if !ok {
			continue
		}
		k := key{r.Service, path, rpc}
		h := agg[k]
		if h == nil {
			h = &TraceEndpointHit{Service: r.Service, Path: path, RPC: rpc}
			agg[k] = h
		}
		h.Traces++
		if r.Status == "error" {
			h.ErrorTraces++
		}
	}
	out := make([]TraceEndpointHit, 0, len(agg))
	for _, h := range agg {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Traces != out[j].Traces {
			return out[i].Traces > out[j].Traces
		}
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Path < out[j].Path
	})
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}

// foldTraceSummaries — SAF: kök = parent'ı boş ya da trace içinde
// bulunmayan ilk span (zaman sırası); hata = ilk status 'error' span;
// en yavaş = en uzun süreli span; süre = kök varsa kökün süresi, yoksa
// min(time)…max(time+duration). En yeni trace önce.
func foldTraceSummaries(rows []traceSpanRow) []TraceSpanSummary {
	if len(rows) == 0 {
		return nil
	}
	byTrace := map[string][]traceSpanRow{}
	order := []string{}
	for _, r := range rows {
		if _, seen := byTrace[r.TraceID]; !seen {
			order = append(order, r.TraceID)
		}
		byTrace[r.TraceID] = append(byTrace[r.TraceID], r)
	}
	out := make([]TraceSpanSummary, 0, len(order))
	for _, id := range order {
		sp := byTrace[id]
		sort.SliceStable(sp, func(i, j int) bool { return sp[i].Time.Before(sp[j].Time) })
		ids := make(map[string]bool, len(sp))
		for _, r := range sp {
			ids[r.SpanID] = true
		}
		sum := TraceSpanSummary{TraceID: id, Spans: len(sp), StartNs: sp[0].Time.UnixNano()}
		var endNs int64
		var slowest int64 = -1
		for _, r := range sp {
			if e := r.Time.UnixNano() + r.DurationNs; e > endNs {
				endNs = e
			}
			if sum.RootService == "" && (r.ParentID == "" || !ids[r.ParentID]) {
				sum.RootService, sum.RootOp = r.Service, r.Name
				sum.DurationNs = r.DurationNs
			}
			if r.Status == "error" {
				sum.ErrorSpans++
				if sum.ErrorService == "" {
					sum.ErrorService, sum.ErrorOp = r.Service, r.Name
				}
			}
			if r.DurationNs > slowest {
				slowest = r.DurationNs
				sum.SlowestService, sum.SlowestOp = r.Service, r.Name
			}
		}
		if sum.DurationNs == 0 {
			sum.DurationNs = endNs - sum.StartNs
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartNs > out[j].StartNs })
	return out
}
