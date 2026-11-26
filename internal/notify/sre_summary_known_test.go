package notify

import (
	"strings"
	"testing"
)

// v0.10.2477 — PIN: platformun bildiği (Problem grafı kökeni) yalnız mevcut
// "Kanıt:" satırı biçiminde görünür; başlık / neden / kör nokta / rapor
// satırları ve sıraları aynen (Zoom özeti bayt biçimi sabit).
func TestSREBlockLinesKnownEvidencePinned(t *testing.T) {
	d := sufficientDigest()
	d.TopKind, d.TopLabel = "dependency", "Bağımlılık arızası"
	d.Evidence = []DigestEvidence{{Check: "graph_origin_elsewhere", Source: "graph_origin"}, {Check: "downstream_worse", Source: "deps"}}
	tr := strings.Join(sreBlockLines(d, true, "https://coremetry.example/sre/report/sre-p-1"), "\n")
	wantTR := strings.Join([]string{
		"CoSRE araştırması",
		"En olası neden: Bağımlılık arızası",
		"Kanıt: Problem grafı: köken başka varlık (Problem grafı kökeni)",
		"Kanıt: Aşağı akış kötüleşti (Bağımlılık)",
		"Kör noktalar: Trace pivotu (zaman aşımı)",
		"Rapor: https://coremetry.example/sre/report/sre-p-1",
	}, "\n")
	if tr != wantTR {
		t.Fatalf("TR blok:\n%s\nbeklenen:\n%s", tr, wantTR)
	}
	en := sreBlockLines(d, false, "")
	if en[2] != "Evidence: Problem graph: origin is another entity (Problem graph origin)" {
		t.Fatalf("EN kanıt satırı: %q", en[2])
	}
	for _, k := range []string{"graph_origin_rollout", "graph_origin_elsewhere", "graph_origin_external", "graph_origin_db"} {
		if _, ok := checkNames[k]; !ok {
			t.Fatalf("%s kontrol adının TR/EN metni yok", k)
		}
	}
}
