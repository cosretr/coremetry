package logstore

// es_ml_pods.go — v0.10.2043 (Operator-reported: log ML olayı "Api" diye
// izlenmeyen bir servise yazıldı; ES servis alanı uygulamanın kendi yazdığı
// genel ad). Katalogda olmayan log servis adları için TEK ek sorgu: bu
// adların penceredeki pod adları (servis başına en çok 3). size 0; tavanı
// ve sayacı çağıranın ML bütçesinde (ML tik ES sayacı).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MLPodResolver — isteğe bağlı arayüz (yalnız ES); CH log deposu uygulamaz.
type MLPodResolver interface {
	MLServicePods(ctx context.Context, services []string, from, to time.Time, perService int) (map[string][]string, error)
}

// mlServicePodsBody — SAF: ES gövdesi.
func mlServicePodsBody(tsField, svcField, podField string, services []string, from, to time.Time, perService int, timeout string) []byte {
	body := map[string]any{
		"size": 0, "track_total_hits": false, "timeout": timeout,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"range": map[string]any{tsField: map[string]any{"gte": from.UTC().Format(time.RFC3339Nano), "lt": to.UTC().Format(time.RFC3339Nano)}}},
			map[string]any{"terms": map[string]any{svcField: services}},
		}}},
		"aggs": map[string]any{"svc": map[string]any{
			"terms": map[string]any{"field": svcField, "size": len(services)},
			"aggs":  map[string]any{"pod": map[string]any{"terms": map[string]any{"field": podField, "size": perService}}},
		}},
	}
	b, _ := json.Marshal(body)
	return b
}

// parseMLServicePods — SAF.
func parseMLServicePods(aggs json.RawMessage) (map[string][]string, error) {
	var a struct {
		Svc struct {
			Buckets []struct {
				Key json.RawMessage `json:"key"`
				Pod struct {
					Buckets []struct {
						Key json.RawMessage `json:"key"`
					} `json:"buckets"`
				} `json:"pod"`
			} `json:"buckets"`
		} `json:"svc"`
	}
	if err := json.Unmarshal(aggs, &a); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, b := range a.Svc.Buckets {
		svc := rawKeyString(b.Key)
		for _, p := range b.Pod.Buckets {
			out[svc] = append(out[svc], rawKeyString(p.Key))
		}
	}
	return out, nil
}

// MLServicePods — adların pod'ları. Pod alanı toplanamazsa (nil, nil): pod
// çözümü yok, çağıran yalnız katalog eşleşmesine düşer.
func (s *ESStore) MLServicePods(ctx context.Context, services []string, from, to time.Time, perService int) (map[string][]string, error) {
	if len(services) == 0 {
		return map[string][]string{}, nil
	}
	if perService <= 0 || perService > 3 {
		perService = 3
	}
	svcField := s.termsAggField(ctx, s.fields.Service)
	podField := s.MLResolveField(ctx, esPodFields)
	if svcField == "" || podField == "" {
		return nil, nil
	}
	body := mlServicePodsBody(s.fields.Timestamp, svcField, podField, services, from, to, perService, esTimeseriesTimeoutFromEnv("10s"))
	_, aggs, env, err := s.rawCountSearchEnv(ctx, nil, body, 0, true)
	if err != nil {
		return nil, err
	}
	if env.partial() {
		return nil, fmt.Errorf("%w: %s", ErrESPartial, env.describe())
	}
	return parseMLServicePods(aggs)
}
