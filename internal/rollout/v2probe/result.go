package v2probe

// result.go — v0.10.979 — sonuç modeli, Prometheus zarf ayrıştırıcıları ve
// sourcestate sınıflaması. SAF.
//
// v2probe thanos'u içe aktarmaz: api katmanı ConsoleResult alanlarını
// (ResultType, Result, Warnings, Infos, Series, TotalSeries, Truncated) ve
// hata türünü küçük bir Raw yapısına kopyalar; sınıflama burada, tek yerde.
// argocd.ParseCountVector yalnız namespace anahtarlar (discover.go); burada
// genel vektör ayrıştırıcı: her satırın bütün etiketleri + değeri.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// Row — bir vektör satırı: etiketler (jetonlanır) + ham değer dizesi.
type Row struct {
	Labels map[string]string `json:"labels"`
	Value  string            `json:"value"`
}

// Result — bir çağrının sonucu. Unit ham koşuda birimin kimliği (Remote
// Cluster EffectiveID ya da "clickhouse"); Finalize jetona çevirir.
type Result struct {
	ID      string            `json:"id"`
	Unit    string            `json:"unit"`
	Variant string            `json:"variant,omitempty"`
	State   sourcestate.State `json:"state"`
	Skipped bool              `json:"skipped,omitempty"`
	Detail  string            `json:"detail,omitempty"`
	Scalar  *string           `json:"scalar,omitempty"`
	Rows    []Row             `json:"rows,omitempty"`
	Names   []string          `json:"names,omitempty"`
	// Truncated/Total — thanos akışlı tavan ya da MaxRows kesimi; Total
	// upstream'in döndürdüğü satır/seri sayısı.
	Truncated  bool      `json:"truncated,omitempty"`
	Total      int       `json:"total,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
	Infos      []string  `json:"infos,omitempty"`
	SampleAt   time.Time `json:"sampleAt"`
	DurationMs int64     `json:"durationMs"`
	// VariantRaw — inst varyantının ham hubNamespace'i; Finalize jetonlar ve
	// siler. JSON'a GİRMEZ.
	VariantRaw string `json:"-"`
}

// Key — sonuç dizini anahtarı.
func (r Result) Key() string { return r.ID + "|" + r.Unit + "|" + r.Variant }

// Usable — kanıt olarak okunabilir (sourcestate.Status.Usable ile aynı küme).
func (r Result) Usable() bool {
	if r.Skipped {
		return false
	}
	switch r.State {
	case sourcestate.OK, sourcestate.Empty, sourcestate.Partial, sourcestate.Truncated, sourcestate.Delayed:
		return true
	}
	return false
}

// ScalarFloat — skaler değer (ya da tek satırlı vektör) sayı olarak.
func (r Result) ScalarFloat() (float64, bool) {
	if r.Scalar != nil {
		return parseNum(*r.Scalar)
	}
	if len(r.Rows) == 1 {
		return parseNum(r.Rows[0].Value)
	}
	return 0, false
}

// ScalarOrZero — Usable ve boş → 0 (count biçimli sorgular için "0 (empty)").
func (r Result) ScalarOrZero() (float64, bool) {
	if !r.Usable() {
		return 0, false
	}
	if r.State == sourcestate.Empty {
		return 0, true
	}
	return r.ScalarFloat()
}

// HasName — ShapeNames listesinde ya da count by (__name__) satırlarında ad var mı.
func (r Result) HasName(name string) bool {
	for _, n := range r.Names {
		if n == name {
			return true
		}
	}
	for _, row := range r.Rows {
		if row.Labels["__name__"] == name {
			return true
		}
	}
	return false
}

// HasLabelKey — herhangi bir satırda etiket anahtarı var mı.
func (r Result) HasLabelKey(key string) bool {
	for _, row := range r.Rows {
		if _, ok := row.Labels[key]; ok {
			return true
		}
	}
	return false
}

// HasLabelValue — bir satırda key=value var mı.
func (r Result) HasLabelValue(key, value string) bool {
	for _, row := range r.Rows {
		if row.Labels[key] == value {
			return true
		}
	}
	return false
}

func parseNum(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// ParseVector — Prometheus data.result (vector) → satırlar. Değer dize değilse "".
func ParseVector(raw json.RawMessage) ([]Row, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Row{}, nil
	}
	var rows []struct {
		Metric map[string]string `json:"metric"`
		Value  []json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("vector çözülemedi: %w", err)
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		v := ""
		if len(r.Value) == 2 {
			_ = json.Unmarshal(r.Value[1], &v)
		}
		labels := r.Metric
		if labels == nil {
			labels = map[string]string{}
		}
		out = append(out, Row{Labels: labels, Value: v})
	}
	return out, nil
}

// ParseScalar — Prometheus data.result (scalar/string): [ts, "v"] → "v".
func ParseScalar(raw json.RawMessage) (string, error) {
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		return "", fmt.Errorf("scalar çözülemedi")
	}
	var v string
	if err := json.Unmarshal(pair[1], &v); err != nil {
		return "", fmt.Errorf("scalar değeri dize değil")
	}
	return v, nil
}

// Raw — api katmanının bir başarılı okumadan kopyaladığı alanlar.
type Raw struct {
	ResultType string
	Result     json.RawMessage
	Warnings   []string
	Infos      []string
	// Values — label API'leri (adlar / __name__ değerleri).
	Values []string
	// Total/Truncated — thanos'un döndürdüğü toplam ve kesilme.
	Total     int
	Truncated bool
}

// FromRaw — başarılı okuma → Result (biçim Shape'e göre). MaxRows kesimi
// Finalize'da (sıralamadan sonra) — burada bütün satırlar tutulur.
func FromRaw(q Query, raw Raw) (Result, error) {
	res := Result{Warnings: raw.Warnings, Infos: raw.Infos, Truncated: raw.Truncated, Total: raw.Total}
	returned := 0
	switch q.Kind {
	case KindLabels, KindLabelValues:
		res.Names = append([]string{}, raw.Values...)
		sort.Strings(res.Names)
		returned = len(res.Names)
	default:
		switch raw.ResultType {
		case "scalar", "string":
			v, err := ParseScalar(raw.Result)
			if err != nil {
				return res, err
			}
			res.Scalar = &v
			returned = 1
		default:
			rows, err := ParseVector(raw.Result)
			if err != nil {
				return res, err
			}
			switch q.Shape {
			case ShapeScalar:
				if len(rows) == 1 && len(rows[0].Labels) == 0 {
					v := rows[0].Value
					res.Scalar = &v
				} else {
					res.Rows = rows
				}
			case ShapeNames:
				// H5.2 fallback: yalnız etiket ADLARI; değerler burada düşer.
				seen := map[string]bool{}
				for _, r := range rows {
					for k := range r.Labels {
						seen[k] = true
					}
				}
				for k := range seen {
					res.Names = append(res.Names, k)
				}
				sort.Strings(res.Names)
			default:
				res.Rows = rows
			}
			returned = len(rows)
			if res.Total < returned {
				res.Total = returned
			}
		}
	}
	st := sourcestate.Result("metrics", "thanos", sourcestate.Outcome{
		Returned: returned, Partial: len(raw.Warnings) > 0, Truncated: raw.Truncated,
	})
	res.State = st.State
	return res, nil
}

// ErrorKind — api katmanının thanos/chstore hatasından çıkardığı tür.
// ConsoleErr* dizeleri aynen; ek: "cluster_unavailable", "token_unresolved",
// "deadline" (bağlam bütçesi), "not_configured".
const (
	ErrKindClusterUnavailable = "cluster_unavailable"
	ErrKindTokenUnresolved    = "token_unresolved"
	ErrKindDeadline           = "deadline"
	ErrKindNotConfigured      = "not_configured"
)

// ClassifyError — hata türü + upstream kodu → (durum, atlandı mı, detay
// öneki). SAF; §6.1 tablosu.
func ClassifyError(kind string, upstreamStatus int) (sourcestate.State, bool) {
	if upstreamStatus == 401 || upstreamStatus == 403 {
		return sourcestate.Unauthorized, false
	}
	switch kind {
	case "timeout":
		return sourcestate.Timeout, false
	case "unavailable", "internal":
		return sourcestate.Unreachable, false
	case "bad_data", "execution", "response_too_large":
		return sourcestate.Error, false
	case "canceled", ErrKindDeadline:
		return sourcestate.Error, true
	case ErrKindClusterUnavailable, ErrKindNotConfigured:
		return sourcestate.NotConfigured, false
	case ErrKindTokenUnresolved:
		return sourcestate.Unauthorized, false
	}
	return sourcestate.Error, false
}

// IsBadData — H5.1 → H5.2 fallback tetikleyicisi (detay "bad_data: …").
func (r Result) IsBadData() bool {
	return r.State == sourcestate.Error && !r.Skipped && strings.HasPrefix(r.Detail, "bad_data")
}
