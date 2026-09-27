package api

// rollouts_flag_off_msg_test.go — v0.10.969 — bayrak kapalı mesajı var
// olmayan bir yeri gösteriyordu (docs/rollouts/v2-audit.md §2.6 madde 5):
// "Settings → Rollouts → Enable" — Ayarlar'da Rollouts sekmesi YOK. Bayrağı
// açmanın gerçek yolu /rollouts sayfasındaki admin "Etkinleştir" düğmesi
// (Rollouts.tsx: GET + {...settings, enabled:true} ile read-modify-write).
//
// PUT /api/settings/rollouts tam blobu DEĞİŞTİRİR (putRolloutSettings sıfır
// rollout.Settings'e decode eder, birleştirme yok): yalnız {"enabled":true}
// göndermek ayarlı interval/bucket/threshold/source/kinds… hepsini
// varsayılana döndürür ve 200 döner. Mesaj bu yıkıcı kısayolu önermemeli;
// bir API ipucu varsa GET → tam blob → PUT sırasını söylemeli.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRolloutFlagOffMessagePointsToRealEnablePath(t *testing.T) {
	w := httptest.NewRecorder()
	if (&Server{}).rolloutEnabled(w) {
		t.Fatal("rolloutCfg nil → bayrak kapalı sayılmalı")
	}
	// Cevap şekli değişmez: 404 {disabled:true, error:"…"}.
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 404 beklenir", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["disabled"] != true {
		t.Errorf("disabled = %v, true beklenir", body["disabled"])
	}
	if len(body) != 2 {
		t.Errorf("cevap alanları = %v; yalnız disabled + error beklenir", body)
	}
	msg, _ := body["error"].(string)
	if strings.Contains(msg, "Settings → Rollouts") {
		t.Errorf("mesaj var olmayan Ayarlar sekmesini gösteriyor: %q", msg)
	}
	for _, must := range []string{"/rollouts", "Etkinleştir"} {
		if !strings.Contains(msg, must) {
			t.Errorf("mesaj gerçek açma yolunu söylemiyor (%q eksik): %q", must, msg)
		}
	}
	// Kısmi gövdeli PUT önerisi ayarları sessizce sıfırlar.
	if strings.Contains(msg, `PUT /api/settings/rollouts {"enabled":true}`) {
		t.Errorf("mesaj tam-blob PUT'u kısmi gövdeyle öneriyor (öteki ayarları sıfırlar): %q", msg)
	}
	// API ipucu kalırsa read-modify-write olmalı: önce GET, sonra TAM blob.
	if strings.Contains(msg, "/api/settings/rollouts") {
		for _, must := range []string{"GET /api/settings/rollouts", "tam blob"} {
			if !strings.Contains(msg, must) {
				t.Errorf("API ipucu read-modify-write değil (%q eksik): %q", must, msg)
			}
		}
	}
}

// Aynı yanlış yol rolloutLayerNote'un "henüz koşmadı" notunda da vardı
// (§2.6: rollouts.go:128). Not store'dan beslendiği için birim testi
// yerine kaynak taraması: yorumlar hariç dosyada eski yol kalmamalı.
func TestRolloutsGoHasNoPhantomSettingsTab(t *testing.T) {
	raw, err := os.ReadFile("rollouts.go")
	if err != nil {
		t.Fatal(err)
	}
	if src := stripGoComments(string(raw)); strings.Contains(src, "Settings → Rollouts") {
		t.Error("rollouts.go hâlâ 'Settings → Rollouts' diyor — Ayarlar'da Rollouts sekmesi yok; açma yolu /rollouts sayfasındaki \"Etkinleştir\"")
	}
}
