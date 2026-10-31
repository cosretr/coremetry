package logstore

// v0.10.1755 — ML şablon sayımı: tek gövde, aile başına servis term +
// match_phrase (regex/wildcard/script YOK), sıfır kova dahil histogram.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMLTemplateBodyAndParse(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	b := string(mlTemplateBody("@timestamp", "service.keyword", "message", MLTemplateCountRequest{From: t0, To: t0.Add(time.Hour), BucketSec: 300,
		Families: []MLTemplateFamily{{Service: "pay", Phrase: "connection refused to db"}}}, "10s"))
	for _, want := range []string{`"match_phrase":{"message":"connection refused to db"}`, `"term":{"service.keyword":"pay"}`, `"min_doc_count":0`, `"track_total_hits":false`, `"size":0`} {
		if !strings.Contains(b, want) {
			t.Errorf("gövdede %s yok: %s", want, b)
		}
	}
	for _, bad := range []string{"regexp", "wildcard", "script", "query_string"} {
		if strings.Contains(b, bad) {
			t.Errorf("gövdede %s olmamalı", bad)
		}
	}
	raw := json.RawMessage(`{"fam":{"buckets":{"0":{"hist":{"buckets":[{"key":1800000000000,"doc_count":4},{"key":1800000300000,"doc_count":0}]}}}}}`)
	got, err := parseMLTemplateAgg(raw)
	if err != nil || len(got) != 2 || got[0].Index != 0 || got[0].Bucket != 1_800_000_000 || got[0].Count != 4 || got[1].Count != 0 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
