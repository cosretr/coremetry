package ml

// group_live.go — v0.10.2052 (anomali tekilleştirme D4, operatör onayı
// 2026-11-12): ML grubu gerçek Problem açar. Karar group_gate.go
// DecideGroupPromotion'dan (gölgenin AYNI işlevi); bu dosya yalnız Problem
// yaşam döngüsünü planlar (SAF) ve uygular.
//
// KİMLİK / TEKİLLEŞTİRME: Problem kimliği = kural kimliği + ":" + servis,
// kural = `anomaly-auto:group:<grubun ilk olayı>`. Aynı servisin AÇIK grup
// Problem'i varsa yenisi açılmaz, o tazelenir: grup kimliği lider değişimi
// (koşu durumu sıfırlanır) ya da gruba daha erken başlamış bir olayın
// katılmasıyla kaysa da servis başına tek Problem kalır. Durum yalnız
// problems tablosunda (açık anlık görüntü); yeni tablo/ayar yok.
//
// KAPANIŞ: servisin karar verilen (açık üyeli) grubu kalmadığında Problem
// "anomali grubu bitti" gerekçesiyle kapanır — yalnız lider ≥ 3 ardışık tik
// koştuktan sonra (yeni liderin boş koşu durumu "bitti" sanılmasın; seyrek
// dedektör/ yaşam döngüsü dersi, /aiops §2 soru 1). Bayat süpürme
// (evaluator) grup Problem'ini ML tikinin 3 katı + pay boyunca süpürmez
// (chstore.MLGroupSweepExempt); ML durursa "source silent" ile kapanır.
//
// BİLDİRİM: yalnız AÇILIŞTA, mevcut hattan (notify.SendProblemAlert; tür
// "anomaly" — chstore.ProblemNotifyKind). Tazeleme ve kapanış bildirim
// üretmez (evaluator terfi yoluyla aynı). Tek istisna (v0.10.2164, D5): ML
// üyesi yabancı sahibi P1'e çıkarırsa bir kez "yükseldi" (ownerEscalated). Incident bağlama yok (Incidents
// emekli; kapı v0.10.2070'te silindi).

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// ProblemNotifier — mevcut Problem bildirim hattı (notify.Notifier).
type ProblemNotifier interface {
	SendProblemAlert(ctx context.Context, p chstore.Problem)
}

// groupProblemMetric — Problem'in ölçüsü: farklı sinyal sayısı.
const groupProblemMetric = "ml_group_signals"

// groupProblemRuleName — "ML · " öneki manşeti açıklamadan alır
// (chstore.MLPromotedHeadline): başlık = düz Türkçe cümle.
const groupProblemRuleName = chstore.MLPromotedRuleNamePrefix + "anomali grubu"

// groupResolveReason — kapanış gerekçesi (açıklamaya eklenir).
const groupResolveReason = " · kendiliğinden kapandı: anomali grubu bitti"

// signalName — SAF: sinyalin Türkçe adı (kartın signalTR haritası; "Trace",
// "Log", "Metrik"); bilinmeyen adı aynen.
func signalName(s string) string {
	if v, ok := signalTR[s]; ok {
		return v
	}
	return s
}

// joinTR — SAF: "a", "a ve b", "a, b ve c".
func joinTR(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " ve " + xs[len(xs)-1]
}

