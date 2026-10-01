package templater

import (
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1023 — çıplak HTTP fiili kümesinin Go'daki tek yazımı
// chstore.BareHTTPMethods (Operations sekmesinin rota bölmesi + trace_health
// regex'i). Ingest'in op_group normalleştiricisi kendi httpMethods
// haritasını taşıyor; chstore templater'ı import edemediği (puller.go tersini
// yapıyor) için eşitlik buradan pinlenir. Operatör bildirimi: "Operation
// kısmında POST GET neden detail gözükmüyor, sonra trace'e girince çıkıyor."
func TestHTTPMethodsMatchChstoreBareList(t *testing.T) {
	var tmpl []string
	for m := range httpMethods {
		tmpl = append(tmpl, m)
	}
	ch := append([]string(nil), chstore.BareHTTPMethods...)
	sort.Strings(tmpl)
	sort.Strings(ch)
	if strings.Join(tmpl, ",") != strings.Join(ch, ",") {
		t.Errorf("templater.httpMethods %v ≠ chstore.BareHTTPMethods %v", tmpl, ch)
	}
}
