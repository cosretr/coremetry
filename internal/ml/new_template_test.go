package ml

// new_template_test.go — v0.10.2069: eski log_template_new kararının ML
// "ilk kez görüldü" kipi (new_template). Pinler: olay alanları + eski
// satırın kimliği, /anomalies'te kalıbın TEK satır görünmesi, ES isteği
// eklenmemesi (pod sorgusu, örnek satır, kaynak), Logs bağlantısı o kalıba,
// varsayılan açık + ayar listesi.

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func sampleNewTemplate() anomaly.LogTemplateAnomaly {
	return anomaly.LogTemplateAnomaly{
		TemplateID: "tpl-7", Template: "payment gateway rejected card for order <*>", Service: "orders-api",
		FirstSeenNs: time.Date(2026, 11, 12, 10, 1, 0, 0, time.UTC).UnixNano(),
		LastSeenNs:  time.Date(2026, 11, 12, 10, 4, 0, 0, time.UTC).UnixNano(),
		TotalCount:  37, Sample: "payment gateway rejected card for order 991",
	}
}

func TestNewTemplateMLEventFields(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	a := sampleNewTemplate()
	ev := newTemplateMLEvent(a, cfg, time.Date(2026, 11, 12, 10, 5, 0, 0, time.UTC))
	if ev.Detector != detNewTemplate || ev.Signal != chstore.MLSignalLog || ev.Service != "orders-api" || ev.Code != a.Template {
		t.Fatalf("olay alanları: %+v", ev)
	}
	if ev.ID != chstore.MLEventID(detNewTemplate, "orders-api", "newtpl|tpl-7") {
		t.Errorf("kimlik Drain kalıp kimliğine bağlı değil: %s", ev.ID)
	}
	if ev.StartedAt != a.FirstSeenNs || ev.LastSeen != a.LastSeenNs || ev.Observed != 37 || ev.Baseline != 0 || ev.Direction != "up" {
		t.Errorf("zaman/değer: %+v", ev)
	}
	if ev.Score < cfg.CriticalZ {
		t.Errorf("skor kritik z'nin altında (kalite süzgeci keser): %v", ev.Score)
	}
	m := evidenceMap(ev.Evidence)
	if evStr(m, "legacyId") != a.LegacyEventID() || a.LegacyEventID() == "" {
		t.Errorf("legacyId eski satır kimliği değil: %q", evStr(m, "legacyId"))
	}
	if q := evStr(m, "logQuery"); !strings.Contains(q, "payment gateway rejected card for order") {
		t.Errorf("logQuery kalıbın araması değil: %q", q)
	}
	// FirstSeen yoksa tik anı; LastSeen başlangıçtan geri gitmez.
	b := a
	b.FirstSeenNs, b.LastSeenNs = 0, 0
	now := time.Date(2026, 11, 12, 10, 5, 0, 0, time.UTC)
	if ev2 := newTemplateMLEvent(b, cfg, now); ev2.StartedAt != now.UnixNano() || ev2.LastSeen != now.UnixNano() {
		t.Errorf("boş zaman: %+v", ev2)
	}
}

func TestNewTemplateWindow(t *testing.T) {
	for _, c := range []struct {
		tick int
		want time.Duration
	}{{60, 10 * time.Minute}, {300, 10 * time.Minute}, {600, 20 * time.Minute}, {3600, 2 * time.Hour}} {
		if got := newTemplateWindow(c.tick); got != c.want {
			t.Errorf("tick %d s: pencere %v, beklenen %v", c.tick, got, c.want)
		}
	}
}

// TestBuildUnifiedSupersedesLegacyNewTemplate — aynı kalıp eski satır + ML
// olayı olarak gelir: liste eski satırı düşürür (tek satır). Farklı kalıbın
// eski satırı ve ML olayı yokken eski satır aynen kalır.
func TestBuildUnifiedSupersedesLegacyNewTemplate(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 10, 0, 0, time.UTC)
	cfg := chstore.DefaultMLConfig()
	a := sampleNewTemplate()
	ml := newTemplateMLEvent(a, cfg, now)
	same := chstore.AnomalyEvent{ID: a.LegacyEventID(), Kind: "log_template_new", Service: "orders-api", StartedAt: a.FirstSeenNs, LastSeen: a.LastSeenNs, Status: "active"}
	other := chstore.AnomalyEvent{ID: "ae-other", Kind: "log_template_new", Service: "billing-api", StartedAt: a.FirstSeenNs, LastSeen: a.LastSeenNs, Status: "active"}

	rows := BuildUnified(UnifiedInput{ML: []chstore.MLEvent{ml}, Legacy: []chstore.AnomalyEvent{same, other}, Now: now, Slack: 10 * time.Minute})
	if len(rows) != 2 {
		t.Fatalf("satır sayısı %d, beklenen 2 (ML + başka kalıbın eski satırı): %+v", len(rows), rows)
	}
	for _, r := range rows {
		for _, li := range r.Legacy {
			if li == 0 {
				t.Fatalf("aynı kalıbın eski satırı listede kaldı: %+v", r)
			}
		}
	}
	// ML olayı yok → eski satır görünür.
	rows = BuildUnified(UnifiedInput{Legacy: []chstore.AnomalyEvent{same}, Now: now, Slack: 10 * time.Minute})
	if len(rows) != 1 || len(rows[0].Legacy) != 1 {
		t.Fatalf("ML yokken eski satır kaybolmamalı: %+v", rows)
	}
	// Başka dedektörün legacyId taşıması bastırmaz (yalnız new_template).
	fake := ml
	fake.Detector = detRareTemplate
	rows = BuildUnified(UnifiedInput{ML: []chstore.MLEvent{fake}, Legacy: []chstore.AnomalyEvent{same}, Now: now, Slack: 10 * time.Minute})
	n := 0
	for _, r := range rows {
		n += len(r.Legacy)
	}
	if n != 1 {
		t.Fatalf("yalnız new_template bastırmalı: %+v", rows)
	}
}

