package api

// problem_log_templates.go — v0.10.1113 (operatör onaylı kuyruk maddesi "Log
// şablonu kök neden bağlantısı"): Problem detayında "Başlangıçta doğan log
// şablonları" — problemin başlangıcı çevresinde (−10 dk … +5 dk) özne
// servisinde ya da RCA şüphelilerinde DOĞAN, gerçekten yeni aileye ait
// Drain şablonları. Seçim anomaly.LogTemplateEvidenceFor'da (dedektörün aile
// süzgeciyle aynı işlevler; ayrıntı orada).
//
//   GET /api/problems/{id}/log-templates
//     → {services, startedAt, fromNs, toNs, templates: LogTemplateEvidence[]}
//
// Neden AYRI uç, /rootcause demetine alan DEĞİL: (1) RootCausePanel her
// Problem türünde çizilmiyor — dış seri Problem'i ExternalEvidencePanel alır;
// (2) /rootcause demeti soğukta onlarca saniye süren bir fan-out (v0.9.1082
// ölçümü 34 s + 45 s) — iki ucuz log_templates okumasının cevabı o demeti
// beklememeli; (3) demetin önbellek anahtarı problem kimliğidir, bu cevap ise
// yalnız (başlangıç, özne, servis kümesi)'nin işlevi — aynı servis ve
// başlangıçtaki iki Problem aynı girdiyi paylaşır.
//
// Servis kümesi (≤5): özne servis (yalnız kind=service) + kalıcı hipotezin
// çağrı-grafiği adayları RCA sırasıyla. Kind'ı BOŞ OLMAYAN her aday atlanır
// (node: Service bir node adı; rollout: Service
// "rollout:<cluster>/<ns>/<workload>@<rev>" öznesi) — log_templates.services
// yalnız servis adı taşır. Hipotez okuması TEK FINAL nokta okuması (ORDER BY anahtarı);
// okunamazsa yalnız özne. Küme boşsa (db / dış özne, hipotez yok) CH okuması
// YOK, önbellek girdisi YOK — boş liste.
//
// Önbellek: serveCached 60 s; anahtar TÜM girdileri taşır (v0.5.187) —
// başlangıç ns (pencere ondan türer), özne (atıf önce özneyi seçer) ve
// SIRALI servis kümesinin FNV özeti. Pencere `now`'a değil problemin SABİT
// başlangıcına bağlı: problem başına tek basamak, dakikaya yuvarlamaya
// gerek yok — yuvarlamak aynı servisleri paylaşan iki Problem'e birbirinin
// "N sn önce" sapmasını ve sıralamasını (59 sn'ye kadar yanlış) sunardı.
// Hata (bilinen-şablon okuması fail-closed dahil) önbelleğe YAZILMAZ.
//
// Rol kapısı YOK — salt-okunur (viewer görür). api.go BÜYÜMEZ: defter.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("problem-log-templates", (*Server).registerProblemLogTemplatesRoutes)
}

func (s *Server) registerProblemLogTemplatesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/log-templates", s.getProblemLogTemplates)
}

const problemLogTemplatesTTL = 60 * time.Second

// problemLogTemplatesStore — ucun üç okuması (*chstore.Store karşılar);
// handler testi sahte depoyla koşsun diye dikiş (alertRuleSeriesStoreOf
// emsali).
type problemLogTemplatesStore interface {
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	GetHypothesis(ctx context.Context, anchorKind, anchorID string) (*chstore.RootCauseHypothesis, error)
	ListLogTemplates(ctx context.Context, f chstore.ListLogTemplatesFilter) ([]chstore.LogTemplate, error)
}

var problemLogTemplatesStoreOf = func(s *Server) problemLogTemplatesStore { return s.store }

// problemLogTemplatesResponse — FE: ProblemLogTemplatesResponse.
type problemLogTemplatesResponse struct {
	// Services — bakılan servis kümesi, özne önce (≤5); boş = okuma yok.
	Services  []string                      `json:"services"`
	StartedAt int64                         `json:"startedAt"`
	FromNs    int64                         `json:"fromNs"`
	ToNs      int64                         `json:"toNs"`
	Templates []anomaly.LogTemplateEvidence `json:"templates"`
}

// problemLogTemplateSubject — özne bir servisse adı, değilse "" (db / dış
// öznenin adı log_templates.services'te hiç geçmez). SAF.
func problemLogTemplateSubject(p chstore.Problem) string {
	if chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
		return ""
	}
	return strings.TrimSpace(p.Service)
}

// problemLogTemplateServices — özne + hipotezin çağrı-grafiği adayları (RCA
// sırası), ≤ anomaly.LogTemplateEvidenceMaxServices. SAF; hyp nil olabilir.
func problemLogTemplateServices(subject string, hyp *chstore.RootCauseHypothesis) []string {
	var related []string
	if hyp != nil {
		for _, c := range hyp.Candidates {
			// Kind'ı boş olmayan HER aday atlanır: "node" (Service bir node
			// adı), "rollout" (Service "rollout:<cluster>/<ns>/<workload>@<rev>")
			// ve ileride gelecek her servis-dışı tür. Boş Kind = çağrı grafiği
			// şüphelisi = servis adı.
			if c.Kind != "" {
				continue
			}
			related = append(related, c.Service)
		}
	}
	return anomaly.LogTemplateEvidenceServices(subject, related)
}

// problemLogTemplatesKey — SAF; cevabı değiştiren her girdi: başlangıç (ns;
// pencere ondan türer), özne ve SIRALI servis kümesi (FNV — uzunluk değil
// içerik, v0.5.187).
func problemLogTemplatesKey(startedNs int64, subject string, services []string) string {
	sorted := append([]string(nil), services...)
	sort.Strings(sorted)
	return fmt.Sprintf("problem-log-templates:v1:at=%d:subj=%s:svc=%s",
		startedNs, fnvStr(subject), fnvStr(sorted...))
}

func (s *Server) getProblemLogTemplates(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem id required")
		return
	}
	// Önbellek DIŞINDA: eksik problem temiz 404 (önbelleğe yazılmış boş
	// cevap değil); problems küçük + FINAL (getProblemRootCause duruşu).
	st := problemLogTemplatesStoreOf(s)
	p, err := st.GetProblem(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p == nil {
		writeJSONError(w, http.StatusNotFound, "problem not found")
		return
	}
	subject := problemLogTemplateSubject(*p)
	var hyp *chstore.RootCauseHypothesis
	if h, herr := st.GetHypothesis(r.Context(), "problem", p.ID); herr == nil {
		hyp = h // yumuşak: okunamazsa yalnız özne
	}
	services := problemLogTemplateServices(subject, hyp)
	fromNs, toNs := anomaly.LogTemplateEvidenceWindow(p.StartedAt)
	resp := problemLogTemplatesResponse{
		Services: services, StartedAt: p.StartedAt, FromNs: fromNs, ToNs: toNs,
		Templates: []anomaly.LogTemplateEvidence{},
	}
	if len(services) == 0 || p.StartedAt <= 0 {
		writeJSON(w, resp)
		return
	}
	s.serveCached(w, r, problemLogTemplatesKey(p.StartedAt, subject, services), problemLogTemplatesTTL, func(ctx context.Context) (any, error) {
		ts, err := anomaly.LogTemplateEvidenceFor(ctx, st, subject, services, p.StartedAt)
		if err != nil {
			return nil, err
		}
		out := resp // kopya: SWR tazelemesi dış değişkeni paylaşmasın
		out.Templates = ts
		return out, nil
	})
}
