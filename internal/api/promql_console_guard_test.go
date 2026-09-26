package api

// v0.10.952 — PromQL konsolu saf korkulukları (spec C4; karar 3, 4, 7, 9).
// Tablolu kenarlar: 7g ±1s, 11k nokta sınırı, min-step tabanı, 2 eşzamanlı
// + 1 → 429, pencere sıfırlanması (enjekte saat, uyku yok), metadata
// anahtarının ayrıklığı / kararlılığı / permütasyon değişmezliği
// (cache_key_test.go deseni, v0.5.187). Ağ yok, CH yok, Thanos yok.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Adım bütçesi ────────────────────────────────────────────────────────────

func TestPromQLEffectiveStep(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	minStep60 := def
	minStep60.MinStepS = 60
	const day = 24 * time.Hour
	const week = 7 * day
	// 11000 × 15 s = 165000 s ≈ 45.83 h: 15 s tabanıyla 11k tavanının el
	// değiştirdiği nokta (audit §6).
	const boundary = 165000 * time.Second

	cases := []struct {
		name       string
		cfg        promqlConsoleSettings
		rng        time.Duration
		requested  time.Duration
		points     int
		wantStep   time.Duration
		wantRaised bool
	}{
		// Otomatik — grafik genişliği (targetPoints 1000), taban 15 s.
		{"oto 1h → min-step tabanı", def, time.Hour, 0, 0, 15 * time.Second, false},
		{"oto 24h → ceil(86400/1000)=87s", def, day, 0, 0, 87 * time.Second, false},
		{"oto 7g → ceil(604800/1000)=605s", def, week, 0, 0, 605 * time.Second, false},
		{"oto points=2000, 24h → ceil(43.2)=44s", def, day, 0, 2000, 44 * time.Second, false},
		{"oto points=50 → 100'e kelepçe, 1h → 36s", def, time.Hour, 0, 50, 36 * time.Second, false},
		{"oto points=20000 → 11000'e kelepçe, 7g → 55s", def, week, 0, 20000, 55 * time.Second, false},
		{"oto aralık 0 (tek nokta) → taban", def, 0, 0, 0, 15 * time.Second, false},
		{"oto minStepS=60 tabanı", minStep60, time.Hour, 0, 0, 60 * time.Second, false},
		{"oto negatif istek = oto", def, day, -time.Second, 0, 87 * time.Second, false},
		// Elle — taban max(minStep, ceil(rng/11000)); altı YÜKSELTİLİR.
		{"elle 1s, 1h → min-step'e yükseltilir", def, time.Hour, time.Second, 0, 15 * time.Second, true},
		{"elle 15s, 1h → aynen", def, time.Hour, 15 * time.Second, 0, 15 * time.Second, false},
		{"elle 30s, 1h → aynen", def, time.Hour, 30 * time.Second, 0, 30 * time.Second, false},
		{"elle 15.5s kesirli korunur", def, time.Hour, 15500 * time.Millisecond, 0, 15500 * time.Millisecond, false},
		{"11k sınırı: rng/step = 11000 tam → geçerli", def, boundary, 15 * time.Second, 0, 15 * time.Second, false},
		{"11k sınırı +1s → 16s'e yükseltilir", def, boundary + time.Second, 15 * time.Second, 0, 16 * time.Second, true},
		{"elle 15s, 7g → 55s'e yükseltilir", def, week, 15 * time.Second, 0, 55 * time.Second, true},
		{"elle 60s, 7g → aynen", def, week, 60 * time.Second, 0, 60 * time.Second, false},
		{"elle 30s, minStepS=60 → 60s", minStep60, time.Hour, 30 * time.Second, 0, 60 * time.Second, true},
		{"elle adım points'i YOK sayar", def, time.Hour, 30 * time.Second, 5000, 30 * time.Second, false},
	}
	for _, c := range cases {
		step, raised := promqlEffectiveStep(c.rng, c.requested, c.points, c.cfg)
		if step != c.wantStep || raised != c.wantRaised {
			t.Errorf("%s: step=%s raised=%v, beklenen %s/%v", c.name, step, raised, c.wantStep, c.wantRaised)
		}
	}
}

