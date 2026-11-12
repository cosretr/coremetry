package ml

// thanos_adapter.go — v0.10.2019: ML metrik Thanos yolunun mevcut Thanos
// istemcisine bağlantısı. Yeni HTTP katmanı YOK: arka plan işçilerinin tek
// girişi thanos.Service.WorkerQuery (anlık sorgu, fail-closed token,
// cluster etiketi enjeksiyonu, dedup=true, partial_response=false, seri ve
// gövde tavanı). Kesik ya da kısmi sonuç HATA sayılır: kısmi okumadan
// yokluk çıkarılmaz (worker_query.go §4.10 kuralı).

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// ThanosCluster — etkin bir Thanos cluster'ı (ML'nin gördüğü kadar).
type ThanosCluster struct {
	ID, Name        string
	NamespaceFilter string // namespace=~ regex'i; boş = hepsi
}

// ThanosSample — anlık vektörün bir örneği.
type ThanosSample struct {
	Labels map[string]string
	Value  float64
}

// ThanosQuerier — ML'nin Thanos okuma sözleşmesi (sahte sunucuyla sınanır).
type ThanosQuerier interface {
	Clusters() []ThanosCluster
	Instant(ctx context.Context, clusterID, expr string, at time.Time) ([]ThanosSample, error)
}

// errThanosPartial — kesik ya da kısmi yanıt (sonuç kullanılmaz).
var errThanosPartial = errors.New("thanos: kısmi ya da kesik yanıt")

type thanosAdapter struct{ svc *thanos.Service }

// NewThanosQuerier — svc nil ise nil (Thanos yapılandırılmamış).
func NewThanosQuerier(svc *thanos.Service) ThanosQuerier {
	if svc == nil {
		return nil
	}
	return thanosAdapter{svc: svc}
}

func (a thanosAdapter) Clusters() []ThanosCluster {
	var out []ThanosCluster
	for _, c := range a.svc.CurrentSettings().Clusters {
		if c.Enabled && c.URL != "" {
			out = append(out, ThanosCluster{ID: c.EffectiveID(), Name: c.Name, NamespaceFilter: c.NamespaceFilter})
		}
	}
	return out
}

func (a thanosAdapter) Instant(ctx context.Context, clusterID, expr string, at time.Time) ([]ThanosSample, error) {
	res, err := a.svc.WorkerQuery(ctx, clusterID, expr, thanos.WorkerLimits{MaxSeries: thanosMaxSeries, TimeoutS: thanosQueryTimeoutS, Time: at})
	if err != nil {
		return nil, err
	}
	if res.Truncated || res.Partial() {
		return nil, errThanosPartial
	}
	return decodeVector(res.ResultType, res.Result)
}

// decodeVector — SAF: Prometheus anlık vektörü → örnekler (sonlu değerler).
func decodeVector(resultType string, raw json.RawMessage) ([]ThanosSample, error) {
	if resultType != "vector" || len(raw) == 0 {
		return nil, nil
	}
	var rows []struct {
		Metric map[string]string `json:"metric"`
		Value  [2]any            `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]ThanosSample, 0, len(rows))
	for _, r := range rows {
		s, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, ThanosSample{Labels: r.Metric, Value: v})
	}
	return out, nil
}
