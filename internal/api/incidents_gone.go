package api

// incidents_gone.go — v0.10.2075 Incidents ölü kod 5 (operatör onayı
// 2026-11-13). Incidents akışı v0.10.1826–1828'de emekli oldu; sayfalar,
// otomatik bağlama, CoSRE/MCP dalları ve ayarlar v0.10.2070–2073'te
// silindi. Kalan dokuz /api/incidents* rotası (yalnız POST 410'du; GET /
// PUT / ack / resolve / note açıktı) ve /api/settings/incidents saplaması
// burada TEK bir 410 Gone işleyicisine indi:
//
//   *  /api/incidents            → 410 {"error": "<Türkçe>"}
//   *  /api/incidents/...        → 410
//   *  /api/settings/incidents   → 410 (admin kapısı arkasında)
//
// Neden rotalar tamamen silinmedi: kayıtsız /api/* yolu SPA catch-all'ına
// düşer ve 200 + index.html döner (/api-route "sessizce bozulanlar") —
// eski istemci / betik ne olduğunu göremezdi. Neden metotsuz kalıp: amaç
// HER metodu aynı cevapla kapatmak; /api-route'un "metot öneki zorunlu"
// kuralı kazara tüm metotları açmamak içindir, burada bilinçli.
//
// Kimlik: küresel auth middleware kimliksiz isteği zaten 401 yapar (önce
// de öyleydi). Rol sınıfı korunur: /api/incidents okuması (GET/HEAD) her
// oturumlu kullanıcıya, diğer metotlar editor+ (eski yazma rotalarının
// kapısı), ayar yolu admin. Hiçbir şey yazılmaz → audit yok.
//
// AYRI KALAN (bilerek): bildirim "Sustur" bağlantısı incident ack'ini
// /api/public/notify/ignore/{token} üzerinden yapar (notify_actions.go),
// bu dosyadaki yollardan değil — çalışmaya devam eder.

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("incidents-gone", (*Server).registerIncidentsGoneRoutes) }

func (s *Server) registerIncidentsGoneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/incidents", incidentsGoneByRole)
	mux.HandleFunc("/api/incidents/", incidentsGoneByRole)
	mux.HandleFunc("/api/settings/incidents", auth.RequireRole(auth.RoleAdmin, incidentsGone))
}

// incidentsRetiredMsg — 410 gövdesi (writeJSONError: {"error": msg}).
const incidentsRetiredMsg = "Incidents emekli; Anomalies ve Problems sayfalarını kullanın"

// incidentsGoneEditorOnly — yazma metotları için eski editor+ kapısı.
var incidentsGoneEditorOnly = auth.RequireAnyRole(editorRoles, incidentsGone)

// incidentsGoneByRole — eski rota tablosunun rol sınıfı: okuma her
// oturumlu kullanıcıya, diğer her metot editor+.
func incidentsGoneByRole(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		incidentsGone(w, r)
		return
	}
	incidentsGoneEditorOnly(w, r)
}

func incidentsGone(w http.ResponseWriter, _ *http.Request) {
	writeJSONError(w, http.StatusGone, incidentsRetiredMsg)
}

// shellFeatures — SAF (v0.10.1826): kabuğun kullanıcı gövdesiyle aldığı
// sunucu bayrakları. `incidents` = eski Incidents akışı açık mı; üretimde
// chstore.IncidentsEnabled() hep false (v0.10.2024).
func shellFeatures(incidentsOn bool) map[string]any {
	return map[string]any{"incidents": incidentsOn}
}
