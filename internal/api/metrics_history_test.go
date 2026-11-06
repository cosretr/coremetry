package api

// v0.10.1864 — Metrics Explorer sorgu geçmişi (Faz 2 dilim 2.4, S9:
// sunucuda kişisel 50). Saf push/doğrulama tabloları + uçtan uca
// GET/POST/DELETE (sahte saved_views deposu, kimlik bağlamı).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHistoryPush(t *testing.T) {
	e := func(q, cl string) metricsHistoryEntry {
		return metricsHistoryEntry{Query: q, Cluster: cl, Status: "ok"}
	}
	cases := []struct {
		name string
		cur  []metricsHistoryEntry
		in   metricsHistoryEntry
		want []string
	}{
		{"boş", nil, e("a", ""), []string{"a"}},
		{"en yeni önde", []metricsHistoryEntry{e("a", "")}, e("b", ""), []string{"b", "a"}},
		{"ardışık özdeş tekilleşir", []metricsHistoryEntry{e("a", ""), e("b", "")}, e("a", ""), []string{"a", "b"}},
		{"cluster farklı → ayrı giriş", []metricsHistoryEntry{e("a", "acme-prod-1")}, e("a", ""), []string{"a", "a"}},
	}
	for _, tc := range cases {
		out, _, err := metricsHistoryPush(tc.cur, tc.in, 50, 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		var got []string
		for _, x := range out {
			got = append(got, x.Query)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	// tavan 50 + bayt duvarı en eskiyi düşürür
	var many []metricsHistoryEntry
	for i := 0; i < 60; i++ {
		many = append(many, e(fmt.Sprintf("q%d", i), ""))
	}
	out, _, _ := metricsHistoryPush(many, e("new", ""), 50, 1<<20)
	if len(out) != 50 || out[0].Query != "new" || out[49].Query != "q48" {
		t.Fatalf("tavan: len=%d first=%s last=%s", len(out), out[0].Query, out[len(out)-1].Query)
	}
	big := strings.Repeat("x", 4000)
	out, raw, _ := metricsHistoryPush([]metricsHistoryEntry{e(big+"1", ""), e(big+"2", "")}, e(big+"3", ""), 50, 9000)
	if len(out) != 2 || len(raw) > 9000 {
		t.Fatalf("bayt duvarı: len=%d raw=%d", len(out), len(raw))
	}
}

func TestMetricsHistoryValidate(t *testing.T) {
	cases := []struct {
		name string
		e    metricsHistoryEntry
		ok   bool
	}{
		{"geçerli", metricsHistoryEntry{Query: " up ", Status: "ok"}, true},
		{"hata konumlu", metricsHistoryEntry{Query: "up)", Status: "error", Line: 1, Col: 3}, true},
		{"boş q", metricsHistoryEntry{Query: "  ", Status: "ok"}, false},
		{"uzun q", metricsHistoryEntry{Query: strings.Repeat("a", maxPromQLQueryLen+1), Status: "ok"}, false},
		{"durum", metricsHistoryEntry{Query: "up", Status: "maybe"}, false},
		{"negatif", metricsHistoryEntry{Query: "up", Status: "ok", Series: -1}, false},
	}
	for _, tc := range cases {
		e := tc.e
		if err := metricsHistoryValidate(&e); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
}

func TestMetricsHistoryEndpoints(t *testing.T) {
	hist := newPromQLFakeHistoryStore()
	prev := promqlHistoryStoreOf
	promqlHistoryStoreOf = func(*Server) promqlHistoryStore { return hist }
	t.Cleanup(func() { promqlHistoryStoreOf = prev })
	s := &Server{}
	mux := http.NewServeMux()
	s.registerMetricsHistoryRoutes(mux)
	do := func(method, body, uid string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/metrics/history", bytes.NewBufferString(body))
		if uid != "" {
			r = promqlAs(r, uid, "viewer")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("kimliksiz GET: %d", w.Code)
	}
	if w := do("POST", `{"q":"sum(rate(up[5m]))","status":"ok","series":4,"durationMs":318}`, "u1"); w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	if w := do("POST", `{"q":"up)","status":"error","line":1,"col":3}`, "u1"); w.Code != 200 {
		t.Fatalf("POST hata: %d %s", w.Code, w.Body)
	}
	if w := do("POST", `{"q":"","status":"ok"}`, "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("boş q 400 olmalı: %d", w.Code)
	}
	var resp metricsHistoryResponse
	w := do("GET", "", "u1")
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Enabled || resp.MaxEntries != 50 || len(resp.Entries) != 2 || resp.Entries[0].Query != "up)" || resp.Entries[0].Col != 3 || resp.Entries[1].Series != 4 || resp.Entries[1].At == 0 {
		t.Fatalf("GET: %+v", resp)
	}
	// başka kullanıcı görmez
	resp = metricsHistoryResponse{}
	_ = json.Unmarshal(do("GET", "", "u2").Body.Bytes(), &resp)
	if len(resp.Entries) != 0 {
		t.Fatalf("u2 u1'in geçmişini gördü: %+v", resp)
	}
	// token çağıranı tutmaz
	resp = metricsHistoryResponse{}
	_ = json.Unmarshal(do("GET", "", "token:ci").Body.Bytes(), &resp)
	if resp.Enabled {
		t.Fatal("token çağıranında enabled false olmalı")
	}
	if w := do("DELETE", "", "u1"); w.Code != 200 {
		t.Fatalf("DELETE: %d", w.Code)
	}
	resp = metricsHistoryResponse{}
	_ = json.Unmarshal(do("GET", "", "u1").Body.Bytes(), &resp)
	if len(resp.Entries) != 0 {
		t.Fatalf("DELETE sonrası boş olmalı: %+v", resp)
	}
}
