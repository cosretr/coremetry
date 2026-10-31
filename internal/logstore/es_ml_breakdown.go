package logstore

// es_ml_breakdown.go — ML log dilim 3 (v0.10.1756): nadir hata kodu + aykırı
// pod için TEK _search. Servis terms altında iki kol: hata seviyeleri
// filtresi → kod terms (ilk 20), pod terms (ilk 50) → toplam + hata filtresi.
// Kod / pod alanı çağırandan (keşif MLResolveField ile, saatte bir); boş
// alanın kolu gövdeye girmez. size 0, track_total_hits false, script yok.
// Backfill (nadir kod tabanı): aynı gövde, kod kolu altında günlük histogram.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MLPodFieldCandidates — pod alanı adayları (log Filter.Pod ile aynı liste).
func MLPodFieldCandidates() []string { return append([]string{}, esPodFields...) }

type MLBreakdownRequest struct {
	From, To    time.Time
	MaxServices int
	ErrorLevels []string
	CodeField   string // keyword; "" = kod kolu yok
	PodField    string // keyword; "" = pod kolu yok
	// DailyCodes — backfill: kod kolu altında 1 günlük histogram.
	DailyCodes bool
}

type MLCodeRow struct {
	Service, Code string
	Day           int64 // unix s (DailyCodes); 0 = pencere toplamı
	Count         int64
}

type MLPodRow struct {
	Service, Pod  string
	Total, Errors int64
}

type MLBreakdownResult struct {
	Codes []MLCodeRow
	Pods  []MLPodRow
}

type MLLogBreakdowner interface {
	MLLogBreakdown(ctx context.Context, req MLBreakdownRequest) (*MLBreakdownResult, error)
}

// mlBreakdownBody — SAF.
func mlBreakdownBody(tsField, svcField, lvlField string, req MLBreakdownRequest, timeout string) []byte {
	errFilter := map[string]any{"terms": map[string]any{lvlField: req.ErrorLevels}}
	svcAggs := map[string]any{}
	if req.CodeField != "" {
		code := map[string]any{"terms": map[string]any{"field": req.CodeField, "size": 20}}
		if req.DailyCodes {
			code["aggs"] = map[string]any{"day": map[string]any{"date_histogram": map[string]any{"field": tsField, "fixed_interval": "1d", "min_doc_count": 1}}}
		}
		svcAggs["err"] = map[string]any{"filter": errFilter, "aggs": map[string]any{"code": code}}
	}
	if req.PodField != "" && !req.DailyCodes {
		svcAggs["pod"] = map[string]any{"terms": map[string]any{"field": req.PodField, "size": 50},
			"aggs": map[string]any{"perr": map[string]any{"filter": errFilter}}}
	}
	body := map[string]any{
		"size": 0, "track_total_hits": false, "timeout": timeout,
		"query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"range": map[string]any{
			tsField: map[string]any{"gte": req.From.UTC().Format(time.RFC3339Nano), "lt": req.To.UTC().Format(time.RFC3339Nano)}}}}}},
		"aggs": map[string]any{"svc": map[string]any{"terms": map[string]any{"field": svcField, "size": req.MaxServices}, "aggs": svcAggs}},
	}
	b, _ := json.Marshal(body)
	return b
}

// parseMLBreakdown — SAF.
func parseMLBreakdown(aggs json.RawMessage) (*MLBreakdownResult, error) {
	type kb struct {
		Key      json.RawMessage `json:"key"`
		DocCount int64           `json:"doc_count"`
		Day      *struct {
			Buckets []struct {
				Key      int64 `json:"key"`
				DocCount int64 `json:"doc_count"`
			} `json:"buckets"`
		} `json:"day"`
		Perr *struct {
			DocCount int64 `json:"doc_count"`
		} `json:"perr"`
	}
	var a struct {
		Svc struct {
			Buckets []struct {
				Key json.RawMessage `json:"key"`
				Err *struct {
					Code struct{ Buckets []kb } `json:"code"`
				} `json:"err"`
				Pod *struct{ Buckets []kb } `json:"pod"`
			} `json:"buckets"`
		} `json:"svc"`
	}
	if err := json.Unmarshal(aggs, &a); err != nil {
		return nil, err
	}
	out := &MLBreakdownResult{}
	for _, s := range a.Svc.Buckets {
		svc := rawKeyString(s.Key)
		if s.Err != nil {
			for _, c := range s.Err.Code.Buckets {
				code := rawKeyString(c.Key)
				if c.Day != nil {
					for _, d := range c.Day.Buckets {
						out.Codes = append(out.Codes, MLCodeRow{Service: svc, Code: code, Day: d.Key / 1000, Count: d.DocCount})
					}
					continue
				}
				out.Codes = append(out.Codes, MLCodeRow{Service: svc, Code: code, Count: c.DocCount})
			}
		}
		if s.Pod != nil {
			for _, p := range s.Pod.Buckets {
				r := MLPodRow{Service: svc, Pod: rawKeyString(p.Key), Total: p.DocCount}
				if p.Perr != nil {
					r.Errors = p.Perr.DocCount
				}
				out.Pods = append(out.Pods, r)
			}
		}
	}
	return out, nil
}

func (s *ESStore) MLLogBreakdown(ctx context.Context, req MLBreakdownRequest) (*MLBreakdownResult, error) {
	if req.MaxServices <= 0 {
		req.MaxServices = 500
	}
	svcField := s.termsAggField(ctx, s.fields.Service)
	if svcField == "" {
		return nil, fmt.Errorf("ml: servis alanı toplanabilir değil (%q)", s.fields.Service)
	}
	lvlField := s.MLResolveField(ctx, severityCandidateKeywordFields(s.fields.SeverityTx))
	if lvlField == "" {
		return nil, fmt.Errorf("ml: seviye alanı toplanabilir değil")
	}
	body := mlBreakdownBody(s.fields.Timestamp, svcField, lvlField, req, esTimeseriesTimeoutFromEnv("10s"))
	_, aggs, err := s.rawCountSearch(ctx, nil, body, 0, true)
	if err != nil {
		return nil, err
	}
	return parseMLBreakdown(aggs)
}
