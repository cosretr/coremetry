package anomaly

// problem_member_test.go — v0.10.2156 (Davis tarzı tek Problem D2): eski
// yazarların ortak katılma yolu. Sözleşme:
//   - açık sahip varsa yeni satır AÇILMAZ, üye olayı yazılır (bildirim yok);
//   - sahip yoksa bugünkü gibi satır açılır;
//   - üyelik dedektöre kendi açık satırı gibi görünür (sanal satır);
//     tazeleme olayı tazeler, kapanış olayı kapatır — Problem yazılmaz;
//   - üye yalnız önceliği yükseltiyorsa sahip satırı yazılır, P1'e çıkış bir
//     kez bildirilir (OnEscalate);
//   - "problem değil" imzalı aile üye olmaz; kararlar okunamazsa yeni üyelik
//     yok; üye olayları okunamazsa katılma sürer (yeni Problem seli yok).
// Servis adları SENTETİK.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeMemberStore struct {
	problems   []chstore.Problem
	events     map[string]chstore.MLEvent
	verdicts   []chstore.ProblemVerdict
	evErr      error
	verdictErr error
	verdictN   int
}

func newFakeMemberStore() *fakeMemberStore {
	return &fakeMemberStore{events: map[string]chstore.MLEvent{}}
}

func (f *fakeMemberStore) UpsertProblem(_ context.Context, p chstore.Problem) error {
	f.problems = append(f.problems, p)
	return nil
}

func (f *fakeMemberStore) UpsertMLEvent(_ context.Context, e chstore.MLEvent) error {
	f.events[e.ID] = e
	return nil
}

func (f *fakeMemberStore) ListMLEvents(_ context.Context, flt chstore.MLEventsFilter) ([]chstore.MLEvent, error) {
	if f.evErr != nil {
		return nil, f.evErr
	}
	var out []chstore.MLEvent
	for _, e := range f.events {
		if flt.Detector != "" && e.Detector != flt.Detector {
			continue
		}
		if flt.Service != "" && e.Service != flt.Service {
			continue
		}
		if !flt.Members && chstore.IsMLMemberDetector(e.Detector) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeMemberStore) ListProblemVerdicts(context.Context) ([]chstore.ProblemVerdict, error) {
	f.verdictN++
	return f.verdicts, f.verdictErr
}

var memNow = time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

func memMLOwner() chstore.Problem {
	return chstore.Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api", RuleName: "ML · servis anomalisi", Severity: "critical",
		Service: "orders-api", Metric: "error_rate", Comparator: ">=", Value: 0.03, Threshold: 0.01, Status: "open", StartedAt: memNow.Add(-10 * time.Minute).UnixNano()}
}

func memSlowRow() chstore.Problem {
	return chstore.Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: "Yaygın yavaşlama", Severity: "critical",
		Service: "orders-api", Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "open", Description: "yavaşlama cümlesi",
		StartedAt: memNow.Add(-5 * time.Minute).UnixNano()}
}

func memErrRateOwner() chstore.Problem {
	return chstore.Problem{ID: "a1", RuleID: "anomaly:orders-api:error_rate", RuleName: "Anomaly · Error rate", Severity: "warning",
		Service: "orders-api", Metric: "error_rate", Comparator: ">", Value: 3, Threshold: 2, Status: "open", Description: "hata cümlesi",
		StartedAt: memNow.Add(-10 * time.Minute).UnixNano()}
}

