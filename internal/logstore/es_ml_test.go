package logstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// v0.10.1747 — ML log dilim 1: tek agregasyon gövdesi pinleri + parser.
func TestMLLogAggBodyShape(t *testing.T) {
	req := MLLogAggRequest{From: time.Unix(1700000000, 0), To: time.Unix(1700007200, 0), BucketSec: 300, MaxServices: 500,
		InfluencerField: "channel.keyword", ErrorLevels: []string{"error", "fatal"}}
	b := mlLogAggBody("@timestamp", "service.name", "log.level", req, "10s")
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["size"] != float64(0) || m["track_total_hits"] != false || m["timeout"] != "10s" {
		t.Fatalf("guards: %v", m)
	}
	s := string(b)
	for _, want := range []string{`"fixed_interval":"300s"`, `"min_doc_count":1`, `"size":500`, `"channel.keyword"`, `"latest"`, `"lt":"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("gövdede %s yok: %s", want, s)
		}
	}
	if strings.Contains(s, "script") || strings.Contains(s, "regexp") {
		t.Fatal("script/regex yasak")
	}
	aggs := m["aggs"].(map[string]any)
	if len(aggs) != 2 {
		t.Fatalf("kök aggs 2 olmalı (svc, latest): %d", len(aggs))
	}
	// influencer yokken err alt agg'i basılmaz
	b2 := mlLogAggBody("@timestamp", "service.name", "log.level", MLLogAggRequest{From: req.From, To: req.To, BucketSec: 300, MaxServices: 10}, "10s")
	if strings.Contains(string(b2), `"err"`) {
		t.Fatal("influencer alanı yokken err agg olmamalı")
	}
}

func TestParseMLLogAgg(t *testing.T) {
	raw := `{"svc":{"buckets":[{"key":"orders-api","doc_count":12,
	 "lvl":{"buckets":[{"key":"error","hist":{"buckets":[{"key":1700000000000,"doc_count":7},{"key":1700000300000,"doc_count":5}]}}]},
	 "err":{"inf":{"buckets":[{"key":"MOBILE","doc_count":6},{"key":7,"doc_count":1}]}}}]},
	 "latest":{"value":1700000599000.0}}`
	r, err := parseMLLogAgg(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 2 || r.Rows[0].Service != "orders-api" || r.Rows[0].Level != "error" || r.Rows[0].Bucket != 1700000000 || r.Rows[1].Count != 5 {
		t.Fatalf("rows: %+v", r.Rows)
	}
	if len(r.Influencers) != 2 || r.Influencers[0].Value != "MOBILE" || r.Influencers[1].Value != "7" {
		t.Fatalf("influencers: %+v", r.Influencers)
	}
	if r.LatestDocMs != 1700000599000 {
		t.Fatalf("latest: %d", r.LatestDocMs)
	}
}
