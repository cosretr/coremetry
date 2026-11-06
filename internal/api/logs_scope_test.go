package api

// logs_scope_test.go — v0.10.1855 (Log Search Faz 2 dilim 2.4): `scope=mine`
// → Filter.Services (≤100, sıralı), çözülemezse 400 + arka uca gidilmez, açık
// servis kazanır, CH log yolunda yok sayılır (yeni yetenek yalnız ES), anahtar
// servis kümesini taşır (sıra-bağımsız, kapsamsız anahtar bayt-bayt aynı),
// seçici sağlığı scoreHealth kuralıyla ve veri yokken boş.

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogsScopeParam(t *testing.T) {
	cases := map[string][2]bool{"": {false, true}, "all": {false, true}, " mine ": {true, true}, "MINE": {false, false}, "team": {false, false}}
	for raw, want := range cases {
		mine, ok := logsScopeParam(raw)
		if mine != want[0] || ok != want[1] {
			t.Errorf("%q → (%v,%v) want %v", raw, mine, ok, want)
		}
	}
}

func TestLogsServicesKey(t *testing.T) {
	base := logstore.Filter{Search: "x"}
	if logsServicesKey(base) != "" || logsScopeKeys(base) != logsTeamKey(base) {
		t.Fatal("kapsamsız anahtar eki boş olmalı")
	}
	a := logsServicesKey(logstore.Filter{Services: []string{"orders-api", "payments-mobile"}})
	b := logsServicesKey(logstore.Filter{Services: []string{"payments-mobile", "orders-api"}})
	c := logsServicesKey(logstore.Filter{Services: []string{"orders-api"}})
	if a == "" || a != b || a == c {
		t.Fatalf("küme özeti sıra-bağımsız ve kümeye duyarlı olmalı: %q %q %q", a, b, c)
	}
	if logsServicesKey(logstore.Filter{Service: "x", Services: []string{"orders-api"}}) != "" {
		t.Fatal("açık servis varken küme anahtara girmez (sunucu da uygulamaz)")
	}
	k1 := logsSearchKey(logstore.Filter{Services: []string{"orders-api"}}, "a", "b")
	k2 := logsSearchKey(logstore.Filter{Services: []string{"branch-teller"}}, "a", "b")
	if k1 == k2 {
		t.Fatal("liste anahtarı servis kümesini taşımalı (v0.5.187)")
	}
}

type esScopeLogStore struct{ patternFilterLogStore }

func (s *esScopeLogStore) Backend() string { return "elasticsearch" }

func seedLogsScope(uid string, us userScope) {
	logsScopeCache.Lock()
	logsScopeCache.m[uid] = logsScopeEntry{scope: us, at: time.Now()}
	logsScopeCache.Unlock()
}

// enableLogsScope — kapsam mantığı testleri yeteneği açarak koşar (prod
// varsayılanı KAPALI, v0.10.1877).
func enableLogsScope(t *testing.T) {
	t.Helper()
	prev := logsScopeEnabled
	logsScopeEnabled = true
	t.Cleanup(func() { logsScopeEnabled = prev })
}