// Değişmez: hangi aralık/istek olursa olsun, sonuç Prometheus'un 11k
// kuralını (rng/step ≤ maxPointsPerSeries) ve min-step tabanını çiğnemez.
func TestPromQLEffectiveStepNeverExceedsPointCeiling(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	for _, rng := range []time.Duration{0, time.Second, time.Minute, 45*time.Hour + 50*time.Minute,
		165001 * time.Second, 7 * 24 * time.Hour, 30 * 24 * time.Hour} {
		for _, req := range []time.Duration{0, time.Millisecond, time.Second, 15 * time.Second, time.Hour} {
			for _, pts := range []int{0, 1, 100, 1000, 11000, 1 << 20} {
				step, _ := promqlEffectiveStep(rng, req, pts, def)
				if step < def.minStep() {
					t.Fatalf("rng=%s req=%s pts=%d: step %s < min-step", rng, req, pts, step)
				}
				if rng/step > time.Duration(def.MaxPointsPerSeries) {
					t.Fatalf("rng=%s req=%s pts=%d: step %s → %d nokta > 11000", rng, req, pts, step, rng/step)
				}
			}
		}
	}
}

func TestPromQLClampPoints(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	low := def
	low.MaxPointsPerSeries = 500
	low = normalizePromQLConsoleSettings(low)
	cases := []struct {
		in   int
		cfg  promqlConsoleSettings
		want int
	}{
		{0, def, 1000}, {-3, def, 1000}, {99, def, 100}, {100, def, 100},
		{2500, def, 2500}, {11000, def, 11000}, {11001, def, 11000},
		{0, low, 500}, {900, low, 500},
	}
	for _, c := range cases {
		if got := promqlClampPoints(c.in, c.cfg); got != c.want {
			t.Errorf("points=%d max=%d: %d, beklenen %d", c.in, c.cfg.MaxPointsPerSeries, got, c.want)
		}
	}
}

// ── Aralık kontrolü ─────────────────────────────────────────────────────────

func TestPromQLCheckRange(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	oneHour := def
	oneHour.MaxRangeH = 1
	end := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	const week = 7 * 24 * time.Hour
	cases := []struct {
		name      string
		cfg       promqlConsoleSettings
		start     time.Time
		wantType  string // "" = geçer
		wantLimit int
	}{
		{"7g - 1s geçer", def, end.Add(-week + time.Second), "", 0},
		{"7g tam sınır geçer", def, end.Add(-week), "", 0},
		{"7g + 1s REDDEDİLİR", def, end.Add(-week - time.Second), "guardrail", 168},
		{"30g REDDEDİLİR (sessiz clamp yok)", def, end.Add(-30 * 24 * time.Hour), "guardrail", 168},
		{"anlık (start=end) geçer", def, end, "", 0},
		{"end < start → bad_data", def, end.Add(time.Second), "bad_data", 0},
		{"maxRangeH=1: 1h geçer", oneHour, end.Add(-time.Hour), "", 0},
		{"maxRangeH=1: 1h+1s reddedilir", oneHour, end.Add(-time.Hour - time.Second), "guardrail", 1},
	}
	for _, c := range cases {
		err := promqlCheckRange(c.start, end, c.cfg)
		if c.wantType == "" {
			if err != nil {
				t.Errorf("%s: beklenmeyen ret %v", c.name, err)
			}
			continue
		}
		var ge *promqlGuardError
		if !errors.As(err, &ge) {
			t.Errorf("%s: *promqlGuardError bekleniyordu, %v", c.name, err)
			continue
		}
		if ge.ErrorType != c.wantType || ge.Status != http.StatusBadRequest {
			t.Errorf("%s: type=%s status=%d", c.name, ge.ErrorType, ge.Status)
		}
		if c.wantType == "guardrail" && (ge.Guardrail != "maxRange" || ge.Limit != c.wantLimit || ge.Unit != "h") {
			t.Errorf("%s: limit gövdede taşınmalı: %+v", c.name, ge)
		}
	}
	// Mesaj operatörün diliyle: "168h (7d)".
	err := promqlCheckRange(end.Add(-week-time.Second), end, def)
	if err == nil || !strings.Contains(err.Error(), "168h (7d)") {
		t.Fatalf("mesaj sınırı insan diliyle söylemeli: %v", err)
	}
}

// ── Kullanıcı başına limitler ───────────────────────────────────────────────

type promqlFakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *promqlFakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *promqlFakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func asGuard(t *testing.T, err error) *promqlGuardError {
	t.Helper()
	var ge *promqlGuardError
	if !errors.As(err, &ge) {
		t.Fatalf("*promqlGuardError bekleniyordu, %v", err)
	}
	return ge
}

// 2 eşzamanlı + 1 → 429 (karar 3); başka kullanıcı etkilenmez; release
// slotu boşaltır ve İKİ KEZ çağrılsa da tek slot boşaltır.
func TestPromQLLimiterConcurrency(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 10, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings() // perUserConcurrency=2

	r1, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.acquireQuery("user-a", cfg)
	ge := asGuard(t, err)
	if ge.Status != http.StatusTooManyRequests || ge.ErrorType != "rate_limited" ||
		ge.Guardrail != "perUserConcurrency" || ge.Limit != 2 || ge.retryAfterSeconds() != 1 {
		t.Fatalf("3. eşzamanlı: %+v", ge)
	}
	// Başka kullanıcının kendi bütçesi var.
	rb, err := l.acquireQuery("user-b", cfg)
	if err != nil {
		t.Fatalf("user-b etkilenmemeli: %v", err)
	}
	rb()

	// İdempotent release: r1 iki kez → yalnız BİR slot boşalır.
	r1()
	r1()
	r3, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatalf("release sonrası slot boşalmalı: %v", err)
	}
	if _, err := l.acquireQuery("user-a", cfg); err == nil {
		t.Fatal("çift release iki slot boşalttı (r2 + r3 uçuşta, limit 2)")
	}
	r2()
	r3()
	if n := len(l.inflight); n != 0 {
		t.Fatalf("hepsi bitince inflight haritası boşalmalı, %d girdi", n)
	}
}

// Reddedilen çağrı bütçe YAKMAZ (mcp_gate.go dersi).
func TestPromQLLimiterRejectionDoesNotBurnBudget(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 10, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserConcurrency = 1
	cfg.PerUserPerMin = 3

	hold, err := l.acquireQuery("user-a", cfg) // sayaç 1
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := l.acquireQuery("user-a", cfg); asGuard(t, err).Guardrail != "perUserConcurrency" {
			t.Fatalf("eşzamanlılık reddi bekleniyordu")
		}
	}
	hold()
	for i := 0; i < 2; i++ { // sayaç 2, 3
		rel, err := l.acquireQuery("user-a", cfg)
		if err != nil {
			t.Fatalf("eşzamanlılık retleri bütçe yakmış: %d. çağrı %v", i+2, err)
		}
		rel()
	}
	_, err = l.acquireQuery("user-a", cfg)
	if ge := asGuard(t, err); ge.Guardrail != "perUserPerMin" || ge.Limit != 3 {
		t.Fatalf("4. çağrı dakika bütçesine takılmalı: %+v", ge)
	}
	// Hız reddi de slot tüketmez: inflight boş kalmalı.
	if n := len(l.inflight); n != 0 {
		t.Fatalf("hız reddi slot tuttu: %d", n)
	}
}

// Sabit pencere: 30/dk dolar → 429 + Retry-After pencere sonuna; pencere
// dönünce sıfırlanır. Saat enjekte — uyku yok.
func TestPromQLLimiterRateWindowReset(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 10, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings() // perUserPerMin=30
	for i := 0; i < 30; i++ {
		rel, err := l.acquireQuery("user-a", cfg)
		if err != nil {
			t.Fatalf("%d. çağrı: %v", i+1, err)
		}
		rel()
	}
	_, err := l.acquireQuery("user-a", cfg)
	ge := asGuard(t, err)
	if ge.Guardrail != "perUserPerMin" || ge.Status != http.StatusTooManyRequests || ge.retryAfterSeconds() != 50 {
		t.Fatalf("31. çağrı: %+v (retry %ds)", ge, ge.retryAfterSeconds())
	}
	// Pencere sonuna yarım saniye: hâlâ ret, Retry-After ≥ 1.
	clk.set(time.Date(2026, 9, 26, 12, 0, 59, 500e6, time.UTC))
	_, err = l.acquireQuery("user-a", cfg)
	if ge := asGuard(t, err); ge.retryAfterSeconds() != 1 {
		t.Fatalf("pencere sonu Retry-After 1 olmalı, %d", ge.retryAfterSeconds())
	}
	// Başka kullanıcı etkilenmez.
	if rel, err := l.acquireQuery("user-b", cfg); err != nil {
		t.Fatalf("user-b: %v", err)
	} else {
		rel()
	}
	// Pencere döner → sıfır.
	clk.set(time.Date(2026, 9, 26, 12, 1, 0, 0, time.UTC))
	rel, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatalf("yeni pencerede sıfırlanmalı: %v", err)
	}
	rel()
	// Bayat kovalar süpürüldü: harita yalnız bu pencerenin kovasını taşır.
	if len(l.queryRate) != 1 {
		t.Fatalf("bayat kovalar süpürülmedi: %d kova", len(l.queryRate))
	}
}

