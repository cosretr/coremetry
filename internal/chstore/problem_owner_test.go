package chstore

// problem_owner_test.go — v0.10.2155 (Davis tarzı tek Problem D1, spec
// docs/plans/spec-davis-style-problem-merge.md): servis başına TEK açık
// Problem'in SAF sahip çözümü. Sözleşme:
//   - aile sınıfı kural kimliğinden: sahip olabilen / yalnız üye / ayrı satır;
//   - sahip = servisin en eski açık, sahip olabilen satırı (StartedAt, eşitse ID);
//   - sahip varken gelen her birleşen aile ÜYEDİR (yeni satır yok);
//   - üye sahibi yalnız ÖNCELİĞİ yükseltiyorsa değiştirir (şiddet + ölçü
//     birlikte; öncelik asla düşmez), yalnız-üye aile hiç yükseltmez;
//   - "problem değil" imzalı satır ne sahip ne üye; alarm / dış kaynak /
//     self / küme / fırtına hiç katılmaz.
// Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"
)

var ownerT0 = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC).UnixNano()

func ownerRow(rule, svc, sev string, v, th float64, startMin int) *Problem {
	id := rule
	if !strings.HasSuffix(rule, ":"+svc) && !strings.HasPrefix(rule, RuleSvcSlowdownPrefix) && !IsMLSvcProblem(rule) {
		id = rule + ":" + svc
	}
	return &Problem{ID: id, RuleID: rule, RuleName: "kural", Severity: sev, Service: svc, Metric: "m", Comparator: ">=",
		Value: v, Threshold: th, Status: "open", StartedAt: ownerT0 + int64(startMin)*int64(time.Minute)}
}

func ownerPol() OwnerPolicy {
	return OwnerPolicy{Keep: DefaultInboxKeepSourcePriority(), Priority: DefaultProblemPriority(), NowNs: ownerT0 + int64(30*time.Minute)}
}

func TestProblemMergeClass(t *testing.T) {
	cases := []struct {
		rule string
		want MergeClass
	}{
		{"svc-slowdown:orders-api", MergeOwner},
		{"anomaly:orders-api:error_rate", MergeOwner},
		{"anomaly-auto:svc:orders-api", MergeOwner},
		{"anomaly-auto:group:ev1", MergeOwner},
		{"anomaly:orders-api:p99_ms", MergeMemberOnly},
		{"anomaly:orders-api:request_rate", MergeMemberOnly},
		{"anomaly-auto:1f2e3d", MergeMemberOnly},
		{"anomaly-auto:subject:orders-api:p99_ms:up", MergeMemberOnly},
		// AYRI satır: alarm kuralı, dış kaynak, DB, self, küme, fırtına, heap
		{"rule-7f3a", MergeSeparate},
		{"builtin-error-rate-15pct", MergeSeparate},
		{"anomaly:ext:orasrc:ext:error_count", MergeSeparate},
		{"anomaly:orders-api:ext:error_rate", MergeSeparate},
		{"anomaly:ext-down:ext:orasrc", MergeSeparate},
		{"anomaly:ext-cap:ext:orasrc:ext:fail_count", MergeSeparate},
		{"anomaly:orders-api:jvm_heap_after_gc", MergeSeparate},
		{"anomaly-cluster:orders-api", MergeSeparate},
		{"anomaly-cluster:ext:orasrc", MergeSeparate},
		{"exception-storm", MergeSeparate},
		{"db-health:oracle@db1/core", MergeSeparate},
		{"self-ingest-stall", MergeSeparate},
		{"monitor:checkout", MergeSeparate},
		{"svc-slowdown:", MergeSeparate},
		{"anomaly-auto:", MergeSeparate},
		{"", MergeSeparate},
	}
	for _, c := range cases {
		if got := ProblemMergeClass(c.rule); got != c.want {
			t.Errorf("%q: sınıf %d, istenen %d", c.rule, got, c.want)
		}
		if OwnerEligibleRule(c.rule) != (c.want == MergeOwner) || MemberEligibleRule(c.rule) != (c.want != MergeSeparate) {
			t.Errorf("%q: OwnerEligibleRule / MemberEligibleRule sınıfla tutarsız", c.rule)
		}
	}
}

