package api

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.1128 — /api/copilot/config handler'ı ve dar yanıt tipleri api.go'dan
// buraya taşındı (api.go büyümez — /api-route). Gövde aynen; tek ek `wiki`
// bayrağı (CoSRE karşılamasındaki yetenek ipucu).

// copilotConfig surfaces whether the feature is enabled — UI uses this
// to show or hide the "AI explain" buttons. Doesn't leak the key.
// wf — Active() (not Configured()): the AI affordances hide when the
// operator flips the "Enable AI Copilot" toggle off even though the
// creds are still stored. Active() nil-guards internally (s.copilot is
// nil when no key was ever configured), so no separate nil check.
//
// v0.9.1037 — yanıt `model` alanını da taşıyor (AI çekmecesinin model
// çipi). ÜÇ sınır, üçü de kasıtlı:
//
//   - Uç KİMLİK İSTER: auth.SkipPath("/api/copilot/config") false'tur,
//     yani anonim bir çağıran (public trace / status sayfaları) buradan
//     hiçbir şey okuyamaz. Model adı bu yüzden "yalnız kimlikli yanıt"
//     kuralını otomatik sağlar; copilotConfigPublicShapeTest bunu pinler.
//   - Model YALNIZ Active() iken sızar (copilot.ActiveModel) — kapalı
//     kurulum boş döner ve çip hiç çizilmez.
//   - baseURL / apiKey / provider ASLA girmez. Model adı operatörün
//     Helm values'ında duran bir tanımlayıcı; baseURL bir ADRES,
//     apiKey bir SIR. Yanıt tipi bu üçünü taşıyamayacak şekilde DAR
//     yazıldı (getAISettings admin yüzeyi ayrı ve öyle kalıyor).
//
// copilotProfileOption — v0.10.183: sohbet seçicisi için SIRSIZ görünüm
// (kimlik + etiket + model adı); baseUrl/apiKey/provider ASLA (şekil testi).
type copilotProfileOption struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`
	// v0.10.1138 — seçici menüsündeki kısa açıklama ("Hızlı", "Derin"); sır değil.
	Description string `json:"description,omitempty"`
}

// copilotProfileOptions — v0.10.1138, SAF: çağıranın rolünün AÇIKÇA
// seçebileceği profiller (rol allowlist'i boş = herkes). Liste yalnız >1
// seçenek varsa döner: tek seçenekte seçici anlamsız (eski şekil korunur).
// Varsayılan profil listede olmasa da seçimsiz istek onunla koşar — allowlist
// yalnız açık seçimi kısıtlar (sunucu kapısı chatProfileGate ile aynı kural).
func copilotProfileOptions(profiles []copilot.ModelProfile, role string) []copilotProfileOption {
	var out []copilotProfileOption
	for _, p := range profiles {
		if !copilot.ProfileRoleAllowed(p, role) {
			continue
		}
		out = append(out, copilotProfileOption{ID: p.ID, Label: p.Label, Model: p.Model, Description: p.Description})
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

type copilotConfigResponse struct {
	Enabled bool `json:"enabled"`
	// omitempty: kapalı/modelsiz kurulumda alan hiç görünmez, yani
	// istemci "boş string mi yoksa yok mu" ayrımını yapmak zorunda
	// kalmaz — çip yoksa alan da yok.
	Model string `json:"model,omitempty"`
	// v0.10.183 — birden çok profil varsa seçici için liste + varsayılan; tek
	// profilde alanlar hiç görünmez (eski şekil aynen).
	Profiles       []copilotProfileOption `json:"profiles,omitempty"`
	DefaultProfile string                 `json:"defaultProfile,omitempty"`
	// v0.10.1128 — sohbet BU çağırana wiki'den cevap verebilir mi (yalnız
	// boolean: proje/wiki adı, mod, DevOps adresi ASLA). Kapı sohbetteki
	// kapıyla aynı (chatWikiAvailable); false iken alan hiç görünmez.
	Wiki bool `json:"wiki,omitempty"`
	// v0.10.1451 (karar 8, R1) — kod okuma durumu: yalnız DevOps bağlıyken
	// döner. available=false iken reason ("kapalı: yerel profil yok");
	// profileLabel kod/wiki turlarının gideceği yerel profilin ETİKETİ (adres yok).
	CodeRead *copilotCodeRead `json:"codeRead,omitempty"`
	// v0.10.1592 — CoSRE v2 admin bayrağı (cosre_v2_flag.go): istemcinin v2
	// lazy chunk'ını açar. false iken alan hiç görünmez (eski şekil aynen).
	CosreV2 bool `json:"cosreV2,omitempty"`
}

type copilotCodeRead struct {
	Available    bool   `json:"available"`
	Reason       string `json:"reason,omitempty"`
	ProfileLabel string `json:"profileLabel,omitempty"`
}

// copilotCodeReadFor — v0.10.1451, SAF: DevOps bağlı değilse nil (alan yok);
// yerel profil yoksa kapalı + gerekçe.
func copilotCodeReadFor(devopsConfigured bool, localID, localLabel string) *copilotCodeRead {
	if !devopsConfigured {
		return nil
	}
	if localID == "" {
		return &copilotCodeRead{Reason: localOnlyClosedReason}
	}
	if localLabel == "" {
		localLabel = localID
	}
	return &copilotCodeRead{Available: true, ProfileLabel: localLabel}
}

// chatWikiAvailable — SAF kapı: sohbetin wiki kademesi/araçları bu çağırana
// açılır mı? Copilot aktif + wiki bilgisi açık (DevOps bağlı) + oturum
// kullanıcısı (API token'ı değil) — wikiChatAnswer / ragWikiHits ile aynı üçlü.
func chatWikiAvailable(copilotActive, wikiEnabled bool, c *auth.Claims) bool {
	return copilotActive && wikiEnabled && sourceCodeCallerAllowed(c)
}

func (s *Server) copilotConfig(w http.ResponseWriter, r *http.Request) {
	active := s.copilot.Active()
	resp := copilotConfigResponse{
		Enabled: active,
		Model:   s.copilot.ActiveModel(),
	}
	claims := auth.FromContext(r.Context())
	role := ""
	if claims != nil {
		role = claims.Role
	}
	if profiles, def, _ := s.copilot.ProfilesSnapshot(); len(profiles) > 1 {
		// v0.10.1138 — yalnız çağıranın rolüne açık profiller (rol allowlist'i).
		if opts := copilotProfileOptions(profiles, role); len(opts) > 0 {
			resp.DefaultProfile = def
			resp.Profiles = opts
		}
	}
	// v0.10.1451 — kod/wiki turlarının yerel profili (karar 8): yoksa wiki
	// ipucu da söner (kademe "kapalı: yerel profil yok" der).
	localID, localLabel := "", ""
	if s.copilot.ProfileLocalOnly(r.Context()) {
		localID = s.copilot.DefaultProfileID()
	} else if id, ok := s.copilot.LocalOnlyProfileFor(role); ok {
		localID = id
	}
	if localID != "" {
		localLabel, _, _, _, _ = s.copilot.ProfileView(localID)
	}
	wk := wikiKB()
	resp.Wiki = chatWikiAvailable(active, wk != nil && wk.Enabled(), claims) && localID != ""
	if active {
		resp.CodeRead = copilotCodeReadFor(s.devops != nil && s.devops.Configured(), localID, localLabel)
	}
	resp.CosreV2 = s.cosreV2Enabled(r.Context())
	writeJSON(w, resp)
}