// Metadata ayrı ve daha yüksek bütçe: query bütçesi bitse de otomatik
// tamamlama çalışır; 240 dolunca 429 metaPerUserPerMin; pencereyle sıfırlanır.
func TestPromQLLimiterMetaBudgetSeparate(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings()
	for i := 0; i < cfg.PerUserPerMin; i++ {
		rel, err := l.acquireQuery("user-a", cfg)
		if err != nil {
			t.Fatal(err)
		}
		rel()
	}
	for i := 0; i < 240; i++ {
		rel, err := l.acquireMeta("user-a", cfg)
		if err != nil {
			t.Fatalf("meta %d. çağrı (query bütçesi bitmiş olsa da): %v", i+1, err)
		}
		rel()
	}
	_, err := l.acquireMeta("user-a", cfg)
	ge := asGuard(t, err)
	if ge.Guardrail != "metaPerUserPerMin" || ge.Limit != 240 || ge.retryAfterSeconds() != 60 {
		t.Fatalf("241. meta: %+v retry=%d", ge, ge.retryAfterSeconds())
	}
	// Anında bırakılan meta slotları iz bırakmaz; query slotuna hiç dokunmaz.
	if len(l.inflight) != 0 || len(l.metaInflight) != 0 {
		t.Fatalf("slot sızdı: inflight=%v metaInflight=%v", l.inflight, l.metaInflight)
	}
	clk.set(clk.now().Add(time.Minute))
	rel, err := l.acquireMeta("user-a", cfg)
	if err != nil {
		t.Fatalf("meta penceresi sıfırlanmalı: %v", err)
	}
	rel()
}

// v0.10.952 — metadata eşzamanlılık kapısı: önceden YOKTU, bir kullanıcı
// ıskada dakikalık bütçenin tamamını (240) aynı anda uçurabiliyordu. Kapı
// max(4, 2×perUserConcurrency); ret hız kovasından ÖNCE (bütçe yakmaz);
// query ve metadata slotları birbirinden bağımsız.
func TestPromQLLimiterMetaConcurrency(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 10, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings() // perUserConcurrency=2 → meta kapısı 4

	for _, c := range []struct{ conc, want int }{{1, 4}, {2, 4}, {3, 6}, {10, 20}} {
		k := cfg
		k.PerUserConcurrency = c.conc
		if got := promqlMetaConcurrency(k); got != c.want {
			t.Errorf("promqlMetaConcurrency(perUserConcurrency=%d) = %d, beklenen %d", c.conc, got, c.want)
		}
	}

	var held []func()
	for i := 0; i < 4; i++ {
		rel, err := l.acquireMeta("user-a", cfg)
		if err != nil {
			t.Fatalf("%d. tutulan meta: %v", i+1, err)
		}
		held = append(held, rel)
	}
	countBefore := l.metaRate["user-a"].count
	_, err := l.acquireMeta("user-a", cfg)
	ge := asGuard(t, err)
	if ge.Status != http.StatusTooManyRequests || ge.ErrorType != "rate_limited" || ge.Guardrail != "metaPerUserConcurrency" ||
		ge.Limit != 4 || ge.Unit != "requests" || ge.retryAfterSeconds() != 1 || !strings.Contains(ge.Msg, "at most 4") {
		t.Fatalf("5. eşzamanlı meta: %+v", ge)
	}
	if got := l.metaRate["user-a"].count; got != countBefore {
		t.Fatalf("eşzamanlılık reddi metaRate'i yaktı: %d → %d", countBefore, got)
	}
	// Başka kullanıcı etkilenmez.
	rb, err := l.acquireMeta("user-b", cfg)
	if err != nil {
		t.Fatalf("user-b etkilenmemeli: %v", err)
	}
	rb()
	// Meta slotları dolu iken query slotu hâlâ alınır…
	rq, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatalf("meta slotları query kapısını daraltmamalı: %v", err)
	}
	// …ve tersi: query slotları doluyken meta kapısı yalnız kendi sayacına bakar.
	rq2, err := l.acquireQuery("user-a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	held[0]()
	held[0]() // idempotent: yalnız bir slot boşalır
	rm, err := l.acquireMeta("user-a", cfg)
	if err != nil {
		t.Fatalf("query slotları dolu (2/2) iken meta alınabilmeli: %v", err)
	}
	if _, err := l.acquireMeta("user-a", cfg); asGuard(t, err).Guardrail != "metaPerUserConcurrency" {
		t.Fatal("çift release iki meta slotu boşalttı")
	}
	rq()
	rq2()
	rm()
	for _, r := range held[1:] {
		r()
	}
	if len(l.inflight) != 0 || len(l.metaInflight) != 0 {
		t.Fatalf("hepsi bitince haritalar boşalmalı: inflight=%v metaInflight=%v", l.inflight, l.metaInflight)
	}
	rel, err := l.acquireMeta("user-a", cfg)
	if err != nil {
		t.Fatalf("release sonrası: %v", err)
	}
	rel()
}