func TestMembersOpenJoinsOwner(t *testing.T) {
	ctx := context.Background()
	t.Run("ML sahip + yaygın yavaşlama açılacak → üye olayı, satır yok", func(t *testing.T) {
		st := newFakeMemberStore()
		snap := chstore.NewOpenProblems([]chstore.Problem{memMLOwner()})
		m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow)
		owner, err := m.Open(ctx, memSlowRow())
		if err != nil || owner != "anomaly-auto:svc:orders-api" {
			t.Fatalf("katılma: %q %v", owner, err)
		}
		if len(st.problems) != 0 || len(st.events) != 1 {
			t.Fatalf("Problem yazılmamalı (sahip zaten P1), tek üye olayı yazılmalı: %+v %+v", st.problems, st.events)
		}
		for _, e := range st.events {
			ev, ok := chstore.MemberEvidenceOf(e)
			if !ok || e.Detector != "svc_slowdown" || e.Stage != chstore.MLStageLive || ev.ProblemID != owner || ev.RuleID != "svc-slowdown:orders-api" ||
				e.StartedAt != memSlowRow().StartedAt || e.LastSeen != memNow.UnixNano() {
				t.Errorf("üye olayı: %+v %+v", e, ev)
			}
		}
		// aynı tikte dedektör satırını artık açık görür
		if r := m.Row("svc-slowdown:orders-api", "orders-api"); r == nil || !m.IsMember(*r) {
			t.Error("katılan satır aynı tikte sanal satır olmalı")
		}
	})
	t.Run("sahip yok → bugünkü gibi satır açılır", func(t *testing.T) {
		st := newFakeMemberStore()
		m := LoadMembers(ctx, st, chstore.NewOpenProblems(nil), MemberDetSvcSlowdown, memNow)
		if owner, err := m.Open(ctx, memSlowRow()); err != nil || owner != "" || len(st.problems) != 1 || len(st.events) != 0 {
			t.Fatalf("açılış: %q %v %+v", owner, err, st)
		}
		if st.verdictN != 0 {
			t.Error("sahip yokken karar okuması gereksiz")
		}
	})
	t.Run("yalnız-üye aile sahip yokken kendi satırını açar; sahip varken üye olur ve yükseltmez", func(t *testing.T) {
		p99 := chstore.Problem{ID: "x1", RuleID: "anomaly:orders-api:p99_ms", Severity: "critical", Service: "orders-api", Metric: "p99_ms", Comparator: ">",
			Value: 9000, Threshold: 300, Status: "open", StartedAt: memNow.UnixNano()}
		st := newFakeMemberStore()
		m := LoadMembers(ctx, st, chstore.NewOpenProblems(nil), MemberDetMetricAnomaly, memNow)
		if owner, _ := m.Open(ctx, p99); owner != "" || len(st.problems) != 1 {
			t.Fatalf("sahip yok: %q %+v", owner, st.problems)
		}
		st = newFakeMemberStore()
		m = LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{memErrRateOwner()}), MemberDetMetricAnomaly, memNow)
		if owner, _ := m.Open(ctx, p99); owner != "a1" || len(st.problems) != 0 || len(st.events) != 1 {
			t.Fatalf("sahip var: %q %+v", owner, st.problems)
		}
	})
	t.Run("uyarı sahibine yaygın yavaşlama katılır → sahip P1'e yükselir, yükseliş BİR kez", func(t *testing.T) {
		st := newFakeMemberStore()
		owner := memErrRateOwner()
		var esc []chstore.Problem
		m := LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{owner}), MemberDetSvcSlowdown, memNow).
			OnEscalate(func(_, after chstore.Problem) { esc = append(esc, after) })
		if id, err := m.Open(ctx, memSlowRow()); err != nil || id != "a1" {
			t.Fatalf("katılma: %q %v", id, err)
		}
		if len(st.problems) != 1 || len(esc) != 1 {
			t.Fatalf("sahip bir kez yazılmalı ve bir yükseliş bildirilmeli: %+v %d", st.problems, len(esc))
		}
		r := st.problems[0]
		if r.ID != "a1" || r.RuleID != owner.RuleID || r.Description != "hata cümlesi" || r.Severity != "critical" || r.Value != 9000 || r.Metric != "p99_ms" {
			t.Errorf("yükseltilmiş sahip: %+v", r)
		}
		// sonraki tik: sahip yükseltilmiş, üye tazelenir → sahip yazımı / bildirim yok
		esc = nil
		m2 := LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{r}), MemberDetSvcSlowdown, memNow.Add(time.Minute)).
			OnEscalate(func(_, after chstore.Problem) { esc = append(esc, after) })
		row := m2.Row("svc-slowdown:orders-api", "orders-api")
		if row == nil {
			t.Fatal("üyelik sanal satır olarak görünmeli")
		}
		if err := m2.Write(ctx, *row); err != nil || len(st.problems) != 1 || len(esc) != 0 {
			t.Fatalf("tazeleme sahibi yeniden yazmamalı / bildirmemeli: %v %d %d", err, len(st.problems), len(esc))
		}
	})
}