// TestLogsScopeDisabledByDefault — v0.10.1877 regresyon kapısı.
//
// Belirti (operatör, prod): 1876'da Elasticsearch'te aşırı CPU ve bellek;
// 1851'de yok. En olası neden: /logs'un varsayılan `scope=mine` kapsamı her
// açılışta ≤100 servislik kümeyi çok değerli `terms` yan tümceleriyle TÜM
// indekslerde arıyordu. Varsayılan KAPALI kalmalı: ES log yolunda bile uç
// "yetenek yok" der ve `scope=mine` sorguya servis kümesi EKLEMEZ.
func TestLogsScopeDisabledByDefault(t *testing.T) {
	if logsScopeEnabled {
		t.Fatal("logsScopeEnabled varsayılanı false olmalı (indeks daraltması gelmeden açma)")
	}
	const win = "&from=1759489200000000000&to=1759492800000000000"
	seedLogsScope("scope-off-u1", userScope{Reason: scopeReasonTeam, Team: "orders-team", Services: []string{"payments-mobile", "orders-api"}})
	es := &esScopeLogStore{}
	s := &Server{logs: es, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	if s.logsScopeAvailable() {
		t.Fatal("ES log yolunda yetenek kapalı görünmeli")
	}
	// Eski link / istemci scope=mine yollasa da sorgu KAPSAMSIZ gider (400 değil).
	r := httptest.NewRequest("GET", "/api/logs?scope=mine"+win, nil)
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "scope-off-u1"}))
	w := httptest.NewRecorder()
	s.getLogs(w, r)
	if w.Code != 200 || es.calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, es.calls, w.Body.String())
	}
	if len(es.got.Services) != 0 {
		t.Fatalf("kapalıyken Filter.Services dolmamalı: %v", es.got.Services)
	}
	// Uç "yetenek yok" der → istemci kapsam parametresi yollamaz, çip çizmez.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/logs/scope", nil)
	r2 = r2.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "scope-off-u1"}))
	s.getLogsScope(w2, r2)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), `"available":false`) {
		t.Fatalf("code=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestApplyLogsScopeParam(t *testing.T) {
	enableLogsScope(t)
	const win = "&from=1759489200000000000&to=1759492800000000000"
	call := func(st logstore.Store, uid, q string) (*httptest.ResponseRecorder, *Server) {
		s := &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
		r := httptest.NewRequest("GET", "/api/logs?"+q+win, nil)
		if uid != "" {
			r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid}))
		}
		w := httptest.NewRecorder()
		s.getLogs(w, r)
		return w, s
	}

	seedLogsScope("scope-u1", userScope{Reason: scopeReasonTeam, Team: "orders-team", Services: []string{"payments-mobile", "orders-api"}})
	es := &esScopeLogStore{}
	if w, _ := call(es, "scope-u1", "scope=mine"); w.Code != 200 || es.calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", w.Code, es.calls, w.Body.String())
	}
	if !reflect.DeepEqual(es.got.Services, []string{"orders-api", "payments-mobile"}) {
		t.Fatalf("Filter.Services = %v", es.got.Services)
	}

	// Açık servis kazanır.
	es2 := &esScopeLogStore{}
	if w, _ := call(es2, "scope-u1", "scope=mine&service=branch-teller"); w.Code != 200 || len(es2.got.Services) != 0 || es2.got.Service != "branch-teller" {
		t.Fatalf("açık servis: code=%d f=%+v", w.Code, es2.got)
	}

	// Takımsız / kimliksiz → 400, arka uca gidilmez (kapsamsız app-* yok).
	seedLogsScope("scope-u2", userScope{Unscoped: true, Reason: scopeReasonNoTeam})
	for _, uid := range []string{"scope-u2", ""} {
		es3 := &esScopeLogStore{}
		if w, _ := call(es3, uid, "scope=mine"); w.Code != 400 || es3.calls != 0 {
			t.Fatalf("uid=%q: code=%d calls=%d", uid, w.Code, es3.calls)
		}
	}

	// Geçersiz değer → 400.
	es4 := &esScopeLogStore{}
	if w, _ := call(es4, "scope-u1", "scope=everything"); w.Code != 400 || es4.calls != 0 {
		t.Fatalf("geçersiz scope: code=%d", w.Code)
	}

	// CH log yolu: yeni yetenek kapalı — scope yok sayılır, istek bugünkü gibi.
	ch := &patternFilterLogStore{}
	if w, _ := call(ch, "scope-u2", "scope=mine"); w.Code != 200 || ch.calls != 1 || len(ch.got.Services) != 0 {
		t.Fatalf("CH: code=%d calls=%d services=%v", w.Code, ch.calls, ch.got.Services)
	}
}

func TestGetLogsScopeUnavailableOnCH(t *testing.T) {
	s := &Server{logs: &patternFilterLogStore{}, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	r := httptest.NewRequest("GET", "/api/logs/scope", nil)
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u1"}))
	w := httptest.NewRecorder()
	s.getLogsScope(w, r)
	if w.Code != 200 || w.Body.String() != `{"available":false,"scoped":false,"reason":"unavailable","services":[]}`+"\n" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestLogsScopeHealth(t *testing.T) {
	rows := []chstore.ServiceSummary{
		{Name: "orders-api", SpanCount: 1000, ErrorRate: 0.2},
		{Name: "payments-mobile", SpanCount: 500, ErrorRate: 3.1},
	}
	open := map[string]chstore.OpenProblemCounts{"notify-gateway": {Critical: 1}}
	got := logsScopeHealth([]string{"branch-teller", "notify-gateway", "orders-api", "payments-mobile"}, rows, open)
	want := []logsScopeService{
		{Name: "branch-teller"}, // pencerede span yok → sağlık boş (uydurulmaz)
		{Name: "notify-gateway", Health: "red", HealthReason: "1 open critical", OpenProblems: 1},
		{Name: "orders-api", Health: "green", ErrorRate: 0.2},
		{Name: "payments-mobile", Health: "yellow", HealthReason: "error rate 3.1%", ErrorRate: 3.1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestLogsScopeKey(t *testing.T) {
	if logsScopeKey("u1", true) == logsScopeKey("u2", true) || logsScopeKey("u1", true) == logsScopeKey("u1", false) {
		t.Fatal("anahtar kullanıcı + yeteneği taşımalı")
	}
}
