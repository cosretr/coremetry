package api

// activity_meter_test.go — v0.10.1902 (login özeti aktivite sayaçları).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// strictMetricSink — gerçek yazma katmanının kurallarını uygular: bilinmeyen
// seri adı, kapalı küme dışı tür, dakikaya hizasız zaman, etiket/değer sayısı
// uyumsuzluğu ve sıfır değer REDDEDİLİR (her şeyi kabul eden sahte, bu
// sınıf hatayı saklardı).
type strictMetricSink struct {
	got []*chstore.MetricPoint
	err error
}

func (f *strictMetricSink) InsertMetrics(_ context.Context, pts []*chstore.MetricPoint) error {
	if f.err != nil {
		return f.err
	}
	for _, p := range pts {
		if p.Metric != chstore.SelfActivityMetric {
			return fmt.Errorf("bilinmeyen seri %q", p.Metric)
		}
		if len(p.AttrKeys) != len(p.AttrValues) || len(p.AttrKeys) != 2 {
			return fmt.Errorf("etiket uyumsuz: %v %v", p.AttrKeys, p.AttrValues)
		}
		if k := p.AttrValues[0]; k != chstore.ActivityKindLogin && k != chstore.ActivityKindTraceView {
			return fmt.Errorf("kapalı küme dışı tür %q", k)
		}
		if !p.Time.Equal(p.Time.Truncate(time.Minute)) || p.Value <= 0 || p.SeriesFingerprint == 0 {
			return fmt.Errorf("geçersiz nokta: %+v", p)
		}
		f.got = append(f.got, p)
	}
	return nil
}

func reqWithUser(uid string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/traces/abc", nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if uid != "" {
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: uid, Email: "ops@acme.test", Role: "viewer"}))
	}
	return r
}

