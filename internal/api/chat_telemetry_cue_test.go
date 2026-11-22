package api

// chat_telemetry_cue_test.go — v0.10.2465 (operator-reported, prod 2026-11-22):
// telemetriyi açıkça soran soru ("son 1 saat … url'inde X içeren … çağrı /
// trace") yüklü envanterde eşleşse de kaynak sorusunu almaz; lookup router'da
// endpoint_lookup(url=X, range_s=pencere). Çıplak envanter terimi soruyu alır.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestExplicitTelemetryAsk(t *testing.T) {
	for _, c := range []struct {
		q    string
		want bool
	}{
		// operatörün sorusu (sentetik parça) + varyantlar
		{"Son 1 saat içerisinde url'inde c1ocp içeren bir servis çağrımı veya trace'i var mı?", true},
		{"son 30 dakikada endpoint'inde c1ocp geçen çağrı var mı", true},
		{"c1ocp içeren trace var mı", true},
		{"url'inde c1ocp içeren istek var mı", true},
		{"orders-api trace'lerinde hata var mı", true},
		{"son 2 saatte c1ocp", true},
		{"4bf92f3577b34da6a3ce929d0e0e4736 trace'i nerede", true},
		{"https://api.example.com/v1/orders çağrılıyor mu", true},
		{"orders-api p95 latency ne durumda", true},
		{"billing-api hata oranı", true},
		// belirsiz / başka kaynak: kaynak sorusu kalır
		{"c1ocp", false},
		{"c1ocp hangi ekipte", false},
		{"c1ocp sunucuları hangi ortamda", false},
		{"c1ocp url'i nedir", false}, // envanterde de URL sütunu olabilir
		{"envanterde c1ocp içeren url var mı", false},
		{"wikide trace nasıl açılır", false},
		{"trace nedir", false},
		{"opentelemetry trace nasıl eklenir", false},
		{"", false},
	} {
		if got := explicitTelemetryAsk(c.q); got != c.want {
			t.Errorf("explicitTelemetryAsk(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestLookupAskValueAndURLFragment(t *testing.T) {
	for _, c := range []struct {
		q, tool, key, val, rangeS string
	}{
		// çıplak değer → traces_search (cluster → env → namespace → url çözümü araçta)
		{"Son 1 saat içerisinde url'inde c1ocp içeren bir servis çağrımı veya trace'i var mı?", mcptools.TracesSearchName, "value", "c1ocp", "3600"},
		{"son 30 dakikada endpoint'inde c1ocp geçen çağrı var mı", mcptools.TracesSearchName, "value", "c1ocp", "1800"},
		{"c1ocp içeren trace var mı", mcptools.TracesSearchName, "value", "c1ocp", ""},
		{"c1ocp'te trace var mı", mcptools.TracesSearchName, "value", "c1ocp", ""},
		{"son 2 saatte c1 cluster'ında çağrı var mı", mcptools.TracesSearchName, "value", "c1", "7200"},
		// yol biçimli parça → endpoint_lookup url
		{"son 15 dk url'i /v1/orders içeren hatalı çağrılar", mcptools.EndpointLookupName, "url", "/v1/orders", "900"},
	} {
		a := lookupAskFor(c.q)
		if a.Tool != c.tool {
			t.Fatalf("%q → %q (%s), want %s", c.q, a.Tool, a.Why, c.tool)
		}
		if a.Args[c.key] != c.val || a.Args["range_s"] != c.rangeS {
			t.Errorf("%q args = %v, want %s=%q range_s=%q", c.q, a.Args, c.key, c.val, c.rangeS)
		}
	}
	// komşu: geçen hafta pencere ifadesi, parça değil; v0.10.2449 domain dalı aynen.
	if f := lookupURLFragment(guidedTokens(normalizeGuidedMsg("geçen hafta trace var mı"))); f != "" {
		t.Errorf("geçen hafta parça sayıldı: %q", f)
	}
	if a := lookupAskFor("api.example.com bu domainin çözdüğü podu soruyorum"); a.Tool != mcptools.EndpointLookupName || a.Args["domain"] != "api.example.com" || a.Args["url"] != "" {
		t.Errorf("domain dalı değişti: %+v", a)
	}
	if hint := lookupLoopPromptTR(lookupAskFor("c1ocp içeren trace var mı")); !strings.Contains(hint, "traces_search(value=c1ocp)") {
		t.Errorf("ipucu: %s", hint)
	}
}