// Yarış: N goroutine aynı kullanıcı için meta try-acquire → en fazla kapı
// kadarı geçer. -race ile anlamlı.
func TestPromQLLimiterConcurrentMetaAcquireRespectsLimit(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ok       int
		releases []func()
	)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, err := l.acquireMeta("user-a", cfg); err == nil {
				mu.Lock()
				ok++
				releases = append(releases, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if want := promqlMetaConcurrency(cfg); ok != want {
		t.Fatalf("%d eşzamanlı meta geçti, kapı %d", ok, want)
	}
	if got := l.metaRate["user-a"].count; got != ok {
		t.Fatalf("yalnız geçenler bütçe yakmalı: sayaç %d, geçen %d", got, ok)
	}
	for _, r := range releases {
		r()
	}
	if len(l.metaInflight) != 0 {
		t.Fatalf("metaInflight boşalmalı: %v", l.metaInflight)
	}
}

// Yarış: N goroutine aynı kullanıcı için try-acquire → en fazla limit kadarı
// geçer (tek mutex, yarım tüketim yok). -race ile de anlamlı.
func TestPromQLLimiterConcurrentAcquireRespectsLimit(t *testing.T) {
	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}
	l := newPromQLLimiter(clk.now)
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserPerMin = 600
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ok       int
		releases []func()
	)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, err := l.acquireQuery("user-a", cfg); err == nil {
				mu.Lock()
				ok++
				releases = append(releases, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != cfg.PerUserConcurrency {
		t.Fatalf("%d eşzamanlı geçti, limit %d", ok, cfg.PerUserConcurrency)
	}
	for _, r := range releases {
		r()
	}
}

// ── Hata yazımı ─────────────────────────────────────────────────────────────

func TestPromQLGuardErrorWrite(t *testing.T) {
	cases := []struct {
		name       string
		err        *promqlGuardError
		wantCode   int
		wantRetry  string
		wantFields map[string]any
	}{
		{"429 hız: Retry-After yukarı yuvarlanır",
			&promqlGuardError{Status: 429, ErrorType: "rate_limited", Guardrail: "perUserPerMin", Limit: 30,
				Unit: "requests/min", RetryAfter: 49200 * time.Millisecond, Msg: "query rate limit reached"},
			429, "50",
			map[string]any{"status": "error", "errorType": "rate_limited", "guardrail": "perUserPerMin",
				"limit": float64(30), "unit": "requests/min", "retryAfterS": float64(50)}},
		{"429 alt-saniye → 1",
			&promqlGuardError{Status: 429, ErrorType: "rate_limited", Guardrail: "perUserConcurrency", Limit: 2,
				Unit: "requests", RetryAfter: 300 * time.Millisecond, Msg: "too many"},
			429, "1", map[string]any{"retryAfterS": float64(1)}},
		{"400 guardrail: Retry-After YOK, limit gövdede",
			&promqlGuardError{Status: 400, ErrorType: "guardrail", Guardrail: "maxRange", Limit: 168, Unit: "h", Msg: "too wide"},
			400, "", map[string]any{"errorType": "guardrail", "guardrail": "maxRange", "limit": float64(168), "unit": "h"}},
		{"400 bad_data: guardrail alanları yok",
			&promqlGuardError{Status: 400, ErrorType: "bad_data", Msg: "end before start"},
			400, "", map[string]any{"errorType": "bad_data", "error": "end before start"}},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		writePromQLGuardError(rec, c.err)
		if rec.Code != c.wantCode {
			t.Errorf("%s: status %d", c.name, rec.Code)
		}
		if got := rec.Header().Get("Retry-After"); got != c.wantRetry {
			t.Errorf("%s: Retry-After %q, beklenen %q", c.name, got, c.wantRetry)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", c.name, ct)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: gövde JSON değil: %s", c.name, rec.Body)
			continue
		}
		for k, v := range c.wantFields {
			if body[k] != v {
				t.Errorf("%s: %s=%v, beklenen %v", c.name, k, body[k], v)
			}
		}
		if c.err.ErrorType == "bad_data" {
			if _, ok := body["guardrail"]; ok {
				t.Errorf("%s: bad_data guardrail alanı taşımamalı", c.name)
			}
		}
	}
}

