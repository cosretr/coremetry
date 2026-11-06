package logstore

// es_narrow_source_test.go — v0.10.1831 (Log Search Faz 1 dilim 1.8): dar
// `_source` (görünen kolonlar + satır çizim alanları), partial + docRef,
// sayım kapalı, tavan 5000; doküman referansı çözümü.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestESSearchNarrowSource(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	st.set(func(s *esStubServer) {
		s.searchStatus = 200
		s.searchBody = `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"app-orders-2026.11.05","_id":"Ab1","_source":{"message":"order accepted","CHANNEL_CODE":"MOBILE"},"sort":[1,2]}]}}`
	})
	now := time.Now()
	page, err := es.Search(context.Background(), Filter{Service: "orders-api", Limit: 500, SkipTotal: true,
		SourceFields: []string{"CHANNEL_CODE"}, From: now.Add(-time.Hour), To: now})
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	raw := st.lastSearchBody
	st.mu.Unlock()
	var body struct {
		Size           int `json:"size"`
		TrackTotalHits any `json:"track_total_hits"`
		Source         struct {
			Includes []string `json:"includes"`
		} `json:"_source"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.Size != 500 || body.TrackTotalHits != false {
		t.Errorf("size=%d track_total_hits=%v", body.Size, body.TrackTotalHits)
	}
	has := map[string]bool{}
	for _, f := range body.Source.Includes {
		has[f] = true
	}
	for _, want := range []string{"CHANNEL_CODE", "message", "trace.id", "span.id", "kubernetes.pod_name"} {
		if !has[want] {
			t.Errorf("_source.includes %q içermeli: %v", want, body.Source.Includes)
		}
	}
	if len(page.Logs) != 1 || !page.Logs[0].Partial || page.Logs[0].DocRef != "app-orders-2026.11.05/Ab1" {
		t.Fatalf("satır partial + docRef taşımalı: %+v", page.Logs)
	}

	// Kolonsuz istek: bugünkü gibi tam _source, partial yok.
	if _, err := es.Search(context.Background(), Filter{Service: "orders-api", From: now.Add(-time.Hour), To: now}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	raw = st.lastSearchBody
	st.mu.Unlock()
	var full map[string]any
	_ = json.Unmarshal(raw, &full)
	if _, ok := full["_source"]; ok {
		t.Error("kolonsuz istekte _source süzgeci olmamalı")
	}
}

func TestSplitDocRef(t *testing.T) {
	for _, c := range []struct {
		ref, idx, id string
		ok           bool
	}{
		{"app-x/abc", "app-x", "abc", true},
		{"app-x/a/b", "app-x", "a/b", true},
		{"/abc", "", "", false}, {"app-x/", "", "", false}, {"abc", "", "", false}, {"", "", "", false},
	} {
		i, d, ok := splitDocRef(c.ref)
		if i != c.idx || d != c.id || ok != c.ok {
			t.Errorf("splitDocRef(%q) = %q %q %v", c.ref, i, d, ok)
		}
	}
	if docRef("", "x") != "" || docRef("i", "x") != "i/x" {
		t.Error("docRef")
	}
}
