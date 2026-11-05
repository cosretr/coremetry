package logstore

// es_ml.go — ML log dilim 1: tik başına TEK agregasyon (servis × seviye ×
// date_histogram) + en son belge damgası (ES ingest gecikmesi) + isteğe bağlı
// influencer (kanal kodu) terms'i, hepsi aynı istekte. size:0,
// track_total_hits:false, keyword alanlar, script/regex yok. Gövde SAF
// (mlLogAggBody, test), yürütme rawCountSearch (mevcut maliyet korumaları).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrESPartial — ML okuması kısmi (timed_out / _shards.failed > 0): sonuç
// seriye YAZILMAZ, kova eksik veri sayılır (v0.10.1765). Çağıran errors.Is ile tanır.
var ErrESPartial = errors.New("ES kısmi sonuç")

type MLLogAggRequest struct {
	From, To        time.Time
	BucketSec       int
	MaxServices     int
	InfluencerField string // keyword alan; boş = yok
	ErrorLevels     []string
	// Timeout — ES gövde zaman aşımı (backfill kendi süresini verir); 0 = tik varsayılanı.
	Timeout time.Duration
	// CountServices — v0.10.1812: kardeş cardinality agregasyonu (tavan dışı
	// servis sayısı, es_ml_truncation.go); yalnız tik isteği.
	CountServices bool
}

type MLLogRow struct {
	Service, Level string
	Bucket         int64 // unix s
	Count          int64
}

type MLLogInfluencer struct {
	Service, Value string
	Count          int64
}

type MLLogAggResult struct {
	Rows        []MLLogRow
	Influencers []MLLogInfluencer
	LatestDocMs int64 // penceredeki en yeni belge (ingest gecikmesi için); 0 = yok
	// Partial — timed_out ya da _shards.failed > 0: sayımlar alt küme (v0.10.1763).
	Partial       bool
	PartialReason string
	// DroppedServices — v0.10.1812: MaxServices tavanı dışında kalan servis
	// (MLDroppedServices); 0 = kesilme yok.
	DroppedServices int
}

// MLLogAggregator — isteğe bağlı arayüz; yalnız ES depo uygular. Çağıran
// Unwrap(store).(MLLogAggregator) ile yoklar; CH log deposu → dedektör sessiz.
type MLLogAggregator interface {
	MLLogAggregate(ctx context.Context, req MLLogAggRequest) (*MLLogAggResult, error)
	// MLResolveField — adaylardan ilk toplanabilir keyword alanı ("" = yok).
	MLResolveField(ctx context.Context, candidates []string) string
}

// mlLogAggBody — SAF: ES gövdesi.
func mlLogAggBody(tsField, svcField, lvlField string, req MLLogAggRequest, timeout string) []byte {
	lvl := map[string]any{
		"terms": map[string]any{"field": lvlField, "size": 12, "missing": "unknown"},
		"aggs": map[string]any{"hist": map[string]any{"date_histogram": map[string]any{
			"field": tsField, "fixed_interval": fmt.Sprintf("%ds", req.BucketSec), "min_doc_count": 1}}},
	}
	svcAggs := map[string]any{"lvl": lvl}
	if req.InfluencerField != "" && len(req.ErrorLevels) > 0 {
		svcAggs["err"] = map[string]any{
			"filter": map[string]any{"terms": map[string]any{lvlField: req.ErrorLevels}},
			"aggs":   map[string]any{"inf": map[string]any{"terms": map[string]any{"field": req.InfluencerField, "size": 5}}},
		}
	}
	body := map[string]any{
		"size": 0, "track_total_hits": false, "timeout": timeout,
		"query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"range": map[string]any{
			tsField: map[string]any{"gte": req.From.UTC().Format(time.RFC3339Nano), "lt": req.To.UTC().Format(time.RFC3339Nano)}}}}}},
		"aggs": map[string]any{
			"svc":    map[string]any{"terms": map[string]any{"field": svcField, "size": req.MaxServices}, "aggs": svcAggs},
			"latest": map[string]any{"max": map[string]any{"field": tsField}},
		},
	}
	if req.CountServices {
		body["aggs"].(map[string]any)["svc_n"] = mlServiceCardinalityAgg(svcField, req.MaxServices)
	}
	b, _ := json.Marshal(body)
	return b
}

