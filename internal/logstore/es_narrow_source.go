package logstore

// es_narrow_source.go — v0.10.1831 (Log Search Faz 1 dilim 1.8; audit §3.2
// "Satırlar … _source.includes = görünen kolonlar + gövde + trace/span",
// "Tam doküman: ids sorgusu … yalnız detay açılınca").
//
// /logs listesi 500 satırlık örnek çeker; her satırın tam `_source`u
// (yüzlerce alan) çoğu zaman çizilmeyen veridir. Filter.SourceFields
// verilince ES `_source.includes` = tablonun ihtiyacı (zaman, gövde,
// seviye, servis, trace/span, pod/cluster zincirleri) + görünen kolonlar.
// Dönen satır `partial: true` ve `docRef` (<index>/<_id>) taşır; satır
// açılınca istemci tam dokümanı FetchDoc ile (tek `ids` sorgusu) alır.
// CH backend'inde etkisiz: satır zaten yalnız gereken kolonlar + öznitelik
// dizileridir (aynı API, `partial` hiç gelmez).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// MaxSourceFields — istemcinin isteyebileceği kolon alanı tavanı (URL /
// önbellek anahtarı kardinalitesi + ES isteği boyu).
const MaxSourceFields = 50

// DocFetcher — tam dokümanı kimlikle okuyabilen arka uç (yalnız ES). Çağıran
// Unwrap(store) üzerinden tip-sorgular.
type DocFetcher interface {
	FetchDoc(ctx context.Context, ref string, near time.Time) (*LogRecord, error)
}

// ErrDocNotFound — kimlik bu pencerede yok (silinmiş / ILM).
var ErrDocNotFound = errors.New("log document not found")

// narrowSourceFields — SAF: satır çiziminin gerektirdiği alanlar + kolonlar.
// Kolon adları attribute yollarıdır; ES `_source` alan joker/nokta yollarını
// aynen kabul eder. Sıralı + tekil (önbellek ve gövde deterministik).
func (s *ESStore) narrowSourceFields(cols []string) []string {
	base := append(s.leanSourceFields(),
		s.fields.TraceID, "trace.id", "trace_id", "traceId", "TraceId",
		s.fields.SpanID, "span.id", "span_id", "spanId", "SpanId",
		s.fields.Cluster, s.fields.Namespace, s.fields.Pod, s.fields.Env,
		"kubernetes.pod_name", "kubernetes.pod.name", "k8s.pod.name", "resource.k8s.pod.name", "pod_name", "pod",
		"kubernetes.namespace_name", "openshift.labels.cluster", "kubernetes.cluster.name", "k8s.cluster.name",
		"kubernetes.container_name", "resource.k8s.namespace.name",
		// v0.10.1882 — exception özeti (satır rozeti + katlı stack) için stack
		// alanları (stackparse.FromLog yazımları) + OTel tür/mesaj.
		"exception.type", "exception.message", "exception.stacktrace", "exception.stack_trace",
		"error.stack_trace", "error.stacktrace", "stack_trace", "stacktrace", "throwable", "*.throwable",
	)
	base = append(base, cols...)
	seen := make(map[string]struct{}, len(base))
	out := make([]string, 0, len(base))
	for _, f := range base {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// docRef — "<index>/<_id>"; boş parça → "".
func docRef(index, id string) string {
	if index == "" || id == "" {
		return ""
	}
	return index + "/" + id
}

// splitDocRef — SAF ters çevrim; indeks adında '/' olamaz (ES kuralı).
func splitDocRef(ref string) (index, id string, ok bool) {
	i := strings.IndexByte(ref, '/')
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// FetchDoc — tek dokümanı `ids` sorgusuyla, kendi indeksinde okur (size 1,
// tam _source). `near` yalnız kayıt (indeks zaten kesin); zaman süzgeci
// eklenmez — kimlik benzersiz.
func (s *ESStore) FetchDoc(ctx context.Context, ref string, _ time.Time) (*LogRecord, error) {
	index, id, ok := splitDocRef(ref)
	if !ok {
		return nil, fmt.Errorf("%w: geçersiz doküman referansı", ErrBadQuery)
	}
	body, err := json.Marshal(map[string]any{
		"size":             1,
		"track_total_hits": false,
		"timeout":          "5s",
		"query":            map[string]any{"ids": map[string]any{"values": []string{id}}},
		"docvalue_fields":  []any{map[string]any{"field": s.fields.Timestamp, "format": "epoch_millis"}},
	})
	if err != nil {
		return nil, err
	}
	req := esapi.SearchRequest{Index: []string{index}, Body: bytes.NewReader(body)}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, s.recordQueryError("doc", []string{index}, body, 0, fmt.Errorf("ES doc: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, s.recordQueryError("doc", []string{index}, body, res.StatusCode, parseESError("doc", res, s.cfg.Index))
	}
	var raw struct {
		esSearchEnvelope // timed_out + _shards (TestEverySearchDecodeCarriesTheEnvelope)
		Hits             struct {
			Hits []struct {
				Index  string         `json:"_index"`
				ID     string         `json:"_id"`
				Source map[string]any `json:"_source"`
				Fields map[string]any `json:"fields"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode ES doc: %w", err)
	}
	if len(raw.Hits.Hits) == 0 {
		return nil, ErrDocNotFound
	}
	h := raw.Hits.Hits[0]
	r := s.mapHit(h.ID, h.Source, h.Fields, "", "")
	r.DocRef = docRef(h.Index, h.ID)
	return r, nil
}
