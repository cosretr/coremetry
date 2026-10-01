package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// service_operation_routes_test.go — v0.10.1023. Operatör bildirimi:
// "Operation kısmında POST GET neden detail gözükmüyor, sonra trace'e girince
// çıkıyor." Çivilenen: önbellek anahtarı her girdiyi taşır, env doluyken
// sorgusuz kısa devre, bozuk pencere 400, kayıt + serveCached kaynak pinleri.

func TestOpRoutesKeyDistinct(t *testing.T) {
	from := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := opRoutesKey("payments-api", "", from, to)
	for name, other := range map[string]string{
		"servis":  opRoutesKey("checkout", "", from, to),
		"env":     opRoutesKey("payments-api", "prod", from, to),
		"pencere": opRoutesKey("payments-api", "", from.Add(-time.Hour), to),
		"to":      opRoutesKey("payments-api", "", from, to.Add(time.Minute)),
		// Alan sınırı: "a"+"bc" ile "ab"+"c" aynı anahtara düşmemeli (v0.5.187).
		"sınır": opRoutesKey("payments-ap", "i", from, to),
		// Aynı 30 sn hücresi, farklı ızgara: to tam 5 dk sınırı vs +1 sn.
		"ızgara": opRoutesKey("payments-api", "", from, to.Add(time.Second)),
	} {
		if other == base {
			t.Errorf("%s anahtarı değiştirmiyor: %s", name, base)
		}
	}
	if opRoutesKey("payments-api", "", from, to) != base {
		t.Error("aynı girdi aynı anahtarı vermeli")
	}
	if !strings.HasPrefix(base, "svc-op-routes:v1:") {
		t.Errorf("önek: %s", base)
	}
}

func TestOpRoutesWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }
	cases := []struct {
		name     string
		q        url.Values
		wantErr  bool
		from, to time.Time
	}{
		{"varsayılan 24 sa", url.Values{}, false, now.Add(-24 * time.Hour), now},
		{"since", url.Values{"since": {"1h"}}, false, now.Add(-time.Hour), now},
		{"mutlak", url.Values{"from": {ns(now.Add(-2 * time.Hour))}, "to": {ns(now.Add(-time.Hour))}}, false, now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		{"yalnız from", url.Values{"from": {ns(now.Add(-time.Hour))}}, false, now.Add(-time.Hour), now},
		{"bozuk from", url.Values{"from": {"abc"}}, true, time.Time{}, time.Time{}},
		{"bozuk to", url.Values{"to": {"12x"}}, true, time.Time{}, time.Time{}},
		{"negatif", url.Values{"from": {"-5"}}, true, time.Time{}, time.Time{}},
		{"ters pencere", url.Values{"from": {ns(now)}, "to": {ns(now.Add(-time.Hour))}}, true, time.Time{}, time.Time{}},
		{"sıfır uzunluk", url.Values{"from": {ns(now)}, "to": {ns(now)}}, true, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		from, to, err := opRoutesWindow(c.q, now)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v", c.name, err)
			continue
		}
		if !c.wantErr && (!from.Equal(c.from) || !to.Equal(c.to)) {
			t.Errorf("%s: [%s, %s], [%s, %s] beklenir", c.name, from, to, c.from, c.to)
		}
	}
}

func newOpRoutesTestMux() *http.ServeMux {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerServiceOperationRouteRoutes(mux)
	return mux
}

