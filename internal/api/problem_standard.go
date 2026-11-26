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
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
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
	// impactRule — v0.10.2383: Problem'in etki kuralı olayının gerçekleri
	// ("Neden" satırları; v0.10.2391: hata oranı, yanıt süresi, consumer
	// lag). YALNIZ detay ucu doldurur (problemImpactRule); liste yolunda hep
	// nil → ek okuma yok.
	impactRule *chstore.StdImpactRule
	// impactEvs / impactLoaded — v0.10.2482 (etki özeti): detay ucu "Neden"
	// okumasının olaylarını taşır (özet aynı olaylardan; ikinci okuma yok).
	// Liste yolunda impactLoaded=false: attachInboxStandards TEK toplu okuma
	// yapar (inboxImpactPlan → ListImpactLeadEvents).
	impactEvs    []chstore.MLEvent
	impactLoaded bool
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
// :v3: — v0.10.2383 kanıta "Neden" satırları eklendi (evidence.reasons).
// :v4: — v0.10.2391 yanıt süresi + consumer lag Problem'lerinde de "Neden".
// :v5: — v0.10.2476 lag beklenen günlük desen (lag.patternDays / pattern).
// :v6: — v0.10.2482 etki özeti (impactSummary).
func problemStandardKey(ref string) string { return "pstd:v6:ref=" + strings.TrimSpace(ref) }

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
		f.ImpactRule = src.impactRule // v0.10.2383 — yalnız detay ucunda dolu
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
	// v0.10.2482 — etki özeti olayları: trafik okumasıyla EŞ ZAMANLI tek
	// toplu okuma (satır başına değil). Düşerse özet yok (log, hata dönmez).
	var impactEvs []chstore.MLEvent
	var wg sync.WaitGroup
	if plan, ok := inboxImpactPlan(items, now); ok && s.store != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			evs, err := s.store.ListImpactLeadEvents(ctx, plan.services, plan.detectors, plan.ids, plan.since)
			if err != nil {
				log.Printf("[inbox] etki özeti: olay okuması düştü, Etki boş: %v", err)
				return
			}
			impactEvs = evs
		}()
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
	wg.Wait()
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
	attachInboxImpacts(items, impactEvs)
}

// inboxImpactLookback — liste toplu okumasının en geri sınırı: etki özeti
// "şimdi"yi söyler; son 24 saatte görülmemiş etki olayı özet üretmez.
const inboxImpactLookback = 24 * time.Hour

// inboxImpactRead — toplu okuma girdileri.
type inboxImpactRead struct {
	services, detectors, ids []string
	since                    time.Time
}

// inboxImpactPlan — SAF (v0.10.2482): listede etki özeti için okunacaklar.
// Yalnız etki kuralı olan açık Problem satırları (ml.ImpactLeadReadOf); OOM
// okuma istemez (Problem'in sayıları). Kümeler sıralı (deterministik SQL).
// ok=false: okunacak satır yok → sorgu yok.
func inboxImpactPlan(items []InboxItem, now time.Time) (inboxImpactRead, bool) {
	svcs, dets, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var since time.Time
	for i := range items {
		p := items[i].stdSrc.problemOrNil()
		if p == nil || items[i].stdSrc.impactLoaded {
			continue
		}
		lr, ok := ml.ImpactLeadReadOf(*p)
		if !ok {
			continue
		}
		if lr.LeadID != "" {
			ids[lr.LeadID] = true
		} else {
			svcs[p.Service] = true
			for _, d := range lr.Detectors {
				dets[d] = true
			}
		}
		if t := stdImpactEventsSince(p.StartedAt, now); since.IsZero() || t.Before(since) {
			since = t
		}
	}
	if len(svcs) == 0 && len(ids) == 0 {
		return inboxImpactRead{}, false
	}
	if floor := now.Add(-inboxImpactLookback); since.Before(floor) {
		since = floor
	}
	return inboxImpactRead{services: sortedKeys(svcs), detectors: sortedKeys(dets), ids: sortedKeys(ids), since: since}, true
}