func TestServiceOwner(t *testing.T) {
	slow := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, 10)
	ml := ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.03, 0.01, 5)
	p99 := ownerRow("anomaly:orders-api:p99_ms", "orders-api", "warning", 900, 300, 0)
	alarm := ownerRow("rule-7f3a", "orders-api", "critical", 20, 15, -30)
	other := ownerRow("svc-slowdown:payments-api", "payments-api", "critical", 9000, 300, 1)
	open := []*Problem{slow, ml, p99, alarm, other}

	if o := ServiceOwnerOf(open, "orders-api", ownerPol()); o == nil || o.ID != ml.ID {
		t.Fatalf("en eski sahip olabilen satır sahip olmalı (yalnız-üye ve alarm daha eski olsa da): %+v", o)
	}
	if o := ServiceOwnerOf(open, "payments-api", ownerPol()); o == nil || o.ID != other.ID {
		t.Fatalf("başka servis kendi sahibini alır: %+v", o)
	}
	if o := ServiceOwnerOf(open, "", ownerPol()); o != nil {
		t.Fatalf("servissiz satırın sahibi olmaz: %+v", o)
	}
	t.Run("eşit başlangıç → küçük ID", func(t *testing.T) {
		a := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, 5)
		if o := ServiceOwnerOf([]*Problem{a, ml}, "orders-api", ownerPol()); o == nil || o.ID != ml.ID {
			t.Fatalf("eşitlikte ID sırası: %+v", o)
		}
	})
	t.Run("kapanmış / dış özneli / servissiz satır sahip değil", func(t *testing.T) {
		closed := *ml
		closed.Status = "resolved"
		ext := *ml
		ext.Kind = ProblemKindExternal
		if o := ServiceOwnerOf([]*Problem{&closed, &ext, slow}, "orders-api", ownerPol()); o == nil || o.ID != slow.ID {
			t.Fatalf("sahip: %+v", o)
		}
	})
	t.Run("problem değil imzalı satır sahip olamaz", func(t *testing.T) {
		pol := ownerPol()
		pol.Noise = map[string]struct{}{RuleProblemVerdictSignature(ml.RuleID, ml.Service): {}}
		if o := ServiceOwnerOf(open, "orders-api", pol); o == nil || o.ID != slow.ID {
			t.Fatalf("susturulmuş satır atlanmalı: %+v", o)
		}
	})
	t.Run("inbox istisna listesinden çıkarılan aile sahip olamaz", func(t *testing.T) {
		pol := ownerPol()
		pol.Keep = []string{InboxKeepSvcSlowdown} // operatör ML ailelerini P3'e indirdi
		if o := ServiceOwnerOf(open, "orders-api", pol); o == nil || o.ID != slow.ID {
			t.Fatalf("yalnız listede kalan aile sahip: %+v", o)
		}
		pol.Keep = []string{}
		if o := ServiceOwnerOf(open, "orders-api", pol); o != nil {
			t.Fatalf("boş liste = kimse sahip değil (birleşme yok): %+v", o)
		}
	})
	t.Run("anlık görüntü yöntemi aynı kuralı okur; kopya döner", func(t *testing.T) {
		snap := &OpenProblems{all: open}
		o := snap.ServiceOwner("orders-api", ownerPol())
		if o == nil || o.ID != ml.ID {
			t.Fatalf("sahip: %+v", o)
		}
		o.Value = -1
		if ml.Value == -1 {
			t.Fatal("ServiceOwner paylaşılan satırı değil kopyayı döndürmeli")
		}
		var nilSnap *OpenProblems
		if nilSnap.ServiceOwner("orders-api", ownerPol()) != nil {
			t.Fatal("nil anlık görüntü güvenli olmalı")
		}
	})
}

