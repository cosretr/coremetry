package anomaly

// problem_member.go — v0.10.2156 (Davis tarzı tek Problem D2, spec
// docs/plans/spec-davis-style-problem-merge.md): eski yazarlar (yaygın
// yavaşlama, metrik anomalisi, anomali terfisi) açık sahibe KATILIR.
//
// Kural (chstore/problem_owner.go): servisin açık bir sahibi varsa birleşen
// her aile ÜYEDİR — Problem satırı açmaz, bildirim göndermez; üyeliği bir
// ml_events satırıdır (chstore/problem_member.go). Sahip yoksa bugünkü gibi
// kendi satırını açar (sahip olabilen aile sahip olur; yalnız-üye aile kendi
// P3 satırını açar).
//
// DEDEKTÖR DEĞİŞMEZ: üye olayı dedektörün açacağı satırı taşır ve dedektör
// onu KENDİ AÇIK SATIRI gibi görür (Members.Row / Rows — "sanal satır").
// Histerezis, tazeleme ve kapanış kararları aynen çalışır; yalnız yazım yolu
// değişir (Members.Open / Write):
//
//	yeni satır açılacak     → Open:  sahip varsa üye olayı (+ sahibin önceliği
//	                                 yükseliyorsa sahip satırı), yoksa UpsertProblem
//	sanal satır tazelenecek → Write: üye olayının last_seen'i (bildirim yok)
//	sanal satır kapanacak   → Write: üye olayı stage=closed (üyeden ayrılma)
//	gerçek satır            → Write: UpsertProblem (bugünkü yol)
//
// YUMUŞAK HATA YÖNÜ: üye olayları okunamadıysa sanal satır yok ama katılma
// sürer (kimlik deterministik: yeniden katılma aynı satıra yazar; okuma
// kesintisi mevcut üyeleri yeni Problem + bildirim olarak geri açmaz).
// "Problem değil" kararları okunamadıysa YENİ katılma yok (bugünkü gibi
// kendi satırı; susturulmuş bir sahibin altına sinyal gizlenmez), mevcut
// üyelikler sürer.

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Üye dedektör adları (ml_events.detector).
const (
	MemberDetSvcSlowdown   = chstore.MLDetMemberSvcSlowdown
	MemberDetMetricAnomaly = chstore.MLDetMemberMetricAnomaly
	MemberDetPromotion     = chstore.MLDetMemberPromotion
)

// memberLookback — üye olayı okumasının penceresi (açık üye ≤ 20 dk taze).
const memberLookback = time.Hour

// memberReadCap — üye olayı okuma tavanı.
const memberReadCap = 2000

// MemberStore — Members'ın dokunduğu depo yüzeyi (*chstore.Store karşılar;
// testte sahte).
type MemberStore interface {
	UpsertProblem(ctx context.Context, p chstore.Problem) error
	UpsertMLEvent(ctx context.Context, e chstore.MLEvent) error
	ListMLEvents(ctx context.Context, f chstore.MLEventsFilter) ([]chstore.MLEvent, error)
	ListProblemVerdicts(ctx context.Context) ([]chstore.ProblemVerdict, error)
}

// Members — bir yazarın bir tikteki üyelik görünümü.
type Members struct {
	store    MemberStore
	detector string
	snap     *chstore.OpenProblems
	pol      chstore.OwnerPolicy
	now      time.Time
	// noiseOK — "problem değil" kararları okundu (pol.Noise güvenilir).
	noiseOK, noiseTried bool
	events              map[string]chstore.MLEvent // kural|servis → açık üye olayı
	rows                map[string]chstore.Problem // kural|servis → sanal satır
	// onEscalate — sahibin önceliği üye yüzünden P1'e çıktı (D5 bildirimi; nil = yok).
	onEscalate func(before, after chstore.Problem)
}

// LoadMembers — tik başına bir kez: dedektörün açık üye olaylarını okur ve
// sanal satırları kurar. Asla nil dönmez.
func LoadMembers(ctx context.Context, store MemberStore, snap *chstore.OpenProblems, detector string, now time.Time) *Members {
	prio := chstore.CurrentProblemPriority()
	m := &Members{store: store, detector: detector, snap: snap, now: now,
		pol:    chstore.OwnerPolicy{Keep: prio.InboxKeepSourcePriorityList(), Priority: prio, NowNs: now.UnixNano()},
		events: map[string]chstore.MLEvent{}, rows: map[string]chstore.Problem{}}
	evs, err := store.ListMLEvents(ctx, chstore.MLEventsFilter{Detector: detector, Since: now.Add(-memberLookback), Limit: memberReadCap, Members: true})
	if err != nil {
		log.Printf("[problem-member] %s: üye olayları okunamadı (bu tik sanal satır yok, katılma sürer): %v", detector, err)
		return m
	}
	if len(evs) == 0 {
		return m
	}
	m.loadNoise(ctx)
	m.events, m.rows = memberRows(evs, snap, m.pol, m.noiseOK, now.UnixNano())
	return m
}

