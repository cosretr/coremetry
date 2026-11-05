package api

// metric_name_drop_test.go — v0.10.1809.
//
// Sözleşme: Settings › Pipeline metrik DROP kuralı (örn. metric =~
// healthcheck) yalnız yazma anında uygulanıyordu; kuraldan önce VM'e
// yazılmış adlar picker/katalogda görünmeye devam ediyordu. Okuma
// tarafı artık ad listelerini süzer, gizli adın etiket/anahtar keşfini
// boş döner ve cache anahtarı kural kümesinin özetini taşır.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/pipeline"
)

func serverWithMetricDrop(t *testing.T, pattern string) *Server {
	t.Helper()
	eng := pipeline.New()
	if pattern != "" {
		if _, err := eng.Upsert(context.Background(), &pipelineTestStore{}, pipeline.Rule{
			Name: "hc", Kind: pipeline.KindDrop, Signal: pipeline.SignalMetrics, Enabled: true,
			When: pipeline.Condition{Key: "metric", Op: pipeline.OpMatches, Value: pattern},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{pipeline: eng}
}

func infos(names ...string) []chstore.MetricInfo {
	out := make([]chstore.MetricInfo, 0, len(names))
	for _, n := range names {
		out = append(out, chstore.MetricInfo{Name: n})
	}
	return out
}

func TestMetricNameDropTagAndFilter(t *testing.T) {
	if fn, tag := (&Server{}).metricNameDrop(); fn != nil || tag != "" {
		t.Fatalf("no pipeline: want (nil, \"\"), got tag %q", tag)
	}
	if fn, tag := serverWithMetricDrop(t, "").metricNameDrop(); fn != nil || tag != "" {
		t.Fatalf("no rules: key must stay byte-identical, got tag %q", tag)
	}
	a := serverWithMetricDrop(t, "healthcheck")
	b := serverWithMetricDrop(t, "debug")
	dropA, tagA := a.metricNameDrop()
	_, tagB := b.metricNameDrop()
	if !strings.HasPrefix(tagA, ":nd=") || tagA == tagB {
		t.Fatalf("tags must carry the rule digest and differ per rule set: %q vs %q", tagA, tagB)
	}
	got := filterDroppedMetricNames(infos("app_healthcheck_total", "http_requests"), dropA)
	if len(got) != 1 || got[0].Name != "http_requests" {
		t.Fatalf("filtered = %+v", got)
	}
	if got := filterDroppedMetricNames(infos("x"), nil); len(got) != 1 {
		t.Fatalf("nil dropper must pass through")
	}
}

func TestMetricNamesPage(t *testing.T) {
	drop, _ := serverWithMetricDrop(t, "^hc").metricNameDrop()
	cases := []struct {
		name          string
		page          []chstore.MetricInfo
		total, offset int
		wantNames     int
		wantTotal     int
		wantHasMore   bool
	}{
		{"last page, one hidden", infos("a", "hc_x", "b"), 3, 0, 2, 2, false},
		// Short page must NOT end infinite scroll — hasMore from the RAW page.
		{"mid page, all hidden", infos("hc_1", "hc_2"), 10, 0, 0, 8, true},
		{"nothing hidden", infos("a", "b"), 5, 2, 2, 5, true},
		{"empty page", nil, 0, 0, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := metricNamesPage(tc.page, tc.total, tc.offset, "vm", drop)
			names := m["names"].([]chstore.MetricInfo)
			if names == nil {
				t.Fatal("names must be [] not null")
			}
			if len(names) != tc.wantNames || m["total"] != tc.wantTotal || m["hasMore"] != tc.wantHasMore {
				t.Errorf("got names=%d total=%v hasMore=%v", len(names), m["total"], m["hasMore"])
			}
			if m["source"] != "vm" {
				t.Errorf("source lost")
			}
		})
	}
}

func TestServeHiddenMetricEmpty(t *testing.T) {
	s := serverWithMetricDrop(t, "healthcheck")
	rec := httptest.NewRecorder()
	if !s.serveHiddenMetricEmpty(rec, "app_healthcheck_total") {
		t.Fatal("hidden metric must short-circuit")
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want []", body)
	}
	rec = httptest.NewRecorder()
	if s.serveHiddenMetricEmpty(rec, "http_requests") || rec.Body.Len() != 0 {
		t.Fatal("visible metric must fall through without writing")
	}
	if (&Server{}).serveHiddenMetricEmpty(httptest.NewRecorder(), "x") {
		t.Fatal("no pipeline must never hide")
	}
}
