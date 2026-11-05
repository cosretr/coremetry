package api

// anomaly_red_source.go — v0.10.1478 (operatör: "Servislerin kendi içerisinde
// haftalık olağan akışlarını takip etse ve gerçek bir anomaly öyle yakalasa").
//
// Metrik dedektörü + davranış motoru RED'i artık VictoriaMetrics'in
// ÖRNEKLENMEMİŞ OTel HTTP/gRPC sunucu histogramından okuyabiliyor
// (internal/anomaly/red_vm_*.go). Ayar mevcut anomaly_sensitivity blobunda
// (redSource auto|span, redSourceEnv) — ayrı blob DEĞİL: operatör buraya tek
// soruyla geliyor ("ne zaman olay sayılsın") ve geri alma tek alan (span).
//
//	GET /api/settings/anomaly-red-source/status — admin; dedektörün son tikinin
//	ölçümü (sorgu sayısı / süresi / ısınma / aile kapsamı / metrik başına kaynak
//	sayımı). Süreç-içi: lider olmayan pod boş döner (davranış motoru kartıyla aynı
//	duruş — o pod gerçekten taramıyor).
//
// Neden ayrı uç (api.go'daki /admin/stats'e alan DEĞİL): api.go büyümez
// (TestApiGoDoesNotGrow) ve bu ölçüm ayar sayfasının "kaynak" satırının verisi.
// Yazma yolu putAnomalySensitivity'de kalır; kaynak DEĞİŞİNCE ek bir audit
// satırı (anomaly.red_source) atılır ki geri alma denetimde ayrıca görünsün.

import (
	"fmt"
	"net/http"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("anomaly-red-source", (*Server).registerAnomalyREDSourceRoutes) }

func (s *Server) registerAnomalyREDSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/anomaly-red-source/status", auth.RequireRole(auth.RoleAdmin, s.getAnomalyREDSourceStatus))
}

// redSourceStatusResponse — v0.10.1489: anomali ölçümü + alarm kuralı
// okuyucusunun son evaluator tiki (`alert`; evaluator lideri olmayan pod boş).
type redSourceStatusResponse struct {
	anomaly.REDSourceStats
	Alert anomaly.AlertVMStats `json:"alert"`
	// Subjects — v0.10.1791 (Davis F1.3): özne türlerinin son tiki + atlanma
	// sayaçları (seri tavanı / VM bütçesi).
	Subjects anomaly.SubjectStats `json:"subjects"`
}

func (s *Server) getAnomalyREDSourceStatus(w http.ResponseWriter, _ *http.Request) {
	st := anomaly.REDSourceObservability()
	if st.Families == nil {
		st.Families = []anomaly.REDSourceFamilyStats{}
	}
	al := anomaly.AlertVMObservability()
	if al.Families == nil {
		al.Families = []anomaly.AlertVMFamilyStats{}
	}
	writeJSON(w, redSourceStatusResponse{REDSourceStats: st, Alert: al, Subjects: anomaly.SubjectObservability()})
}

// anomalyREDSourceAuditDetail — SAF: kaynak ya da ortam kapsamı değiştiyse
// denetim satırının metni. ok=false → değişiklik yok, ek satır yok.
func anomalyREDSourceAuditDetail(prev, next chstore.AnomalySensitivityConfig) (string, bool) {
	pm, nm := prev.REDSourceMode(), next.REDSourceMode()
	pe, ne := prev.REDSourceEnvScope(), next.REDSourceEnvScope()
	if pm == nm && pe == ne {
		return "", false
	}
	return fmt.Sprintf(`{"from":{"redSource":%q,"redSourceEnv":%q},"to":{"redSource":%q,"redSourceEnv":%q}}`, pm, pe, nm, ne), true
}
