package logstore

// v0.10.1473 — takım son-log kontrolü (team_logs.go). Takım adları ve
// gövdeler SENTETİK (team-alpha, orders-api, db.example.test).

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

func TestParseTeamFields(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"  ,  ", nil, false},
		{"kubernetes.labels.team-a", []string{"kubernetes.labels.team-a"}, false},
		{" labels.team.keyword , labels.owner ", []string{"labels.team", "labels.owner"}, false},
		{"labels.team,labels.team.keyword", []string{"labels.team"}, false}, // tekilleşir
		{"a,b,c", []string{"a", "b", "c"}, false},
		{"a,b,c,d", nil, true},                // tavan 3
		{"labels.*team", nil, true},           // wildcard
		{"labels.team:x", nil, true},          // sorgu dili
		{"labels team", nil, true},            // boşluk
		{`labels."team"`, nil, true},          // tırnak
		{strings.Repeat("x", 300), nil, true}, // uzunluk
	} {
		got, err := ParseTeamFields(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestTeamLogsWindowClamp(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		from, to time.Time
		wantFrom time.Time
		wantTo   time.Time
	}{
		{"sıfır → son 15 dk", time.Time{}, time.Time{}, now.Add(-15 * time.Minute), now},
		{"5 dk aynen", now.Add(-5 * time.Minute), now, now.Add(-5 * time.Minute), now},
		{"60 dk aynen", now.Add(-time.Hour), now, now.Add(-time.Hour), now},
		{"3 sa → tavan 60 dk", now.Add(-3 * time.Hour), now, now.Add(-time.Hour), now},
		{"ters pencere → varsayılan", now.Add(time.Minute), now, now.Add(-15 * time.Minute), now},
	} {
		f, to := TeamLogsWindow(c.from, c.to, now)
		if !f.Equal(c.wantFrom) || !to.Equal(c.wantTo) {
			t.Errorf("%s: got [%v, %v] want [%v, %v]", c.name, f, to, c.wantFrom, c.wantTo)
		}
	}
}

func testTeamFields() ESFieldMap {
	c := ESConfig{}
	c.defaults()
	return c.Fields
}

// Gövde maliyet bekçileri: size 0, tavanlı total, 3 s timeout, terminate_after,
// pencere sorguda, takım exact-term (keyword + çıplak), seviye ERROR bandı,
// top_hits ≤50 dar _source, by_service 5. Yasak: query_string, wildcard,
// significant_text, regexp.
func TestBuildTeamLogsBody_CostGuards(t *testing.T) {
	from := time.Date(2026, 10, 23, 11, 45, 0, 0, time.UTC)
	to := from.Add(15 * time.Minute)
	body := buildTeamLogsBody(testTeamFields(), []string{"labels.team"}, []string{"team-alpha", "Team-Alpha"}, "service.name.keyword", from, to)
	if body["size"] != 0 || body["timeout"] != "3s" || body["terminate_after"] != teamLogsTerminateAfter || body["track_total_hits"] != teamLogsTrackTotalCap {
		t.Fatalf("maliyet bekçileri: %v", body)
	}
	if teamLogsSampleSize > 50 || teamLogsTerminateAfter > 100000 {
		t.Fatal("örneklem / terminate_after tavanı gevşedi")
	}
	raw, _ := json.Marshal(body)
	s := string(raw)
	for _, want := range []string{
		`"range":{"@timestamp":{"gte":"2026-10-23T11:45:00Z","lte":"2026-10-23T12:00:00Z"}}`,
		`{"terms":{"labels.team.keyword":["team-alpha","Team-Alpha"]}}`,
		`"must_not":[{"exists":{"field":"labels.team.keyword"}}]`,
		`"prefix":{"log.level.keyword":{"case_insensitive":true,"value":"err"}}`,
		`"prefix":{"level":{"case_insensitive":true,"value":"fatal"}}`,
		`"by_service":{"terms":{"field":"service.name.keyword","shard_size":50,"size":5}}`,
		`"top_hits":{"_source":[`,
		`"size":50`,
		`"sort":[{"@timestamp":{"order":"desc","unmapped_type":"date"}}]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gövde %s taşımıyor:\n%s", want, s)
		}
	}
	for _, banned := range []string{"query_string", "wildcard", "significant_text", "regexp", `"scroll"`, `"pit"`} {
		if strings.Contains(s, banned) {
			t.Errorf("gövdede %s olmamalı: %s", banned, s)
		}
	}
	// Seviye yan tümcesi şerit histogramının ERROR bandıyla AYNI.
	want, _ := json.Marshal(severityBandClause(errorBand(), severityCandidateKeywordFields("log.level"), "", "message"))
	if !strings.Contains(s, string(want)) {
		t.Error("seviye yüklemi histogramın ERROR bandından ayrıştı")
	}
	// Servis agg alanı çözülemediyse agg yok (servis örneklemden).
	noSvc, _ := json.Marshal(buildTeamLogsBody(testTeamFields(), []string{"labels.team"}, []string{"x"}, "", from, to))
	if strings.Contains(string(noSvc), "by_service") {
		t.Error("svcAggField boşken by_service olmamalı")
	}
	// Sayısal seviye alanı yapılandırıldıysa OTel aralığı da girer.
	f := testTeamFields()
	f.SeverityNo = "severity_number"
	num, _ := json.Marshal(buildTeamLogsBody(f, []string{"labels.team"}, []string{"x"}, "", from, to))
	if !strings.Contains(string(num), `"range":{"severity_number":{"gte":17,"lte":24}}`) {
		t.Errorf("sayısal ERROR aralığı yok: %s", num)
	}
}

func TestTeamValueClause_MultiField(t *testing.T) {
	c := teamValueClause([]string{"labels.team", "labels.owner"}, []string{"team-alpha"})
	raw, _ := json.Marshal(c)
	s := string(raw)
	if strings.Count(s, `"terms"`) != 4 || !strings.Contains(s, `"minimum_should_match":1`) {
		t.Errorf("iki alan × iki biçim bekleniyordu: %s", s)
	}
}

func TestTeamLogsFromRecords(t *testing.T) {
	long := "connection refused to db.example.test:5432 after 3000ms " + strings.Repeat("x", 400)
	recs := []*LogRecord{
		{Timestamp: 5, ServiceName: "orders-api", Body: "timeout calling payments id=12345", TraceID: "t5"},
		{Timestamp: 4, ServiceName: "orders-api", Body: "timeout calling payments id=99881"},
		{Timestamp: 3, ServiceName: "billing-api", Body: long},
		{Timestamp: 2, ServiceName: "orders-api", Body: "timeout calling payments id=55555"},
		{Timestamp: 1, ServiceName: "", Body: "line\nwith   breaks"},
		nil,
	}
	pats, lines, svcs := teamLogsFromRecords(recs)
	if len(pats) != 3 || pats[0].Count != 3 || pats[0].Service != "orders-api" || pats[0].Template != "timeout calling payments id=<x>" {
		t.Fatalf("desenler: %+v", pats)
	}
	if pats[1].Count != 1 || pats[1].Service != "billing-api" { // eşitlikte en yeni önce
		t.Errorf("eşitlik sırası: %+v", pats[1])
	}
	if len(lines) != 3 || lines[0].TraceID != "t5" || lines[0].Service != "orders-api" {
		t.Fatalf("satırlar: %+v", lines)
	}
	if r := []rune(lines[2].Line); len(r) != teamLogsLineMax+1 || !strings.HasSuffix(lines[2].Line, "…") {
		t.Errorf("uzun satır kırpılmalı: %d", len(r))
	}
	if len(svcs) != 2 || svcs[0].Service != "orders-api" || svcs[0].Count != 3 {
		t.Errorf("servisler: %+v", svcs)
	}
	if TrimLogLine("a\n  b\tc", 0) != "a b c" {
		t.Error("TrimLogLine boşluk sıkıştırmalı")
	}
}

func TestCHTeamRecentLogsUnsupported(t *testing.T) {
	_, err := (&CHStore{}).TeamRecentLogs(context.Background(), TeamLogsQuery{Values: []string{"x"}})
	if !errors.Is(err, ErrTeamLogsUnsupported) {
		t.Fatalf("CH unsupported dönmeli: %v", err)
	}
}

// teamLogsESServer — sahte ES: ping + field_caps + _search sayacı.
func teamLogsESServer(t *testing.T, searchBody string) (*httptest.Server, *int, *string, *string) {
	t.Helper()
	var mu sync.Mutex
	searches := 0
	var sent, rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"name":"n1","cluster_name":"c","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`))
		case strings.HasSuffix(r.URL.Path, "/_field_caps"):
			_, _ = w.Write([]byte(`{"indices":["app-1"],"fields":{"service.name":{"keyword":{"type":"keyword","searchable":true,"aggregatable":true}}}}`))
		case strings.HasSuffix(r.URL.Path, "/_search"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			searches++
			sent, rawQuery = string(b), r.URL.RawQuery
			mu.Unlock()
			_, _ = w.Write([]byte(searchBody))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","reason":"stub"},"status":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &searches, &sent, &rawQuery
}

// Takım alanı boş → ErrTeamLogsDisabled ve SIFIR _search (özellik kapalı).
func TestESTeamRecentLogs_DisabledZeroCalls(t *testing.T) {
	srv, searches, _, _ := teamLogsESServer(t, `{}`)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = es.TeamRecentLogs(context.Background(), TeamLogsQuery{Values: []string{"team-alpha"}})
	if !errors.Is(err, ErrTeamLogsDisabled) {
		t.Fatalf("kapalı: %v", err)
	}
	if *searches != 0 {
		t.Fatalf("kapalıyken _search YAPILMAMALI, %d", *searches)
	}
	// Değersiz istek de ağa çıkmaz.
	es2, _ := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Team: "labels.team"}})
	if _, err := es2.TeamRecentLogs(context.Background(), TeamLogsQuery{}); err == nil || *searches != 0 {
		t.Fatalf("değersiz istek: err=%v searches=%d", err, *searches)
	}
}

