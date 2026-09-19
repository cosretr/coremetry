package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.809 — ürün yol tarifi tablosu + eşleme + bağlantı sözleşmesi.

var guidePages = map[string]bool{"/services": true, "/service": true, "/traces": true, "/trace": true, "/logs": true, "/inbox": true,
	"/problems": true, "/incidents": true, "/slos": true, "/service-map": true, "/explore": true, "/dashboards": true,
	"/alerts": true, "/settings/channels": true, "/clusters": true, "/rollouts": true}

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