// ── Metadata ────────────────────────────────────────────────────────────────

func TestPromQLMetaWindow(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	now := time.Date(2026, 9, 26, 12, 0, 47, 0, time.UTC)
	snapNow := time.Date(2026, 9, 26, 12, 0, 30, 0, time.UTC)
	cases := []struct {
		name       string
		start, end time.Time
		wantStart  time.Time
		wantEnd    time.Time
		wantErr    bool
	}{
		{"verilmemiş → son 1h, 30s ızgarası", time.Time{}, time.Time{}, snapNow.Add(-time.Hour), snapNow, false},
		{"yalnız end → end-1h", time.Time{}, now.Add(-2 * time.Hour), snapNow.Add(-3 * time.Hour), snapNow.Add(-2 * time.Hour), false},
		{"açık pencere korunur (ızgaraya oturur)", now.Add(-6 * time.Hour), now, snapNow.Add(-6 * time.Hour), snapNow, false},
		{"30g → maxRange'e KELEPÇE (ret değil)", now.Add(-30 * 24 * time.Hour), now, snapNow.Add(-7 * 24 * time.Hour), snapNow, false},
		{"end < start → bad_data", now, now.Add(-time.Minute), time.Time{}, time.Time{}, true},
	}
	for _, c := range cases {
		s, e, err := promqlMetaWindow(c.start, c.end, now, def)
		if c.wantErr {
			if ge := asGuard(t, err); ge.ErrorType != "bad_data" || ge.Status != http.StatusBadRequest {
				t.Errorf("%s: %+v", c.name, ge)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !s.Equal(c.wantStart) || !e.Equal(c.wantEnd) {
			t.Errorf("%s: [%s, %s], beklenen [%s, %s]", c.name, s, e, c.wantStart, c.wantEnd)
		}
		if e.Sub(s) > def.maxRange() {
			t.Errorf("%s: pencere maxRange'i aştı", c.name)
		}
	}
}

func TestPromQLMetaLimit(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 1000}, {-5, 1000}, {1, 1}, {50, 50}, {1000, 1000}, {1001, 1000}, {1 << 30, 1000},
	} {
		if got := promqlMetaLimit(c.in); got != c.want {
			t.Errorf("limit=%d: %d, beklenen %d", c.in, got, c.want)
		}
	}
}

func promqlMetaKeyBase() promqlMetaKeyInput {
	start := time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC)
	return promqlMetaKeyInput{
		Kind: "label-values", LabelName: "__name__", ClusterID: "c-0000000a",
		URL: "http://thanos.example.invalid", InjectName: "cluster", InjectValue: "cluster-a",
		Matches: []string{`{job="api"}`, `up`}, Start: start, End: start.Add(time.Hour),
		Limit: 1000,
	}
}