// TestOwnerDedupe — spec D1 tablosu: kim sahip, kim üye, kim ayrı.
func TestOwnerDedupe(t *testing.T) {
	slow := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, 0)
	mlSvc := ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.03, 0.01, 0)
	p99 := ownerRow("anomaly:orders-api:p99_ms", "orders-api", "warning", 900, 300, 0)
	cases := []struct {
		name     string
		open     []*Problem
		rule     string
		wantRole JoinRole
		wantOwn  string
	}{
		{"svc-slowdown sahip + ML gelir → üye", []*Problem{slow}, "anomaly-auto:svc:orders-api", JoinMember, slow.ID},
		{"svc-slowdown sahip + ML grubu gelir → üye", []*Problem{slow}, "anomaly-auto:group:ev1", JoinMember, slow.ID},
		{"ML sahip + slowdown gelir → üye", []*Problem{mlSvc}, "svc-slowdown:orders-api", JoinMember, mlSvc.ID},
		{"ML sahip + error_rate gelir → üye", []*Problem{mlSvc}, "anomaly:orders-api:error_rate", JoinMember, mlSvc.ID},
		{"ML sahip + p99 gelir → üye (yalnız üye aile)", []*Problem{mlSvc}, "anomaly:orders-api:p99_ms", JoinMember, mlSvc.ID},
		{"sahip yok + slowdown gelir → kendi satırı (sahip)", nil, "svc-slowdown:orders-api", JoinOpen, ""},
		{"sahip yok + p99 gelir → bugünkü gibi kendi satırı", nil, "anomaly:orders-api:p99_ms", JoinOpen, ""},
		{"yalnız p99 açık + ML gelir → ML sahip olarak açılır", []*Problem{p99}, "anomaly-auto:svc:orders-api", JoinOpen, ""},
		{"kendi satırı sahip → tazeleme, üyelik değil", []*Problem{slow}, "svc-slowdown:orders-api", JoinOpen, ""},
		{"alarm kuralı hiç katılmaz", []*Problem{slow}, "rule-7f3a", JoinOpen, ""},
		{"dış kaynak hiç katılmaz", []*Problem{slow}, "anomaly:ext:orasrc:ext:error_count", JoinOpen, ""},
		{"self hiç katılmaz", []*Problem{slow}, "self-ingest-stall", JoinOpen, ""},
		{"küme hiç katılmaz", []*Problem{slow}, "anomaly-cluster:orders-api", JoinOpen, ""},
	}
	for _, c := range cases {
		role, owner := DecideJoin(c.open, c.rule, "orders-api", ownerPol())
		if role != c.wantRole {
			t.Errorf("%s: rol %d, istenen %d", c.name, role, c.wantRole)
			continue
		}
		if (owner == nil) != (c.wantOwn == "") || (owner != nil && owner.ID != c.wantOwn) {
			t.Errorf("%s: sahip %+v, istenen %q", c.name, owner, c.wantOwn)
		}
	}
	t.Run("susturulmuş aile üye de olmaz", func(t *testing.T) {
		pol := ownerPol()
		pol.Noise = map[string]struct{}{RuleProblemVerdictSignature("svc-slowdown:orders-api", "orders-api"): {}}
		if role, owner := DecideJoin([]*Problem{mlSvc}, "svc-slowdown:orders-api", "orders-api", pol); role != JoinOpen || owner != nil {
			t.Fatalf("problem değil imzalı aile bugünkü gibi kendi satırını açar: %d %+v", role, owner)
		}
	})
}

