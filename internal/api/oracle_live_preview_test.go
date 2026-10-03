package api

// v0.10.998 — Oracle canlıya geçiş önizlemesi (kaynak kipi gölge → canlı).
// Cevabın kuruluşu saf ve tablo testli; uç yalnız admin, bilinmeyen kaynak 404.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func TestBuildOracleLivePreview(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ch := func(name string, enabled bool, kinds ...string) chstore.NotificationChannel {
		return chstore.NotificationChannel{Name: name, Enabled: enabled, MatchRules: chstore.ChannelMatchRules{Kinds: kinds}}
	}
	channels := []chstore.NotificationChannel{
		ch("hepsi", true),                     // süzgeçsiz kanal: problem + anomali + incident
		ch("yalniz-problem", true, "problem"), // anomali tiki kaldırılmış → ALMAZ
		ch("anomali-acik", true, "anomaly", "problem"),
		ch("kapali", false), // etkin değil → sayılmaz
		ch("yalniz-exception", true, "exception"),
	}
	stats := chstore.ExternalProblemStats{Opened24h: 37, Opened7d: 212, Critical7d: 40, Clusters7d: 3, OpenNow: 5,
		Top: []chstore.ExternalProblemTop{{Subject: "acme-payments", Opened: 61}}}
	src := oracle.SourceConfig{ID: "s1", Name: "core-oracle"} // kip boş → shadow

	p := buildOracleLivePreview(src, stats, channels, chstore.TeamContacts{Enabled: true, Contacts: map[string]string{"sre": "a@example.test"}}, 20, now)
	if p.Mode != oracle.ProblemModeShadow || p.SourceID != "s1" || p.SourceName != "core-oracle" {
		t.Errorf("kaynak / kip: %+v", p)
	}
	// Dış seri Problem'i bildirimde ANOMALİ türüdür; önizleme türü sınıflayıcıdan alır.
	if p.NotifyKind != chstore.NotifyKindAnomaly {
		t.Errorf("tür %q, anomali beklenir", p.NotifyKind)
	}
	if p.ChannelsEnabled != 4 || p.ChannelsAccepting != 2 || strings.Join(p.ChannelNames, ",") != "hepsi,anomali-acik" {
		t.Errorf("kanallar: etkin=%d alan=%d adlar=%v", p.ChannelsEnabled, p.ChannelsAccepting, p.ChannelNames)
	}
	if !p.TeamMail || p.OpenCapPerTick != 20 || p.Opened24h != 37 || p.Opened7d != 212 || p.OpenNow != 5 || len(p.Top) != 1 {
		t.Errorf("sayım / ekip maili: %+v", p)
	}
	if p.GeneratedAt != now.UnixMilli() {
		t.Errorf("üretim anı: %d", p.GeneratedAt)
	}

	// Ekip maili: kapalı, alıcısız ya da türü süzen → false.
	for name, tc := range map[string]chstore.TeamContacts{
		"kapalı":         {Enabled: false, Contacts: map[string]string{"sre": "a@example.test"}},
		"alıcı yok":      {Enabled: true},
		"anomali kapalı": {Enabled: true, Contacts: map[string]string{"sre": "a@example.test"}, Kinds: []string{"problem"}},
	} {
		if buildOracleLivePreview(src, stats, nil, tc, 20, now).TeamMail {
			t.Errorf("ekip maili %s iken true olmamalı", name)
		}
	}

	// Kip normalleşir; boş sayım JSON'da null değil [] üretir; ad tavanı 10.
	var many []chstore.NotificationChannel
	for i := 0; i < 14; i++ {
		many = append(many, ch("k", true))
	}
	live := buildOracleLivePreview(oracle.SourceConfig{ID: "s2", Name: "x", ProblemMode: "LIVE"}, chstore.ExternalProblemStats{}, many, chstore.TeamContacts{}, 0, now)
	if live.Mode != oracle.ProblemModeLive || live.Top == nil || live.ChannelNames == nil ||
		live.ChannelsAccepting != 14 || len(live.ChannelNames) != oracleLivePreviewChannelNames {
		t.Errorf("canlı / boş / tavan: %+v", live)
	}
}

func TestOracleLivePreviewRouteGates(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	s.oracle = oracle.New()
	mux := http.NewServeMux()
	s.registerOracleLivePreviewRoutes(mux)
	get := func(path, role string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, asRole(httptest.NewRequest("GET", path, nil), role))
		return w.Code
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if code := get("/api/settings/oracle/s1/live-preview", role); code != http.StatusForbidden {
			t.Errorf("rol %s → %d, 403 beklenir (kaynak adı + kanal adları admin bilgisidir)", role, code)
		}
	}
	// Depo bağlı değil → 503 (sıfır değerli yanıt üretilmez).
	if code := get("/api/settings/oracle/s1/live-preview", auth.RoleAdmin); code != http.StatusServiceUnavailable {
		t.Errorf("depo yokken %d, 503 beklenir", code)
	}
}

// Kaynak pini: okuma sınırlı (FINAL + max_execution_time + LIMIT), uç
// önbellekli ve kendi dosyasında kayıtlı (api.go büyümez).
func TestOracleLivePreviewSourcePins(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	st := read("../chstore/external_problem_stats.go")
	if strings.Count(st, "FROM problems FINAL") != 2 || strings.Count(st, "SETTINGS max_execution_time = 5") != 2 ||
		!strings.Contains(st, "LIMIT ?") || !strings.Contains(st, "toUnixTimestamp64Nano(started_at) >= ?") {
		t.Error("ExternalSourceProblemStats: iki okuma da FINAL + max_execution_time + zaman sınırı, üst liste LIMIT taşımalı")
	}
	h := read("oracle_live_preview.go")
	for _, w := range []string{
		`registerRoutesExtra("oracle-live-preview"`,
		`auth.RequireRole(auth.RoleAdmin, s.getOracleLivePreview)`,
		"s.serveCached(w, r, key, 60*time.Second",
		`fmt.Sprintf("oracle-live-preview:id=%s:name=%s:mode=%s", src.ID, src.Name, oracle.ProblemModeOf(*src))`,
	} {
		if !strings.Contains(h, w) {
			t.Errorf("oracle_live_preview.go %q taşımalı", w)
		}
	}
}
