package api

// auth_session.go — v0.10.1375 (operatör-bildirimi, OIDC/Keycloak SSO:
// "OIDC ile logout olunca sayfa bir daha yüklenir gibi oluyor. Ve
// network'te hata oluyor.").
//
//   GET /api/auth/session → 200 {"authenticated":false}
//                         | 200 {"authenticated":true,"user":{…/api/auth/me şekli…}}
//
// Neden: IdP çıkışından dönen tam sayfa /login yüklemesinde SPA'nın açılış
// kontrolü /api/auth/me'yi çağırıyordu → oturum yok → 401 (31 baytlık
// {"error":"missing credentials"}), Network sekmesinde kırmızı. Giriş
// sayfasında "oturum yok" bir HATA değil, beklenen cevap; sonda bunu 200
// ile söyler.
//
// Neden /api/auth/me'yi 200'e çevirmek DEĞİL: me'nin 401 sözleşmesi
// (kimliksiz → 401 → SPA'nın küresel unauthorized işleyicisi) her diğer
// çağıran ve harici istemciler için doğru; tek çağıranın kozmetiği için
// sözleşme kırılmaz.
//
// Kimlik çözümü auth.Service.Authenticate — Middleware'in KENDİ yolu
// (JWT/cmk_/güvenilir başlık + canlı yetki); kopya değil, böylece sonda
// "oturum var" derken sonraki her çağrı 401 alamaz. Yol auth.SkipPath'te
// (yalnız GET): middleware 401 yazmaz, handler kimliği kendisi çözer.
// Kullanıcı gövdesi meUserPayload — /api/auth/me ile aynı fonksiyon.
//
// Rol kapısı YOK (kimliksiz cevap verir), audit YOK (salt-okunur),
// serveCached YOK ve Cache-Control: no-store — cevap çerezle değişir,
// paylaşılan bir cache girdisi bir kullanıcının kimliğini ötekine sızdırır.

import (
	"context"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("auth-session", (*Server).registerAuthSessionRoutes) }

func (s *Server) registerAuthSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", s.authSession)
}

func (s *Server) authSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var c *auth.Claims
	if s.auth != nil {
		c, _ = s.auth.Authenticate(w, r)
	}
	if c == nil {
		writeJSON(w, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, map[string]any{"authenticated": true, "user": s.meUserPayload(r.Context(), c)})
}

// meUserPayload — /api/auth/me ve /api/auth/session'ın ORTAK kullanıcı
// gövdesi (v0.10.1375'te api.go me()'den taşındı; davranış aynı).
//
// Hydrate from the store so customRole + customRolePages reach
// the SPA on every page load. The JWT only carries the base
// role; custom-role assignments live in the row.
// v0.8.519 — 30s cache: bu okuma her cold tab'ın SERİ kapısı
// (SPA me() bitmeden route verisi istemiyor); FINAL'lı satır
// okumasını tab başına değil 30s'de bire indirir.
func (s *Server) meUserPayload(ctx context.Context, c *auth.Claims) map[string]any {
	if u, ok := s.meUsers.get(c.UserID, time.Now()); ok && u != nil {
		return s.userPayload(u)
	}
	u, err := s.store.GetUserByID(ctx, c.UserID)
	if err != nil || u == nil {
		// Fall back to claim data — keeps /api/auth/me honest when
		// the row is temporarily unreachable. Custom-role gating
		// will simply not apply this round.
		return map[string]any{
			"id": c.UserID, "email": c.Email, "role": c.Role,
			"features": shellFeatures(chstore.IncidentsEnabled()), // v0.10.1826
		}
	}
	s.meUsers.put(c.UserID, u, time.Now())
	return s.userPayload(u)
}