// v0.10.2164 (D5) — yükseliş bildirimi gövdesi: sahibin YENİ hali (P1)
// bildirilir; kanal yoksa (send nil) yalnız log, panik yok.
func TestEscalationAlertSendsRaisedOwner(t *testing.T) {
	got := make(chan chstore.Problem, 1)
	before, after := memErrRateOwner(), memErrRateOwner()
	after.Severity = "critical"
	EscalationAlert("test", nil, func(_ context.Context, p chstore.Problem) { got <- p })(before, after)
	select {
	case p := <-got:
		if p.ID != "a1" || p.Severity != "critical" {
			t.Fatalf("bildirilen satır sahibin yükselmiş hali olmalı: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bildirim gönderilmedi")
	}
	EscalationAlert("test", nil, nil)(before, after)
}

// setnxCache — paylaşılan Redis'in SETNX davranışı (süreçler arası tek
// önbellek); yalnız SetNX kullanılır.
type setnxCache struct {
	cache.Cache
	mu   sync.Mutex
	keys map[string]time.Duration
}

func (c *setnxCache) SetNX(_ context.Context, key string, _ []byte, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.keys[key]; ok {
		return false, nil
	}
	c.keys[key] = ttl
	return true, nil
}

// v0.10.2173 — worker ve sre aynı sahibi aynı anda P1'e çıkarır: paylaşılan
// önbellekte ikinci çağıran susar, tek bildirim gider.
func TestEscalationAlertDedupesAcrossProcesses(t *testing.T) {
	shared := &setnxCache{keys: map[string]time.Duration{}}
	got := make(chan string, 2)
	before, after := memErrRateOwner(), memErrRateOwner()
	after.Severity = "critical"
	EscalationAlert("worker", shared, func(context.Context, chstore.Problem) { got <- "worker" })(before, after)
	EscalationAlert("sre", shared, func(context.Context, chstore.Problem) { got <- "sre" })(before, after)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("ilk çağıran bildirmeli")
	}
	select {
	case who := <-got:
		t.Fatalf("ikinci çağıran susturulmalı, %s de gönderdi", who)
	case <-time.After(200 * time.Millisecond):
	}
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if ttl := shared.keys[EscalationDedupKey("a1")]; ttl != EscalationDedupTTL {
		t.Fatalf("anahtar %q TTL %v olmalı, %v", EscalationDedupKey("a1"), EscalationDedupTTL, ttl)
	}
}

func TestMembersVirtualRowLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newFakeMemberStore()
	owner := memMLOwner()
	snap := chstore.NewOpenProblems([]chstore.Problem{owner})
	if _, err := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow).Open(ctx, memSlowRow()); err != nil {
		t.Fatal(err)
	}
	t1 := memNow.Add(time.Minute)
	m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, t1)
	rows := m.Rows()
	if len(rows) != 1 || rows[0].ID != "svc-slowdown:orders-api" || rows[0].Status != "open" || rows[0].StartedAt != memSlowRow().StartedAt ||
		rows[0].Value != 9000 || rows[0].Description != "yavaşlama cümlesi" {
		t.Fatalf("sanal satır dedektörün açacağı satırdır: %+v", rows)
	}
	if r := m.OpenRow("svc-slowdown:orders-api", "orders-api"); r == nil || r.ID != rows[0].ID {
		t.Fatal("OpenRow sanal satırı döndürmeli")
	}
	if LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, t1).Row("svc-slowdown:orders-api", "orders-api") != nil {
		t.Fatal("başka dedektör bu üyeliği görmemeli")
	}
	// tazeleme: ölçü güncel, başlangıç taşınır, Problem yazılmaz
	q := *rows[0]
	q.Value, q.Description = 12000, "güncel cümle"
	if err := m.Write(ctx, q); err != nil || len(st.problems) != 0 || len(st.events) != 1 {
		t.Fatalf("tazeleme: %v %+v", err, st.problems)
	}
	for _, e := range st.events {
		if e.LastSeen != t1.UnixNano() || e.StartedAt != memSlowRow().StartedAt || e.Observed != 12000 || e.Stage != chstore.MLStageLive {
			t.Errorf("tazelenmiş olay: %+v", e)
		}
	}
	// kapanış = üyeden ayrılma: olay stage=closed, Problem yazılmaz, sanal satır biter
	t2 := t1.Add(time.Minute)
	m = LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, t2)
	c := *m.Row("svc-slowdown:orders-api", "orders-api")
	chstore.MarkResolved(&c, t2.UnixNano())
	if !m.IsMember(c) {
		t.Fatal("kapanacak satır üye olarak tanınmalı")
	}
	if err := m.Write(ctx, c); err != nil || len(st.problems) != 0 {
		t.Fatalf("ayrılma: %v %+v", err, st.problems)
	}
	for _, e := range st.events {
		if e.Stage != chstore.MLStageClosed {
			t.Errorf("ayrılan üyenin olayı kapalı olmalı: %+v", e)
		}
	}
	if LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, t2.Add(time.Minute)).Row("svc-slowdown:orders-api", "orders-api") != nil {
		t.Fatal("ayrılan üyenin sanal satırı kalmamalı")
	}
}

func TestMembersRowValidity(t *testing.T) {
	ctx := context.Background()
	join := func() *fakeMemberStore {
		st := newFakeMemberStore()
		if _, err := LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{memMLOwner()}), MemberDetSvcSlowdown, memNow).Open(ctx, memSlowRow()); err != nil {
			t.Fatal(err)
		}
		return st
	}
	t1 := memNow.Add(time.Minute)
	t.Run("sahip kapandı → üyelik yetim (dedektör sıfırdan karar verir)", func(t *testing.T) {
		if m := LoadMembers(ctx, join(), chstore.NewOpenProblems(nil), MemberDetSvcSlowdown, t1); len(m.Rows()) != 0 {
			t.Fatalf("yetim üyelik sanal satır olmamalı: %+v", m.Rows())
		}
	})
	t.Run("dedektörün gerçek satırı da açık → gerçek satır kazanır", func(t *testing.T) {
		snap := chstore.NewOpenProblems([]chstore.Problem{memMLOwner(), memSlowRow()})
		m := LoadMembers(ctx, join(), snap, MemberDetSvcSlowdown, t1)
		if len(m.Rows()) != 0 || m.IsMember(memSlowRow()) {
			t.Fatal("gerçek satır varken sanal satır olmamalı")
		}
		st := join()
		m = LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, t1)
		if err := m.Write(ctx, memSlowRow()); err != nil || len(st.problems) != 1 {
			t.Fatalf("gerçek satır bugünkü gibi yazılır: %v %+v", err, st.problems)
		}
	})
	t.Run("bayat üye olayı (dedektör durmuş) sanal satır değil", func(t *testing.T) {
		snap := chstore.NewOpenProblems([]chstore.Problem{memMLOwner()})
		if m := LoadMembers(ctx, join(), snap, MemberDetSvcSlowdown, memNow.Add(21*time.Minute)); len(m.Rows()) != 0 {
			t.Fatal("20 dk tazelenmeyen üyelik bitmiş sayılır")
		}
	})
	t.Run("sahip sonradan 'problem değil' → üyelik biter", func(t *testing.T) {
		st := join()
		st.verdicts = []chstore.ProblemVerdict{{Signature: chstore.RuleProblemVerdictSignature("anomaly-auto:svc:orders-api", "orders-api"), Verdict: chstore.ProblemVerdictNoise}}
		if m := LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{memMLOwner()}), MemberDetSvcSlowdown, t1); len(m.Rows()) != 0 {
			t.Fatal("susturulmuş sahibin altında üyelik sürmemeli")
		}
	})
	t.Run("anlık görüntü yok (okuma hatası) → mevcut üyelik sürer, yeni katılma yok", func(t *testing.T) {
		st := join()
		m := LoadMembers(ctx, st, nil, MemberDetSvcSlowdown, t1)
		if len(m.Rows()) != 1 {
			t.Fatal("sahibi doğrulanamayan üyelik bilinmiyor sayılır, düşürülmez")
		}
		if err := m.Write(ctx, *m.Rows()[0]); err != nil || len(st.problems) != 0 {
			t.Fatalf("tazeleme üye olayına gitmeli: %v %+v", err, st.problems)
		}
	})
}

