package logstore

// fieldstats_sample_test.go — v0.10.1852 (Log Search Faz 2 dilim 2.1).
// Alan paneli üst değerleri ES random_sampler altında: olasılık basamakları,
// sürüm kapısı (ES < 8.2 / OpenSearch → örneklemesiz), ret → örneklemesiz
// tekrar + süreç ömrü kilidi, örneklemesiz gövdenin bayt-bayt aynı kalması.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFieldStatsSampleProbability(t *testing.T) {
	cases := []struct {
		est  int64
		want float64
	}{
		{0, 0},
		{199_999, 0},              // hedefin altı → örneklemesiz
		{200_000, 0.5},            // oran 1 → tavan 0.5
		{399_999, 0.5},            // oran > 0.5 → tavan
		{400_000, 0.5},            // oran tam 0.5
		{400_001, 0.5},            // oran 0.49999 → hedefi karşılayan en küçük basamak 0.5
		{1_000_000, 0.2},          // oran 0.2
		{1_000_001, 0.2},          // 0.199999 → 0.2
		{2_000_000, 0.1},          // 0.1
		{4_000_000, 0.05},         // 0.05
		{10_000_000, 0.02},        // 0.02
		{20_000_000, 0.01},        // 0.01
		{40_000_000, 0.005},       // 0.005
		{100_000_000, 0.002},      // 0.002
		{200_000_000, 0.001},      // 0.001
		{400_000_000, 0.0005},     // 0.0005
		{1_000_000_000, 0.0002},   // 0.0002
		{2_000_000_000, 0.0001},   // 0.0001
		{100_000_000_000, 0.0001}, // en küçük basamağın altı → en küçük basamak
	}
	for _, c := range cases {
		if got := FieldStatsSampleProbability(c.est); got != c.want {
			t.Errorf("est=%d: p=%v, want %v", c.est, got, c.want)
		}
		if p := FieldStatsSampleProbability(c.est); p > 0 && float64(c.est)*p < float64(FieldStatsSampleTarget) && p != fieldStatsSampleRungs[0] && p != fieldStatsSampleRungs[len(fieldStatsSampleRungs)-1] {
			t.Errorf("est=%d p=%v: örnek hedefin altında kalıyor", c.est, p)
		}
	}
}

func TestESSupportsRandomSampler(t *testing.T) {
	cases := map[string]bool{
		"":               true, // okunamadı → dene (ret kilitler)
		"garbage":        true,
		"7.17.9":         false,
		"8.0.0":          false,
		"8.1.3":          false,
		"8.2.0":          true,
		"8.2.0-SNAPSHOT": true,
		"8.13.4":         true,
		"9.0.1":          true,
		"2.11.0":         false, // OpenSearch
		"1.3.18":         false,
	}
	for v, want := range cases {
		if got := esSupportsRandomSampler(v); got != want {
			t.Errorf("version %q: %v, want %v", v, got, want)
		}
	}
}

// Örneklemesiz gövde bugünküyle aynı (aggs.vals); örneklemli gövde terms'i
// random_sampler altına koyar, maliyet bekçileri (size:0, sayım yok, soft
// timeout) iki gövdede de aynı.
func TestBuildFieldStatsBodySampled(t *testing.T) {
	q := map[string]any{"match_all": map[string]any{}}
	plain, _ := json.Marshal(buildFieldStatsBody(q, "f.keyword", 5, "10s"))
	zero, _ := json.Marshal(buildFieldStatsBodySampled(q, "f.keyword", 5, "10s", 0))
	if string(plain) != string(zero) {
		t.Fatalf("p=0 gövdesi değişti:\n%s\n%s", plain, zero)
	}
	if strings.Contains(string(plain), "random_sampler") {
		t.Fatal("örneklemesiz gövdede random_sampler olmamalı")
	}
	b := buildFieldStatsBodySampled(q, "f.keyword", 5, "10s", 0.05)
	if b["size"] != 0 || b["track_total_hits"] != false || b["timeout"] != "10s" {
		t.Fatalf("maliyet bekçileri: %#v", b)
	}
	s := b["aggs"].(map[string]any)["s"].(map[string]any)
	rs := s["random_sampler"].(map[string]any)
	if rs["probability"] != 0.05 || rs["seed"] != fieldStatsSampleSeed {
		t.Fatalf("random_sampler: %#v", rs)
	}
	terms := s["aggs"].(map[string]any)["vals"].(map[string]any)["terms"].(map[string]any)
	if terms["field"] != "f.keyword" || terms["size"] != 5 || terms["shard_size"] != 50 {
		t.Fatalf("terms: %#v", terms)
	}
}

// samplerES — kök + _search; random_sampler'lı gövdeyi `reject` ise 400 ile reddeder.
type samplerES struct {
	mu      sync.Mutex
	version string
	reject  bool
	bodies  []string
}