// OnEscalate — üye yüzünden P1'e çıkan sahip için çağrılır (D5).
func (m *Members) OnEscalate(fn func(before, after chstore.Problem)) *Members {
	m.onEscalate = fn
	return m
}

// loadNoise — "problem değil" imzaları, tik başına en çok bir kez.
func (m *Members) loadNoise(ctx context.Context) {
	if m.noiseTried {
		return
	}
	m.noiseTried = true
	list, err := m.store.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[problem-member] %s: \"problem değil\" kararları okunamadı (bu tik yeni üyelik yok): %v", m.detector, err)
		return
	}
	m.pol.Noise, m.noiseOK = chstore.NoiseVerdictSignatures(list), true
}

// memberKey — sanal satır anahtarı (OpenProblems.ByKey ile aynı biçim).
func memberKey(ruleID, service string) string { return chstore.OpenProblemKey(ruleID, service) }

// memberRows — SAF: dedektörün üye olayları → geçerli üyelikler. Üyelik
// geçerlidir: olay açık (MLEventOpen), sahibi anlık görüntüde hâlâ açık ve
// (kararlar okunduysa) hâlâ sahip olabilir, üyenin kendi (kural, servis)
// satırı AÇIK DEĞİL (gerçek satır varsa o kazanır; ikiz göçü onu kapatır).
// Aynı (kural, servis) için birden çok olay varsa en son görülen.
// snap nil = anlık görüntü okunamadı: sahip doğrulanamaz, üyelik BİLİNMİYOR
// sayılır ve düşürülmez (kör tikte üyeler yeni Problem olarak açılmasın).
func memberRows(evs []chstore.MLEvent, snap *chstore.OpenProblems, pol chstore.OwnerPolicy, noiseOK bool, nowNs int64) (map[string]chstore.MLEvent, map[string]chstore.Problem) {
	events, rows := map[string]chstore.MLEvent{}, map[string]chstore.Problem{}
	for _, e := range evs {
		ev, ok := chstore.MemberEvidenceOf(e)
		if !ok || ev.Row == nil || !chstore.MLEventOpen(e, nowNs) {
			continue
		}
		if snap != nil {
			owner := snap.ByID(ev.ProblemID)
			if owner == nil || owner.Status == "resolved" || owner.Service != e.Service {
				continue // sahip kapandı: üyelik yetim, dedektör bir sonraki kararını sıfırdan verir
			}
			if noiseOK && (!pol.CanOwn(owner) || !pol.MemberRule(ev.RuleID, e.Service)) {
				continue // sahip ya da üye sonradan "problem değil" / liste dışı
			}
			if real := snap.ByKey(ev.RuleID, e.Service); real != nil && real.ID != "" {
				continue
			}
		}
		k := memberKey(ev.RuleID, e.Service)
		if cur, dup := events[k]; dup && cur.LastSeen >= e.LastSeen {
			continue
		}
		events[k], rows[k] = e, ev.Row.Problem()
	}
	return events, rows
}

// Row — (kural, servis) üyeyse sanal açık satırı (KOPYA), değilse nil.
func (m *Members) Row(ruleID, service string) *chstore.Problem {
	if m == nil {
		return nil
	}
	p, ok := m.rows[memberKey(ruleID, service)]
	if !ok {
		return nil
	}
	return &p
}

// Rows — bütün sanal satırlar (KOPYA; kural, servis sıralı).
func (m *Members) Rows() []*chstore.Problem {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m.rows))
	for k := range m.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*chstore.Problem, 0, len(keys))
	for _, k := range keys {
		p := m.rows[k]
		out = append(out, &p)
	}
	return out
}

// IsMember — satır bu tikte sanal mı (üye olayına yazılacak).
func (m *Members) IsMember(p chstore.Problem) bool {
	if m == nil {
		return false
	}
	_, ok := m.rows[memberKey(p.RuleID, p.Service)]
	return ok
}