// TestUnmatchedLogNamesNoExtraESQuery — pod sorgusu (ES) yalnız başka log
// olayı onu zaten açıyorsa new_template adlarını taşır.
func TestUnmatchedLogNamesNoExtraESQuery(t *testing.T) {
	known := map[string]bool{"orders-api": true}
	nt := chstore.MLEvent{Detector: detNewTemplate, Signal: chstore.MLSignalLog, Service: "Api"}
	vol := chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "Web"}
	cat := chstore.MLEvent{Detector: detNewTemplate, Signal: chstore.MLSignalLog, Service: "orders-api"}
	if got := unmatchedLogNames([]chstore.MLEvent{nt, cat}, known); got != nil {
		t.Fatalf("yalnız new_template varken pod sorgusu açılmamalı: %v", got)
	}
	got := unmatchedLogNames([]chstore.MLEvent{nt, vol, cat}, known)
	if strings.Join(got, ",") != "Web,Api" {
		t.Fatalf("sorgu zaten açıkken ad eklenmeli (önce asıl adlar): %v", got)
	}
	// new_template yokken eski davranış: sıralı, tekil.
	got = unmatchedLogNames([]chstore.MLEvent{vol, {Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "Api"}, vol}, known)
	if strings.Join(got, ",") != "Api,Web" {
		t.Fatalf("eski sıralı davranış: %v", got)
	}
}

// TestNewTemplateSamplesNoES — örnek satır olay kanıtından: depo/ES
// çağrılmaz (Store nil olsa da paniklemez), ES bütçesi harcanmaz.
func TestNewTemplateSamplesNoES(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	ev := newTemplateMLEvent(sampleNewTemplate(), cfg, time.Now())
	raw := collectSamples(context.Background(), Deps{}, ev, cfg, true)
	var out []chstore.MLSample
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) != 1 || out[0].Kind != "log" || !strings.Contains(out[0].Body, "order 991") {
		t.Fatalf("örnek: %s (%v)", raw, err)
	}
	if eventPart(ev) != "" {
		t.Errorf("new_template ml_series serisi okumamalı: %q", eventPart(ev))
	}
}

// TestNewTemplateSourceHasNoES — kaynak pini: dedektör ES'e gitmez.
func TestNewTemplateSourceHasNoES(t *testing.T) {
	src, err := os.ReadFile("new_template.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, bad := range []string{"deps.Logs", "ObserveES", "ESLeft", "MLTemplateCounts"} {
		if strings.Contains(body, bad) {
			t.Errorf("new_template.go ES yolu içeriyor: %s", bad)
		}
	}
	if !strings.Contains(body, "anomaly.NewLogTemplateFamilies(") {
		t.Error("karar eski dedektörün ortak gövdesinden gelmiyor")
	}
}

// TestNewTemplateLogsLinkAndSentence — Logs bağlantısı (ve ondan türeyen
// canlı log grafiği) o kalıbın aramasını taşır; özet cümle kalıbı adıyla söyler.
func TestNewTemplateLogsLinkAndSentence(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	now := time.Date(2026, 11, 12, 10, 10, 0, 0, time.UTC)
	ev := newTemplateMLEvent(sampleNewTemplate(), cfg, now)
	in := CardInputFromEvent(ev, now, time.UTC)
	card := BuildExplainCard(in)
	var href string
	for _, l := range card.WhereToLook {
		if strings.HasPrefix(l.Href, "/logs?") {
			href = l.Href
			break
		}
	}
	u, err := url.Parse(href)
	if err != nil || href == "" {
		t.Fatalf("Logs bağlantısı yok: %+v", card.WhereToLook)
	}
	if u.Query().Get("service") != "orders-api" || !strings.Contains(u.Query().Get("q"), "payment gateway rejected card for order") {
		t.Errorf("Logs bağlantısı kalıba gitmiyor: %s", href)
	}
	s := ExplainSummary(SummaryInput(ev, cfg, now, time.UTC))
	if !strings.Contains(s, "ilk kez görülen") || !strings.Contains(s, "payment gateway rejected card") {
		t.Errorf("özet cümle: %q", s)
	}
	t.Logf("özet: %s", s)
}

func TestNewTemplateDefaultOnAndListed(t *testing.T) {
	if !chstore.MLDetectorDefaultOn(detNewTemplate) || !chstore.DefaultMLConfig().Detectors[detNewTemplate] {
		t.Fatal("new_template varsayılan açık olmalı")
	}
	if chstore.MLDetectorSignal(detNewTemplate) != chstore.MLSignalLog || UnifiedSignalOf(detNewTemplate, "") != chstore.MLSignalLog {
		t.Fatal("new_template log sinyali olmalı")
	}
	// Kayıtlı blobda false → korunur.
	c := chstore.DefaultMLConfig()
	c.Detectors[detNewTemplate] = false
	if chstore.NormalizeMLConfig(c).Detectors[detNewTemplate] {
		t.Fatal("operatörün kapattığı değer korunmalı")
	}
	fe, err := os.ReadFile("../../frontend/src/pages/settings/MLSubsection.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fe), "id: 'new_template'") {
		t.Fatal("Settings › ML dedektör listesinde yok")
	}
}
