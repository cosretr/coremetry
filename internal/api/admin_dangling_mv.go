package api

// admin_dangling_mv.go — v0.10.762 "Sarkan MV onarımı" sihirbazı
// (Admin → ClickHouse). Prod olayı 2026-09-17: bir node'da combined MV'nin
// iç tablosu silinmiş, view nesnesi kalmış → o shard INSERT reddediyor →
// spans spool'u 509K dosya / 398 GiB. Gerekçe + mekanizma
// internal/chstore/dangling_mv_admin.go.
//
//	GET  /api/admin/clickhouse/dangling-mv           (admin) — tespit, önbelleksiz
//	POST /api/admin/clickhouse/dangling-mv/repair    (admin) {host, view} — audit'li
//
// api.go BÜYÜMEZ: route defteri.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("dangling-mv", (*Server).registerDanglingMVRoutes) }

func (s *Server) registerDanglingMVRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/dangling-mv", auth.RequireRole(auth.RoleAdmin, s.getDanglingMVs))
	mux.HandleFunc("POST /api/admin/clickhouse/dangling-mv/repair", auth.RequireRole(auth.RoleAdmin, s.postDanglingMVRepair))
}

func (s *Server) getDanglingMVs(w http.ResponseWriter, r *http.Request) {
	rows, cluster, err := s.store.DanglingMVs(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"cluster": cluster, "rows": rows, "generatedAt": time.Now().UnixNano()}
	// v0.10.825 — kapsama (ok | plain | dangling | missing) aynı çağrıda:
	// kart artık "view var, iç tablo yok"un yanında "iç tablo DÜZ" ve "view
	// YOK" durumlarını da gösterir. Kapsama hatası sarkan listeyi DÜŞÜRMEZ
	// (kartın eski yarısı çalışmaya devam eder, hata rozetle görünür).
	// v0.10.833 — targetError AYRI: hedef uuid okuması düşse bile kapsama
	// sınıfları durur (hücreler `unmeasured` olur). Zarfta ayrı bir alan
	// olması FE'nin YEŞİL rozeti "ölçüldü VE bulgu yok" koşuluna bağlaması
	// içindir — ölçülmemiş bir şey sağlıklı ilan edilemez.
	rep, cerr := s.store.MVCoverage(r.Context())
	if cerr != nil {
		out["coverageError"] = cerr.Error()
	} else {
		out["coverage"] = rep.Rows
		if rep.TargetError != "" {
			out["targetError"] = rep.TargetError
		}
	}
	// v0.10.830 — artıklar (terfi öncesi çıplak MV + sahipsiz iç tablo) aynı
	// çağrıda. Liste HER ZAMAN dizidir (null değil): kart "kalıntı yok"u
	// "ölçülmedi"den ayırt edemezse yeşil rozeti yanlış basar.
	//
	// v0.10.833 — öksüz kararı kapsamanın topladığı `TO INNER UUID` kümesine
	// bakar (ikinci bir probe AÇILMAZ): nesne uuid'si o kümede olan iç tablo
	// CANLI bir MV'nin hedefidir. Kapsama düştüyse küme ölçülmemiştir ve
	// öksüz satırları düğmesiz (Blocked) gelir.
	lo, _, lerr := s.store.MVLeftovers(r.Context(), rep.Targets)
	if lerr != nil {
		out["leftoverError"] = lerr.Error()
	}
	if lo == nil {
		lo = []chstore.MVLeftover{}
	}
	out["leftovers"] = lo
	writeJSON(w, out)
}

type danglingMVRepairInput struct {
	Host string `json:"host"`
	View string `json:"view"`
	// Peer — v0.10.825: EKRANDA "Eşten kur" yazıyordu. Sunucu eşi
	// çözemezse istek 409 ile REDDEDİLİR; sessizce "Yeniden kur"a (DROP +
	// kanonik CREATE, tarihçe sıfırlanır) düşmez — operatör onu görmedi.
	Peer bool `json:"peer"`
}

func (s *Server) postDanglingMVRepair(w http.ResponseWriter, r *http.Request) {
	var in danglingMVRepairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in.Host, in.View = strings.TrimSpace(in.Host), strings.TrimSpace(in.View)
	if in.View == "" {
		writeJSONError(w, http.StatusBadRequest, "view zorunlu")
		return
	}
	steps, err := s.store.RepairDanglingMV(r.Context(), in.Host, in.View, in.Peer)
	target := in.View + "@" + in.Host
	if err != nil {
		s.audit(r, "clickhouse.dangling_mv_repair", "clickhouse", target, fmt.Sprintf(`{"ok":false,"error":%q,"steps":%d}`, err.Error(), len(steps)))
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "clickhouse.dangling_mv_repair", "clickhouse", target, fmt.Sprintf(`{"ok":true,"steps":%d}`, len(steps)))
	writeJSON(w, map[string]any{"ok": true, "host": in.Host, "view": in.View, "steps": steps})
}
