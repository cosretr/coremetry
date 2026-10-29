package logstore

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: ES CountPatterns sürdürme
// pencerelerini AYNI tek _msearch'te prev_window_<i> filtre agg'leriyle sayar;
// base_window üstü en eski sürdürme başlangıcı. prevStarts boşken gövde
// öncekiyle aynı (prev agg yok, base üstü curFrom).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestESCountPatternsPrevWindows(t *testing.T) {
	var mu sync.Mutex
	var msearches int
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"name":"n1","cluster_name":"c","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`))
		case strings.HasSuffix(r.URL.Path, "/_msearch"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			msearches++
			sent = string(b)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"responses":[
				{"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"aggregations":{
					"cur_window":{"doc_count":40,"by_service":{"buckets":[{"key":"orders-api","doc_count":40}]},"sample":{"hits":{"hits":[]}}},
					"prev_window_0":{"doc_count":35},
					"base_window":{"doc_count":120}}}
			]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","reason":"stub"},"status":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Body: "message"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	cur, prev, base := now.Add(-5*time.Minute), now.Add(-10*time.Minute), now.Add(-time.Hour)
	ora := PatternSpec{Name: "Oracle errors (ORA-)", Regex: `ORA-[0-9]+`, Tokens: []string{"ora-"}}
	got, err := es.CountPatterns(context.Background(), []PatternSpec{ora}, cur, []time.Time{prev}, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if msearches != 1 {
		t.Fatalf("tek _msearch bekleniyordu, %d", msearches)
	}
	if g := got[0]; g.Cur != 40 || g.Base != 120 || !reflect.DeepEqual(g.Prev, []uint64{35}) || g.Service != "orders-api" {
		t.Fatalf("stats = %+v", g)
	}
	if !strings.Contains(sent, `"prev_window_0":{"filter":{"range":{"@timestamp":{"gte":"2026-10-29T09:50:00Z","lt":"2026-10-29T09:55:00Z"}}}}`) {
		t.Errorf("prev_window_0 [prev, cur) olmalı: %s", sent)
	}
	if !strings.Contains(sent, `"base_window":{"filter":{"range":{"@timestamp":{"gte":"2026-10-29T09:00:00Z","lt":"2026-10-29T09:50:00Z"}}}}`) {
		t.Errorf("base_window üstü en eski sürdürme başı olmalı: %s", sent)
	}

	// prevStarts boş → gövde öncekiyle aynı, Prev nil.
	legacy := patternCountBody(tokClause, "message", "@timestamp", "service.name", "a", "b", "c", "10s")
	same := patternCountBody(tokClause, "message", "@timestamp", "service.name", "a", "b", "c", "10s", []string{}...)
	if !reflect.DeepEqual(legacy, same) {
		t.Fatal("boş prevFroms gövdeyi değiştirdi")
	}
	if _, ok := legacy["aggs"].(map[string]any)["prev_window_0"]; ok {
		t.Fatal("legacy gövdede prev agg olmamalı")
	}
	got, err = es.CountPatterns(context.Background(), []PatternSpec{ora}, cur, nil, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Prev != nil || strings.Contains(sent, "prev_window") {
		t.Fatalf("prevStarts boşken prev yok: %+v %s", got[0], sent)
	}
}
