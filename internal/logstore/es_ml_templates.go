package logstore

// es_ml_templates.go — ML log dilim 2 (v0.10.1755, operatör kararı C: ES'te
// gerçek sayım). Drain şablon kimliği tikten tike kararsız ve sayımı 1000
// satır örnekten (v0.10.1030) → spike ES'te sayılır: tik başına TEK _search,
// aile başına bir `filters` kolu (servis term + gövdede sabit ifade
// match_phrase), her kolda date_histogram (min_doc_count 0 → sıfır kova da
// gelir; nadir şablon tabanı sıfırlardan oluşur). size 0, track_total_hits
// false, script/regex/wildcard yok. Gövde SAF (test).

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// MLTemplateFamily — bir aile probu.
type MLTemplateFamily struct {
	Service string
	Phrase  string // gövdede aranan sabit ifade (match_phrase)
}

type MLTemplateCountRequest struct {
	From, To  time.Time
	BucketSec int
	Families  []MLTemplateFamily // ≤ MLTemplateFamilyCap
}

// MLTemplateFamilyCap — tek istekteki kol tavanı.
const MLTemplateFamilyCap = 100

// MLTemplateCount — aile × kova sayımı (Families sırasıyla aynı indeks).
type MLTemplateCount struct {
	Index  int
	Bucket int64 // unix s
	Count  int64
}

// MLTemplateCounter — isteğe bağlı arayüz (yalnız ES).
type MLTemplateCounter interface {
	MLTemplateCounts(ctx context.Context, req MLTemplateCountRequest) ([]MLTemplateCount, error)
}

// mlTemplateBody — SAF.
func mlTemplateBody(tsField, svcField, bodyField string, req MLTemplateCountRequest, timeout string) []byte {
	filters := map[string]any{}
	for i, f := range req.Families {
		filters[strconv.Itoa(i)] = map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{svcField: f.Service}},
			map[string]any{"match_phrase": map[string]any{bodyField: f.Phrase}},
		}}}
	}
	from, to := req.From.UTC(), req.To.UTC()
	body := map[string]any{
		"size": 0, "track_total_hits": false, "timeout": timeout,
		"query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"range": map[string]any{
			tsField: map[string]any{"gte": from.Format(time.RFC3339Nano), "lt": to.Format(time.RFC3339Nano)}}}}}},
		"aggs": map[string]any{"fam": map[string]any{
			"filters": map[string]any{"filters": filters},
			"aggs": map[string]any{"hist": map[string]any{"date_histogram": map[string]any{
				"field": tsField, "fixed_interval": fmt.Sprintf("%ds", req.BucketSec), "min_doc_count": 0,
				"extended_bounds": map[string]any{"min": from.UnixMilli(), "max": to.Add(-time.Millisecond).UnixMilli()}}}},
		}},
	}
	b, _ := json.Marshal(body)
	return b
}

// parseMLTemplateAgg — SAF.
func parseMLTemplateAgg(aggs json.RawMessage) ([]MLTemplateCount, error) {
	var a struct {
		Fam struct {
			Buckets map[string]struct {
				Hist struct {
					Buckets []struct {
						Key      int64 `json:"key"`
						DocCount int64 `json:"doc_count"`
					} `json:"buckets"`
				} `json:"hist"`
			} `json:"buckets"`
		} `json:"fam"`
	}
	if err := json.Unmarshal(aggs, &a); err != nil {
		return nil, err
	}
	var out []MLTemplateCount
	for k, b := range a.Fam.Buckets {
		i, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		for _, h := range b.Hist.Buckets {
			out = append(out, MLTemplateCount{Index: i, Bucket: h.Key / 1000, Count: h.DocCount})
		}
	}
	return out, nil
}

func (s *ESStore) MLTemplateCounts(ctx context.Context, req MLTemplateCountRequest) ([]MLTemplateCount, error) {
	if len(req.Families) == 0 {
		return nil, nil
	}
	if len(req.Families) > MLTemplateFamilyCap {
		req.Families = req.Families[:MLTemplateFamilyCap]
	}
	if req.BucketSec <= 0 {
		req.BucketSec = 300
	}
	svcField := s.termsAggField(ctx, s.fields.Service)
	if svcField == "" {
		return nil, fmt.Errorf("ml: servis alanı toplanabilir değil (%q)", s.fields.Service)
	}
	body := mlTemplateBody(s.fields.Timestamp, svcField, s.fields.Body, req, esTimeseriesTimeoutFromEnv("10s"))
	_, aggs, env, err := s.rawCountSearchEnv(ctx, nil, body, 0, true)
	if err != nil {
		return nil, err
	}
	if env.partial() {
		return nil, fmt.Errorf("%w: %s", ErrESPartial, env.describe())
	}
	return parseMLTemplateAgg(aggs)
}
