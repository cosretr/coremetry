package logstore

// pattern_logs_pivot_test.go — v0.10.1062 (operatör, prod ES: servissiz log
// deseni anomalisinde "Ne yapabilirim" yalnız "servis adı yok" diyordu,
// operatör Kibana'ya elle gidiyordu). Çivilenen: ES servis atfı zinciri —
// aynı _search'te, öncelik sırasıyla, doküman ilk taşıdığı alanda sayılır;
// cevap ≤5 servise birleşir.
//
// v0.10.1071 — 1062'nin PatternSearchText testleri silindi (işlev silindi:
// /logs artık arama metni değil `pattern=` alır; parite testi
// pattern_logs_filter_test.go).

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestPatternServiceAggs_Golden(t *testing.T) {
	if patternServiceAggs(nil) != nil {
		t.Fatal("alan yokken zincir nil olmalı")
	}
	got, err := json.Marshal(patternServiceAggs([]string{"service.name.keyword", "kubernetes.container_name", "kubernetes.labels.app"}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"svc":{"terms":{"field":"service.name.keyword","size":5}},` +
		`"svc_rest":{"aggs":{"svc":{"terms":{"field":"kubernetes.container_name","size":5}},` +
		`"svc_rest":{"aggs":{"svc":{"terms":{"field":"kubernetes.labels.app","size":5}}},` +
		`"filter":{"bool":{"must_not":[{"exists":{"field":"kubernetes.container_name"}}]}}}},` +
		`"filter":{"bool":{"must_not":[{"exists":{"field":"service.name.keyword"}}]}}}}`
	if string(got) != want {
		t.Fatalf("zincir:\n got %s\nwant %s", got, want)
	}
}

// Zincir aynı gövdeye eklenir: sorgu (pencere + token query_string) ve
// maliyet korumaları değişmez, yalnız aggs'e svc / svc_rest gelir.
func TestPatternHistogramBody_WithServiceChain(t *testing.T) {
	plain := patternHistogramBody(`message:"ora-"`, "message", "@timestamp",
		"2026-10-02T10:00:00Z", "2026-10-02T12:00:00Z", 60, "10s", nil)
	withSvc := patternHistogramBody(`message:"ora-"`, "message", "@timestamp",
		"2026-10-02T10:00:00Z", "2026-10-02T12:00:00Z", 60, "10s", []string{"service.name.keyword", "kubernetes.container_name"})
	for _, k := range []string{"size", "track_total_hits", "timeout", "query"} {
		if !reflect.DeepEqual(plain[k], withSvc[k]) {
			t.Fatalf("%s değişmiş: %v → %v", k, plain[k], withSvc[k])
		}
	}
	aggs := withSvc["aggs"].(map[string]any)
	if len(aggs) != 3 || aggs["buckets"] == nil || aggs["svc"] == nil || aggs["svc_rest"] == nil {
		t.Fatalf("aggs: %v", aggs)
	}
}

func TestTopServicesFromChain(t *testing.T) {
	type b = struct {
		Key      any     `json:"key"`
		DocCount float64 `json:"doc_count"`
	}
	lvl := func(rest *esServiceChain, bs ...b) *esServiceChain {
		c := &esServiceChain{Rest: rest}
		c.Svc.Buckets = bs
		return c
	}
	cases := []struct {
		name string
		in   *esServiceChain
		want []PatternServiceHit
	}{
		{"boş", &esServiceChain{}, []PatternServiceHit{}},
		{"tek seviye, sayı azalan",
			lvl(nil, b{"orders-svc", 40}, b{"billing-svc", 90}),
			[]PatternServiceHit{{"billing-svc", 90}, {"orders-svc", 40}}},
		{"service.name yok → yedek alan sayılır",
			lvl(lvl(nil, b{"orders-svc", 781})),
			[]PatternServiceHit{{"orders-svc", 781}}},
		{"aynı ad iki seviyede toplanır",
			lvl(lvl(nil, b{"orders-svc", 5}), b{"orders-svc", 10}, b{"billing-svc", 12}),
			[]PatternServiceHit{{"orders-svc", 15}, {"billing-svc", 12}}},
		{"boş ad / nil / sıfır atılır, eşitlikte ad artan",
			lvl(nil, b{"", 99}, b{nil, 50}, b{"zeta-svc", 3}, b{"alpha-svc", 3}, b{"gamma-svc", 0}),
			[]PatternServiceHit{{"alpha-svc", 3}, {"zeta-svc", 3}}},
		{"sayısal anahtar metne çevrilir",
			lvl(nil, b{float64(42), 7}),
			[]PatternServiceHit{{"42", 7}}},
		{"en çok 5",
			lvl(nil, b{"a", 9}, b{"b", 8}, b{"c", 7}, b{"d", 6}, b{"e", 5}, b{"f", 4}),
			[]PatternServiceHit{{"a", 9}, {"b", 8}, {"c", 7}, {"d", 6}, {"e", 5}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := topServicesFromChain(c.in); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// Gerçek ES cevap şekli (kökte svc + iç içe svc_rest) uçtan uca çözülür.
func TestDecodePatternHistogram_TopServices(t *testing.T) {
	raw := `{"aggregations":{
	  "buckets":{"buckets":[{"key":1759400000000,"doc_count":781}]},
	  "svc":{"buckets":[]},
	  "svc_rest":{"doc_count":781,
	    "svc":{"buckets":[{"key":"orders-svc","doc_count":700},{"key":"billing-svc","doc_count":81}]},
	    "svc_rest":{"doc_count":0,"svc":{"buckets":[]}}}}}`
	var r esPatternHistogramResponse
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	got := decodePatternHistogram(r)
	if len(got.Points) != 1 || got.Points[0].V != 781 {
		t.Fatalf("noktalar: %+v", got.Points)
	}
	want := []PatternServiceHit{{"orders-svc", 700}, {"billing-svc", 81}}
	if !reflect.DeepEqual(got.TopServices, want) {
		t.Fatalf("TopServices %+v, want %+v", got.TopServices, want)
	}
	// Zincirsiz (eski şekil / servis alanı yok) cevap: TopServices nil.
	if decodePatternHistogram(esPatternHistogramResponse{}).TopServices != nil {
		t.Fatal("zincirsiz cevapta TopServices nil olmalı")
	}
}

// Atıf alanları = servis SÜZGECİNİN alanları (yapılandırılmış alan, sonra
// esServiceFallbackFields), her biri terms yazımına çözümlü, yineleme bir kez.
// Gösterilen ad /logs?service= ile süzülebilen ad olmalı (v0.8.265 sınıfı).
func TestPatternServiceAggFields_OrderAndSpelling(t *testing.T) {
	s := &ESStore{}
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	// termsAggField önbelleği önceden dolu: ağ yok.
	spell := map[string]string{
		s.fields.Service:            s.fields.Service + ".keyword",
		"kubernetes.container.name": "kubernetes.container.name.keyword",
		"kubernetes.container_name": "kubernetes.container_name",
		"kubernetes.labels.app":     "kubernetes.labels.app.keyword",
		"kubernetes.labels_app":     "kubernetes.labels.app.keyword", // yinelenen yazım
	}
	s.groupFields.byAxis = map[string]esGroupFieldsVerdict{}
	for bare, f := range spell {
		s.groupFields.byAxis["terms:"+bare] = esGroupFieldsVerdict{fields: []string{f}, expires: time.Now().Add(time.Hour)}
	}
	got := s.patternServiceAggFields(t.Context())
	want := []string{
		s.fields.Service + ".keyword",
		"kubernetes.container.name.keyword",
		"kubernetes.container_name",
		"kubernetes.labels.app.keyword",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
