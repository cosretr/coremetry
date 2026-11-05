package api

// logs_query_error_test.go — v0.10.1818 (Log Search Faz 1 dilim 1.2).
//
// Regresyon: ES sorgu sözdizimi reddi /api/logs'ta HTTP 500 + ham ES metni
// dönüyordu (audit §2 yan bulgu: api_logs.go → cache.go writeErr). Şimdi
// 400 {error, pos, detail}; ham metin yalnız admin'e (`raw`). Ayrıca
// `hasTraceUnapplied` / `unappliedFilters` yanıta yazılmıyordu (LogsNotices
// okuyor, rozetler hiç görünmüyordu).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const esReject = `{"error":{"root_cause":[{"type":"query_shard_exception","reason":"Failed to parse query [level:error AND (]"}],` +
	`"type":"search_phase_execution_exception","reason":"all shards failed",` +
	`"failed_shards":[{"reason":{"type":"query_shard_exception","reason":"Failed to parse query [level:error AND (]",` +
	`"caused_by":{"type":"parse_exception","reason":"Cannot parse 'level:error AND (': Encountered \"<EOF>\" at line 1, column 17.\nWas expecting one of: ..."}}}]},"status":400}`

func badQuery() error {
	return logstore.NewBadQueryError("ES search 400: all shards failed (search_phase_execution_exception): backend rejected query syntax", []byte(esReject))
}

func TestESQueryErrorBody(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		admin   bool
		wantPos int // -1 = yok
		wantRaw bool
	}{
		{"kullanıcı metni yankıyla aynı → konum", logsQueryErr(badQuery(), "level:error AND ("), false, 16, false},
		{"metin farklı (kısayol açılımı) → konum yok", logsQueryErr(badQuery(), "level:error AND"), false, -1, false},
		{"sarılmamış (handler bilinmiyor) → konum yok", badQuery(), false, -1, false},
		{"admin ham metni görür", logsQueryErr(badQuery(), "level:error AND ("), true, 16, true},
	}
	for _, c := range cases {
		b := esQueryErrorBody(c.err, c.admin)
		got := -1
		if b.Pos != nil {
			got = *b.Pos
		}
		if got != c.wantPos {
			t.Errorf("%s: pos %d want %d", c.name, got, c.wantPos)
		}
		if (b.Raw != "") != c.wantRaw {
			t.Errorf("%s: raw=%q", c.name, b.Raw)
		}
		if !strings.HasPrefix(b.Detail, "Cannot parse 'level:error AND (': Encountered") || strings.Contains(b.Detail, "Was expecting") {
			t.Errorf("%s: detail yalnız gerekçe cümlesi olmalı: %q", c.name, b.Detail)
		}
		if !strings.HasPrefix(b.Error, "Sorgu sözdizimi hatalı") {
			t.Errorf("%s: error %q", c.name, b.Error)
		}
	}
}

// Uçtan uca: ES reddi /api/logs, /timeseries, /fieldstats'ta 400 + konum;
// viewer ham ES metnini görmez.
func TestLogsEndpointsBadQueryIs400(t *testing.T) {
	s := degradeTestServer(fmt.Errorf("sarılı: %w", badQuery()))
	viewer := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u1", Role: auth.RoleViewer}))
		w := httptest.NewRecorder()
		switch {
		case strings.HasPrefix(path, "/api/logs/timeseries"):
			s.getLogsTimeseries(w, r)
		case strings.HasPrefix(path, "/api/logs/fieldstats"):
			s.getLogsFieldStats(w, r)
		default:
			s.getLogs(w, r)
		}
		return w
	}
	q := "search=level%3Aerror+AND+%28&from=1&to=2"
	for _, p := range []string{"/api/logs?" + q, "/api/logs/timeseries?" + q, "/api/logs/fieldstats?field=level&" + q} {
		w := viewer(p)
		if w.Code != 400 {
			t.Fatalf("%s: status %d, 400 beklendi — %s", p, w.Code, w.Body.String())
		}
		var b logQueryErrorBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		if b.Pos == nil || *b.Pos != 16 || b.Raw != "" || strings.Contains(w.Body.String(), "search_phase_execution_exception") {
			t.Errorf("%s: gövde %s", p, w.Body.String())
		}
	}
}

func TestCHSyntaxRejectCarriesPos(t *testing.T) {
	s := &Server{logs: &degradeLogStore{err: errors.New("çağrılmamalı")}}
	s.logs = chBackendStub{s.logs}
	w := httptest.NewRecorder()
	s.getLogs(w, httptest.NewRequest("GET", "/api/logs?search=%22disk&from=1&to=2", nil))
	if w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
	var b logQueryErrorBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if b.Pos == nil || *b.Pos != 0 || !strings.Contains(b.Error, "konum") || b.Detail == "" {
		t.Fatalf("CH reddi konumlu olmalı: %s", w.Body.String())
	}
}

type chBackendStub struct{ logstore.Store }

func (chBackendStub) Backend() string { return "clickhouse" }

func TestLogsSearchPayloadCarriesUnapplied(t *testing.T) {
	out := logsSearchPayload(&logstore.Page{HasTraceUnapplied: true, UnappliedFilters: []string{logstore.FilterTeam}})
	if out["hasTraceUnapplied"] != true {
		t.Errorf("hasTraceUnapplied yok: %v", out)
	}
	if got, _ := out["unappliedFilters"].([]string); len(got) != 1 || got[0] != logstore.FilterTeam {
		t.Errorf("unappliedFilters: %v", out["unappliedFilters"])
	}
	clean := logsSearchPayload(&logstore.Page{})
	if _, ok := clean["hasTraceUnapplied"]; ok {
		t.Error("tam cevapta bayrak yazılmamalı")
	}
	if _, ok := clean["unappliedFilters"]; ok {
		t.Error("tam cevapta liste yazılmamalı")
	}
}