// groupProblemDescription — SAF: Problem cümlesi. Önce servis + kaç sinyal,
// sonra sinyal başına en güçlü üyenin kart özeti (liste/detayla AYNI cümle,
// servis öneki atılır). summary: üye → ExplainSummary.
func groupProblemDescription(g decidedGroup, summary func(chstore.MLEvent) string) string {
	best := map[string]chstore.MLEvent{}
	for _, e := range g.Members {
		if e.ID == "" {
			continue
		}
		sig := memberSignal(e)
		if !containsStr(g.D.Signals, sig) {
			continue // batch kuralının saymadığı sinyal cümleye girmez
		}
		cur, ok := best[sig]
		if !ok || eventRatio(e) > eventRatio(cur) || (eventRatio(e) == eventRatio(cur) && e.ID < cur.ID) {
			best[sig] = e
		}
	}
	sigs := make([]string, 0, len(g.D.Signals))
	for _, s := range g.D.Signals {
		sigs = append(sigs, strings.ToLower(signalName(s)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: aynı anda %d farklı sinyalde anomali var (%s).", g.G.Service, len(g.D.Signals), joinTR(sigs))
	for _, s := range g.D.Signals {
		e, ok := best[s]
		if !ok {
			continue
		}
		line := strings.TrimSpace(strings.TrimPrefix(summary(e), e.Service+": "))
		if line == "" {
			continue
		}
		fmt.Fprintf(&b, " %s: %s", signalName(s), line)
	}
	return truncRunes(b.String(), 600)
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// groupProblemPlan — bir tikin Problem eylemleri.
type groupProblemPlan struct {
	Open    []chstore.Problem // yeni: bildirim
	Refresh []chstore.Problem // tam satır, operatör alanları taşınmış
	Resolve []chstore.Problem
	// LiveGroup — grup kimliği → Problem kimliği (üye işareti "Problem açtı").
	LiveGroup map[string]string
	// SingleOpen — v0.10.2092: tek sinyal kolundan AÇILAN Problem kimlikleri (sayaç).
	SingleOpen map[string]bool
	// LiveSvc — v0.10.2130: servis düzeyi olay kimliği → bağlandığı Problem
	// (kendi `anomaly-auto:svc:` satırı ya da servisin grup Problem'i).
	LiveSvc map[string]string
	// SvcOpen — v0.10.2130: servis düzeyi kararla AÇILAN Problem kimlikleri (sayaç).
	SvcOpen map[string]bool
	// Raised — v0.10.2155: bu tikte ML üyesinin önceliğini yükselttiği (ya da
	// ikizinin atananını devraldığı) YABANCI sahip kimlikleri; satırları Refresh'te.
	Raised []string
	// Merged — v0.10.2155: yabancı sahibe katılıp kapanan ML ikizleri (Resolve'da).
	Merged []string
}

// newGroupProblem — SAF: açılacak Problem satırı.
func newGroupProblem(g decidedGroup, desc string) chstore.Problem {
	n := float64(len(g.D.Signals))
	return chstore.Problem{
		ID: g.D.RuleID + ":" + g.G.Service, RuleID: g.D.RuleID, RuleName: groupProblemRuleName,
		Severity: g.D.Severity, Service: g.G.Service, Metric: groupProblemMetric, Comparator: ">=",
		Value: n, Threshold: n, Status: "open", Description: desc, StartedAt: g.G.Start,
	}
}

// planGroupProblems — SAF: yalnız grup kararları (servis düzeyi girdi yok);
// planMLProblems'in v0.10.2052 imzası.
func planGroupProblems(groups []decidedGroup, open []chstore.Problem, canResolve bool, desc func(decidedGroup) string) groupProblemPlan {
	return planMLProblems(groups, nil, open, canResolve, desc)
}

// planMLProblems — planMLProblemsWith'in sıfır politikalı hali (statik
// aileler, susturma yok); testlerin v0.10.2130 imzası.
func planMLProblems(groups []decidedGroup, svcs []svcProblemIn, open []chstore.Problem, canResolve bool, desc func(decidedGroup) string) groupProblemPlan {
	return planMLProblemsWith(groups, svcs, open, canResolve, desc, chstore.OwnerPolicy{})
}

// planMLProblemsWith — SAF: grup + servis düzeyi kararlar + açık Problem'ler
// → eylemler. groups: bu tikte karar verilen gruplar (decideGroups sırası);
// svcs: servis düzeyi canlı girdiler (svcProblemInputs); open: açık anlık
// görüntü (TÜM aileler — sahip çözümü için; kapanış yalnız ML ailesine);
// canResolve: kapanış izni (lider sürekliliği). desc: grup → Problem cümlesi.
//
// v0.10.2130 (spec Karar 4) — SERVİS BAŞINA TEK AÇIK ML PROBLEM'İ:
// `anomaly-auto:group:*` ve `anomaly-auto:svc:*` aynı anahtarı (servis)
// paylaşır. Önce açan sahiptir; sonra gelen üye olur (yeni Problem yok),
// satır tam taşınır. Aynı tikte grup önce işlenir (gölge kararıyla aynı:
// grup varsa svc üye). Value/Threshold yalnız DAHA YÜKSEK oranla
// güncellenir (öncelik yalnız yükselir; grubun sayım satırı oran 1). Sahip
// svc ise açıklama her tik güncel cümle. Kapanış: servisin ne bitmemiş
// grubu ne açık svc olayı kalınca (mevcut 3 tik lider sürekliliği kuralı).
//
// v0.10.2155 (Davis tarzı tek Problem D1) — sahip artık ML ailesiyle sınırlı
// değil: servisin sahibi chstore.ServiceOwnerOf (en eski açık, sahip
// olabilen satır — svc-slowdown / anomaly:<svc>:error_rate dahil). Sahip
// ESKİ bir aileyse ML kararı ona ÜYE olur: yeni Problem ve bildirim yok,
// olay kanıtına o Problem'in kimliği damgalanır (LiveGroup / LiveSvc).
//   - yalnız "açardı" kararı (WouldOpen) üye olur: tek başına Problem
//     açmayacak zayıf bir grup eski sahibin kapanışını geciktirmez;
//   - sahibin satırı YALNIZ öncelik yükseliyorsa yazılır (chstore.RaiseOwner:
//     şiddet + ölçü birlikte); değişmeyen yabancı satır yeniden yazılmaz
//     (iki süreçli tam-satır yazım yarışı en aza iner);
//   - servisin ML satırı da açıksa (ikiz) o satır tazelenmez ve "merged into
//     <sahip>" ile kapanır, atananı sahip boşsa taşınır;
//   - ML kuralı "problem değil" imzalıysa üye OLMAZ: bugünkü gibi kendi
//     satırında kalır / kendi satırını açar.
func planMLProblemsWith(groups []decidedGroup, svcs []svcProblemIn, open []chstore.Problem, canResolve bool, desc func(decidedGroup) string, pol chstore.OwnerPolicy) groupProblemPlan {
	plan := groupProblemPlan{LiveGroup: map[string]string{}, SingleOpen: map[string]bool{}, LiveSvc: map[string]string{}, SvcOpen: map[string]bool{}}
	var own []chstore.Problem
	var ptrs []*chstore.Problem
	byID := map[string]chstore.Problem{}
	for i := range open {
		p := open[i]
		if p.Status == "resolved" {
			continue
		}
		ptrs = append(ptrs, &open[i])
		byID[p.ID] = p
		if chstore.IsMLLiveProblem(p.RuleID) {
			own = append(own, p)
		}
	}
	sort.Slice(own, func(i, j int) bool {
		if own[i].StartedAt != own[j].StartedAt {
			return own[i].StartedAt < own[j].StartedAt
		}
		return own[i].ID < own[j].ID
	})
	mlRow := map[string]string{} // servis → servisin en eski ML satırı
	for _, p := range own {
		if _, ok := mlRow[p.Service]; !ok {
			mlRow[p.Service] = p.ID
		}
	}
	owners := map[string]*chstore.Problem{} // servis → sahip (nil = yok); tik içi bellek
	ownerOf := func(svc string) *chstore.Problem {
		o, ok := owners[svc]
		if !ok {
			o = chstore.ServiceOwnerOf(ptrs, svc, pol)
			owners[svc] = o
		}
		return o
	}
	opened := map[string]string{} // bu tikte açılan: servis → Problem kimliği
	// target — kararın bağlanacağı açık satır: ML ailesinden sahip (bugünkü
	// yol), yabancı sahip (üyelik; kural susturulmamışsa) ya da servisin ML
	// satırı. ok=false → açık satır yok.
	target := func(svc, ruleID string) (id string, foreign, ok bool) {
		if id, ok := opened[svc]; ok {
			return id, false, true
		}
		if o := ownerOf(svc); o != nil {
			if chstore.IsMLLiveProblem(o.RuleID) {
				return o.ID, false, true
			}
			if pol.MemberRule(ruleID, svc) {
				return o.ID, true, true
			}
		}
		if id, ok := mlRow[svc]; ok {
			return id, false, true
		}
		return "", false, false
	}
	openIdx, refreshIdx := map[string]int{}, map[string]int{}
	// row — bu tikte dokunulan ML satırı (yoksa açık satırdan tazeleme kopyası).
	row := func(id string) *chstore.Problem {
		if i, ok := openIdx[id]; ok {
			return &plan.Open[i]
		}
		if i, ok := refreshIdx[id]; ok {
			return &plan.Refresh[i]
		}
		plan.Refresh = append(plan.Refresh, byID[id]) // tam satır: Status (ack), Assignee, Pod, AISummary … taşınır
		refreshIdx[id] = len(plan.Refresh) - 1
		return &plan.Refresh[len(plan.Refresh)-1]
	}
	foreign := map[string]chstore.Problem{} // yabancı sahip → yükseltilmiş kopya (yalnız değiştiyse)
	raise := func(ownerID string, member chstore.Problem) {
		cur, ok := foreign[ownerID]
		if !ok {
			cur = byID[ownerID]
		}
		if next, changed := chstore.RaiseOwner(cur, member, pol); changed {
			foreign[ownerID] = next
		}
	}
	for _, g := range groups {
		if g.D.Finished {
			continue
		}
		svc := g.G.Service
		id, isForeign, ok := target(svc, g.D.RuleID)
		if ok && isForeign {
			if g.D.WouldOpen {
				plan.LiveGroup[g.G.ID] = id
				raise(id, newGroupProblem(g, desc(g)))
			}
			continue
		}
		if ok {
			plan.LiveGroup[g.G.ID] = id
			_, fresh := openIdx[id]
			_, done := refreshIdx[id]
			if fresh || done {
				continue
			}
			q := row(id)
			if g.D.WouldOpen && chstore.IsMLGroupProblem(q.RuleID) {
				q.Description = desc(g)
				if mlProblemRatio(q.Value, q.Threshold) <= 1 { // svc'nin yükselttiği oran korunur
					n := float64(len(g.D.Signals))
					q.Value, q.Threshold, q.Metric = n, n, groupProblemMetric
				}
			}
			continue
		}
		if !g.D.WouldOpen {
			continue
		}
		p := newGroupProblem(g, desc(g))
		if g.D.SingleWouldOpen {
			plan.SingleOpen[p.ID] = true
		}
		plan.Open = append(plan.Open, p)
		openIdx[p.ID] = len(plan.Open) - 1
		opened[svc] = p.ID
		plan.LiveGroup[g.G.ID] = p.ID
	}
	for _, in := range svcs {
		id, isForeign, ok := target(in.Service, MLSvcRulePrefix+in.Service)
		switch {
		case !ok:
			p := newSvcProblem(in)
			plan.Open = append(plan.Open, p)
			openIdx[p.ID] = len(plan.Open) - 1
			plan.SvcOpen[p.ID] = true
			opened[in.Service] = p.ID
			id = p.ID
		case isForeign:
			raise(id, newSvcProblem(in))
		default:
			q := row(id)
			if mlProblemRatio(in.Value, in.Threshold) > mlProblemRatio(q.Value, q.Threshold) {
				q.Value, q.Threshold, q.Metric = in.Value, in.Threshold, in.Metric
			}
			if chstore.IsMLSvcProblem(q.RuleID) {
				q.Description = in.Desc
			}
		}
		for _, ev := range in.Events {
			plan.LiveSvc[ev] = id
		}
	}
	if canResolve {
		for _, p := range own {
			if _, ok := refreshIdx[p.ID]; ok {
				continue
			}
			// İkiz: servisin yabancı bir sahibi var ve bu ML satırı ona
			// katılabilir → "merged into", atanan sahip boşsa taşınır.
			if o := ownerOf(p.Service); o != nil && o.ID != p.ID && !chstore.IsMLLiveProblem(o.RuleID) && pol.MemberRule(p.RuleID, p.Service) {
				cur, ok := foreign[o.ID]
				if !ok {
					cur = *o
				}
				next, changed, closed := chstore.MergeInto(cur, p, pol)
				if changed {
					foreign[o.ID] = next
				}
				plan.Resolve = append(plan.Resolve, closed)
				plan.Merged = append(plan.Merged, closed.ID)
				continue
			}
			q := p
			reason := groupResolveReason
			if chstore.IsMLSvcProblem(q.RuleID) {
				reason = svcResolveReason
			}
			if !strings.Contains(q.Description, reason) {
				q.Description += reason
			}
			plan.Resolve = append(plan.Resolve, q)
		}
	}
	ids := make([]string, 0, len(foreign))
	for id := range foreign {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		plan.Refresh = append(plan.Refresh, foreign[id])
		plan.Raised = append(plan.Raised, id)
	}
	return plan
}

// svcResolveReason — v0.10.2130: servis düzeyi Problem'in kapanış gerekçesi.
const svcResolveReason = " · kendiliğinden kapandı: servis anomalisi bitti"

// liveCounts — tik sayaçları (MLTickStatus).
type liveCounts struct{ Opened, Refreshed, Resolved, SingleOpened, SvcOpened int64 }

// liveGroupProblems — recorder tik adımı: açık anlık görüntüyü TEK kez
// okur (OpenProblemsSnapshot, 5 sn memo), planı uygular, canlı kararları
// gruplara işler. Anlık görüntü okunamazsa HİÇBİR ŞEY yapılmaz (yinelenen
// Problem ya da kör kapanış, kaçırılmış bir tikten kötü).
//
// v0.10.2130: servis düzeyi kararlar (svcDec; nil = yok) aynı plana girer;
// başarılı yazımın kararlarına Live + ProblemID işlenir (markLiveSvc).
//
// v0.10.2155 (Davis tarzı tek Problem D1): plan TÜM açık satırları görür —
// servisin sahibi eski bir aileyse (svc-slowdown / anomaly:<svc>:error_rate)
// ML kararı ona üye olur (planMLProblemsWith). Eski "çift kayıt" notu
// (legacyTwinNote) kalktı: çift satır artık açılmıyor.
func (r *Recorder) liveGroupProblems(ctx context.Context, groups []decidedGroup, kept []chstore.MLEvent, svcDec map[string]SvcShadow, canResolve bool, desc func(decidedGroup) string, now time.Time) (liveCounts, error) {
	var c liveCounts
	snap, err := r.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		return c, fmt.Errorf("ml group problems: open snapshot: %w", err)
	}
	var all []chstore.Problem
	for _, p := range snap.All() {
		if p != nil {
			all = append(all, *p)
		}
	}
	all, pol := r.ownerPolicy(ctx, all, now)
	svcs := svcProblemInputs(kept, svcDec)
	plan := planMLProblemsWith(groups, svcs, all, canResolve, desc, pol)
	raised, merged := map[string]bool{}, map[string]bool{}
	for _, id := range plan.Raised {
		raised[id] = true
	}
	for _, id := range plan.Merged {
		merged[id] = true
	}
	failed := map[string]bool{}
	var firstErr error
	for _, p := range plan.Refresh {
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			failed[p.ID] = true
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Refreshed++
		if raised[p.ID] {
			log.Printf("[ml] PROBLEM RAISED BY MEMBER %s · %s · %s (ML üyesi sahibin önceliğini / atananını güncelledi)", p.Service, p.ID, p.Severity)
			// v0.10.2164 (D5) — P1'e geçişte tek "yükseldi" bildirimi (eski
			// yazarlarla aynı gövde, anomaly.EscalationAlert).
			if before := snap.ByID(p.ID); ownerEscalated(before, p, pol) {
				var send func(context.Context, chstore.Problem)
				if r.notifier != nil {
					send = r.notifier.SendProblemAlert
				}
				anomaly.EscalationAlert("ml", r.dedup, send)(*before, p)
			}
		}
	}
	for _, p := range plan.Open {
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			failed[p.ID] = true
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Opened++
		if plan.SingleOpen[p.ID] {
			c.SingleOpened++
		}
		if plan.SvcOpen[p.ID] {
			c.SvcOpened++
		}
		log.Printf("[ml] ML PROBLEM OPENED %s · %s · %s", p.Service, p.ID, p.Description)
		if r.notifier != nil {
			go r.notifier.SendProblemAlert(context.Background(), p)
		}
	}
	// v0.10.2157 (D3) — kapanış = üyeden ayrılma: ML sinyali biten Problem'in
	// canlı üyesi (eski dedektörün üye olayı) varsa satır açık kalır ve aynen
	// tazelenir; ML her tik yeniden dener, son üye de bitince kapanır.
	closeNow, held := splitHeldResolves(plan.Resolve, merged, func(p chstore.Problem) bool { return r.holdResolve(ctx, p, now) })
	for _, p := range held {
		if o := snap.ByID(p.ID); o != nil {
			if err := r.store.UpsertProblem(ctx, *o); err != nil {
				firstErr = firstNonNil(firstErr, err)
				continue
			}
			c.Refreshed++
		}
		log.Printf("[ml] ML PROBLEM HELD OPEN %s · %s — ML sinyali bitti, canlı üyesi var", p.Service, p.ID)
	}
	for _, p := range closeNow {
		chstore.MarkResolved(&p, now.UnixNano())
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Resolved++
		if merged[p.ID] {
			log.Printf("[ml] PROBLEM MERGED %s · %s → servisin sahibi (ikiz satır kapandı)", p.Service, p.ID)
			continue
		}
		log.Printf("[ml] ML PROBLEM RESOLVED %s · %s", p.Service, p.ID)
	}
	markLiveGroups(groups, plan.LiveGroup, failed)
	markLiveSvc(svcDec, plan.LiveSvc, failed)
	return c, firstErr
}

// splitHeldResolves — SAF (v0.10.2157): kapanış adaylarını "şimdi kapat" ve
// "canlı üyesi var, açık tut" diye ayırır. Yabancı sahibe katılıp kapanan
// ikiz (merged) asla tutulmaz. Sıra korunur.
func splitHeldResolves(resolve []chstore.Problem, merged map[string]bool, live func(chstore.Problem) bool) (closeNow, held []chstore.Problem) {
	for _, p := range resolve {
		if !merged[p.ID] && live(p) {
			held = append(held, p)
			continue
		}
		closeNow = append(closeNow, p)
	}
	return closeNow, held
}

// holdResolve — ML Problem'inin canlı (eski dedektör) üyesi var mı; okuma
// hatası = bilinmiyor → tut (kör kapanış, bir tik gecikmeden kötü).
func (r *Recorder) holdResolve(ctx context.Context, p chstore.Problem, now time.Time) bool {
	live, err := anomaly.HasLiveMembers(ctx, r.store, p, now)
	if err != nil {
		log.Printf("[ml] group problems: canlı üyeler okunamadı %s (bu tik kapatılmıyor): %v", p.ID, err)
		return true
	}
	return live
}

// ownerPolicy — v0.10.2155: tikin birleşme politikası. Yabancı (ML dışı)
// birleşen satır yoksa ek okuma yok. "Problem değil" imzaları okunamazsa
// plan yalnız ML ailesini görür (v0.10.2130 davranışı): susturulmuş bir
// satırın altına ML Problem'i gizlemek, bir tik çift satırdan kötüdür.
func (r *Recorder) ownerPolicy(ctx context.Context, all []chstore.Problem, now time.Time) ([]chstore.Problem, chstore.OwnerPolicy) {
	prio := chstore.CurrentProblemPriority()
	pol := chstore.OwnerPolicy{Keep: prio.InboxKeepSourcePriorityList(), Priority: prio, NowNs: now.UnixNano()}
	foreign := false
	for _, p := range all {
		if !chstore.IsMLLiveProblem(p.RuleID) && chstore.MemberEligibleRule(p.RuleID) {
			foreign = true
			break
		}
	}
	if !foreign {
		return all, pol
	}
	list, err := r.store.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[ml] group problems: \"problem değil\" kararları okunamadı (bu tik yalnız ML ailesi): %v", err)
		return mlFamilyOnly(all), pol
	}
	pol.Noise = chstore.NoiseVerdictSignatures(list)
	return all, pol
}

// mlFamilyOnly — SAF: yalnız ML ailesinin satırları.
func mlFamilyOnly(all []chstore.Problem) []chstore.Problem {
	out := all[:0:0]
	for _, p := range all {
		if chstore.IsMLLiveProblem(p.RuleID) {
			out = append(out, p)
		}
	}
	return out
}

// markLiveGroups — SAF: yazımı başarılı Problem'in grubunun kararına
// Live + ProblemID işlenir (üye kanıtına damgalanır).
func markLiveGroups(groups []decidedGroup, live map[string]string, failed map[string]bool) {
	for i := range groups {
		if pid, ok := live[groups[i].G.ID]; ok && !failed[pid] {
			groups[i].D.Live, groups[i].D.ProblemID = true, pid
		}
	}
}

// ownerEscalated — SAF (v0.10.2164, D5): ML üyesinin yükselttiği sahip bu
// yazımla P1'e Mİ geçti. Önceki hali bilinmiyorsa (görüntüde yok) bildirim
// yok; zaten P1 ise yok (ek üye asla bildirmez).
func ownerEscalated(before *chstore.Problem, after chstore.Problem, pol chstore.OwnerPolicy) bool {
	return before != nil && chstore.EscalatedToP1(*before, after, pol)
}

func firstNonNil(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// singleProblemDescription — SAF (v0.10.2092): tek sinyal kolunun Problem
// cümlesi — düz Türkçe, tek güçlü sinyal olduğunu söyler, sinyali adıyla,
// değeri ve normaliyle verir. card: üye → kart girdisi (liste özetiyle aynı
// kaynak; birim / ölçü oradan).
func singleProblemDescription(g decidedGroup, card func(chstore.MLEvent) CardInput) string {
	var e chstore.MLEvent
	for _, m := range g.Members {
		if m.ID == g.D.SingleEvent {
			e = m
		}
	}
	if e.ID == "" {
		return fmt.Sprintf("%s: tek sinyalde güçlü ve süren kötüleşme (%s).", g.G.Service, strings.ToLower(signalName(joinTR(g.D.Signals))))
	}
	in := card(e)
	sp := specFor(in)
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	normal := "normalde hiç yok"
	if in.Baseline > 0 {
		normal = "normalde " + f.one(in.Baseline, metricS)
	}
	what := capFirst(fill(sp.fact, in))
	mins := int(in.Now.Sub(in.StartedAt).Minutes())
	return truncRunes(fmt.Sprintf("%s: tek güçlü sinyal (%s, başka sinyal teyit etmedi) — %s şimdi %s, %s; %d dakikadır sürüyor.",
		g.G.Service, strings.ToLower(signalName(memberSignal(e))), what, f.one(in.Observed, metricS), normal, mins), 600)
}
