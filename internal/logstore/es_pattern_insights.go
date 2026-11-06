package logstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// es_pattern_insights.go — v0.10.1896 (Log Search Faz 4 dilim 4.2): ESStore
// PatternInsighter uygulaması. İki yol da TEK `_msearch`, ≤ PatternInsightsMax
// alt arama; her alt arama size:0, zaman aralığı sınırlı (buildQuery'nin
// range süzgeci), `timeout` PatternInsightTimeout (2 sn). İndeks listesi
// queryIndices ile (tek servis → servis şablonu indeksi; gün daraltması).
// Çağıran bunları yalnız `logs_search.patternInsights` açıkken ve Desenler
// sekmesi açılınca çağırır (api/api_logs_patterns.go).

var _ PatternInsighter = (*ESStore)(nil)

// patternInsightQuery — SAF: sayfa süzgeci + desen sorgusu, "⊕ ekle" ile aynı
// birleşim (CombinePatternSearch) → buildQuery. Dil (lucene / logql) aynen.
func (s *ESStore) patternInsightQuery(f Filter, pattern string) (Filter, map[string]any) {
	fq := f
	fq.Search = CombinePatternSearch(f.Search, pattern)
	return fq, s.buildQuery(fq)
}

// patternTrendBody — SAF: eğilim alt araması. Kovalar epoch hizalı,
// min_doc_count 0 + extended_bounds → boş kova da gelir (çizgi kesilmez).
func patternTrendBody(query map[string]any, tsField string, from, to time.Time, bucketSec int) map[string]any {
	return map[string]any{
		"size":             0,
		"track_total_hits": false,
		"timeout":          PatternInsightTimeout,
		"query":            query,
		"aggs": map[string]any{
			"t": map[string]any{
				"date_histogram": map[string]any{
					"field":           tsField,
					"fixed_interval":  fmt.Sprintf("%ds", bucketSec),
					"min_doc_count":   0,
					"extended_bounds": map[string]any{"min": from.UnixMilli(), "max": to.UnixMilli() - 1},
				},
			},
		},
	}
}

// patternExistBody — SAF: varlık yoklaması. terminate_after:1 → her shard ilk
// eşleşmede durur. track_total_hits 1 (false DEĞİL): size:0 + sayım kapalı
// sorguda ES eşleşmeyi hiç toplamayabilir ve "yok" ile "var" ayırt edilemez —
// yanlış "ilk kez görüldü" üretirdi. 1'e kadar sayım terminate_after ile aynı
// maliyet sınırında kalır.
func patternExistBody(query map[string]any) map[string]any {
	return map[string]any{
		"size":             0,
		"terminate_after":  1,
		"track_total_hits": 1,
		"timeout":          PatternInsightTimeout,
		"query":            query,
	}
}

type esInsightTrendResp struct {
	esSearchEnvelope
	Aggregations struct {
		T struct {
			Buckets []struct {
				Key      int64 `json:"key"`
				DocCount int64 `json:"doc_count"`
			} `json:"buckets"`
		} `json:"t"`
	} `json:"aggregations"`
	Error any `json:"error,omitempty"`
}

type esInsightExistResp struct {
	esSearchEnvelope
	TerminatedEarly bool `json:"terminated_early"`
	Hits            struct {
		Total *esTotal `json:"total"`
	} `json:"hits"`
	Error any `json:"error,omitempty"`
}

// existStateOf — SAF: tek yoklama yanıtı → durum. Eşleşme varsa (kısmi bile
// olsa) found; sıfır + kısmi / hata → unknown (yokluk kanıtı değil).
func existStateOf(r esInsightExistResp) ExistState {
	if r.Error != nil {
		return ExistUnknown
	}
	if (r.Hits.Total != nil && r.Hits.Total.Value > 0) || r.TerminatedEarly {
		return ExistFound
	}
	if r.partial() || r.Hits.Total == nil {
		return ExistUnknown
	}
	return ExistAbsent
}

// trendOf — SAF: tek eğilim yanıtı → PatternTrend.
func trendOf(r esInsightTrendResp, from, to time.Time, bucketSec int) PatternTrend {
	if r.Error != nil {
		return PatternTrend{Failed: true}
	}
	by := make(map[int64]int64, len(r.Aggregations.T.Buckets))
	for _, b := range r.Aggregations.T.Buckets {
		by[b.Key] = b.DocCount
	}
	return PatternTrend{Points: TrendPoints(by, from, to, bucketSec), Partial: r.partial()}
}