func TestTraceViewDedupWindow(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		at   time.Duration
		key  string
		want bool
	}{
		{"ilk görüş", 0, "u1\x00t1", true},
		{"pencere içinde aynı çift", 9*time.Minute + 59*time.Second, "u1\x00t1", false},
		{"başka kullanıcı", time.Minute, "u2\x00t1", true},
		{"başka trace", time.Minute, "u1\x00t2", true},
		{"pencere dolunca yeniden", 10 * time.Minute, "u1\x00t1", true},
	}
	d := newTraceViewDedup(traceViewDedupWindow, traceViewDedupMax)
	for _, c := range cases {
		if got := d.firstSeen(c.key, t0.Add(c.at)); got != c.want {
			t.Errorf("%s: firstSeen=%v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestTraceViewDedupCap(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	d := newTraceViewDedup(traceViewDedupWindow, traceViewDedupMax)
	for i := 0; i < traceViewDedupMax+500; i++ {
		d.firstSeen(fmt.Sprintf("u\x00t%d", i), t0.Add(time.Duration(i)*time.Millisecond))
	}
	if n := d.size(); n != traceViewDedupMax {
		t.Fatalf("tablo boyu %d, beklenen tam tavan %d", n, traceViewDedupMax)
	}
	// En eski 500 girdi atıldı: yeniden görülünce sayılır; en yeni hâlâ bastırılır.
	if !d.firstSeen("u\x00t0", t0.Add(time.Second)) {
		t.Error("atılan en eski girdi yeniden sayılmalı")
	}
	if d.firstSeen(fmt.Sprintf("u\x00t%d", traceViewDedupMax+499), t0.Add(time.Second)) {
		t.Error("tavandaki en yeni girdi bastırılmalı")
	}
	if n := d.size(); n > traceViewDedupMax {
		t.Fatalf("tavan aşıldı: %d", n)
	}
}

func TestRecordTraceView(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 30, 0, time.UTC)
	cases := []struct {
		name    string
		uid     string
		hdr     map[string]string
		traceID string
		want    bool
	}{
		{"normal açılış sayılır", "u1", nil, "t1", true},
		{"prefetch başlığı sayılmaz", "u1", map[string]string{purposeHeader: "prefetch"}, "t9", false},
		{"başlık büyük/küçük harf duyarsız", "u1", map[string]string{purposeHeader: " Prefetch "}, "t8", false},
		{"başka amaç değeri sayılır", "u1", map[string]string{purposeHeader: "other"}, "t7", true},
		{"boş trace id sayılmaz", "u1", nil, "", false},
		{"tekrar (aynı kullanıcı+trace) sayılmaz", "u1", nil, "t1", false},
		{"başka kullanıcı sayılır", "u2", nil, "t1", true},
		{"kimliksiz istek anon anahtarıyla bir kez", "", nil, "t1", true},
		{"kimliksiz tekrar sayılmaz", "", nil, "t1", false},
	}
	c := newActivityCounter()
	d := newTraceViewDedup(traceViewDedupWindow, traceViewDedupMax)
	wantTotal := 0
	for _, tc := range cases {
		if got := recordTraceView(c, d, reqWithUser(tc.uid, tc.hdr), tc.traceID, now); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
		if tc.want {
			wantTotal++
		}
	}
	cells := c.drain(now.Add(2 * time.Minute))
	var total uint64
	for _, cell := range cells {
		if cell.Kind != chstore.ActivityKindTraceView {
			t.Errorf("beklenmeyen tür %q", cell.Kind)
		}
		total += cell.Count
	}
	if int(total) != wantTotal {
		t.Errorf("sayaç %d, beklenen %d", total, wantTotal)
	}
}

func TestActivityCounterDrainKeepsCurrentMinute(t *testing.T) {
	c := newActivityCounter()
	m0 := time.Date(2026, 11, 6, 12, 0, 10, 0, time.UTC)
	c.add(m0, chstore.ActivityKindLogin)
	c.add(m0.Add(5*time.Second), chstore.ActivityKindLogin)
	c.add(m0.Add(time.Minute), chstore.ActivityKindTraceView) // 12:01
	// 12:01:20'de yalnız 12:00 dakikası tamamdır.
	got := c.drain(m0.Add(70 * time.Second))
	if len(got) != 1 || got[0].Count != 2 || got[0].Kind != chstore.ActivityKindLogin || !got[0].Minute.Equal(m0.Truncate(time.Minute)) {
		t.Fatalf("beklenmeyen boşaltma: %+v", got)
	}
	if again := c.drain(m0.Add(70 * time.Second)); len(again) != 0 {
		t.Fatalf("boşaltılan hücre geri geldi: %+v", again)
	}
	if rest := c.drain(m0.Add(3 * time.Minute)); len(rest) != 1 || rest[0].Kind != chstore.ActivityKindTraceView {
		t.Fatalf("içinde bulunulan dakika kaybolmuş: %+v", rest)
	}
}

func TestActivityCounterBoundsMinutes(t *testing.T) {
	c := newActivityCounter()
	t0 := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	for i := 0; i < activityMaxMinutes+50; i++ {
		c.add(t0.Add(time.Duration(i)*time.Minute), chstore.ActivityKindLogin)
		c.add(t0.Add(time.Duration(i)*time.Minute), chstore.ActivityKindTraceView)
	}
	mins := map[int64]bool{}
	c.mu.Lock()
	for k := range c.cells {
		mins[k.minute] = true
	}
	c.mu.Unlock()
	if len(mins) > activityMaxMinutes {
		t.Fatalf("yazıcı koşmazken dakika sayısı %d > %d", len(mins), activityMaxMinutes)
	}
}

func TestFlushActivityStats(t *testing.T) {
	c := newActivityCounter()
	t0 := time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC)
	c.add(t0, chstore.ActivityKindLogin)
	c.add(t0, chstore.ActivityKindLogin)
	c.add(t0, chstore.ActivityKindTraceView)
	now := t0.Add(90 * time.Second)

	sink := &strictMetricSink{}
	if err := flushActivityStats(context.Background(), sink, c, "coremetry-api-0", now); err != nil {
		t.Fatalf("yazma hatası: %v", err)
	}
	vals := map[string]float64{}
	for _, p := range sink.got {
		vals[p.AttrValues[0]] = p.Value
		if p.HostName != "coremetry-api-0" || p.AttrValues[1] != "coremetry-api-0" {
			t.Errorf("pod etiketi yanlış: %+v", p)
		}
	}
	if vals[chstore.ActivityKindLogin] != 2 || vals[chstore.ActivityKindTraceView] != 1 {
		t.Fatalf("değerler yanlış: %v", vals)
	}

	// Yazma hatası: hücreler ATILIR (geri birikmez), hata dönülür.
	c.add(t0, chstore.ActivityKindLogin)
	failing := &strictMetricSink{err: errors.New("ch down")}
	if err := flushActivityStats(context.Background(), failing, c, "p", now); err == nil {
		t.Fatal("hata yutuldu")
	}
	if left := c.drain(now); len(left) != 0 {
		t.Fatalf("başarısız yazımdan sonra hücre birikti: %+v", left)
	}
	// Boş sayaç: yazıcıya hiç gitmez.
	if err := flushActivityStats(context.Background(), &strictMetricSink{err: errors.New("çağrılmamalı")}, c, "p", now); err != nil {
		t.Fatalf("boş boşaltmada yazıcı çağrıldı: %v", err)
	}
}

// noteLogin store'suz da sayar (store nil → damga atlanır, sayaç artar).
func TestNoteLoginCountsWithoutStore(t *testing.T) {
	s := &Server{}
	before := time.Now()
	s.noteLogin(httptest.NewRequest(http.MethodPost, "/api/auth/login", nil), "u1", "ops@acme.test", "auth")
	cells := activityStats.drain(before.Add(3 * time.Minute))
	var logins uint64
	for _, c := range cells {
		if c.Kind == chstore.ActivityKindLogin {
			logins += c.Count
		}
	}
	if logins < 1 {
		t.Fatalf("giriş sayılmadı: %+v", cells)
	}
}

// Çağrı noktaları çivili: getTrace sayar, iki giriş yolu noteLogin kullanır,
// CORS ön-yükleme başlığını izinli listeler.
func TestActivityCallSitesPinned(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if !strings.Contains(read("trace_routes.go"), "s.noteTraceView(r, id)") {
		t.Error("getTrace noteTraceView çağırmıyor")
	}
	api := read("api.go")
	if strings.Count(api, "s.noteLogin(r, user.ID, user.Email,") != 2 {
		t.Error("iki giriş yolu da s.noteLogin kullanmalı")
	}
	if strings.Contains(api, "TouchUserLogin(") {
		t.Error("api.go doğrudan TouchUserLogin çağırmamalı (noteLogin sarar)")
	}
	if !strings.Contains(read("cors.go"), "X-Coremetry-Purpose") {
		t.Error("CORS izinli başlık listesinde X-Coremetry-Purpose yok")
	}
}
