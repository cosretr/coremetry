package logstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.1794 — Operator-reported: `"level":"INFO"` yalnız message JSON'unda
// olan prod satırları histogram bantlarında ve rozette görünmüyordu.
func TestJSONBodyLevel(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"timestamp":"2026-11-05T10:23:41,067+0300","level":"INFO","location":"KafkaConsumerProxy.java:46","message":"x"}`, "INFO"},
		{`{"Level":" warn "}`, "warn"},
		{`{"severity":"ERROR"}`, "ERROR"},
		{`{"level":5}`, ""},
		{`{"msg":"no level here"}`, ""},
		{`plain text level=info`, ""},
		{`{"level":"INFO"`, ""},
		{``, ""},
	}
	for _, tc := range cases {
		if got := jsonBodyLevel(tc.body); got != tc.want {
			t.Errorf("jsonBodyLevel(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestBodyLevelClauses(t *testing.T) {
	raw, _ := json.Marshal(bodyLevelPhraseClause("message", "err"))
	if s := string(raw); !strings.Contains(s, `"match_phrase_prefix":{"message":{"max_expansions":10,"query":"level err"}}`) {
		t.Errorf("phrase clause = %s", s)
	}
	if got := bodyLevelQueryStringPart("message", `"error"`); got != `message:"level error"` {
		t.Errorf("query_string part = %q", got)
	}
	if got := bodyLevelQueryStringPart("message", `err*`); got != "" {
		t.Errorf("wildcard value must not become a phrase: %q", got)
	}
}

// Histogram bandı + takım hata yüklemi gövde kolunu taşır; "" gövde alanı
// eski gövdeyi bayt bayt korur (ikinci yarı: team_logs aynı ERROR bandı).
func TestSeverityBandClause_BodyLevel(t *testing.T) {
	cands := severityCandidateKeywordFields("log.level")
	with := severityBandClause(errorBand(), cands, "", "message")
	without := severityBandClause(errorBand(), cands, "", "")
	ws, _ := json.Marshal(with)
	wo, _ := json.Marshal(without)
	if strings.Contains(string(wo), "match_phrase_prefix") {
		t.Fatalf("empty body field must add no phrase clause: %s", wo)
	}
	if n := strings.Count(string(ws), "match_phrase_prefix"); n != len(errorBand().Prefixes) {
		t.Fatalf("phrase clause count = %d, want %d: %s", n, len(errorBand().Prefixes), ws)
	}
	body := buildSeverityHistogramBody(map[string]any{"match_all": map[string]any{}}, "@timestamp", cands, "", "message", 30, "10s")
	bs, _ := json.Marshal(body)
	if !strings.Contains(string(bs), `"query":"level info"`) {
		t.Errorf("histogram INFO band lacks body phrase: %s", bs)
	}
}
