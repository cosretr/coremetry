package api

// v0.10.952 — PromQL konsolu `promql_console` ayar blobu (spec C3, karar 13).
// Saf seam'ler tablolu: varsayılanlar, okuma-yolu kelepçesi (sıfır/aralık
// dışı → korunan varsayılan), PUT doğrulaması (aralık dışı → 400), JSON alan
// adları (FE sözleşmesi) ve kablo (reload case + config-import listesi +
// main.go boot/yenileme). Canlı CH yok: store'suz Server ile handler'lar
// yalnız doğrulama ve geri-düşüş dallarında koşar.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// withPromQLConsoleSettings — paket-global'i test süresince değiştirir,
// bitince eski işaretçiyi geri koyar (başka testler varsayılanı görsün).
func withPromQLConsoleSettings(t *testing.T, c promqlConsoleSettings) {
	t.Helper()
	prev := promqlConsoleCfg.Load()
	setPromQLConsoleSettings(c)
	t.Cleanup(func() { promqlConsoleCfg.Store(prev) })
}

func TestPromQLConsoleSettingsDefaults(t *testing.T) {
	want := promqlConsoleSettings{
		TimeoutS: 30, MaxRangeH: 168, MinStepS: 15, MaxPointsPerSeries: 11000,
		TargetPoints: 1000, MaxSeries: 500, MaxBodyMiB: 32, PerUserConcurrency: 2,
		PerUserPerMin: 30, MetaPerUserPerMin: 240, PartialResponse: false,
	}
	if got := defaultPromQLConsoleSettings(); got != want {
		t.Fatalf("varsayılanlar spec karar 13'ten saptı:\n got %+v\nwant %+v", got, want)
	}
	// Varsayılanlar kendi kelepçesinden geçmeli (tablo tutarlılığı).
	if got := normalizePromQLConsoleSettings(want); got != want {
		t.Fatalf("varsayılanlar normalize'da değişti: %+v", got)
	}
	d := defaultPromQLConsoleSettings()
	if d.timeout() != 30*time.Second || d.maxRange() != 7*24*time.Hour ||
		d.minStep() != 15*time.Second || d.maxBodyBytes() != 32<<20 {
		t.Fatalf("birim yardımcıları: timeout=%s maxRange=%s minStep=%s body=%d",
			d.timeout(), d.maxRange(), d.minStep(), d.maxBodyBytes())
	}
}

// Hiç hidre edilmemiş süreç (test ikilisi, boot'tan önce) varsayılanı görür.
func TestPromQLConsoleSettingsCurrentBeforeLoad(t *testing.T) {
	prev := promqlConsoleCfg.Load()
	promqlConsoleCfg.Store(nil)
	t.Cleanup(func() { promqlConsoleCfg.Store(prev) })
	if got := currentPromQLConsoleSettings(); got != defaultPromQLConsoleSettings() {
		t.Fatalf("hidre öncesi = varsayılan olmalı, %+v", got)
	}
}

// JSON alan adları FE (lib/types.ts) + Settings paneli sözleşmesidir.
func TestPromQLConsoleSettingsJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(defaultPromQLConsoleSettings())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"maxBodyMiB", "maxPointsPerSeries", "maxRangeH", "maxSeries", "metaPerUserPerMin",
		"minStepS", "partialResponse", "perUserConcurrency", "perUserPerMin", "targetPoints", "timeoutS"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("alan adları:\n got %v\nwant %v", got, want)
	}
	// Tamsayı tablosu her tamsayı alanını kapsar (10 tamsayı + 1 bool).
	if len(promqlConsoleIntFields) != len(want)-1 {
		t.Fatalf("promqlConsoleIntFields %d alan, beklenen %d", len(promqlConsoleIntFields), len(want)-1)
	}
}

