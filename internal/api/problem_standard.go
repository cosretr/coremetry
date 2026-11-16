package api

// problem_standard.go — v0.10.1529 (Problem standardı, dilim R2: satır +
// detay başlığı UI; operatör onayı 2026-10-24 D1–D13, mockup "Problem
// standard mockup" 2026-10-25 onayı: mockup kolonları + ikincil kolonlar,
// başlık tüm detay sayfalarında).
//
// R1 (v0.10.1523) standardı yalnız gölge bildirim politikasına verdi. R2 aynı
// SAF çekirdeği (chstore.Standardize*) ekrana taşır:
//
//   - /api/inbox: her satır `std` taşır (Ne bozuk · Etki · Ne zamandan beri ·
//     Güven · Kaynak · Sonraki adım). Hesap aday kümesinin TAMAMINDA, sıralama
//     ve tavandan ÖNCE — D10 ("düşük güvenli satır görünür ama aşağıda
//     sıralanır") tavanı da ilgilendirir; sayfayı sıralamak v0.9.318
//     yalanının aynısı olurdu.
//   - GET /api/problem-standard?ref=<kind>:<id>: detay başlığı. ref biçimi
//     InboxItem.ID ile AYNI (problem:/exception:/anomaly:/incident:), böylece
//     üç detay sayfası tek kanca + tek bileşenle çizer.
//
// Neden ayrı bir detay ucu, mevcut detay cevaplarına alan eklemek DEĞİL:
// üç ayrı uç (problem, exception grubu, anomali olayı) üç ayrı zenginleştirme
// zinciri taşıyor; standardı her birine ayrı ayrı kablolamak gerçeklerin
// (trafik, takvim, batch) üç kez farklı okunması demekti — R1 dosya başlığının
// "her çağıran farklı gerçeklerle farklı güven üretirdi" uyarısı. Tek uç, tek
// stdFactsFor.
//
// Trafik gerçeği (G1) problem satırlarında TEK toplu service_summary_5m
// okumasıyla (chstore.ServiceBucketTraffic, gölge politikayla aynı kova
// çifti: açılıştan önceki tamamlanmış kova + bir saat önce). Okuma düşerse
// trafik "bilinmiyor" kalır — bilinmiyor güveni ASLA düşürmez
// (signal_gates.go), yani hata kuyruğu boşaltmaz, yalnız rozetleri "orta"ya
// yaslar. Doğrulanmış çağıranlar (topology_edges_5m) R2'de okunmuyor:
// satır "çağıran" iddiasında bulunmaz, Etki ikinci satırı ölçülen dakikalık
// hatayı söyler.

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-standard", (*Server).registerProblemStandardRoutes) }

func (s *Server) registerProblemStandardRoutes(mux *http.ServeMux) {
	// Rol kapısı YOK — salt-okunur; viewer başlığı GÖRMELİ (CLAUDE.md inv. 7).
	mux.HandleFunc("GET /api/problem-standard", s.getProblemStandard)
}

// problemStandardTTL — liste önbelleğiyle aynı (inboxListTTL): başlık ile
// satır aynı yaşta gerçek göstersin, biri "yüksek" öteki "orta" demesin.
const problemStandardTTL = inboxListTTL

// inboxStdSrc — satırın standardı için kaynak nesne (yalnız biri dolu).
// JSON'a çıkmaz; build içinde attachInboxStandards okur.
type inboxStdSrc struct {
	problem   *chstore.Problem
	exception *chstore.ExceptionGroup
	anomaly   *chstore.AnomalyEvent
	incident  *chstore.Incident
}

// parseStdRef — SAF: "<kind>:<id>" → (kind, id). Bilinmeyen tür / boş id → ok=false.
func parseStdRef(ref string) (kind, id string, ok bool) {
	kind, id, found := strings.Cut(strings.TrimSpace(ref), ":")
	if !found || id == "" {
		return "", "", false
	}
	switch kind {
	case "problem", "exception", "anomaly", "incident":
		return kind, id, true
	}
	return "", "", false
}

