package evaluator

// problem_merge.go — v0.10.2157 (Davis tarzı tek Problem D3, spec
// docs/plans/spec-davis-style-problem-merge.md): İKİZ GÖÇÜ + bayat süpürme
// muafiyeti.
//
// İKİZ GÖÇÜ (mergeServiceTwins, tikin SON pası): aynı serviste bir sahip ve
// ona katılabilecek başka açık satırlar varsa (sahip olabilen genç ikiz ya
// da yalnız-üye satır) gençler "merged into <sahip>" ile kapanır ve üyeye
// döner. Üç durumu tek kuralla karşılar:
//   - geçiş: bu sürümden önce prod'da açılmış çift satırlar ilk tikte birleşir
//     (veri göçü yok, elle iş yok);
//   - yarış: iki süreç (worker + sre) aynı pencerede ayrı satır açtıysa;
//   - sonradan gelen sahip: yalnız-üye bir satır (p99 / request_rate / terfi)
//     sahip yokken açılmış, sahip sonra gelmişse.
//
// Plan SAF ve anlık görüntüden türer (chstore.PlanServiceMerges) → idempotent:
// kapanan satır bir sonraki görüntüde yoktur. Yazım sırası güvenlik içindir:
// ÖNCE sahip (öncelik yükseltmesi / atanan), SONRA üye olayı ve gencin
// kapanışı — sahip yazılamazsa o servis bu tik birleşmez (yüksek öncelikli
// ikiz, önceliği taşınmadan kapanmaz). Bildirim YOK: kapanan ikiz açılırken
// kendi bildirimini göndermişti; yalnız-üye satır sahibi yükseltmez.
//
// Kapanan eski aile satırı üye olayına dönüşür (dedektörün açacağı satırla):
// dedektör bir sonraki tikinde üyeliğini kendi açık satırı gibi görür ve
// yaşam döngüsünü (histerezis, kapanış) kaldığı yerden sürdürür. ML ailesinin
// üyeliği kendi olaylarıdır (ML kaydedicisi damgalar) — üye olayı yazılmaz.
//
// "Problem değil" kararları okunamazsa bu tik birleşme YOK (susturulmuş bir
// satırın altına sinyal gizlemek, bir tik daha çift satırdan kötüdür).

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// mergeWrite — bir servisin birleşme yazımları (sıra: Owner → Events → Closed).
type mergeWrite struct {
	Service string
	OwnerID string
	Owner   *chstore.Problem // nil = sahip değişmedi, yazılmaz
	Events  []chstore.MLEvent
	Closed  []chstore.Problem
}

// planMergeWrites — SAF: birleşme planı → yazımlar. orig: kapanan satırın
// AÇIK hali (anlık görüntü; üye olayının taşıyacağı sanal satır). Eski aile
// satırı için üye olayı kurulur (başlangıç = satırın başlangıcı).
func planMergeWrites(merges []chstore.ServiceMerge, orig func(id string) *chstore.Problem, nowNs int64) []mergeWrite {
	out := make([]mergeWrite, 0, len(merges))
	for _, m := range merges {
		w := mergeWrite{Service: m.Owner.Service, OwnerID: m.Owner.ID, Closed: m.Merged}
		if m.OwnerChanged {
			o := m.Owner
			w.Owner = &o
		}
		for _, c := range m.Merged {
			det := chstore.MemberDetectorForRule(c.RuleID)
			row := orig(c.ID)
			if det == "" || row == nil {
				continue
			}
			w.Events = append(w.Events, chstore.NewMemberEvent(det, *row, m.OwnerBefore, row.StartedAt, nowNs, false))
		}
		out = append(out, w)
	}
	return out
}

