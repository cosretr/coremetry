package insight

import (
	"strings"
	"testing"
)

// v0.10.879 (inceleme) — kart satırı: host.name yedeği "host", node eki, ölçüm
// notları kuyrukta; notsuz satır aynen; "ölçülemedi" notlarıyla birlikte.
func TestPodConcentrationRowWordingAndNotes(t *testing.T) {
	v, sev, ok := podConcentrationRow(&PodConcentration{Kind: PodConcYogunlasma, TopPod: "ip-10-0-1-7", TopNode: "n1", TopHostOnly: true,
		TopOccurrences: 799, Attributed: 799, Share: 0.996, Instances: 3, Notes: []string{"tarama tavanı doldu, oran YAKLAŞIK"}})
	if !ok || sev != SevWarn || !strings.HasPrefix(v, "host ip-10-0-1-7 (node n1) · ") || !strings.HasSuffix(v, " · tarama tavanı doldu, oran YAKLAŞIK") || !strings.Contains(v, "%99") {
		t.Fatalf("yoğunlaşma satırı: %q", v)
	}
	v, _, _ = podConcentrationRow(&PodConcentration{Kind: PodConcYogunlasma, TopPod: "cart-7d9", TopOccurrences: 10, Attributed: 10, Share: 1, Instances: 2})
	if !strings.HasPrefix(v, "pod cart-7d9 · ") || strings.Contains(v, "node") {
		t.Fatalf("pod satırı: %q", v)
	}
	v, _, _ = podConcentrationRow(&PodConcentration{Kind: PodConcOlculemedi, Notes: []string{"pod şeması yok"}})
	if v != "ölçülemedi (dağılmış DEMEK DEĞİL) · pod şeması yok" {
		t.Fatalf("ölçülemedi satırı: %q", v)
	}
}