// OpenRow — (kural, servis) için dedektörün gördüğü açık satır: gerçek satır
// (anlık görüntü) ya da sanal satır (üyelik). İkisi de KOPYA; yoksa nil.
func (m *Members) OpenRow(ruleID, service string) *chstore.Problem {
	if m != nil && m.snap != nil {
		if p := m.snap.ByKey(ruleID, service); p != nil && p.ID != "" {
			return p
		}
	}
	return m.Row(ruleID, service)
}

// Open — dedektör YENİ satır açacak. Sahip varsa katılır (ownerID dolu:
// satır yazılmadı, bildirim GÖNDERİLMEZ); yoksa bugünkü gibi yazar
// (ownerID boş: çağıran sayar / bildirir).
func (m *Members) Open(ctx context.Context, p chstore.Problem) (ownerID string, err error) {
	owner := m.joinTarget(ctx, p)
	if owner == nil {
		return "", m.store.UpsertProblem(ctx, p)
	}
	ev := chstore.NewMemberEvent(m.detector, p, *owner, p.StartedAt, m.now.UnixNano(), false)
	if err := m.store.UpsertMLEvent(ctx, ev); err != nil {
		return "", err
	}
	// Aynı tikte dedektör satırını artık üyelik olarak görür.
	k := memberKey(p.RuleID, p.Service)
	row := p
	row.Status, row.ResolvedAt = "open", nil
	m.events[k], m.rows[k] = ev, row
	m.raise(ctx, *owner, p)
	return owner.ID, nil
}

// joinTarget — SAF karar + tembel karar okuması: p açılacak olsa katılacağı
// sahip (yoksa nil).
func (m *Members) joinTarget(ctx context.Context, p chstore.Problem) *chstore.Problem {
	if m.snap == nil || !chstore.MemberEligibleRule(p.RuleID) {
		return nil
	}
	// Ucuz ön eleme: susturma bilinmeden de sahip yoksa okuma gereksiz.
	if role, _ := chstore.DecideJoin(m.snap.All(), p.RuleID, p.Service, m.pol); role != chstore.JoinMember {
		return nil
	}
	m.loadNoise(ctx)
	if !m.noiseOK {
		return nil
	}
	role, owner := chstore.DecideJoin(m.snap.All(), p.RuleID, p.Service, m.pol)
	if role != chstore.JoinMember {
		return nil
	}
	return owner
}

// raise — üye sahibin önceliğini yükseltiyorsa sahip satırını yazar.
func (m *Members) raise(ctx context.Context, owner, member chstore.Problem) {
	next, changed := chstore.RaiseOwner(owner, member, m.pol)
	if !changed {
		return
	}
	if err := m.store.UpsertProblem(ctx, next); err != nil {
		log.Printf("[problem-member] %s: sahip yükseltilemedi %s: %v", m.detector, owner.ID, err)
		return
	}
	log.Printf("[problem-member] PROBLEM RAISED BY MEMBER %s · %s ← %s (%s)", owner.Service, owner.ID, member.RuleID, next.Severity)
	if m.onEscalate != nil && chstore.EscalatedToP1(owner, next, m.pol) {
		m.onEscalate(owner, next)
	}
}

// Write — dedektörün MEVCUT satıra yazımı (tazeleme / kapanış). Sanal satır
// üye olayına gider (kapanış = üyeden ayrılma; Problem yazılmaz); gerçek
// satır bugünkü gibi UpsertProblem.
func (m *Members) Write(ctx context.Context, p chstore.Problem) error {
	k := memberKey(p.RuleID, p.Service)
	prev, ok := m.events[k]
	if !ok {
		return m.store.UpsertProblem(ctx, p)
	}
	ev, _ := chstore.MemberEvidenceOf(prev)
	owner := m.snap.ByID(ev.ProblemID) // nil alıcı güvenli
	if owner == nil {
		owner = &chstore.Problem{ID: ev.ProblemID, Service: p.Service} // sahip doğrulanamadı: yalnız olay yazılır
	}
	closed := p.Status == "resolved"
	next := chstore.NewMemberEvent(m.detector, p, *owner, prev.StartedAt, m.now.UnixNano(), closed)
	next.ID = prev.ID // kimlik üyeliğin; sahibin başlangıcı değişse de aynı satır
	if err := m.store.UpsertMLEvent(ctx, next); err != nil {
		return err
	}
	if closed {
		delete(m.events, k)
		delete(m.rows, k)
		return nil
	}
	if owner.RuleID != "" {
		m.raise(ctx, *owner, p)
	}
	return nil
}
