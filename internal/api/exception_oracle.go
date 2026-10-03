package api

// exception_oracle.go — v0.10.1092 (operatör: "Oracle hataları Exceptions
// gibi görünsün, hatta Exceptions altında da olabilir.") — api.go BÜYÜMEZ.
//
// Oracle hata tablosu grupları (`ora:` parmak izi, internal/oracle/exgroups.go)
// exception_groups'ta yaşar; bu dosya onların Exceptions yüzeyindeki
// ekleridir:
//
//   - oraclePriorityAt — Oracle grubunun öncelik kuralı (span merdiveninden
//     AYRI; aşağıda). exceptionPriority önce buraya yönlendirir.
//   - SetOracleGroupStats — main.go'nun enjekte ettiği istatistik kaynağı
//     (oracle.GroupStatsCache: kaynak başına TTL'li blob — saatlik toplamlar,
//     kırılım, kapanmış dakika gecikmesi). Öncelik /inbox'ta, Exceptions'ta ve
//     bildirimcide AYNI kaynaktan: "17 dk önce durdu" denmez, saatler aynı.
//   - annotateOracleGroups — liste satırına kaynak adı + kanal kırılımı +
//     etkilenen servisler + saatlik toplamlar.
//   - normalizeOracleFacet — ?oracle=only|exclude (Exceptions "Oracle" çipi).
//     Varsayılan (param yok) Oracle gruplarını İÇERİR.
//   - GET /api/exception-groups/{fp}/oracle — detay paneli (kırılım; Oracle
//     grubu değilse 404). Rol kapısı YOK: liste satırı her role görünür.

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() { registerRoutesExtra("exception-oracle", (*Server).registerExceptionOracleRoutes) }

func (s *Server) registerExceptionOracleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/exception-groups/{fp}/oracle", s.getExceptionGroupOracle)
}

const (
	// oracleInfoTopChannels / oracleInfoTopServices — satıra giden kırılım boyu.
	oracleInfoTopChannels = 6
	oracleInfoTopServices = 3
	// oracleP2MinLastHour — Oracle grubunun P2 tabanı (son 1 sa).
	oracleP2MinLastHour = 100
	// oracleBurstFactor — patlama: son 1 sa ≥ bu kat × önceki 1 sa.
	oracleBurstFactor = 3
)

// OracleGroupStatsFn — main.go enjekte eder (oracle.GroupStatsCache.Stats).
type OracleGroupStatsFn func(g chstore.ExceptionGroup) (oracle.GroupStats, bool)

var oracleStatsFn atomic.Pointer[OracleGroupStatsFn]

// SetOracleGroupStats — boot'ta bir kez (her rolde; bildirimci de buradan okur).
func SetOracleGroupStats(f OracleGroupStatsFn) { oracleStatsFn.Store(&f) }

// oracleGroupStats — kaynak yoksa / enjekte edilmemişse ok=false.
func oracleGroupStats(g chstore.ExceptionGroup) (oracle.GroupStats, bool) {
	if p := oracleStatsFn.Load(); p != nil && *p != nil {
		return (*p)(g)
	}
	return oracle.GroupStats{}, false
}

// oraclePriorityAt — SAF: Oracle grubunun önceliği. Operatör yönlendirmesi
// (koordinatör kararı, v0.10.1092): "az ama gerçek P1". Oracle grupları span
// merdiveninin YAPIŞKAN hacim P1'inden ve regressed "açıldıktan sonra ≥N" P1'inden
// MUAF — durgun bir akışın ömür toplamı birkaç saatte her eşiği geçer, grup
// kalıcı P1 olurdu ("kazanılmış P1 düşmez" ilkesinden bilinçli sapma; DECISIONS).
//
//	P1  son 1 sa ≥ oracleP1MinOccurrences VE ≥ 3 × önceki 1 sa (patlama)
//	P1  grup YENİ (ilk görülme P1 penceresinde) VE son 1 sa ≥ eşik
//	P2  son 1 sa ≥ 100 VE (taze — son görülme P1 penceresinde — ya da regressed)
//	P3  "sürekli akış (Oracle)"
//
// now: kapanmış dakika gecikmesi kadar geriye çekilmiş "şimdi" (çağıran).
func oraclePriorityAt(g chstore.ExceptionGroup, cfg chstore.ExceptionTriageConfig, now time.Time, lastHour, prevHour uint64) (string, string) {
	cfg = chstore.NormalizeExceptionTriage(cfg)
	thr := uint64(cfg.OracleP1MinOccurrences)
	win := cfg.P1Window()
	fresh := time.Duration(now.UnixNano()-g.LastSeen) <= win
	isNew := time.Duration(now.UnixNano()-g.FirstSeen) <= win
	last, prev := fmtThousands(lastHour), fmtThousands(prevHour)
	if lastHour >= thr && lastHour >= oracleBurstFactor*prevHour {
		return "P1", fmt.Sprintf("Oracle patlaması: son 1 sa %s (önceki 1 sa %s; eşik %s, ≥%d×)", last, prev, fmtThousands(thr), oracleBurstFactor)
	}
	if isNew && lastHour >= thr {
		return "P1", fmt.Sprintf("yeni Oracle grubu: son 1 sa %s (eşik %s)", last, fmtThousands(thr))
	}
	if lastHour >= oracleP2MinLastHour && (fresh || exceptionIsRegressed(g.State)) {
		if exceptionIsRegressed(g.State) {
			return "P2", fmt.Sprintf("regressed · son 1 sa %s", last)
		}
		return "P2", fmt.Sprintf("son 1 sa %s (önceki 1 sa %s)", last, prev)
	}
	return "P3", fmt.Sprintf("sürekli akış (Oracle) · son 1 sa %s", last)
}