// problemStandardKey — SAF: önbellek anahtarı; tek girdi ref (kimlik zaten
// env'e özgü nesneyi seçer).
// :v2: — v0.10.2300 (sunucu metinleri S3) gövdeye öncelik gerekçesi + anahtarı
// eklendi (priorityReason, priorityReasonMsg); dil anahtara GİRMEZ.
func problemStandardKey(ref string) string { return "pstd:v2:ref=" + strings.TrimSpace(ref) }

// stdBaseFacts — SAF: trafik dışındaki gerçekler (takvim, batch, deploy).
func stdBaseFacts(now time.Time, sens chstore.AnomalySensitivityConfig, service string) chstore.StdFacts {
	return chstore.StdFacts{
		Now:          now,
		CalendarKind: sens.Calendar.SpecialKindOn(now),
		Batch:        sens.IsBatchService(service),
	}
}

// stdTrafficSubject — SAF: problem satırı G1 için trafik okuması ister mi;
// yalnız servis özneli problemler (db / dış kaynak service_summary_5m'de yok).
func stdTrafficSubject(p chstore.Problem) bool {
	return p.Service != "" && chstore.ProblemSubjectKind(p.Kind) == chstore.ProblemKindService
}

// standardizeInboxSrc — SAF: kaynak + gerçekler → standart. src boşsa nil.
func standardizeInboxSrc(src *inboxStdSrc, f chstore.StdFacts, incidentProblems int) *chstore.ProblemStandard {
	if src == nil {
		return nil
	}
	var std chstore.ProblemStandard
	switch {
	case src.problem != nil:
		p := *src.problem
		if p.RecentDeploy != nil {
			f.RecentDeploy = p.RecentDeploy.Version
		}
		std = chstore.StandardizeProblem(p, f)
	case src.exception != nil:
		std = chstore.StandardizeException(*src.exception, f)
	case src.anomaly != nil:
		e := *src.anomaly
		f.LastSeen = e.LastSeen
		std = chstore.StandardizeAnomalyEvent(e, f)
	case src.incident != nil:
		f.IncidentProblems = incidentProblems
		std = chstore.StandardizeIncident(*src.incident, f)
	default:
		return nil
	}
	return &std
}

// attachInboxStandards — aday kümesinin her satırına std yazar. Tek toplu
// trafik okuması; düşerse trafik bilinmiyor (log, hata dönmez).
func (s *Server) attachInboxStandards(ctx context.Context, items []InboxItem, now time.Time) {
	if len(items) == 0 {
		return
	}
	var sens chstore.AnomalySensitivityConfig
	if s.store != nil {
		sens = s.store.AnomalySensitivityForDetectors()
	}
	var pairs []chstore.ServiceBucket
	seen := map[chstore.ServiceBucket]bool{}
	for i := range items {
		if p := items[i].stdSrc.problemOrNil(); p != nil && stdTrafficSubject(*p) {
			cur, base := chstore.TrafficPairsFor(p.Service, time.Unix(0, p.StartedAt))
			for _, b := range []chstore.ServiceBucket{cur, base} {
				if !seen[b] {
					seen[b] = true
					pairs = append(pairs, b)
				}
			}
		}
	}
	var traffic map[chstore.ServiceBucket]chstore.BucketTraffic
	if len(pairs) > 0 && s.store != nil {
		m, _, err := s.store.ServiceBucketTraffic(ctx, pairs)
		if err != nil {
			log.Printf("[inbox] standart: trafik okuması düştü, G1 bilinmiyor: %v", err)
		} else {
			traffic = m
		}
	}
	for i := range items {
		it := &items[i]
		f := stdBaseFacts(now, sens, it.Service)
		if p := it.stdSrc.problemOrNil(); p != nil && traffic != nil && stdTrafficSubject(*p) {
			cur, base := chstore.TrafficPairsFor(p.Service, time.Unix(0, p.StartedAt))
			f.Traffic = chstore.StdTrafficFrom(traffic, cur, base)
		}
		n := 0
		if it.Incident != nil {
			n = it.Incident.ProblemCount
		}
		it.Std = standardizeInboxSrc(it.stdSrc, f, n)
	}
}