// mergeServiceTwins — evaluateAll'ın son pası (bkz. dosya başı).
func (e *Evaluator) mergeServiceTwins(ctx context.Context) {
	snap, err := e.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		log.Printf("[evaluator] problem merge: açık problem anlık görüntüsü alınamadı, tik atlanıyor: %v", err)
		return
	}
	// v0.10.2274 (entity grafı S1) — salt gölge: aynı anlık görüntüyle graf
	// bağlarını ölçer, yalnız gölge blobunu yazar (problem_graph_shadow.go).
	e.problemGraphShadow(ctx, snap.All())
	now := time.Now()
	prio := chstore.CurrentProblemPriority()
	pol := chstore.OwnerPolicy{Keep: prio.InboxKeepSourcePriorityList(), Priority: prio, NowNs: now.UnixNano()}
	// Ucuz ön eleme: susturma bilinmeden de aday yoksa karar okuması gereksiz.
	if len(chstore.PlanServiceMerges(snap.All(), pol)) == 0 {
		return
	}
	list, err := e.store.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[evaluator] problem merge: \"problem değil\" kararları okunamadı (bu tik birleşme yok): %v", err)
		return
	}
	pol.Noise = chstore.NoiseVerdictSignatures(list)
	merged, services := e.applyMergeWrites(ctx, e.store, planMergeWrites(chstore.PlanServiceMerges(snap.All(), pol), snap.ByID, now.UnixNano()))
	if merged > 0 {
		log.Printf("[evaluator] problem merge: %d satır %d serviste sahibine katıldı (servis başına tek Problem)", merged, services)
	}
}

// applyMergeWrites — yazımları sırasıyla uygular; dönüş: kapanan satır ve
// birleşen servis sayısı.
func (e *Evaluator) applyMergeWrites(ctx context.Context, st anomaly.MemberStore, writes []mergeWrite) (merged, services int) {
	for _, w := range writes {
		if w.Owner != nil {
			if err := st.UpsertProblem(ctx, *w.Owner); err != nil {
				log.Printf("[evaluator] problem merge: sahip yazılamadı %s (servis bu tik birleşmedi): %v", w.OwnerID, err)
				continue
			}
			log.Printf("[evaluator] PROBLEM RAISED BY MERGE %s · %s (%s; ikizin önceliği / atananı sahibe taşındı)", w.Service, w.OwnerID, w.Owner.Severity)
		}
		for _, ev := range w.Events {
			if err := st.UpsertMLEvent(ctx, ev); err != nil {
				// Üye olayı yazılamadı: satır yine kapanır; dedektör hâlâ
				// ateşliyorsa bir sonraki tikinde sahibe kendisi katılır.
				log.Printf("[evaluator] problem merge: üye olayı yazılamadı %s: %v", w.OwnerID, err)
			}
		}
		n := 0
		for _, c := range w.Closed {
			if err := st.UpsertProblem(ctx, c); err != nil {
				log.Printf("[evaluator] problem merge: %s kapatılamadı: %v", c.ID, err)
				continue
			}
			n++
			log.Printf("[evaluator] PROBLEM MERGED %s · %s (%s) → %s", c.Service, c.ID, c.RuleID, w.OwnerID)
		}
		if n > 0 {
			merged += n
			services++
		}
	}
	return merged, services
}

// splitLiveOwners — SAF: bayat listeden, canlı üyesi olan SAHİP satırları
// ayırır (süpürülmez). Yalnız sahip olabilen aileler sorulur (nokta okuması
// seyrek kalsın); okuma hatası = bilinmiyor → bu tur muaf (kör kapanış,
// bir tik gecikmeden kötü). Sıra korunur.
func splitLiveOwners(stale []chstore.Problem, live func(chstore.Problem) (bool, error)) (sweep, exempt []chstore.Problem) {
	for _, p := range stale {
		if p.Service != "" && chstore.OwnerEligibleRule(p.RuleID) {
			if ok, err := live(p); ok || err != nil {
				exempt = append(exempt, p)
				continue
			}
		}
		sweep = append(sweep, p)
	}
	return sweep, exempt
}

// dropLiveOwners — v0.10.2157: canlı üyesi olan sahip bayat süpürmeyle
// kapanmaz. Sahibin kendi dedektörü susmuş olabilir (ör. ML durdu, eski
// dedektör veri bulamıyor) ama üyeleri sürüyor: Problem onlarla yaşar.
// Üyeler de susunca üye olayları 20 dk'da bayatlar ve satır süpürülür.
func (e *Evaluator) dropLiveOwners(ctx context.Context, stale []chstore.Problem) []chstore.Problem {
	now := time.Now()
	sweep, exempt := splitLiveOwners(stale, func(p chstore.Problem) (bool, error) {
		return anomaly.HasLiveMembers(ctx, e.store, p, now)
	})
	if len(exempt) > 0 {
		log.Printf("[evaluator] stale sweep: %d sahip Problem atlandı (canlı üyesi var)", len(exempt))
	}
	return sweep
}