// Ayrıklık: HER girdi tek başına değişince anahtar değişir (v0.5.187 —
// eksik girdi = çapraz zehirlenme). Başlık vakası: paylaşımlı querier'da
// aynı URL, farklı enjekte değer (cluster-a / cluster-b).
func TestPromQLMetaCacheKeyDistinctPerInput(t *testing.T) {
	base := promqlMetaKeyBase()
	muts := map[string]func(*promqlMetaKeyInput){
		"Kind":            func(k *promqlMetaKeyInput) { k.Kind = "series" },
		"LabelName":       func(k *promqlMetaKeyInput) { k.LabelName = "job" },
		"ClusterID":       func(k *promqlMetaKeyInput) { k.ClusterID = "c-0000000b" },
		"URL":             func(k *promqlMetaKeyInput) { k.URL = "http://thanos-b.example.invalid" },
		"InjectName":      func(k *promqlMetaKeyInput) { k.InjectName = "tenant" },
		"InjectValue":     func(k *promqlMetaKeyInput) { k.InjectValue = "cluster-b" },
		"InjectOff":       func(k *promqlMetaKeyInput) { k.InjectName, k.InjectValue = "", "" },
		"NamespaceFilter": func(k *promqlMetaKeyInput) { k.NamespaceFilter = "team-.*" },
		"Matches":         func(k *promqlMetaKeyInput) { k.Matches = []string{`{job="web"}`, `up`} },
		"MatchesNone":     func(k *promqlMetaKeyInput) { k.Matches = nil },
		"MatchesExtra":    func(k *promqlMetaKeyInput) { k.Matches = append([]string{`node_load1`}, k.Matches...) },
		"Start":           func(k *promqlMetaKeyInput) { k.Start = k.Start.Add(-time.Hour) },
		"End":             func(k *promqlMetaKeyInput) { k.End = k.End.Add(time.Hour) },
		"Limit":           func(k *promqlMetaKeyInput) { k.Limit = 50 },
		"PartialResponse": func(k *promqlMetaKeyInput) { k.PartialResponse = true },
	}
	seen := map[string]string{promqlMetaCacheKey(base): "base"}
	for name, mut := range muts {
		in := base
		in.Matches = append([]string(nil), base.Matches...)
		mut(&in)
		k := promqlMetaCacheKey(in)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s değişikliği %s ile aynı anahtarı verdi: %s — girdi anahtara girmiyor", name, prev, k)
		}
		seen[k] = name
	}
	if !strings.HasPrefix(promqlMetaCacheKey(base), "promql-meta:v947:label-values:c-0000000a:") {
		t.Fatalf("okunur önek: %s", promqlMetaCacheKey(base))
	}
}

// Aynı uzunlukta farklı kümeler ayrışır (v0.5.187'nin kendisi).
func TestPromQLMetaCacheKeySameLengthDistinct(t *testing.T) {
	a, b := promqlMetaKeyBase(), promqlMetaKeyBase()
	a.Matches, b.Matches = []string{"up"}, []string{"node_load1"}
	if promqlMetaCacheKey(a) == promqlMetaCacheKey(b) {
		t.Fatal("v0.5.187 regresyonu: {up} ve {node_load1} aynı anahtar")
	}
}

// Uzunluk öneki: alan sınırı kayması iki girdiyi aynı akışa katlamaz.
func TestPromQLMetaCacheKeyFieldBoundaries(t *testing.T) {
	a, b := promqlMetaKeyBase(), promqlMetaKeyBase()
	a.InjectName, a.InjectValue = "ab", ""
	b.InjectName, b.InjectValue = "a", "b"
	if promqlMetaCacheKey(a) == promqlMetaCacheKey(b) {
		t.Fatal(`("ab","") ve ("a","b") çakıştı — alanlar uzunluk-önekli değil`)
	}
	c, d := promqlMetaKeyBase(), promqlMetaKeyBase()
	c.Matches, d.Matches = []string{"a\x00b"}, []string{"a", "b"}
	if promqlMetaCacheKey(c) == promqlMetaCacheKey(d) {
		t.Fatal(`match[] ["a\x00b"] ve ["a","b"] çakıştı`)
	}
}

func TestPromQLMetaCacheKeyStable(t *testing.T) {
	in := promqlMetaKeyBase()
	first := promqlMetaCacheKey(in)
	for i := 0; i < 10; i++ {
		if got := promqlMetaCacheKey(in); got != first {
			t.Fatalf("kararsız: %s != %s", got, first)
		}
	}
}

