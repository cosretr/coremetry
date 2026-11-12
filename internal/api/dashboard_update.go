package api

// dashboard_update.go — v0.10.2000 (Dashboards Faz 3 dilim 3.10; audit §5.5
// "Sürüm / geri al": PUT'ta updatedAt eşleşmesi, uyuşmazsa 409).
//
// updateDashboard api.go'dan BURAYA taşındı (api.go büyümez, taban iner);
// rota kaydı api.go'da aynı satırda kalır (PUT /api/dashboards/{id},
// editorRoles).
//
// Son yazan kazanıyordu: iki sekmede açık pano, ikinci kaydeden birincinin
// değişikliğini sessizce eziyordu. Şimdi istemci yüklediği sürümün
// updatedAt'ini `ifUpdatedAt` olarak yollar; satırdaki updated_at farklıysa
// 409 + güncel sürüm bilgisi döner ("başkası kaydetti"). Alan yoksa (eski
// istemci, sohbetten panel ekleme gibi sunucu içi yollar) denetim yapılmaz.
//
// SINIR (bilerek): denetim ile yazım arasında kilit yok; aynı milisaniyede
// iki PUT ikisi de geçebilir. Panolar insan hızında düzenlenir; amaç yaygın
// "iki sekme" kaybını kapatmak, dağıtık kilit değil. Kalıcı sürüm geçmişi
// şema ister — programın dışında (audit §5.5).

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// dashboardUpdateInput — PUT gövdesi: pano alanları + isteğe bağlı sürüm.
type dashboardUpdateInput struct {
	chstore.Dashboard
	IfUpdatedAt int64 `json:"ifUpdatedAt,omitempty"` // unix ns; 0 = denetim yok
}

// dashboardConflict — SAF: istemcinin sürümü satırınkiyle uyuşuyor mu?
func dashboardConflict(ifUpdatedAt, current int64) bool {
	return ifUpdatedAt != 0 && ifUpdatedAt != current
}

func (s *Server) updateDashboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.store.GetDashboard(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	var in dashboardUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if dashboardConflict(in.IfUpdatedAt, existing.UpdatedAt) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     "dashboard was saved by someone else",
			"conflict":  true,
			"updatedAt": existing.UpdatedAt,
		})
		return
	}
	// v0.9.563 — gövdede GELMEYEN alanlar mevcut satırdan taşınır.
	// Öncesinde yalnız CreatedAt taşınıyordu; frontend `variables`
	// göndermediği için dashboard'un $service değişkeni İLK KAYITTA
	// kalıcı siliniyordu ve ${service} referanslı paneller sessizce
	// filo geneline geçiyordu (bkz. dashboard_merge.go).
	d := in.Dashboard
	d.ID = id
	d = mergeDashboardUpdate(d, *existing)
	now := time.Now().UnixNano()
	if err := s.store.UpsertDashboardAt(r.Context(), d, now); err != nil {
		writeErr(w, err)
		return
	}
	d.UpdatedAt = now
	details, _ := json.Marshal(map[string]any{"name": d.Name})
	s.audit(r, "dashboard.update", "dashboard", d.ID, string(details))
	writeJSON(w, d)
}