func TestRaiseOwner(t *testing.T) {
	pol := ownerPol()
	prio := func(p Problem) string {
		s, _ := PreviewProblemPriority(p, pol.NowNs, pol.Priority)
		return s
	}
	fresh := int(pol.NowNs-ownerT0)/int(time.Minute) - 1 // yeni açılmış (4 sa kuralı dışında)
	t.Run("uyarı sahibine yaygın yavaşlama katılır → P1, şiddet + ölçü birlikte", func(t *testing.T) {
		owner := *ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		owner.Status, owner.Assignee, owner.Description = "acknowledged", "ops@acme.example", "hata oranı cümlesi"
		member := *ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh)
		member.Metric = "p99_ms"
		got, ok := RaiseOwner(owner, member, pol)
		if !ok || got.Severity != "critical" || got.Value != 9000 || got.Threshold != 300 || got.Metric != "p99_ms" || got.Comparator != ">=" {
			t.Fatalf("yükseltme: %v %+v", ok, got)
		}
		// kimlik, başlık, operatör alanları sahibin
		if got.ID != owner.ID || got.RuleID != owner.RuleID || got.Description != owner.Description || got.Status != "acknowledged" ||
			got.Assignee != "ops@acme.example" || got.StartedAt != owner.StartedAt {
			t.Fatalf("sahip kimliği / operatör alanları korunmalı: %+v", got)
		}
		if prio(owner) == "P1" || prio(got) != "P1" || !EscalatedToP1(owner, got, pol) {
			t.Fatalf("öncelik %s → %s; yükseliş işareti bekleniyor", prio(owner), prio(got))
		}
	})
	t.Run("öncelik yükselmiyorsa satıra dokunulmaz (P1 sahibi daha büyük oranla da değişmez)", func(t *testing.T) {
		owner := *ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh)
		member := *ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.9, 0.01, fresh)
		if got, ok := RaiseOwner(owner, member, pol); ok || got.Value != 9000 || got.Metric != owner.Metric {
			t.Fatalf("P1 sahibi değişmemeli: %v %+v", ok, got)
		}
		if EscalatedToP1(owner, owner, pol) {
			t.Fatal("değişmeyen satır yükseliş değildir")
		}
	})
	t.Run("öncelik asla düşmez: zayıf üye güçlü sahibi indirmez", func(t *testing.T) {
		owner := *ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.03, 0.01, fresh)
		member := *ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		if got, ok := RaiseOwner(owner, member, pol); ok || got.Severity != "critical" || got.Value != 0.03 {
			t.Fatalf("indirme yok: %v %+v", ok, got)
		}
	})
	t.Run("yalnız-üye aile (p99, eski terfi) sahibi yükseltmez", func(t *testing.T) {
		owner := *ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		for _, rule := range []string{"anomaly:orders-api:p99_ms", "anomaly-auto:1f2e3d", "anomaly-auto:subject:orders-api:p99_ms:up"} {
			member := *ownerRow(rule, "orders-api", "critical", 9000, 300, fresh)
			if got, ok := RaiseOwner(owner, member, pol); ok || got.Severity != "warning" {
				t.Fatalf("%s: operatörün P3'e indirdiği aile önceliği yükseltmemeli: %v %+v", rule, ok, got)
			}
		}
	})
	t.Run("operatör yalnız-üye aileyi istisna listesine eklediyse o da yükseltir (önceliği gizlenmez)", func(t *testing.T) {
		p := pol
		p.Keep = append(DefaultInboxKeepSourcePriority(), "anomaly:*:p99_ms")
		owner := *ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		member := *ownerRow("anomaly:orders-api:p99_ms", "orders-api", "critical", 9000, 300, fresh)
		got, ok := RaiseOwner(owner, member, p)
		if !ok || got.Severity != "critical" || got.Value != 9000 || !EscalatedToP1(owner, got, p) {
			t.Fatalf("listeye alınan ailenin P1'i sahibe taşınmalı: %v %+v", ok, got)
		}
		if p.OwnerRule(member.RuleID, member.Service) {
			t.Fatal("listeye alınmak yalnız-üye aileyi sahip yapmaz")
		}
		// liste bilinmiyorsa (nil) yalnız statik sahip aileleri yükseltir
		p.Keep = nil
		if _, ok := RaiseOwner(owner, member, p); ok {
			t.Fatal("liste bilinmezken yalnız-üye aile yükseltmemeli")
		}
	})
	t.Run("inbox istisna listesinde olmayan aile yükseltmez", func(t *testing.T) {
		p := pol
		p.Keep = []string{InboxKeepErrorRateAnomaly}
		owner := *ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		member := *ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh)
		if _, ok := RaiseOwner(owner, member, p); ok {
			t.Fatal("listeden çıkarılan aile sahibi yükseltmemeli")
		}
	})
}