// msearchNDJSON — alt arama başına başlık + gövde satırı.
func msearchNDJSON(idx [][]string, bodies []map[string]any) (string, error) {
	var b strings.Builder
	for i, body := range bodies {
		hb, err := json.Marshal(map[string]any{"index": idx[i], "allow_no_indices": true, "ignore_unavailable": true})
		if err != nil {
			return "", err
		}
		bb, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		b.Write(hb)
		b.WriteByte('\n')
		b.Write(bb)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// doInsightMsearch — tek `_msearch` gidiş-dönüşü; yanıt dizisi ham döner.
func (s *ESStore) doInsightMsearch(ctx context.Context, op string, idx [][]string, bodies []map[string]any) ([]json.RawMessage, error) {
	nd, err := msearchNDJSON(idx, bodies)
	if err != nil {
		return nil, err
	}
	var first []string
	if len(idx) > 0 {
		first = idx[0]
	}
	res, err := esapi.MsearchRequest{Body: strings.NewReader(nd)}.Do(ctx, s.cli)
	if err != nil {
		return nil, s.recordQueryError(op, first, []byte(nd), 0, fmt.Errorf("ES %s: %w", op, err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, s.recordQueryError(op, first, []byte(nd), res.StatusCode, parseESError(op, res, s.cfg.Index))
	}
	var env struct {
		Responses []json.RawMessage `json:"responses"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode ES %s: %w", op, err)
	}
	return env.Responses, nil
}

// PatternTrends — desen başına eğilim kovaları, tek `_msearch`. queries
// PatternInsightsMax'e kırpılır; boş sorgu yuvası istek atmadan boş kalır.
func (s *ESStore) PatternTrends(ctx context.Context, f Filter, queries []string, bucketSec int) ([]PatternTrend, error) {
	if len(queries) > PatternInsightsMax {
		queries = queries[:PatternInsightsMax]
	}
	out := make([]PatternTrend, len(queries))
	if bucketSec <= 0 || f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return out, fmt.Errorf("pattern trends: bounded window required")
	}
	var idx [][]string
	var bodies []map[string]any
	var slot []int
	for i, q := range queries {
		fq, query := s.patternInsightQuery(f, q)
		if fq.Search == "" {
			continue
		}
		idx = append(idx, s.queryIndices(ctx, fq))
		bodies = append(bodies, patternTrendBody(query, s.fields.Timestamp, f.From, f.To, bucketSec))
		slot = append(slot, i)
	}
	if len(bodies) == 0 {
		return out, nil
	}
	resp, err := s.doInsightMsearch(ctx, "msearch pattern-trends", idx, bodies)
	if err != nil {
		return out, err
	}
	for j, raw := range resp {
		if j >= len(slot) {
			break
		}
		var r esInsightTrendResp
		if json.Unmarshal(raw, &r) != nil {
			out[slot[j]] = PatternTrend{Failed: true}
			continue
		}
		if d := r.describe(); d != "" {
			log.Printf("[logstore-es] PARTIAL pattern trend (%s)", d)
		}
		out[slot[j]] = trendOf(r, f.From, f.To, bucketSec)
	}
	return out, nil
}

// PatternsExist — yoklama başına varlık, tek `_msearch`. Her yoklama kendi
// penceresiyle (f'nin From/To'su yoklamanınkiyle değişir). Boş sorgu →
// unknown, istek atılmaz.
func (s *ESStore) PatternsExist(ctx context.Context, f Filter, probes []PatternProbe) ([]ExistState, error) {
	if len(probes) > PatternInsightsMax {
		probes = probes[:PatternInsightsMax]
	}
	out := make([]ExistState, len(probes))
	for i := range out {
		out[i] = ExistUnknown
	}
	var idx [][]string
	var bodies []map[string]any
	var slot []int
	for i, p := range probes {
		if p.From.IsZero() || p.To.IsZero() || !p.To.After(p.From) {
			continue
		}
		pf := f
		pf.From, pf.To = p.From, p.To
		fq, query := s.patternInsightQuery(pf, p.Query)
		if fq.Search == "" {
			continue
		}
		idx = append(idx, s.queryIndices(ctx, fq))
		bodies = append(bodies, patternExistBody(query))
		slot = append(slot, i)
	}
	if len(bodies) == 0 {
		return out, nil
	}
	resp, err := s.doInsightMsearch(ctx, "msearch pattern-exist", idx, bodies)
	if err != nil {
		return out, err
	}
	for j, raw := range resp {
		if j >= len(slot) {
			break
		}
		var r esInsightExistResp
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		out[slot[j]] = existStateOf(r)
	}
	return out, nil
}
