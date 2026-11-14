package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.809 — ürün yol tarifi tablosu + eşleme + bağlantı sözleşmesi.

var guidePages = map[string]bool{"/services": true, "/service": true, "/traces": true, "/trace": true, "/logs": true, "/inbox": true,
	"/problems": true, "/slos": true, "/service-map": true, "/explore": true, "/dashboards": true,
	"/alerts": true, "/settings/channels": true, "/clusters": true, "/rollouts": true,
	"/anomalies": true} // v0.10.1828 — problems konusu Anomalies'e bağlar (Incidents emekli)

func TestProductGuideTable(t *testing.T) {
	seen := map[string]bool{}
	for _, tp := range GuideTopics() {
		if tp.ID == "" || tp.Title == "" || tp.Ask == "" || len(tp.Keywords) == 0 || len(tp.Steps) < 2 || tp.Links == nil {
			t.Errorf("%s: eksik alan", tp.ID)
		}
		if seen[tp.ID] {
			t.Errorf("%s: id tekrar", tp.ID)
		}
		seen[tp.ID] = true
		if !strings.HasSuffix(tp.Ask, "?") {
			t.Errorf("%s: Ask soru olmalı: %q", tp.ID, tp.Ask)
		}
		for _, r := range tp.Related {
			if _, ok := guideTopicByID(r); !ok {
				t.Errorf("%s: related %q yok", tp.ID, r)
			}
		}
		for _, svc := range []string{"", "shop payment"} {
			for _, l := range tp.Links(svc, "abc123") {
				path := l.Href
				if i := strings.Index(path, "?"); i >= 0 {
					path = path[:i]
				}
				if !guidePages[path] {
					t.Errorf("%s: bilinmeyen sayfa %q", tp.ID, l.Href)
				}
				if svc != "" && strings.Contains(l.Href, "shop payment") {
					t.Errorf("%s: servis adı kaçışsız: %q", tp.ID, l.Href)
				}
			}
		}
	}
}

func TestMatchGuideTopics(t *testing.T) {
	cases := map[string]string{
		"shop servisinin hatalı trace'lerine nasıl ulaşırım?": "error_traces",
		"bir trace'in loglarını nereden görürüm?":             "trace_logs",
		"en yavaş istekleri nasıl bulurum, p99 nerede?":       "slow_traces",
		"SLO nasıl tanımlanır, bütçe nerede?":                 "slo",
		"pod restart sayısını nereden görürüm":                "pods",
		"sana nasıl soru sorabilirim":                         "ask_cosre",
	}
	for q, want := range cases {
		m := MatchGuideTopics(q)
		if len(m) == 0 || m[0].ID != want {
			got := "-"
			if len(m) > 0 {
				got = m[0].ID
			}
			t.Errorf("%q → %s, istenen %s", q, got, want)
		}
	}
	if MatchGuideTopics("lorem ipsum dolor") != nil || MatchGuideTopics("") != nil {
		t.Error("eşleşme yokken nil")
	}
}

func TestGuideForFillsPlaceholders(t *testing.T) {
	a, ok := GuideFor("", "hatalı trace'lere nasıl ulaşırım", "shop", "")
	if !ok || a.Topic != "error_traces" {
		t.Fatalf("%+v ok=%v", a, ok)
	}
	if !strings.Contains(a.Steps[0], "shop servisini seç") || strings.Contains(strings.Join(a.Steps, " "), "{svc}") {
		t.Errorf("yer tutucu dolmadı: %v", a.Steps)
	}
	b, ok := GuideFor("trace_logs", "", "", "")
	if !ok || !strings.Contains(b.Steps[0], "trace id") {
		t.Errorf("servissiz/trace'siz metin: %+v", b)
	}
	if _, ok := GuideFor("yok-boyle", "", "", ""); ok {
		t.Error("bilinmeyen topic id → ok=false")
	}
	txt := RenderGuideTR(a)
	if !strings.HasPrefix(txt, "NASIL: ") || !strings.Contains(txt, "1. ") || !strings.Contains(txt, "sayfa kendiliğinden değişmez") {
		t.Errorf("render: %q", txt)
	}
}

func TestProductGuideToolHandler(t *testing.T) {
	tool := productGuideTool(Deps{})
	if tool.Name != "product_guide" || tool.ShortDescription == "" {
		t.Fatal("tool kimliği")
	}
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"question":"sana nasıl soru sorabilirim"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["found"] != true || m["guide"].(GuideAnswer).Topic != "ask_cosre" {
		t.Errorf("%+v", m)
	}
	out, _ = tool.Handler(context.Background(), json.RawMessage(`{"question":"lorem"}`))
	m = out.(map[string]any)
	if m["found"] != false || len(m["topics"].([]map[string]string)) != len(productGuide) {
		t.Errorf("dizin: %+v", m)
	}
}