type esBucket struct {
	Key      json.RawMessage `json:"key"`
	DocCount int64           `json:"doc_count"`
	Lvl      *struct {
		Buckets []struct {
			Key  string `json:"key"`
			Hist struct {
				Buckets []struct {
					Key      int64 `json:"key"`
					DocCount int64 `json:"doc_count"`
				} `json:"buckets"`
			} `json:"hist"`
		} `json:"buckets"`
	} `json:"lvl"`
	Err *struct {
		Inf struct {
			Buckets []struct {
				Key      json.RawMessage `json:"key"`
				DocCount int64           `json:"doc_count"`
			} `json:"buckets"`
		} `json:"inf"`
	} `json:"err"`
}

func rawKeyString(k json.RawMessage) string {
	var s string
	if json.Unmarshal(k, &s) == nil {
		return s
	}
	return string(k)
}

// parseMLLogAgg — SAF: aggregations JSON → satırlar.
func parseMLLogAgg(aggs json.RawMessage) (*MLLogAggResult, error) {
	var a struct {
		Svc struct {
			Buckets  []esBucket
			SumOther int64 `json:"sum_other_doc_count"`
		} `json:"svc"`
		SvcN *struct {
			Value int64 `json:"value"`
		} `json:"svc_n"`
		Latest struct {
			Value *float64 `json:"value"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(aggs, &a); err != nil {
		return nil, err
	}
	out := &MLLogAggResult{}
	var distinct int64
	if a.SvcN != nil {
		distinct = a.SvcN.Value
	}
	out.DroppedServices = MLDroppedServices(len(a.Svc.Buckets), a.Svc.SumOther, distinct, a.SvcN != nil)
	if a.Latest.Value != nil {
		out.LatestDocMs = int64(*a.Latest.Value)
	}
	for _, s := range a.Svc.Buckets {
		svc := rawKeyString(s.Key)
		if s.Lvl != nil {
			for _, l := range s.Lvl.Buckets {
				for _, h := range l.Hist.Buckets {
					out.Rows = append(out.Rows, MLLogRow{Service: svc, Level: l.Key, Bucket: h.Key / 1000, Count: h.DocCount})
				}
			}
		}
		if s.Err != nil {
			for _, i := range s.Err.Inf.Buckets {
				out.Influencers = append(out.Influencers, MLLogInfluencer{Service: svc, Value: rawKeyString(i.Key), Count: i.DocCount})
			}
		}
	}
	return out, nil
}

// mlFieldCache — MLResolveField sonucu (boş dahil) 1 sa (v0.10.1767; prod: her
// tikte field_caps tekrarlanıyordu — termsAggField yalnız bulunan alanı
// önbellekliyor, bulunamayan aday her tik yeniden soruluyordu).
var mlFieldCache sync.Map // key = adaylar → mlFieldEntry

type mlFieldEntry struct {
	v   string
	exp time.Time
}

const mlFieldTTL = time.Hour

func (s *ESStore) MLResolveField(ctx context.Context, candidates []string) string {
	key := strings.Join(candidates, "\x00")
	if e, ok := mlFieldCache.Load(key); ok && time.Now().Before(e.(mlFieldEntry).exp) {
		return e.(mlFieldEntry).v
	}
	v := s.mlResolveFieldUncached(ctx, candidates)
	mlFieldCache.Store(key, mlFieldEntry{v: v, exp: time.Now().Add(mlFieldTTL)})
	return v
}

func (s *ESStore) mlResolveFieldUncached(ctx context.Context, candidates []string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if f := s.termsAggField(ctx, c); f != "" {
			return f
		}
	}
	return ""
}

func (s *ESStore) MLLogAggregate(ctx context.Context, req MLLogAggRequest) (*MLLogAggResult, error) {
	if req.BucketSec <= 0 {
		req.BucketSec = 300
	}
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
	timeout := esTimeseriesTimeoutFromEnv("10s")
	if req.Timeout > 0 {
		timeout = fmt.Sprintf("%dms", req.Timeout.Milliseconds())
	}
	body := mlLogAggBody(s.fields.Timestamp, svcField, lvlField, req, timeout)
	_, aggs, env, err := s.rawCountSearchEnv(ctx, nil, body, 0, true)
	if err != nil {
		return nil, err
	}
	res, err := parseMLLogAgg(aggs)
	if err != nil {
		return nil, err
	}
	res.Partial, res.PartialReason = env.partial(), env.describe()
	return res, nil
}