// normalizeOracleFacet — SAF: yalnız "only" / "exclude"; gerisi "" (ikisi de).
func normalizeOracleFacet(v string) string {
	switch v {
	case "only", "exclude":
		return v
	}
	return ""
}

// oracleGroupInfoFrom — SAF: istatistik → satır bilgisi.
func oracleGroupInfoFrom(g chstore.ExceptionGroup, st oracle.GroupStats, known bool, topServices int) *chstore.OracleGroupInfo {
	info := &chstore.OracleGroupInfo{
		SourceID: st.Source.ID, SourceName: st.Source.Name, Code: g.Type, Operation: g.Message,
		Channels: []chstore.OracleNamedNum{}, Services: []chstore.OracleNamedNum{}, Known: known,
	}
	if !known {
		return info
	}
	info.LagSec = int64(st.Lag / time.Second)
	info.LastHour, info.PrevHour = st.LastHour, st.PrevHour
	b := st.Breakdown
	if b == nil {
		return info
	}
	for i, e := range oracle.SortedBreakdown(b.Channels) {
		if i >= oracleInfoTopChannels {
			break
		}
		info.Channels = append(info.Channels, chstore.OracleNamedNum{Name: e.Name, Count: e.Count})
	}
	svcs := oracle.SortedBreakdown(b.Services)
	info.ServiceCount = len(svcs)
	for i, e := range svcs {
		if topServices > 0 && i >= topServices {
			break
		}
		info.Services = append(info.Services, chstore.OracleNamedNum{Name: e.Name, Count: e.Count})
	}
	return info
}

// annotateOracleGroups — `ora:` satırlarına Oracle bilgisi (enjekte edilen
// istatistik önbelleğinden; kaynak başına TTL'de bir blob okuması). Kaynak
// bulunamazsa (silinmiş) satır yine bilgi alır, Known=false.
func (s *Server) annotateOracleGroups(items []chstore.ExceptionGroup) []chstore.ExceptionGroup {
	for i := range items {
		g := items[i]
		if !chstore.IsOracleGroup(g.Fingerprint) {
			continue
		}
		st, ok := oracleGroupStats(g)
		items[i].Oracle = oracleGroupInfoFrom(g, st, ok, oracleInfoTopServices)
	}
	return items
}

// getExceptionGroupOracle — detay paneli: tüm servis kırılımı (blob zaten
// ≤ 12), kanal kırılımı, kaynak, saatlik toplamlar. Oracle grubu değil / grup
// yok → 404. Önbelleksiz (grup satırı tek okuma; blob TTL'li önbellekte).
func (s *Server) getExceptionGroupOracle(w http.ResponseWriter, r *http.Request) {
	fp := r.PathValue("fp")
	if !chstore.IsOracleGroup(fp) {
		http.Error(w, `{"error":"not an oracle exception group"}`, http.StatusNotFound)
		return
	}
	g, err := s.store.GetExceptionGroup(r.Context(), fp)
	if err != nil {
		writeErr(w, err)
		return
	}
	if g == nil {
		http.Error(w, `{"error":"exception group not found"}`, http.StatusNotFound)
		return
	}
	st, ok := oracleGroupStats(*g)
	writeJSON(w, oracleGroupInfoFrom(*g, st, ok, 0))
}