func TestMembersSoftFail(t *testing.T) {
	ctx := context.Background()
	snap := chstore.NewOpenProblems([]chstore.Problem{memMLOwner()})
	t.Run("'problem değil' imzalı aile üye olmaz, bugünkü gibi kendi satırını açar", func(t *testing.T) {
		st := newFakeMemberStore()
		st.verdicts = []chstore.ProblemVerdict{{Signature: chstore.RuleProblemVerdictSignature("svc-slowdown:orders-api", "orders-api"), Verdict: chstore.ProblemVerdictNoise}}
		m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow)
		if owner, _ := m.Open(ctx, memSlowRow()); owner != "" || len(st.problems) != 1 || len(st.events) != 0 {
			t.Fatalf("susturulmuş aile: %q %+v", owner, st)
		}
	})
	t.Run("kararlar okunamadı → yeni üyelik yok (kendi satırı)", func(t *testing.T) {
		st := newFakeMemberStore()
		st.verdictErr = errors.New("ch down")
		m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow)
		if owner, _ := m.Open(ctx, memSlowRow()); owner != "" || len(st.problems) != 1 || len(st.events) != 0 {
			t.Fatalf("karar okunamadı: %q %+v", owner, st)
		}
	})
	t.Run("üye olayları okunamadı → katılma sürer, aynı satıra yazar (yeni Problem seli yok)", func(t *testing.T) {
		st := newFakeMemberStore()
		if _, err := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow).Open(ctx, memSlowRow()); err != nil {
			t.Fatal(err)
		}
		st.evErr = errors.New("ch down")
		m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow.Add(time.Minute))
		if len(m.Rows()) != 0 {
			t.Fatal("okuma düştüyse sanal satır yok")
		}
		if owner, err := m.Open(ctx, memSlowRow()); err != nil || owner == "" || len(st.problems) != 0 || len(st.events) != 1 {
			t.Fatalf("yeniden katılma aynı olaya yazmalı: %q %v %d %d", owner, err, len(st.problems), len(st.events))
		}
	})
	t.Run("ayrı aile (alarm / dış kaynak) hiç karışmaz", func(t *testing.T) {
		st := newFakeMemberStore()
		m := LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, memNow)
		p := chstore.Problem{ID: "h1", RuleID: "anomaly:orders-api:jvm_heap_after_gc", Service: "orders-api", Status: "open"}
		if owner, _ := m.Open(ctx, p); owner != "" || len(st.problems) != 1 || st.verdictN != 0 {
			t.Fatalf("ayrı aile: %q %+v", owner, st)
		}
	})
}
