// v0.9.1037 — /api/copilot/config yanıt ŞEKLİNİN kilidi.
//
// Bu sürüm yanıta `model` ekledi. Eklenen her alan bir SIZINTI SORUSU
// doğurur: "bunu kim görebiliyor ve yanına yanlışlıkla ne gelir?"
//
// İki iddia:
//
//  1. UÇ KİMLİK İSTER. auth.SkipPath bu yolu atlamıyor, yani anonim
//     yüzeyler (public trace snapshot'ı, public status) buradan hiçbir
//     şey okuyamaz — "model adı yalnız kimlikli yanıta girer" kuralı
//     handler'da bir dal DEĞİL, middleware'in sonucudur. SkipPath'e
//     ileride bu yolu eklemek kuralı sessizce bozardı; test onu durdurur.
//  2. YANIT DAR. Tip yalnız {enabled, model} taşır. baseUrl bir ADRES,
//     apiKey bir SIR, provider ise gereksiz — üçü de bu uçta işi yok.
//     Alan eklemek testi kırar ve karar bilinçli olmaya zorlanır.
package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
)

func TestCopilotConfigRequiresAuth(t *testing.T) {
	if auth.SkipPath(http.MethodGet, "/api/copilot/config") {
		t.Fatal("/api/copilot/config auth'u ATLIYOR — model adı anonim " +
			"yüzeylere (public trace / public status) sızar")
	}
	// Karşılaştırma tabanı: gerçekten public olan bir yolun true
	// döndüğünü de görelim, yoksa SkipPath her şeye false dönüyor
	// olabilir ve test yanlış sebeple yeşil kalırdı.
	if !auth.SkipPath(http.MethodGet, "/api/health") {
		t.Fatal("SkipPath /api/health için false döndü — test tabanı bozuk")
	}
}

func TestCopilotConfigResponseShape(t *testing.T) {
	raw, err := json.Marshal(copilotConfigResponse{Enabled: true, Model: "gemma4-26b-a4b-it"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{"enabled": true, "model": "gemma4-26b-a4b-it"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("yanıt şekli değişti\n got = %v\nwant = %v", got, want)
	}
	// Sır/adres sınıfı alan adları hiç doğmasın diye ayrıca yoklanıyor:
	// yukarıdaki DeepEqual zaten yakalar, ama hata mesajı NEDENİ söylesin.
	for _, forbidden := range []string{"baseUrl", "baseURL", "apiKey", "key", "provider", "host"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("%q /api/copilot/config yanıtına girmiş — bu uç kimlikli "+
				"ama yine de yalnız GÖSTERİM verisi taşır", forbidden)
		}
	}
	// v0.10.183 — profil listesi de SIRSIZ: yalnız id/label/model.
	raw2, _ := json.Marshal(copilotConfigResponse{Enabled: true, Model: "m", DefaultProfile: "a",
		Profiles: []copilotProfileOption{{ID: "a", Label: "Büyük", Model: "m"}, {ID: "b", Model: "q"}}})
	for _, forbidden := range []string{"baseUrl", "apiKey", "provider", "skipTls", "hasKey"} {
		if strings.Contains(string(raw2), forbidden) {
			t.Fatalf("profil listesi %q sızdırıyor: %s", forbidden, raw2)
		}
	}
}

// Model kapalı kurulumda hiç görünmemeli: "çip yok" hâli, boş string
// değil ALAN YOKLUĞU olarak ifade ediliyor (omitempty).
func TestCopilotConfigOmitsEmptyModel(t *testing.T) {
	raw, err := json.Marshal(copilotConfigResponse{Enabled: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"enabled":false}` {
		t.Fatalf("kapalı kurulum yanıtı = %s, want {\"enabled\":false}", raw)
	}
}

// v0.10.1128 — `wiki` bayrağı: CoSRE karşılamasındaki "wiki'de arayabilirim"
// ipucu. Yalnız boolean; false iken alan YOK (eski şekil bayt bayt), true
// iken yanında hiçbir wiki yapılandırma ayrıntısı (proje/mod/adres) yok.
func TestCopilotConfigWikiFlagShape(t *testing.T) {
	raw, _ := json.Marshal(copilotConfigResponse{Enabled: true})
	if string(raw) != `{"enabled":true}` {
		t.Fatalf("wiki=false iken şekil değişti: %s", raw)
	}
	raw, _ = json.Marshal(copilotConfigResponse{Enabled: true, Wiki: true})
	if string(raw) != `{"enabled":true,"wiki":true}` {
		t.Fatalf("wiki=true yanıtı = %s, want yalnız boolean", raw)
	}
}

func TestChatWikiAvailable(t *testing.T) {
	user := &auth.Claims{UserID: "u-1", Email: "dev@example.test", Role: "viewer"}
	token := &auth.Claims{UserID: "token:ci", Role: "viewer"}
	cases := []struct {
		name           string
		active, wikiOn bool
		c              *auth.Claims
		want           bool
	}{
		{"hepsi açık, oturum kullanıcısı", true, true, user, true},
		{"copilot kapalı", false, true, user, false},
		{"wiki kapalı", true, false, user, false},
		{"API token'ı", true, true, token, false},
		{"kimliksiz", true, true, nil, false},
	}
	for _, tc := range cases {
		if got := chatWikiAvailable(tc.active, tc.wikiOn, tc.c); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
