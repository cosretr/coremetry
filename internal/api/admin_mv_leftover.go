package api

// admin_mv_leftover.go — v0.10.830 "MV artığı" temizliği (Admin → ClickHouse
// → MV onarımı kartı). Operatör onayı 2026-09-20.
//
//	POST /api/admin/clickhouse/mv-leftover/drop-view  (admin) {host, view, confirm:true}
//	POST /api/admin/clickhouse/mv-leftover/drop-inner (admin) {host, uuid, confirm:true}
//
// Neden: v0.10.825 kartı "MV'ler sağlıklı" derken Replika tutarlılığı kartı
// `.inner_id.<uuid>` satırlarında KALICI kırmızı gösteriyordu ve satırda hiç
// eylem yoktu. İki sınıf ölçülmüyordu — terfi öncesi çıplak MV (kalıntı) ve
// sahipsiz iç tablo (öksüz); gerekçe + kapılar internal/chstore/mv_leftover.go.
//
// KENDİ DOSYASI ZORUNLU: admin_dangling_mv_test.go o dosyada TAM İKİ
// RequireRole ve TAM İKİ audit literali sayıyor — rotaları oraya yazmak o
// kapıyı sessizce gevşetirdi (admin_mv_rebuild.go ile aynı disiplin).
// api.go BÜYÜMEZ: route defteri.
//
// confirm:true: iki uç da DDL koşar (DROP … SYNC). Yanlışlıkla atılan bir
// POST bir host'un MV nesnesini silmesin. Tespit GET tarafında
// (/api/admin/clickhouse/dangling-mv → leftovers) ve önbelleksiz.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("mv-leftover", (*Server).registerMVLeftoverRoutes) }

func (s *Server) registerMVLeftoverRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/clickhouse/mv-leftover/drop-view", auth.RequireRole(auth.RoleAdmin, s.postMVLeftoverDropView))
	mux.HandleFunc("POST /api/admin/clickhouse/mv-leftover/drop-inner", auth.RequireRole(auth.RoleAdmin, s.postMVLeftoverDropInner))
}

type mvLeftoverInput struct {
	Host string `json:"host"`
	View string `json:"view"`
	UUID string `json:"uuid"`
	// Confirm — DDL koşan uçların ortak kapısı (admin_mv_rebuild.go duruşu).
	Confirm bool `json:"confirm"`
}

// decodeMVLeftover — ortak gövde: JSON + confirm kapısı. Kapı store
// çağrısından ÖNCE durur; false dönerse cevap YAZILMIŞTIR.
func decodeMVLeftover(w http.ResponseWriter, r *http.Request) (mvLeftoverInput, bool) {
	var in mvLeftoverInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return in, false
	}
	in.Host, in.View, in.UUID = strings.TrimSpace(in.Host), strings.TrimSpace(in.View), strings.TrimSpace(in.UUID)
	if !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — bu uç DDL koşar")
		return in, false
	}
	return in, true
}

func (s *Server) postMVLeftoverDropView(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeMVLeftover(w, r)
	if !ok {
		return
	}
	if in.View == "" {
		writeJSONError(w, http.StatusBadRequest, "view zorunlu")
		return
	}
	steps, err := s.store.DropLeftoverMV(r.Context(), in.Host, in.View)
	target := in.View + "@" + in.Host
	if err != nil {
		s.audit(r, "clickhouse.mv_leftover", "clickhouse", target, fmt.Sprintf(`{"ok":false,"kind":"artik","error":%q,"steps":%d}`, err.Error(), len(steps)))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.mv_leftover", "clickhouse", target, fmt.Sprintf(`{"ok":true,"kind":"artik","steps":%d}`, len(steps)))
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "target": in.View, "steps": steps})
}

func (s *Server) postMVLeftoverDropInner(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeMVLeftover(w, r)
	if !ok {
		return
	}
	if in.UUID == "" {
		writeJSONError(w, http.StatusBadRequest, "uuid zorunlu")
		return
	}
	// Nesne adı SUNUCUDA kurulur (chstore.DropOrphanInner): istemci
	// `.inner_id.…` adı veremez, yalnız uuid.
	steps, err := s.store.DropOrphanInner(r.Context(), in.Host, in.UUID)
	target := ".inner_id." + strings.ToLower(in.UUID) + "@" + in.Host
	if err != nil {
		s.audit(r, "clickhouse.mv_leftover", "clickhouse", target, fmt.Sprintf(`{"ok":false,"kind":"oksuz","error":%q,"steps":%d}`, err.Error(), len(steps)))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.mv_leftover", "clickhouse", target, fmt.Sprintf(`{"ok":true,"kind":"oksuz","steps":%d}`, len(steps)))
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "target": ".inner_id." + strings.ToLower(in.UUID), "steps": steps})
}
