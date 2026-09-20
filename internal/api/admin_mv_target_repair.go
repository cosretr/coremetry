package api

// admin_mv_target_repair.go — v0.10.835: v0.10.833'ün SAPTADIĞI hedef uuid
// uyuşmazlığının ONARIM ucu (Admin → ClickHouse → MV onarımı → "Hedef uuid
// bulguları"). Mekanizma + kapılar internal/chstore/mv_target_repair.go.
//
//	POST /api/admin/clickhouse/mv-target/repair (admin)
//	     {host, view, peer, dropEmpty, confirm:true}
//
// YALNIZ ŞEKİL-1: MV'nin hedeflediği nesne uuid'si hiçbir tabloya çözülmüyor
// (düğüm-yerel okuma kod 60) — ingest o host'ta düşüyor. Hedefini ÇÖZEN
// satırda (şekil-2) MV topluyor ve eylem CANLI veriyi siler; kapı sunucuda,
// FE düğmeyi zaten çizmez (iki katman, ikisi de fail-closed: ölçülmemiş bir
// satır da REDDEDİLİR).
//
// KENDİ DOSYASI ZORUNLU: admin_dangling_mv_test.go / admin_mv_rebuild_test.go
// komşu dosyalarda TAM SAYIDA RequireRole ve audit literali sayıyor — dördüncü
// rotayı oraya yazmak o kapıları sessizce gevşetirdi. api.go BÜYÜMEZ: rota
// defteri (registerRoutesExtra, v0.10.247).
//
// confirm:true: uç DDL koşar (boş adın DROP'u + CREATE). dropEmpty AYRI bir
// onaydır ve yalnız 0 satır ÖLÇÜLDÜYSE işe yarar — dolu bir tabloyu hiçbir
// bayrak düşürtmez.
//
// cacheInvalidate: onarım `.inner_id.…` nesnesini doğurur/düşürür ve Replika
// tutarlılığı kartı o satırları 30 sn önbellekliyor (admin_replica_repair.go
// deseni) — bayat bir kart onarımı "olmamış" gösterirdi. Tespit GET'i
// (/api/admin/clickhouse/dangling-mv) önbelleksiz.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("mv-target-repair", (*Server).registerMVTargetRepairRoutes) }

func (s *Server) registerMVTargetRepairRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/clickhouse/mv-target/repair", auth.RequireRole(auth.RoleAdmin, s.postMVTargetRepair))
}

type mvTargetRepairInput struct {
	Host string `json:"host"`
	View string `json:"view"`
	// Peer — operatörün EKRANDA gördüğü dal. Sunucu söz tutulamıyorsa öteki
	// dala SESSİZCE düşmez (v0.10.825 duruşu), 409 ile reddeder.
	Peer bool `json:"peer"`
	// DropEmpty — `.inner_id.<view uuid>` adını tutan BOŞ tablonun
	// düşürülmesine ayrıca verilen onay. Dolu tabloda etkisizdir.
	DropEmpty bool `json:"dropEmpty"`
	Confirm   bool `json:"confirm"`
}

func (s *Server) postMVTargetRepair(w http.ResponseWriter, r *http.Request) {
	var in mvTargetRepairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.Host, in.View = strings.TrimSpace(in.Host), strings.TrimSpace(in.View)
	if in.View == "" {
		writeJSONError(w, http.StatusBadRequest, "view zorunlu")
		return
	}
	if !in.Confirm {
		writeJSONError(w, http.StatusBadRequest, "confirm:true zorunlu — bu uç DDL koşar")
		return
	}
	steps, err := s.store.RepairMVTargetOnHost(r.Context(), in.Host, in.View, in.Peer, in.DropEmpty)
	target := in.View + "@" + in.Host
	if err != nil {
		// YARIM bir onarımın KOŞAN DDL'i operatörün tek kanıtıdır: hata
		// yolunda da hem audit'e hem CEVABA girer (v0.10.835 incelemesi —
		// dosya başlığı "audit + ekran" diyordu ama adımlar kayboluyordu).
		// writeJSONError yalnız {"error"} yazar; burada gövde elle kurulur ve
		// `error` alanı AYNI yerde kalır (istemci onu okur).
		s.audit(r, "clickhouse.mv_target_repair", "clickhouse", target, mvTargetAuditDetail(false, in.Peer, in.DropEmpty, err.Error(), steps))
		s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "steps": steps})
		return
	}
	s.audit(r, "clickhouse.mv_target_repair", "clickhouse", target, mvTargetAuditDetail(true, in.Peer, in.DropEmpty, "", steps))
	s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "view": in.View, "steps": steps})
}

// mvTargetAuditDetail — audit detayı: hangi dal koştu, boş ad düşürüldü mü ve
// GERÇEKTEN KOŞAN ifadeler. "Tarihçe neden sıfırlandı" ile "yarım kalan onarım
// nerede durdu" sorularının tek kalıcı kaydı budur; adımları yalnız SAYMAK
// ikisini de cevapsız bırakıyordu. Marshal edilemezse sayıya düşer — audit
// yazımı hiçbir koşulda isteği düşürmez.
func mvTargetAuditDetail(ok, peer, dropEmpty bool, errMsg string, steps []string) string {
	d := map[string]any{"ok": ok, "peer": peer, "dropEmpty": dropEmpty, "steps": steps}
	if errMsg != "" {
		d["error"] = errMsg
	}
	b, mErr := json.Marshal(d)
	if mErr != nil {
		return fmt.Sprintf(`{"ok":%t,"peer":%t,"dropEmpty":%t,"steps":%d}`, ok, peer, dropEmpty, len(steps))
	}
	return string(b)
}
