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
// üretmez (evaluator terfi yoluyla aynı). Incident bağlama açılışta,
// anomaly_promotion.attachToIncident vidasıyla (terfi yoluyla aynı).

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

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
	Open    []chstore.Problem // yeni: incident bağlama + bildirim
	Refresh []chstore.Problem // tam satır, operatör alanları taşınmış
	Resolve []chstore.Problem
	// LiveGroup — grup kimliği → Problem kimliği (üye işareti "Problem açtı").
	LiveGroup map[string]string
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

// planGroupProblems — SAF: kararlar + açık grup Problem'leri → eylemler.
// groups: bu tikte karar verilen gruplar (decideGroups sırası); open: açık
// anlık görüntü (önek dışı satırlar yok sayılır); canResolve: kapanış izni
// (lider sürekliliği). desc: grup → Problem cümlesi.
func planGroupProblems(groups []decidedGroup, open []chstore.Problem, canResolve bool, desc func(decidedGroup) string) groupProblemPlan {
	plan := groupProblemPlan{LiveGroup: map[string]string{}}
	var own []chstore.Problem
	for _, p := range open {
		if strings.HasPrefix(p.RuleID, MLGroupRulePrefix) && p.Status != "resolved" {
			own = append(own, p)
		}
	}
	sort.Slice(own, func(i, j int) bool {
		if own[i].StartedAt != own[j].StartedAt {
			return own[i].StartedAt < own[j].StartedAt
		}
		return own[i].ID < own[j].ID
	})
	bySvc := map[string]*chstore.Problem{}
	for i := range own {
		if bySvc[own[i].Service] == nil {
			bySvc[own[i].Service] = &own[i]
		}
	}
	touched := map[string]bool{} // Problem kimliği → bu tik tazelendi/açıldı
	for _, g := range groups {
		if g.D.Finished {
			continue
		}
		svc := g.G.Service
		if p := bySvc[svc]; p != nil {
			plan.LiveGroup[g.G.ID] = p.ID
			if touched[p.ID] {
				continue
			}
			touched[p.ID] = true
			q := *p // tam satır: Status (ack), Assignee, Pod, AISummary … taşınır
			if g.D.WouldOpen {
				n := float64(len(g.D.Signals))
				q.Value, q.Threshold, q.Description = n, n, desc(g)
			}
			plan.Refresh = append(plan.Refresh, q)
			continue
		}
		if !g.D.WouldOpen {
			continue
		}
		p := newGroupProblem(g, desc(g))
		plan.Open = append(plan.Open, p)
		bySvc[svc] = &plan.Open[len(plan.Open)-1]
		touched[p.ID] = true
		plan.LiveGroup[g.G.ID] = p.ID
	}
	if !canResolve {
		return plan
	}
	for _, p := range own {
		if touched[p.ID] {
			continue
		}
		q := p
		if !strings.Contains(q.Description, groupResolveReason) {
			q.Description += groupResolveReason
		}
		plan.Resolve = append(plan.Resolve, q)
	}
	return plan
}

// liveCounts — tik sayaçları (MLTickStatus).
type liveCounts struct{ Opened, Refreshed, Resolved int64 }

// liveGroupProblems — recorder tik adımı: açık anlık görüntüyü TEK kez
// okur (OpenProblemsSnapshot, 5 sn memo), planı uygular, canlı kararları
// gruplara işler. Anlık görüntü okunamazsa HİÇBİR ŞEY yapılmaz (yinelenen
// Problem ya da kör kapanış, kaçırılmış bir tikten kötü).
func (r *Recorder) liveGroupProblems(ctx context.Context, groups []decidedGroup, canResolve bool, desc func(decidedGroup) string, now time.Time) (liveCounts, error) {
	var c liveCounts
	snap, err := r.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		return c, fmt.Errorf("ml group problems: open snapshot: %w", err)
	}
	var open []chstore.Problem
	for _, p := range snap.All() {
		if p != nil && strings.HasPrefix(p.RuleID, MLGroupRulePrefix) {
			open = append(open, *p)
		}
	}
	plan := planGroupProblems(groups, open, canResolve, desc)
	failed := map[string]bool{}
	var firstErr error
	for _, p := range plan.Refresh {
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			failed[p.ID] = true
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Refreshed++
	}
	attach := len(plan.Open) > 0 && r.store.GetAnomalyPromotion(ctx).AttachesToIncident()
	for _, p := range plan.Open {
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			failed[p.ID] = true
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Opened++
		log.Printf("[ml] GROUP PROBLEM OPENED %s · %s · %s", p.Service, p.ID, p.Description)
		if attach {
			if _, err := r.store.AttachProblemToIncident(ctx, p); err != nil {
				log.Printf("[ml] group problem incident attach %s: %v", p.ID, err)
			}
		}
		if r.notifier != nil {
			go r.notifier.SendProblemAlert(context.Background(), p)
		}
	}
	for _, p := range plan.Resolve {
		chstore.MarkResolved(&p, now.UnixNano())
		if err := r.store.UpsertProblem(ctx, p); err != nil {
			firstErr = firstNonNil(firstErr, err)
			continue
		}
		c.Resolved++
		log.Printf("[ml] GROUP PROBLEM RESOLVED (anomali grubu bitti) %s · %s", p.Service, p.ID)
	}
	markLiveGroups(groups, plan.LiveGroup, failed)
	return c, firstErr
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

func firstNonNil(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