// v0.10.2140 — CoSRE router boşlukları D7: v2-yalnız konular. v1 tablosu,
// GuideFor ve MCP aracı bunları görmez.
func TestProductGuideV2Table(t *testing.T) {
	pages := map[string]bool{"/traces": true, "/trace": true, "/databases": true, "/databases/slow-queries": true}
	all := GuideTopicsV2()
	if len(all) != len(GuideTopics())+len(productGuideV2) || len(productGuideV2) != 3 {
		t.Fatalf("v2 tablosu = v1 ++ 3 konu olmalı (%d, %d)", len(all), len(productGuideV2))
	}
	ids := map[string]bool{}
	for _, tp := range all {
		if ids[tp.ID] {
			t.Errorf("%s: id tekrar", tp.ID)
		}
		ids[tp.ID] = true
	}
	for _, tp := range productGuideV2 {
		if tp.Title == "" || !strings.HasSuffix(tp.Ask, "?") || len(tp.Keywords) == 0 || len(tp.Steps) < 2 || tp.Links == nil {
			t.Errorf("%s: eksik alan", tp.ID)
		}
		if !IsGuideTopicV2Only(tp.ID) {
			t.Errorf("%s: v2-yalnız sayılmalı", tp.ID)
		}
		if _, ok := guideTopicByID(tp.ID); ok {
			t.Errorf("%s: v1 tablosuna sızdı", tp.ID)
		}
		if _, ok := GuideFor(tp.ID, "", "", ""); ok {
			t.Errorf("%s: v1 GuideFor görmemeli", tp.ID)
		}
		for _, r := range tp.Related {
			if !ids[r] {
				t.Errorf("%s: related %q yok", tp.ID, r)
			}
		}
		for _, l := range tp.Links("svc a", "abc123") {
			path := l.Href
			if i := strings.Index(path, "?"); i >= 0 {
				path = path[:i]
			}
			if !pages[path] {
				t.Errorf("%s: bilinmeyen sayfa %q", tp.ID, l.Href)
			}
		}
	}
	if IsGuideTopicV2Only("error_traces") {
		t.Error("v1 konusu v2-yalnız değil")
	}
}

func TestGuideForV2Matching(t *testing.T) {
	cases := map[string]string{
		"trace detaile hangi menüden gideceğim":       "trace_detail",
		"trace detay sayfasını açar mısın":            "trace_detail",
		"trace id'yi nereye yazacağım":                "trace_detail",
		"tablo insertlerini hangi adımda görebilirim": "db_spans",
		"öznitelikle trace nasıl ararım":              "attr_search",
		// v1 konuları v2'de de aynı konuya gider (ek anahtar sözcükler çalmaz)
		"shop servisinin hatalı trace'lerine nasıl ulaşırım?": "error_traces",
		"bir trace'in loglarını nereden görürüm?":             "trace_logs",
		"en yavaş istekleri nasıl bulurum, p99 nerede?":       "slow_traces",
		"pod restart sayısını nereden görürüm":                "pods",
	}
	for q, want := range cases {
		a, ok := GuideForV2("", q, "", "")
		if !ok || a.Topic != want {
			t.Errorf("%q → %q (ok=%v), istenen %s", q, a.Topic, ok, want)
		}
	}
	// Eşlik: v1'in kendi Ask metinleri v2'de de aynı konuya gider.
	for _, tp := range GuideTopics() {
		v1, _ := GuideFor("", tp.Ask, "", "")
		v2, _ := GuideForV2("", tp.Ask, "", "")
		if v1.Topic != v2.Topic {
			t.Errorf("%q: v1 %s ≠ v2 %s", tp.Ask, v1.Topic, v2.Topic)
		}
	}
	for _, tp := range productGuideV2 { // v2 çipleri kendi konusuna döner
		if a, _ := GuideForV2("", tp.Ask, "", ""); a.Topic != tp.ID {
			t.Errorf("%q → %s, istenen %s", tp.Ask, a.Topic, tp.ID)
		}
	}
	if !strings.Contains(GuideIndexV2TR(), "Trace detayına nasıl giderim?") || strings.Contains(GuideIndexTR(), "Trace detayına nasıl giderim?") {
		t.Error("dizin: yeni konular yalnız v2'de")
	}
}