// attachInboxImpacts — SAF (v0.10.2482): her Problem satırının std'sine etki
// özeti (ml.ProblemImpactSummary). Olaylar servise göre bir kez gruplanır;
// detay ucu kendi okumasını (impactEvs) getirir. Yalnız gösterim: öncelik,
// güven ve bildirim alanlarına dokunmaz.
func attachInboxImpacts(items []InboxItem, evs []chstore.MLEvent) {
	bySvc := map[string][]chstore.MLEvent{}
	for _, e := range evs {
		bySvc[e.Service] = append(bySvc[e.Service], e)
	}
	for i := range items {
		it := &items[i]
		p := it.stdSrc.problemOrNil()
		if p == nil || it.Std == nil {
			continue
		}
		pevs := bySvc[p.Service]
		if it.stdSrc.impactLoaded {
			pevs = it.stdSrc.impactEvs
		}
		it.Std.ImpactSummary = ml.ProblemImpactSummary(*p, pevs)
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
	// v0.10.2473 — gövde görünüm kipine bağlı (köken alanı): anahtar kipi taşır.
	key := problemStandardKey(ref) + inboxGraphKeySuffix(chstore.ProblemGraphMergeOn())
	s.serveCached(w, r, key, problemStandardTTL, func(ctx context.Context) (any, error) {
		src, incProblems, err := s.loadStdSrc(ctx, kind, id)
		if err != nil {
			return nil, err
		}
		if src == nil {
			// Bulunamadı: null gövde — başlık bileşeni sessizce çizilmez,
			// sayfanın geri kalanı (kendi 404'üyle) aynen çalışır.
			return nil, nil
		}
		if src.problem != nil {
			// v0.10.2383 "Neden"; v0.10.2482 etki özeti aynı olaylardan.
			src.impactRule, src.impactEvs = s.problemImpactRule(ctx, *src.problem)
			src.impactLoaded = true
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
			// v0.10.2473 — bağlı kümenin kökeni (liste satırıyla aynı alan,
			// aynı kural; kip on değilse / köken yoksa nil).
			std.Origin = s.problemGraphOrigin(ctx, src.problem.ID)
		}
		return items[0].Std, nil
	})
}

// stdImpactEventsCap — "Neden" okumasının satır tavanı: tek servis × tek
// dedektör, en yeniler (bir Problem'in bölümünde birkaç epizot olur).
const stdImpactEventsCap = 20

// stdImpactRead — detay başlığının "Neden" gerçekleri için okuma planı.
type stdImpactRead struct {
	filter  chstore.MLEventsFilter
	measure string // beklenen StdImpactRule.Measure (Problem'in KENDİ ölçüsü)
	lead    string // grup Problem'i: lider olay kimliği ("" = servis Problem'i)
	lr      ml.ImpactLeadRead
}

// stdImpactReadFor — SAF (v0.10.2383; v0.10.2391 yanıt süresi + consumer
// lag): detay başlığı etki kuralı gerçeklerini okusun mu, hangi okumayla.
// Satırlar Problem'in KENDİ ölçüsünü açıklar:
//
//   - ML servis Problem'i (`anomaly-auto:svc:`), ölçü hata oranı ya da yanıt
//     süresi (p50_ms / p90_ms): servis × dedektör, en yeni ≤ 20 olay.
//   - Tek sinyal kolunun grup Problem'i (`anomaly-auto:group:<olay>`, consumer
//     lag): kural kimliğindeki lider olay, birincil anahtarla TEK satır.
//
// Başka ölçü (yaygın yavaşlama p95_ms), ≥ 2 sinyalli grup, eski aile sahibi:
// okuma yok, bugünkü cümle. Kapanmış Problem'de de okunmaz: kanıt olayın SON
// tikinin penceresidir (kapanmadan önceki sönen pencere) — "son 5 dakikanın
// 1'inde" bitmiş bir Problem için doğru değildir.
func stdImpactReadFor(p chstore.Problem, now time.Time) (stdImpactRead, bool) {
	// v0.10.2482 — plan ml.ImpactLeadReadOf'ta (etki özeti aynı planı okur).
	lr, ok := ml.ImpactLeadReadOf(p)
	if !ok {
		return stdImpactRead{}, false
	}
	since := stdImpactEventsSince(p.StartedAt, now)
	if lr.LeadID != "" {
		return stdImpactRead{filter: chstore.MLEventsFilter{ID: lr.LeadID, Service: p.Service, Since: since, Limit: 1},
			measure: lr.Measure, lead: lr.LeadID, lr: lr}, true
	}
	return stdImpactRead{filter: chstore.MLEventsFilter{Service: p.Service, Detectors: lr.Detectors, Since: since, Limit: stdImpactEventsCap},
		measure: lr.Measure, lr: lr}, true
}

// stdImpactFactsOf — SAF: okunan olaylardan gerçekler (ml.ImpactLeadEvent:
// servis Problem'i → Problem'e bağlı EN YENİ uygun olay; grup Problem'i →
// lider olay). Ölçü Problem'inkiyle uyuşmazsa nil (bugünkü cümle).
func stdImpactFactsOf(rd stdImpactRead, p chstore.Problem, evs []chstore.MLEvent) *chstore.StdImpactRule {
	_, f, _ := ml.ImpactLeadEvent(rd.lr, p, evs)
	return f
}

// stdImpactEventsSince — SAF: okuma penceresinin başı (olay çizelgesiyle aynı:
// Problem başlangıcı − ProblemEventSlack, en çok 30 gün geri).
func stdImpactEventsSince(startedAt int64, now time.Time) time.Time {
	since := time.Unix(0, startedAt).Add(-chstore.ProblemEventSlack)
	if floor := now.Add(-30 * 24 * time.Hour); since.Before(floor) {
		since = floor
	}
	return since
}

// problemImpactRule — detay başlığının "Neden" gerçekleri (stdImpactReadFor
// planı, ml.ImpactRuleFacts). Mevcut okuyucu (ListMLEvents), olay
// çizelgesinin okumasından DAR: servis × dedektör × ≤ 20 satır ya da tek
// olay kimliği; yalnız detay ucunda, problemStandardTTL önbelleğinin
// arkasında. Okuma düşerse gerçek yok → bugünkü tek cümle (hata dönmez).
func (s *Server) problemImpactRule(ctx context.Context, p chstore.Problem) (*chstore.StdImpactRule, []chstore.MLEvent) {
	if s.store == nil {
		return nil, nil
	}
	rd, ok := stdImpactReadFor(p, time.Now())
	if !ok {
		return nil, nil
	}
	evs, err := s.store.ListMLEvents(ctx, rd.filter)
	if err != nil {
		log.Printf("[problem-standard] etki kuralı gerçekleri okunamadı %s (tek cümleyle devam): %v", p.ID, err)
		return nil, nil
	}
	return stdImpactFactsOf(rd, p, evs), evs
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