// Küme semantiği: match[] sırası ve tekrarı anahtarı değiştirmez; çağıranın
// dilimi yerinde sıralanmaz (handler aynı dilimi Thanos'a yollar).
func TestPromQLMetaCacheKeyPermutationInvariant(t *testing.T) {
	a, b, c := promqlMetaKeyBase(), promqlMetaKeyBase(), promqlMetaKeyBase()
	a.Matches = []string{"x", "y", "z"}
	b.Matches = []string{"z", "x", "y"}
	c.Matches = []string{"y", "z", "x", "x"}
	ka, kb, kc := promqlMetaCacheKey(a), promqlMetaCacheKey(b), promqlMetaCacheKey(c)
	if ka != kb || ka != kc {
		t.Fatalf("permütasyon/tekrar anahtarı değiştirdi: %s %s %s", ka, kb, kc)
	}
	if strings.Join(b.Matches, ",") != "z,x,y" {
		t.Fatalf("çağıranın dilimi değişti: %v", b.Matches)
	}
}

// Pencere 30 s ızgarasına oturur: aynı ızgara hücresi = aynı anahtar
// (promqlMetaWindow'un döndürdüğü pencereyle birebir).
func TestPromQLMetaCacheKeyWindowBucketed(t *testing.T) {
	a, b := promqlMetaKeyBase(), promqlMetaKeyBase()
	b.Start, b.End = a.Start.Add(10*time.Second), a.End.Add(20*time.Second)
	if promqlMetaCacheKey(a) != promqlMetaCacheKey(b) {
		t.Fatal("aynı 30 s hücresi farklı anahtar verdi")
	}
	b.End = a.End.Add(30 * time.Second)
	if promqlMetaCacheKey(a) == promqlMetaCacheKey(b) {
		t.Fatal("komşu hücre aynı anahtarı verdi")
	}
}

// ── Metadata önbellek bayt tavanı (v0.10.952) ──────────────────────────────

func TestPromQLTrimMetaStrings(t *testing.T) {
	ab := []string{"aa", "bbb"} // maliyet 5 + 6 = 11
	cases := []struct {
		name    string
		in      []string
		budget  int
		wantLen int
		wantCut bool
	}{
		{"boş", nil, 10, 0, false},
		{"toplam = bütçe → sığar", ab, 11, 2, false},
		{"bütçe fazlası → sığar", ab, 12, 2, false},
		{"toplam = bütçe+1 → son değer kesilir", ab, 10, 1, true},
		{"ilk değer bütçeden büyük → boş + truncated", []string{strings.Repeat("x", 20)}, 22, 0, true},
		{"ilk değer tam sığar", []string{strings.Repeat("x", 20)}, 23, 1, false},
	}
	for _, c := range cases {
		got, cut := promqlTrimMetaStrings(c.in, c.budget)
		if len(got) != c.wantLen || cut != c.wantCut {
			t.Errorf("%s: len=%d cut=%v, beklenen %d/%v", c.name, len(got), cut, c.wantLen, c.wantCut)
		}
	}
}

func TestPromQLTrimMetaSeries(t *testing.T) {
	s1 := map[string]string{"a": "bb"}             // 1+2+6 = 9
	s2 := map[string]string{"k": "v", "jj": "www"} // (1+1+6)+(2+3+6) = 19
	sets := []map[string]string{s1, s2}            // toplam 28
	cases := []struct {
		name    string
		in      []map[string]string
		budget  int
		wantLen int
		wantCut bool
	}{
		{"boş", nil, 10, 0, false},
		{"toplam = bütçe → sığar", sets, 28, 2, false},
		{"bütçe fazlası → sığar", sets, 29, 2, false},
		{"toplam = bütçe+1 → ikinci set kesilir", sets, 27, 1, true},
		{"ilk set bütçeden büyük → boş + truncated", sets, 8, 0, true},
		{"ilk set tam sığar", sets, 9, 1, true},
	}
	for _, c := range cases {
		got, cut := promqlTrimMetaSeries(c.in, c.budget)
		if len(got) != c.wantLen || cut != c.wantCut {
			t.Errorf("%s: len=%d cut=%v, beklenen %d/%v", c.name, len(got), cut, c.wantLen, c.wantCut)
		}
	}
}