func (st *samplerES) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case r.URL.Path == "/":
		_, _ = w.Write([]byte(`{"name":"n1","cluster_name":"c","version":{"number":"` + st.version + `"}}`))
	case strings.HasSuffix(r.URL.Path, "/_search"):
		body, _ := io.ReadAll(r.Body)
		st.bodies = append(st.bodies, string(body))
		if strings.Contains(string(body), "random_sampler") {
			if st.reject {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"type":"x_content_parse_exception","reason":"[1:120] unknown aggregation type [random_sampler]"},"status":400}`))
				return
			}
			_, _ = w.Write([]byte(`{"aggregations":{"s":{"doc_count":2000,"vals":{"sum_other_doc_count":200,"buckets":[{"key":"MOBILE","doc_count":1200},{"key":"WEB","doc_count":600}]}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"aggregations":{"vals":{"sum_other_doc_count":10,"buckets":[{"key":"MOBILE","doc_count":60},{"key":"WEB","doc_count":30}]}}}`))
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"type":"not_found","reason":"stub"},"status":404}`))
	}
}

func newSamplerES(t *testing.T, version string, reject bool) (*ESStore, *samplerES) {
	t.Helper()
	st := &samplerES{version: version, reject: reject}
	srv := httptest.NewServer(http.HandlerFunc(st.handler))
	t.Cleanup(srv.Close)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Index: "app-*"})
	if err != nil {
		t.Fatalf("NewES: %v", err)
	}
	return es, st
}

func (st *samplerES) sent() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.bodies...)
}

func sampleFilter() Filter {
	now := time.Now()
	return Filter{Service: "orders-api", From: now.Add(-15 * time.Minute), To: now}
}

func TestESFieldStatsSampled(t *testing.T) {
	es, st := newSamplerES(t, "8.13.0", false)
	res, err := es.FieldStatsSampled(context.Background(), sampleFilter(), "CHANNEL_CODE", 5, 0.05)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Sampled || res.Probability != 0.05 {
		t.Fatalf("sampled/probability: %+v", res)
	}
	// Pay ve payda aynı örnekten: 1200+600+200.
	if res.Total != 2000 || len(res.Values) != 2 || res.Values[0].Value != "MOBILE" || res.Values[0].Count != 1200 {
		t.Fatalf("örnek içi sayım: %+v", res)
	}
	b := st.sent()
	if len(b) != 1 || !strings.Contains(b[0], `"random_sampler"`) || !strings.Contains(b[0], `"CHANNEL_CODE.keyword"`) {
		t.Fatalf("gönderilen gövde: %v", b)
	}
}

func TestESFieldStatsSampledOldVersionFallsBack(t *testing.T) {
	for _, v := range []string{"8.1.3", "7.17.9", "2.11.0"} {
		es, st := newSamplerES(t, v, false)
		res, err := es.FieldStatsSampled(context.Background(), sampleFilter(), "CHANNEL_CODE", 5, 0.05)
		if err != nil {
			t.Fatal(err)
		}
		if res.Sampled || res.Total != 100 {
			t.Fatalf("%s: örneklemesiz olmalıydı: %+v", v, res)
		}
		for _, b := range st.sent() {
			if strings.Contains(b, "random_sampler") {
				t.Fatalf("%s: random_sampler gönderilmemeli: %s", v, b)
			}
		}
	}
}

func TestESFieldStatsSamplerRejectionLatches(t *testing.T) {
	es, st := newSamplerES(t, "", true) // sürüm okunamadı → dene
	res, err := es.FieldStatsSampled(context.Background(), sampleFilter(), "CHANNEL_CODE", 5, 0.05)
	if err != nil {
		t.Fatalf("ret örneklemesiz yola düşmeliydi: %v", err)
	}
	if res.Sampled || res.Total != 100 {
		t.Fatalf("örneklemesiz sonuç bekleniyordu: %+v", res)
	}
	if !es.samplerDenied.Load() {
		t.Fatal("ret kilidi kurulmadı")
	}
	before := len(st.sent())
	if _, err := es.FieldStatsSampled(context.Background(), sampleFilter(), "CHANNEL_CODE", 5, 0.05); err != nil {
		t.Fatal(err)
	}
	after := st.sent()[before:]
	for _, b := range after {
		if strings.Contains(b, "random_sampler") {
			t.Fatal("kilitten sonra random_sampler yeniden denenmemeli")
		}
	}
}

func TestIsRandomSamplerRejection(t *testing.T) {
	if isRandomSamplerRejection(nil) || isRandomSamplerRejection(errors.New("ES fieldstats: timeout")) {
		t.Fatal("ilgisiz hata ret sayılmamalı")
	}
	if !isRandomSamplerRejection(errors.New("unknown aggregation type [random_sampler] (x_content_parse_exception)")) {
		t.Fatal("random_sampler reddi tanınmalı")
	}
}

// CH log yolu yeteneği UYGULAMAZ (operatör 2026-11-06: prod logları yalnız ES'te;
// CH yolu bugünkü davranışında kalır).
func TestCHStoreHasNoFieldStatsSampler(t *testing.T) {
	var st Store = &CHStore{}
	if _, ok := st.(FieldStatsSampler); ok {
		t.Fatal("CH log yolu örnekleme yeteneği taşımamalı")
	}
}
