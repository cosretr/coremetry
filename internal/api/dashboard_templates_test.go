package api

// dashboard_templates_test.go — v0.10.1997 (Dashboards Faz 3 dilim 3.3):
// şablon kümesinin iç tutarlılığı + ön kontrol özeti + önbellek anahtarı.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDashboardTemplatesConsistent(t *testing.T) {
	tpls := dashboardTemplates()
	if len(tpls) != 4 {
		t.Fatalf("dört VM şablonu bekleniyor, %d", len(tpls))
	}
	seenTpl := map[string]bool{}
	for _, tp := range tpls {
		if seenTpl[tp.ID] {
			t.Errorf("çift şablon kimliği %s", tp.ID)
		}
		seenTpl[tp.ID] = true
		if len(tp.Panels) == 0 || len(tp.Required) == 0 {
			t.Errorf("%s: panel ve gereken metrik zorunlu", tp.ID)
		}
		ids := map[string]bool{}
		var all strings.Builder
		for _, p := range tp.Panels {
			if ids[p.ID] {
				t.Errorf("%s: çift panel kimliği %s", tp.ID, p.ID)
			}
			ids[p.ID] = true
			if p.Width < 1 || p.Width > 4 {
				t.Errorf("%s/%s: genişlik 1-4 olmalı", tp.ID, p.ID)
			}
			q, _ := p.Config["query"].(string)
			if p.Type == "promql" && q == "" {
				t.Errorf("%s/%s: boş sorgu", tp.ID, p.ID)
			}
			all.WriteString(q)
		}
		// Gereken her metrik en az bir sorguda geçmeli: ön kontrol yalnız
		// gerçekten kullanılan aileyi denetler.
		for _, m := range tp.Required {
			if !strings.Contains(all.String(), m) {
				t.Errorf("%s: gereken %s hiçbir sorguda yok", tp.ID, m)
			}
		}
		// ${service} kullanan şablon service değişkenini tanımlamalı.
		if strings.Contains(all.String(), "${service}") && (len(tp.Variables) == 0 || tp.Variables[0].Name != "service") {
			t.Errorf("%s: ${service} var ama değişken tanımlı değil", tp.ID)
		}
		d, err := templateDashboard(tp)
		if err != nil {
			t.Fatalf("%s: %v", tp.ID, err)
		}
		var panels []map[string]any
		if err := json.Unmarshal(d.Panels, &panels); err != nil || len(panels) != len(tp.Panels) {
			t.Errorf("%s: panel JSON bozuk: %v", tp.ID, err)
		}
		var tags []string
		_ = json.Unmarshal(d.Tags, &tags)
		if len(tags) == 0 || tags[0] != "şablon" {
			t.Errorf("%s: etiketler %v", tp.ID, tags)
		}
	}
}

func TestSummarizeTemplate(t *testing.T) {
	tp := dashTemplate{ID: "x", Required: []string{"a", "b"}, Panels: []tplPanel{{ID: "1"}}}
	cases := []struct {
		name    string
		exists  map[string]bool
		missing string
		checked bool
	}{
		{"hepsi var", map[string]bool{"a": true, "b": true}, "", true},
		{"biri eksik", map[string]bool{"a": true, "b": false}, "b", true},
		{"denetlenemedi", map[string]bool{"a": true}, "", false},
	}
	for _, c := range cases {
		got := summarizeTemplate(tp, c.exists)
		if strings.Join(got.Missing, ",") != c.missing || got.Checked != c.checked || got.PanelCount != 1 {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestDashboardTemplatesKeyCarriesBackend(t *testing.T) {
	if dashboardTemplatesKey("vm") == dashboardTemplatesKey("ch") {
		t.Fatal("anahtar backend'i taşımalı (v0.5.187 sınıfı)")
	}
}
