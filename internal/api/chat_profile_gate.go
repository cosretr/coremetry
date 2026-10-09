package api

// chat_profile_gate.go — v0.10.1138 (CoSRE sohbet iyileştirmeleri, operatör
// onaylı): sohbet gövdesindeki AÇIK model profili seçiminin sunucu kapısı.
//
// v0.10.183'te bilinmeyen kimlik sessizce varsayılana düşüyordu; artık seçici
// gerçekten çiziliyor (useCopilotEnabled profilleri taşıyor) ve profillere
// rol allowlist'i geldi. Sessiz düşüş, operatörün "Derin" seçip küçük modelden
// cevap aldığını fark etmemesi demekti — kapı AÇIK cevap verir:
//
//   - boş seçim        → kapı yok (varsayılan / yüzey eşlemesi, eski davranış)
//   - bilinmeyen kimlik → 400 {code:"profile_unknown"}
//   - rolüne kapalı     → 403 {code:"profile_forbidden"}
//
// İstemci (useChatThread) iki kodda da seçimi varsayılana döndürür. Kapı SSE
// başlıklarından ÖNCE koşar: düz JSON hata gövdesi okunabilsin.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatProfileGateError — SAF eşleme: profil erişim hatası → HTTP durum + kod.
// nil → (0, "") — kapı geçti.
func chatProfileGateError(err error) (status int, code string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, copilot.ErrProfileForbidden):
		return http.StatusForbidden, "profile_forbidden"
	default:
		return http.StatusBadRequest, "profile_unknown"
	}
}

// chatProfileGate — istekteki profil kimliğini doğrular; reddederse yanıtı
// yazar ve false döner.
func (s *Server) chatProfileGate(w http.ResponseWriter, r *http.Request, profile string) bool {
	p := strings.TrimSpace(profile)
	if p == "" {
		return true
	}
	role := ""
	if c := auth.FromContext(r.Context()); c != nil {
		role = c.Role
	}
	status, code := chatProfileGateError(s.copilot.CheckProfileAccess(p, role))
	if status == 0 {
		return true
	}
	msg := "model profili bulunamadı"
	if code == "profile_forbidden" {
		msg = "bu model profili rolüne kapalı"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code, "profile": p})
	return false
}