func TestPlanServiceMerges(t *testing.T) {
	pol := ownerPol()
	fresh := int(pol.NowNs-ownerT0)/int(time.Minute) - 5
	t.Run("ikizler: en eski sahip, genç 'merged into' ile kapanır; öncelik ve atanan taşınır", func(t *testing.T) {
		old := ownerRow("anomaly:orders-api:error_rate", "orders-api", "warning", 3, 2, fresh)
		young := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh+2)
		young.Metric, young.Assignee = "p99_ms", "payments"
		alarm := ownerRow("rule-7f3a", "orders-api", "critical", 20, 15, fresh-10)
		ms := PlanServiceMerges([]*Problem{young, old, alarm}, pol)
		if len(ms) != 1 || ms[0].Owner.ID != old.ID || len(ms[0].Merged) != 1 || ms[0].Merged[0].ID != young.ID {
			t.Fatalf("plan: %+v", ms)
		}
		m := ms[0]
		if !m.OwnerChanged || m.Owner.Severity != "critical" || m.Owner.Value != 9000 || m.Owner.Assignee != "payments" {
			t.Errorf("sahip gencin önceliğini ve atananını almalı: %+v", m.Owner)
		}
		c := m.Merged[0]
		if c.Status != "resolved" || c.ResolvedAt == nil || *c.ResolvedAt != pol.NowNs || !strings.HasSuffix(c.Description, MergedIntoNote(old.ID)) || c.Value != 9000 {
			t.Errorf("genç satır: %+v", c)
		}
		// idempotent: ikinci uygulamada (genç kapalı) iş yok
		closed := c
		if again := PlanServiceMerges([]*Problem{&m.Owner, &closed, alarm}, pol); len(again) != 0 {
			t.Errorf("ikinci geçiş boş olmalı: %+v", again)
		}
	})
	t.Run("p99 P3 açık + ML gelir → p99 satırı kapanır, ML sahibi değişmez", func(t *testing.T) {
		p99 := ownerRow("anomaly:orders-api:p99_ms", "orders-api", "critical", 9000, 300, fresh-20)
		p99.Assignee = "payments"
		ml := ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.012, 0.01, fresh)
		ms := PlanServiceMerges([]*Problem{p99, ml}, pol)
		if len(ms) != 1 || ms[0].Owner.ID != ml.ID || len(ms[0].Merged) != 1 || ms[0].Merged[0].ID != p99.ID {
			t.Fatalf("plan: %+v", ms)
		}
		if ms[0].Owner.Value != 0.012 || ms[0].Owner.Assignee != "payments" || !ms[0].OwnerChanged {
			t.Errorf("yalnız-üye satır önceliği yükseltmez, atanan boşsa taşınır: %+v", ms[0].Owner)
		}
	})
	t.Run("sahibin atananı doluysa gencinki taşınmaz; değişmeyen sahip yazılmaz", func(t *testing.T) {
		old := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh)
		old.Assignee = "ops@acme.example"
		young := ownerRow("anomaly-auto:svc:orders-api", "orders-api", "critical", 0.03, 0.01, fresh+1)
		young.Assignee = "payments"
		ms := PlanServiceMerges([]*Problem{old, young}, pol)
		if len(ms) != 1 || ms[0].OwnerChanged || ms[0].Owner.Assignee != "ops@acme.example" {
			t.Fatalf("plan: %+v", ms)
		}
	})
	t.Run("ayrı aileler ve susturulmuş satır birleşmez", func(t *testing.T) {
		slow := ownerRow("svc-slowdown:orders-api", "orders-api", "critical", 9000, 300, fresh)
		alarm := ownerRow("rule-7f3a", "orders-api", "critical", 20, 15, fresh)
		ext := ownerRow("anomaly:ext:orasrc:ext:error_count", "orders-api", "critical", 20, 15, fresh)
		self := ownerRow("self-ingest-stall", "", "critical", 1, 0, fresh)
		muted := ownerRow("anomaly:orders-api:error_rate", "orders-api", "critical", 9, 2, fresh+1)
		p := pol
		p.Noise = map[string]struct{}{RuleProblemVerdictSignature(muted.RuleID, muted.Service): {}}
		if ms := PlanServiceMerges([]*Problem{slow, alarm, ext, self, muted}, p); len(ms) != 0 {
			t.Fatalf("birleşme olmamalı: %+v", ms)
		}
	})
	t.Run("yalnız yalnız-üye satırlar (sahip yok) → dokunulmaz", func(t *testing.T) {
		a := ownerRow("anomaly:orders-api:p99_ms", "orders-api", "warning", 900, 300, fresh)
		b := ownerRow("anomaly:orders-api:request_rate", "orders-api", "warning", 900, 300, fresh)
		if ms := PlanServiceMerges([]*Problem{a, b}, pol); len(ms) != 0 {
			t.Fatalf("sahip yokken birleşme yok: %+v", ms)
		}
	})
}