// env doluyken store'a HİÇ dokunulmaz: test sunucusunun store'u nil —
// sorgu koşsaydı panik olurdu.
func TestOpRoutesEnvShortCircuit(t *testing.T) {
	mux := newOpRoutesTestMux()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/services/payments-api/operations/routes?env=prod", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("env kısa devresi %d, 200 beklenir: %s", w.Code, w.Body.String())
	}
	var got struct {
		Rows    []json.RawMessage `json:"rows"`
		Covered *bool             `json:"covered"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rows == nil || len(got.Rows) != 0 || got.Covered == nil || *got.Covered {
		t.Errorf("gövde {rows: [], covered: false} olmalı: %s", w.Body.String())
	}
}

func TestOpRoutesBadInput400(t *testing.T) {
	mux := newOpRoutesTestMux()
	for _, q := range []string{"from=abc", "to=1x", "from=200&to=100"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/services/payments-api/operations/routes?"+q, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, 400 beklenir", q, w.Code)
		}
	}
}

// R7(4) — gövde şekli ClickHouse'suz: kapsanmayan yol satırsız ve
// truncated'sız; tavana ulaşan kapsanmış yanıt truncated; nil → [].
func TestOpRoutesResultShape(t *testing.T) {
	mk := func(n int) []chstore.OperationSummary {
		out := make([]chstore.OperationSummary, n)
		for i := range out {
			out[i] = chstore.OperationSummary{Name: "GET", Route: "/r" + strconv.Itoa(i), SpanCount: 1}
		}
		return out
	}
	cases := []struct {
		name                   string
		rows                   []chstore.OperationSummary
		covered                bool
		wantLen                int
		wantCovered, wantTrunc bool
	}{
		{"kapsanmıyor, nil", nil, false, 0, false, false},
		// Store kapsanmayan yolda nil döner; yine de satır sızarsa gövdeye girmez.
		{"kapsanmıyor, satır sızmış", mk(3), false, 0, false, false},
		{"kapsanmıyor, tavan dolu satır", mk(chstore.BareVerbRouteLimit), false, 0, false, false},
		{"kapsanıyor, nil", nil, true, 0, true, false},
		{"kapsanıyor, az satır", mk(5), true, 5, true, false},
		{"kapsanıyor, tavan − 1", mk(chstore.BareVerbRouteLimit - 1), true, chstore.BareVerbRouteLimit - 1, true, false},
		{"kapsanıyor, tavan", mk(chstore.BareVerbRouteLimit), true, chstore.BareVerbRouteLimit, true, true},
	}
	for _, c := range cases {
		got := opRoutesResult(c.rows, c.covered)
		if got.Rows == nil || len(got.Rows) != c.wantLen || got.Covered != c.wantCovered || got.Truncated != c.wantTrunc {
			t.Errorf("%s: rows=%d(nil=%v) covered=%v truncated=%v", c.name, len(got.Rows), got.Rows == nil, got.Covered, got.Truncated)
		}
		b, _ := json.Marshal(got)
		if !strings.Contains(string(b), `"rows":[`) {
			t.Errorf("%s: rows JSON'da dizi olmalı (null değil): %s", c.name, b)
		}
		if strings.Contains(string(b), "truncated") != c.wantTrunc {
			t.Errorf("%s: truncated alanı yalnız true iken yazılır: %s", c.name, b)
		}
	}
}

func TestOpRoutesSourcePins(t *testing.T) {
	src, err := os.ReadFile("service_operation_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`registerRoutesExtra("service-operation-routes", (*Server).registerServiceOperationRouteRoutes)`,
		// Kayıt satırının yalnız kuyruğu: tam `mux.HandleFunc("GET …` metni
		// make audit CHECK 7'nin kopya-route grep'ine ikinci kayıt gibi görünür.
		`"GET /api/services/{name}/operations/routes", s.getServiceOperationRoutes)`,
		`key := opRoutesKey(svc, env, from, to)`,
		`s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {`,
		`s.store.GetBareVerbRouteOperations(ctx, svc, from, to)`,
		// Gövde iki yolda da TEK şekillendiriciden (R7): env kısa devresi + store cevabı.
		`writeJSON(w, opRoutesResult(nil, false))`,
		`return opRoutesResult(rows, covered), nil`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("service_operation_routes.go %q taşımalı", want)
		}
	}
	// Kısa devre serveCached'ten ÖNCE: env'li istek ne sorgu ne önbellek girdisi üretir.
	if strings.Index(string(src), "if env != \"\" {") > strings.Index(string(src), "s.serveCached(") {
		t.Error("env kısa devresi serveCached'ten önce olmalı")
	}
}