func (src *inboxStdSrc) problemOrNil() *chstore.Problem {
	if src == nil {
		return nil
	}
	return src.problem
}

// inboxConfidenceLow — SAF: D10 kovası.
func inboxConfidenceLow(it InboxItem) bool {
	return it.Std != nil && it.Std.Confidence.Level == chstore.ConfidenceLow
}

// inboxConfidenceRank — SAF: güven sıralama değeri (std yok = orta; bilinmiyor
// düşürmez kuralının sıralamadaki karşılığı).
func inboxConfidenceRank(it InboxItem) int {
	if it.Std == nil {
		return chstore.ConfidenceRank(chstore.ConfidenceMedium)
	}
	return chstore.ConfidenceRank(it.Std.Confidence.Level)
}

// getProblemStandard — GET /api/problem-standard?ref=<kind>:<id>.
func (s *Server) getProblemStandard(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	kind, id, ok := parseStdRef(ref)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "ref=<problem|exception|anomaly|incident>:<id> zorunlu")
		return
	}
	s.serveCached(w, r, problemStandardKey(ref), problemStandardTTL, func(ctx context.Context) (any, error) {
		src, incProblems, err := s.loadStdSrc(ctx, kind, id)
		if err != nil {
			return nil, err
		}
		if src == nil {
			// Bulunamadı: null gövde — başlık bileşeni sessizce çizilmez,
			// sayfanın geri kalanı (kendi 404'üyle) aynen çalışır.
			return nil, nil
		}
		items := []InboxItem{{ID: kind + ":" + id, stdSrc: src}}
		if incProblems > 0 {
			items[0].Incident = &InboxIncidentRef{ID: id, ProblemCount: incProblems}
		}
		if src.problem != nil {
			items[0].Service = src.problem.Service
		} else if src.exception != nil {
			items[0].Service = src.exception.Service
		} else if src.anomaly != nil {
			items[0].Service = src.anomaly.Service
		} else if src.incident != nil {
			items[0].Service = src.incident.Service
		}
		s.attachInboxStandards(ctx, items, time.Now())
		if std := items[0].Std; std != nil && src.problem != nil {
			// v0.10.2300 (S3) — detay başlığının öncelik gerekçesi anahtarı.
			std.PriorityReason, std.PriorityReasonMsg = src.problem.PriorityReason, src.problem.PriorityReasonMsg
		}
		return items[0].Std, nil
	})
}

// loadStdSrc — kimlikten kaynak nesne. nil, nil = bulunamadı.
func (s *Server) loadStdSrc(ctx context.Context, kind, id string) (*inboxStdSrc, int, error) {
	switch kind {
	case "problem":
		p, err := s.store.GetProblem(ctx, id)
		if err != nil || p == nil {
			return nil, 0, err
		}
		// Liste ile aynı zincir (deploy + öncelik) — başlık satırla aynı bağlamı söylesin.
		rows := s.enrichProblemsForRead(ctx, []chstore.Problem{*p})
		if len(rows) > 0 {
			p = &rows[0]
		}
		return &inboxStdSrc{problem: p}, 0, nil
	case "exception":
		g, err := s.store.GetExceptionGroup(ctx, id)
		if err != nil || g == nil {
			return nil, 0, err
		}
		prio, _ := exceptionPriority(*g) // liste satırıyla aynı merdiven
		return exceptionStdSrc(*g, prio), 0, nil
	case "anomaly":
		e, err := s.store.GetAnomalyEvent(ctx, id, 0)
		if err != nil || e == nil {
			return nil, 0, err
		}
		return &inboxStdSrc{anomaly: e}, 0, nil
	case "incident":
		inc, err := s.store.GetIncident(ctx, id)
		if err != nil || inc == nil {
			return nil, 0, err
		}
		n := 0
		if m, err := s.store.IncidentProblemIDs(ctx, []string{id}); err == nil {
			n = len(m[id])
		}
		return &inboxStdSrc{incident: inc}, n, nil
	}
	return nil, 0, nil
}