func TestPromQLConsoleSettingsNormalize(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	mod := func(f func(*promqlConsoleSettings)) promqlConsoleSettings {
		c := def
		f(&c)
		return c
	}
	cases := []struct {
		name string
		in   promqlConsoleSettings
		want promqlConsoleSettings
	}{
		{"sıfır blob → tüm varsayılanlar", promqlConsoleSettings{}, def},
		{"partialResponse sıfır-değer değil, korunur",
			promqlConsoleSettings{PartialResponse: true},
			mod(func(c *promqlConsoleSettings) { c.PartialResponse = true })},
		{"negatif timeout → varsayılan", mod(func(c *promqlConsoleSettings) { c.TimeoutS = -1 }), def},
		{"timeout 4 (alt sınır altı) → varsayılan", mod(func(c *promqlConsoleSettings) { c.TimeoutS = 4 }), def},
		{"timeout 5 alt sınır korunur",
			mod(func(c *promqlConsoleSettings) { c.TimeoutS = 5 }),
			mod(func(c *promqlConsoleSettings) { c.TimeoutS = 5 })},
		{"timeout 120 üst sınır korunur",
			mod(func(c *promqlConsoleSettings) { c.TimeoutS = 120 }),
			mod(func(c *promqlConsoleSettings) { c.TimeoutS = 120 })},
		{"timeout 121 → varsayılan", mod(func(c *promqlConsoleSettings) { c.TimeoutS = 121 }), def},
		{"maxRangeH 721 → varsayılan", mod(func(c *promqlConsoleSettings) { c.MaxRangeH = 721 }), def},
		{"maxPointsPerSeries 11001 → varsayılan", mod(func(c *promqlConsoleSettings) { c.MaxPointsPerSeries = 11001 }), def},
		{"maxSeries 9 → varsayılan", mod(func(c *promqlConsoleSettings) { c.MaxSeries = 9 }), def},
		{"maxBodyMiB 129 → varsayılan", mod(func(c *promqlConsoleSettings) { c.MaxBodyMiB = 129 }), def},
		{"perUserConcurrency 11 → varsayılan", mod(func(c *promqlConsoleSettings) { c.PerUserConcurrency = 11 }), def},
		{"minStepS 3601 → varsayılan", mod(func(c *promqlConsoleSettings) { c.MinStepS = 3601 }), def},
		{"targetPoints tavanı aşamaz (varsayılan hedef, düşük tavan)",
			mod(func(c *promqlConsoleSettings) { c.MaxPointsPerSeries = 500 }),
			mod(func(c *promqlConsoleSettings) { c.MaxPointsPerSeries = 500; c.TargetPoints = 500 })},
	}
	for _, c := range cases {
		if got := normalizePromQLConsoleSettings(c.in); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestPromQLConsoleSettingsValidate(t *testing.T) {
	cases := []struct {
		name    string
		in      promqlConsoleSettings
		wantErr []string // hata mesajında geçmesi gerekenler; nil = geçerli
		check   func(promqlConsoleSettings) bool
	}{
		{name: "boş gövde = varsayılanlar", in: promqlConsoleSettings{},
			check: func(c promqlConsoleSettings) bool { return c == defaultPromQLConsoleSettings() }},
		{name: "alt sınırlar geçerli",
			in: promqlConsoleSettings{TimeoutS: 5, MaxRangeH: 1, MinStepS: 1, MaxPointsPerSeries: 100,
				TargetPoints: 100, MaxSeries: 10, MaxBodyMiB: 1, PerUserConcurrency: 1, PerUserPerMin: 1, MetaPerUserPerMin: 1},
			check: func(c promqlConsoleSettings) bool {
				return c.TimeoutS == 5 && c.MaxRangeH == 1 && c.TargetPoints == 100
			}},
		{name: "üst sınırlar geçerli",
			in: promqlConsoleSettings{TimeoutS: 120, MaxRangeH: 720, MinStepS: 3600, MaxPointsPerSeries: 11000,
				TargetPoints: 11000, MaxSeries: 2000, MaxBodyMiB: 128, PerUserConcurrency: 10, PerUserPerMin: 600,
				MetaPerUserPerMin: 6000, PartialResponse: true},
			check: func(c promqlConsoleSettings) bool { return c.MaxSeries == 2000 && c.PartialResponse }},
		{name: "timeout 121 reddedilir", in: promqlConsoleSettings{TimeoutS: 121}, wantErr: []string{"timeoutS 5–120"}},
		{name: "negatif reddedilir (0 değil)", in: promqlConsoleSettings{MaxSeries: -1}, wantErr: []string{"maxSeries 10–2000"}},
		{name: "maxPointsPerSeries 11001 reddedilir", in: promqlConsoleSettings{MaxPointsPerSeries: 11001},
			wantErr: []string{"maxPointsPerSeries 100–11000"}},
		{name: "birden çok hata tek mesajda",
			in:      promqlConsoleSettings{TimeoutS: 1, MaxRangeH: 721, PerUserConcurrency: 11},
			wantErr: []string{"timeoutS", "maxRangeH 1–720", "perUserConcurrency 1–10"}},
		{name: "açık targetPoints tavanı aşamaz",
			in:      promqlConsoleSettings{MaxPointsPerSeries: 500, TargetPoints: 600},
			wantErr: []string{"targetPoints (600)", "maxPointsPerSeries (500)"}},
		{name: "varsayılan hedef + düşük tavan = kelepçe, ret değil",
			in:    promqlConsoleSettings{MaxPointsPerSeries: 500},
			check: func(c promqlConsoleSettings) bool { return c.TargetPoints == 500 && c.MaxPointsPerSeries == 500 }},
	}
	for _, c := range cases {
		got, err := validatePromQLConsoleSettings(c.in)
		if c.wantErr != nil {
			if err == nil {
				t.Errorf("%s: hata bekleniyordu, geçti: %+v", c.name, got)
				continue
			}
			for _, frag := range c.wantErr {
				if !strings.Contains(err.Error(), frag) {
					t.Errorf("%s: mesaj %q, %q içermeli", c.name, err, frag)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: beklenmeyen hata %v", c.name, err)
			continue
		}
		if c.check != nil && !c.check(got) {
			t.Errorf("%s: sonuç %+v", c.name, got)
		}
	}
}

func TestPromQLConsoleSettingsDecode(t *testing.T) {
	def := defaultPromQLConsoleSettings()
	cases := []struct {
		name string
		raw  string
		want func(promqlConsoleSettings) bool
	}{
		{"yok (nil)", "", func(c promqlConsoleSettings) bool { return c == def }},
		{"bozuk JSON → varsayılan", `{"timeoutS":`, func(c promqlConsoleSettings) bool { return c == def }},
		{"tip uyuşmazlığı → varsayılan", `{"timeoutS":"30s"}`, func(c promqlConsoleSettings) bool { return c == def }},
		{"kısmi blob: eksik alan varsayılan", `{"timeoutS":60,"partialResponse":true}`,
			func(c promqlConsoleSettings) bool {
				return c.TimeoutS == 60 && c.PartialResponse && c.MaxRangeH == 168 && c.MaxSeries == 500
			}},
		{"aralık dışı kayıtlı değer → varsayılan", `{"maxSeries":100000,"maxBodyMiB":0}`,
			func(c promqlConsoleSettings) bool { return c.MaxSeries == 500 && c.MaxBodyMiB == 32 }},
		{"bilinmeyen alan yok sayılır", `{"maxRangeH":24,"defaults":{},"bounds":{}}`,
			func(c promqlConsoleSettings) bool { return c.MaxRangeH == 24 }},
	}
	for _, c := range cases {
		var raw []byte
		if c.raw != "" {
			raw = []byte(c.raw)
		}
		if got := decodePromQLConsoleSettings(raw); !c.want(got) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// Canlı değiştirme: set → current; set normalize EDER (okuyucu aralık dışı görmez).
func TestPromQLConsoleSettingsLiveSwap(t *testing.T) {
	withPromQLConsoleSettings(t, promqlConsoleSettings{TimeoutS: 60, MaxSeries: 99999})
	got := currentPromQLConsoleSettings()
	if got.TimeoutS != 60 || got.MaxSeries != 500 || got.MaxRangeH != 168 {
		t.Fatalf("canlı değer %+v", got)
	}
}

// GET viewer-okunur (kapı kayıt satırında YOK) ve store'suz podda canlı
// değeri + varsayılanları + sınırları döner; ham hata yansıtılmaz.
func TestPromQLConsoleSettingsGETFallsBackToLive(t *testing.T) {
	withPromQLConsoleSettings(t, promqlConsoleSettings{TimeoutS: 45, PartialResponse: true})
	rec := httptest.NewRecorder()
	(&Server{}).getPromQLConsoleSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings/promql-console", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		promqlConsoleSettings
		Defaults promqlConsoleSettings         `json:"defaults"`
		Bounds   map[string]promqlSettingBound `json:"bounds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("gövde JSON değil: %v — %s", err, rec.Body)
	}
	if body.TimeoutS != 45 || !body.PartialResponse || body.MaxRangeH != 168 {
		t.Fatalf("düz alanlar canlı değeri taşımalı: %+v", body.promqlConsoleSettings)
	}
	if body.Defaults != defaultPromQLConsoleSettings() {
		t.Fatalf("defaults %+v", body.Defaults)
	}
	if b := body.Bounds["timeoutS"]; b.Min != 5 || b.Max != 120 {
		t.Fatalf("bounds.timeoutS %+v", b)
	}
	if b := body.Bounds["maxRangeH"]; b.Min != 1 || b.Max != 720 {
		t.Fatalf("bounds.maxRangeH %+v", b)
	}
	if len(body.Bounds) != len(promqlConsoleIntFields) {
		t.Fatalf("bounds %d alan, tablo %d", len(body.Bounds), len(promqlConsoleIntFields))
	}
}

// PUT doğrulaması store'a DOKUNMADAN önce: bozuk / aralık dışı → 400 JSON;
// geçerli gövde store'suz podda 503 (panik yok) ve canlı değer DEĞİŞMEZ.
func TestPromQLConsoleSettingsPUTValidation(t *testing.T) {
	withPromQLConsoleSettings(t, defaultPromQLConsoleSettings())
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/settings/promql-console", strings.NewReader(body))
		(&Server{}).putPromQLConsoleSettings(rec, req)
		return rec
	}
	cases := []struct {
		name string
		body string
		code int
		frag string
	}{
		{"bozuk JSON", `{"timeoutS":`, http.StatusBadRequest, "geçersiz JSON"},
		{"tip hatası", `{"maxRangeH":"7d"}`, http.StatusBadRequest, "geçersiz JSON"},
		{"aralık dışı", `{"timeoutS":600}`, http.StatusBadRequest, "timeoutS 5–120"},
		{"çapraz alan", `{"maxPointsPerSeries":500,"targetPoints":900}`, http.StatusBadRequest, "targetPoints"},
		{"geçerli ama store yok", `{"timeoutS":60}`, http.StatusServiceUnavailable, "ayar deposu"},
	}
	for _, c := range cases {
		rec := put(c.body)
		if rec.Code != c.code {
			t.Errorf("%s: status %d, beklenen %d — %s", c.name, rec.Code, c.code, rec.Body)
			continue
		}
		var e map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
			t.Errorf("%s: gövde JSON değil (writeJSONError kullanılmalı): %s", c.name, rec.Body)
			continue
		}
		if !strings.Contains(e["error"], c.frag) {
			t.Errorf("%s: error %q, %q içermeli", c.name, e["error"], c.frag)
		}
	}
	if got := currentPromQLConsoleSettings(); got.TimeoutS != 30 {
		t.Fatalf("başarısız PUT canlı değeri değiştirdi: %+v", got)
	}
}

// Peer sinyali store'suz podda (test, ya da CH'siz rol) paniklemeden döner.
func TestPromQLConsoleSettingsReloadSignalNilStore(t *testing.T) {
	withPromQLConsoleSettings(t, promqlConsoleSettings{TimeoutS: 77})
	(&Server{}).reloadConfigOnSignal(context.Background(), "promql_console")
	if got := currentPromQLConsoleSettings(); got.TimeoutS != 77 {
		t.Fatalf("store'suz reload son iyi değeri korumalı, %+v", got)
	}
}

// Kablo pini: publish ↔ reload case (TestEveryConfigReloadTopicHasAListener
// genel kapı; bu test konuya özgü) + config-import listesi + main.go boot +
// 30 s yenileme + PUT audit'i + api.go'ya satır girmedi.
func TestPromQLConsoleSettingsWiring(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return stripGoComments(string(b))
	}
	src := read("promql_console_settings.go")
	for _, want := range []string{
		`s.publishConfigReload(r.Context(), "promql_console")`,
		`s.audit(r, "settings.promql_console.update", "settings", promqlConsoleSettingKey, string(raw))`,
		`promqlConsoleSettingKey = "promql_console"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("promql_console_settings.go %q içermiyor", want)
		}
	}
	cache := read("cache.go")
	i := strings.Index(cache, "func (s *Server) reloadConfigOnSignal")
	if i < 0 {
		t.Fatal("reloadConfigOnSignal yok")
	}
	if !regexp.MustCompile(`case "promql_console":\s*s\.LoadPromQLConsoleSettings\(ctx\)`).MatchString(cache[i:]) {
		t.Error(`reloadConfigOnSignal: case "promql_console" → s.LoadPromQLConsoleSettings(ctx) yok`)
	}
	iox := read("config_iox.go")
	if !strings.Contains(iox, `"promql_console",`) {
		t.Error("config_iox.go import sonrası yeniden yayın listesinde promql_console yok")
	}
	mainGo := read("../../main.go")
	for _, want := range []string{
		"srv.LoadPromQLConsoleSettings(ctx)",
		"go srv.StartPromQLConsoleSettingsRefresh(ctx, 30*time.Second)",
	} {
		if !strings.Contains(mainGo, want) {
			t.Errorf("main.go %q içermiyor (boot hidrasyonu / 30 s yenileme)", want)
		}
	}
	if apiGo := read("api.go"); strings.Contains(apiGo, "promql-console") || strings.Contains(apiGo, "PromQLConsole") {
		t.Error("api.go PromQL konsolu içeriyor — api.go büyümez")
	}
}