// Uçtan uca: TEK _search, request_cache, cevap çözümü + dürüstlük zarfı.
func TestESTeamRecentLogs_SingleSearch(t *testing.T) {
	srv, searches, sent, rawQuery := teamLogsESServer(t, `{
		"timed_out":false,"terminated_early":true,
		"_shards":{"total":2,"successful":2,"failed":0},
		"hits":{"total":{"value":42,"relation":"eq"},"hits":[]},
		"aggregations":{
			"by_service":{"buckets":[{"key":"orders-api","doc_count":30},{"key":"billing-api","doc_count":12}]},
			"newest":{"hits":{"hits":[
				{"_id":"a","_source":{"@timestamp":"2026-10-23T11:59:00Z","message":"timeout calling payments id=12345","service":{"name":"orders-api"},"trace":{"id":"abc123"}}},
				{"_id":"b","_source":{"@timestamp":"2026-10-23T11:58:00Z","message":"timeout calling payments id=99881","service":{"name":"orders-api"}}}
			]}}
		}}`)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Team: "labels.team"}})
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	r, err := es.TeamRecentLogs(context.Background(), TeamLogsQuery{Values: []string{"team-alpha", " ", "team-alpha"}, From: to.Add(-15 * time.Minute), To: to})
	if err != nil {
		t.Fatal(err)
	}
	if *searches != 1 {
		t.Fatalf("tek _search bekleniyordu, %d", *searches)
	}
	if !strings.Contains(*rawQuery, "request_cache=true") {
		t.Errorf("request_cache yok: %s", *rawQuery)
	}
	if !strings.Contains(*sent, `"labels.team.keyword":["team-alpha"]`) {
		t.Errorf("değerler tekilleşmeli: %s", *sent)
	}
	if r.ErrorCount != 42 || !r.CountIsLowerBound || !r.TerminatedEarly || r.Partial {
		t.Errorf("zarf: %+v", r)
	}
	if len(r.Services) != 2 || r.Services[0].Service != "orders-api" || r.ServicesFromSample {
		t.Errorf("servisler: %+v", r.Services)
	}
	if r.Sampled != 2 || len(r.Patterns) != 1 || r.Patterns[0].Count != 2 {
		t.Errorf("desenler: %+v", r.Patterns)
	}
	if len(r.Lines) != 2 || r.Lines[0].TraceID != "abc123" || r.Lines[0].Service != "orders-api" {
		t.Errorf("satırlar: %+v", r.Lines)
	}
	if strings.Join(r.Fields, ",") != "labels.team" || r.ToNs != to.UnixNano() {
		t.Errorf("künye: %+v", r)
	}
}
