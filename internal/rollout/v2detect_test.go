package rollout

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// v2detect_test.go — v0.10.963 — Rollouts v2 P2.1 saf KSM dedektör çekirdeği
// sözleşmesi (docs/rollouts/v2-audit.md §4.3–§4.6, §4.11, §10.3.1–10.3.2;
// kararlar 8, 9, 10). DetectV2 SAF: I/O yok, saat yok (zaman argümanla gelir).
//
//   - İlk-ever koşu (tür başına durum yok) YALNIZ baseline yazar, olay yok.
//   - Nesil artışı rollout DEĞİLDİR: şablon/revizyon kanıtı (yeni RS, yeniden
//     etkinleşen ya da GC sonrası yeniden kurulan bilinen RS, yeni STS
//     update_revision, yeni DS hash'i) ya da observed_generation yetişince
//     "eski pod var" tutma sinyali olmadan ölçek/annotation → OLAY YOK.
//   - SUCCEEDED `kubectl rollout status` eşdeğeri (§4.5) + hedef revizyon
//     hâlâ olayın revizyonu; STUCK: Progressing=false (bayat koşul değil) ya
//     da stuckAfter; ROLLBACK bilinen revizyona → önceki olay rolled_back;
//     yeni START açık olayı superseded yapar.
//   - Yeni incarnation: _created daha yeni, nesil geriledi ya da ≥ K ardışık
//     TAM okumada yoktu; incarnation_at = _created ya da ilk tam okuma
//     (dakikaya kesik). Bootstrap sonrası ilk kez görülen → 'initial'.
//   - pending_started_at dayanıklı durumdadır: failover START zamanını
//     değiştirmez. Kısmi okuma HİÇBİR şey yazmaz, yokluk saymaz.
//   - Her satır yazıcı sözleşmesinden geçer; çıktı sırası deterministik.
//   - İnceleme düzeltmeleri (v0.10.963 — P2.1): kural tablosu bir kez de CH
//     FINAL gidiş-dönüşüyle koşar (1970 sentinel'i, UTC dışı dilim; red yok,
//     sentinel çıktıya sızmaz); açık olayda boşalan eski-eski RS sahte
//     rollback değildir; bootstrap rollout ortası + HPA kapı süresinde olay
//     değildir; geç START / mint'in durum yazımı kaybolursa aynı anahtar
//     yeniden türer (tek incarnation, tek initial); _created'lı hızlı
//     yeniden kurulum tespiti dondurmaz; revizyonsuz STS şablon değişimi
//     emilmez; yarım durum okuması tiki atlar. Önemli kurallar overlay
//     mutasyonlarıyla (go test -overlay, ağaç dışı) sınanmıştır.
//
// Zaman: T0 = 2026-09-27 10:00:00 UTC; tik = KSM örnek zamanı, Now = +5 s.

var detT0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func detAt(sec int) time.Time { return detT0.Add(time.Duration(sec) * time.Second) }

type detB struct{ o V2Observation }

func detDep(name string, at int, gen uint64) *detB {
	return &detB{V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: name, SampleAt: detAt(at),
		Generation: gen, ObservedGeneration: gen, SpecReplicas: 3, StatusReplicas: 3, UpdatedReplicas: 3,
		ReadyReplicas: 3, AvailableReplicas: 3}}
}

func detSts(name string, at int, gen uint64, current, update string) *detB {
	b := detDep(name, at, gen)
	b.o.Kind, b.o.CurrentRevision, b.o.UpdateRevision = V2KindStatefulSet, current, update
	return b
}

func detDs(name string, at int, gen uint64, hashes ...string) *detB {
	b := detDep(name, at, gen)
	b.o.Kind, b.o.StatusReplicas, b.o.RevisionHashes = V2KindDaemonSet, 0, hashes
	return b
}

func (b *detB) og(v uint64) *detB { b.o.ObservedGeneration = v; return b }
func (b *detB) reps(spec, status, updated, avail uint32) *detB {
	b.o.SpecReplicas, b.o.StatusReplicas, b.o.UpdatedReplicas, b.o.AvailableReplicas = spec, status, updated, avail
	return b
}
func (b *detB) ready(n uint32) *detB { b.o.ReadyReplicas = n; return b }
func (b *detB) rs(name string, spec uint32) *detB {
	b.o.ReplicaSets = append(b.o.ReplicaSets, V2ReplicaSet{Name: name, SpecReplicas: spec})
	return b
}
func (b *detB) paused() *detB             { b.o.Paused = true; return b }
func (b *detB) pde() *detB                { b.o.ProgressDeadlineExceeded = true; return b }
func (b *detB) created(t time.Time) *detB { b.o.CreatedAt = t; return b }
func (b *detB) img(rev string, images ...string) *detB {
	if b.o.RevisionImages == nil {
		b.o.RevisionImages = map[string][]string{}
	}
	b.o.RevisionImages[rev] = images
	return b
}
func (b *detB) V() V2Observation { return b.o }

// detSteady — tamamlanmış Deployment: active RS spec 3, eskiler 0.
func detSteady(name string, at int, gen uint64, active string, old ...string) V2Observation {
	b := detDep(name, at, gen).rs(active, 3)
	for _, o := range old {
		b.rs(o, 0)
	}
	return b.V()
}

// detRolling — süren RollingUpdate: eski ve yeni RS ikişer, 4 pod (1 eski fazla).
func detRolling(name string, at int, gen, og uint64, from, to string) V2Observation {
	return detDep(name, at, gen).og(og).reps(3, 4, 2, 3).rs(from, 2).rs(to, 2).V()
}

func detAllPresent() V2Presence {
	return V2Presence{RSOwner: true, RSSpec: true, ProgressingCondition: true, PodRevisionHash: true}
}

// detSim — lider tik döngüsünün testteki aynası: çıktılar V2Carry ile bir
// sonraki tikin önceki durumu olur (P2.2 aynısını CH'den FINAL okur).
type detSim struct {
	t      *testing.T
	set    V2Resolved
	pres   V2Presence
	states []V2WorkloadState
	events []V2Event
	mem    V2Memory
	counts map[string]int
	out    V2Output
	// chRT — v0.10.963 — P2.1: taşınan satırlar CH FINAL okumasındaki gibi
	// döner (DEFAULT 0 → 1970 sentinel'i, bütün zamanlar UTC dışı dilimde).
	chRT bool
}

// detCHZone — sürücünün kolon/sunucu dilimiyle döndürmesini taklit eder.
var detCHZone = time.FixedZone("ch", 3*3600)

// detCHRoundTrip — v0.10.963 — P2.1: V2CHTime ile bağlanıp FINAL ile okunan
// satır: DEFAULT 0 kolonlarında Go sıfırı yerine epoch, dilim UTC değil.
func detCHRoundTrip(states []V2WorkloadState, events []V2Event) ([]V2WorkloadState, []V2Event) {
	rt := func(t time.Time) time.Time { return V2CHTime(t).In(detCHZone) }
	for i := range states {
		s := &states[i]
		s.PendingStartedAt = rt(s.PendingStartedAt)
		s.IncarnationAt, s.FirstSeenAt, s.LastSeenAt = s.IncarnationAt.In(detCHZone), s.FirstSeenAt.In(detCHZone), s.LastSeenAt.In(detCHZone)
	}
	for i := range events {
		e := &events[i]
		e.SucceededAt, e.StuckAt, e.FinishedAt = rt(e.SucceededAt), rt(e.StuckAt), rt(e.FinishedAt)
		e.IncarnationAt, e.StartedAt, e.UpdatedAt = e.IncarnationAt.In(detCHZone), e.StartedAt.In(detCHZone), e.UpdatedAt.In(detCHZone)
	}
	return states, events
}

// saveStates / loseState — v0.10.963 — P2.1: "olay yazıldı, durum
// yazılamadı" (yazım sırası: önce olaylar). loseState iş yükünün durumunu tik
// öncesine döndürür (yoksa siler); olaylar yazılmış kalır.
func (s *detSim) saveStates() []V2WorkloadState { return append([]V2WorkloadState(nil), s.states...) }

func (s *detSim) loseState(before []V2WorkloadState, kind, name string) {
	mine := func(st V2WorkloadState) bool { return st.WorkloadKind == kind && st.Workload == name }
	var out []V2WorkloadState
	for _, st := range s.states {
		if !mine(st) {
			out = append(out, st)
		}
	}
	for _, st := range before {
		if mine(st) {
			out = append(out, st)
		}
	}
	v2SortStates(out)
	s.states = out
}

func newDetSim(t *testing.T) *detSim {
	return &detSim{t: t, set: DefaultSettings().ResolvedV2(), pres: detAllPresent(), counts: map[string]int{}}
}

func (s *detSim) tick(at int, obs ...V2Observation) V2Output {
	s.t.Helper()
	return s.snap(at, V2Snapshot{Complete: true, Presence: s.pres, Workloads: obs})
}

func (s *detSim) snap(at int, sn V2Snapshot) V2Output {
	s.t.Helper()
	out := DetectV2(s.set, V2Input{ClusterID: "cluster-a", Now: detAt(at).Add(5 * time.Second), Snapshot: sn,
		PrevStates: s.states, PrevEvents: s.events, Memory: s.mem})
	for _, e := range out.Events {
		if err := ValidateV2Event(e); err != nil {
			s.t.Fatalf("tik %d: sözleşme dışı olay satırı yayıldı: %v (%+v)", at, err, e)
		}
	}
	for _, st := range out.States {
		if err := ValidateV2State(st); err != nil {
			s.t.Fatalf("tik %d: sözleşme dışı durum satırı yayıldı: %v (%+v)", at, err, st)
		}
	}
	// v0.10.963 — P2.1: çıktıda "ayarlanmadı" = Go sıfırı; okumadaki 1970
	// sentinel'i çıktıya SIZMAZ (bağlama V2CHTime'ın işi).
	leak := func(t time.Time) bool { return !t.IsZero() && !t.After(v2Epoch) }
	for _, st := range out.States {
		if leak(st.PendingStartedAt) {
			s.t.Fatalf("tik %d: durumda epoch sentinel'i sızdı: %+v", at, st)
		}
	}
	for _, e := range out.Events {
		if leak(e.SucceededAt) || leak(e.StuckAt) || leak(e.FinishedAt) {
			s.t.Fatalf("tik %d: olayda epoch sentinel'i sızdı: %+v", at, e)
		}
	}
	s.states, s.events = V2Carry(s.states, s.events, out)
	if s.chRT {
		s.states, s.events = detCHRoundTrip(s.states, s.events)
	}
	s.mem = out.Memory
	for k, v := range out.Diag.Counts {
		s.counts[k] += v
	}
	s.out = out
	open := map[V2Key]int{}
	for _, e := range s.events {
		if e.Status == V2StatusProgressing || e.Status == V2StatusStuck {
			open[e.Key()]++
			if open[e.Key()] > 1 {
				s.t.Fatalf("tik %d: %v için birden çok açık olay", at, e.Key())
			}
		}
	}
	return out
}

func (s *detSim) failover() { s.mem = V2Memory{} }

func detSum(e V2Event) string {
	out := fmt.Sprintf("g%d %s %s %s>%s", e.Generation, e.ChangeType, e.Status, e.OldRevision, e.NewRevision)
	if e.StuckReason != "" {
		out += " stuck=" + e.StuckReason
	}
	return out
}

func (s *detSim) evs(kind, name string) []string {
	var sel []V2Event
	for _, e := range s.events {
		if e.WorkloadKind == kind && e.Workload == name {
			sel = append(sel, e)
		}
	}
	sort.SliceStable(sel, func(i, j int) bool {
		if !sel[i].IncarnationAt.Equal(sel[j].IncarnationAt) {
			return sel[i].IncarnationAt.Before(sel[j].IncarnationAt)
		}
		return sel[i].Generation < sel[j].Generation
	})
	out := []string{}
	for _, e := range sel {
		out = append(out, detSum(e))
	}
	return out
}

func (s *detSim) ev(kind, name string, gen uint64) V2Event {
	s.t.Helper()
	var hit *V2Event
	for i, e := range s.events {
		if e.WorkloadKind == kind && e.Workload == name && e.Generation == gen {
			if hit == nil || e.IncarnationAt.After(hit.IncarnationAt) {
				hit = &s.events[i]
			}
		}
	}
	if hit == nil {
		s.t.Fatalf("%s/%s g%d olayı yok: %v", kind, name, gen, s.evs(kind, name))
	}
	return *hit
}

func (s *detSim) st(kind, name string) V2WorkloadState {
	s.t.Helper()
	for _, st := range s.states {
		if st.WorkloadKind == kind && st.Workload == name {
			return st
		}
	}
	s.t.Fatalf("%s/%s durumu yok", kind, name)
	return V2WorkloadState{}
}

// ── Kural tablosu ─────────────────────────────────────────────────────────

func TestDetectV2_Rules(t *testing.T) {
	const D, S, DS = V2KindDeployment, V2KindStatefulSet, V2KindDaemonSet
	cases := []struct {
		name   string
		tune   func(*V2Resolved, *V2Presence)
		run    func(s *detSim)
		kind   string // "" = Deployment
		wl     string // "" = api
		want   []string
		counts map[string]int // birikimli, alt küme
		check  func(t *testing.T, s *detSim)
	}{
		{name: "ilk-ever koşu yalnız baseline (karar 10)",
			run:    func(s *detSim) { s.tick(0, detSteady("api", 0, 5, "api-b", "api-a")) },
			want:   []string{},
			counts: map[string]int{"baseline": 1, "start": 0, "initial_event": 0}},
		{name: "HPA ölçek gürültüsü asla yazılmaz (§4.4 adım 4)",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).og(1).reps(5, 3, 3, 3).rs("api-a", 5).V())
				s.tick(60, detDep("api", 60, 2).reps(5, 5, 5, 5).rs("api-a", 5).V())
				s.tick(90, detDep("api", 90, 3).reps(2, 2, 2, 2).rs("api-a", 2).V())
			},
			want:   []string{},
			counts: map[string]int{"scale_only": 2, "start": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.Generation != 3 || st.PendingGeneration != 0 {
					t.Fatalf("ölçek artışları emilmeli: %+v", st)
				}
			}},
		{name: "şablonsuz nesil artışı (yalnız annotation) olay değil",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detSteady("api", 30, 2, "api-a"))
			},
			want:   []string{},
			counts: map[string]int{"scale_only": 1}},
		{name: "rollout A→B yeni RS ile başlar, §4.5 ile biter",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 1, "api-a", "api-b"))
				s.tick(60, detRolling("api", 60, 2, 2, "api-a", "api-b"))
				s.tick(90, detSteady("api", 90, 2, "api-b", "api-a"))
			},
			want:   []string{"g2 rollout succeeded api-a>api-b"},
			counts: map[string]int{"baseline": 1, "start": 1, "succeeded": 1}},
		{name: "aynı imaj + yeni RS = config",
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 1).rs("api-a", 3).img("api-a", "reg/checkout-api:1.4").V())
				s.tick(30, detDep("api", 30, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).
					img("api-a", "reg/checkout-api:1.4").img("api-b", "reg/checkout-api:1.4").V())
				s.tick(60, detDep("api", 60, 2).rs("api-b", 3).rs("api-a", 0).img("api-b", "reg/checkout-api:1.4").V())
			},
			want: []string{"g2 config succeeded api-a>api-b"}},
		{name: "ROLLBACK: bilinen RS yeniden etkin → önceki olay rolled_back",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).V())
				s.tick(120, detSteady("api", 120, 3, "api-a", "api-b"))
			},
			want: []string{"g2 rollout rolled_back api-a>api-b", "g3 rollback succeeded api-b>api-a"},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !e.FinishedAt.Equal(detAt(60)) || !strings.Contains(e.Note, "geri alındı") {
					t.Fatalf("başarılı olayın finished_at'i korunur, not düşülür: %+v", e)
				}
			}},
		{name: "GC'lenmiş RS aynı adla yeniden kurulur → ROLLBACK (§4.4 durum c)",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).reps(3, 4, 2, 3).rs("api-b", 2).rs("api-c", 2).V()) // api-a GC'lendi
				s.tick(120, detSteady("api", 120, 3, "api-c", "api-b"))
				s.tick(150, detDep("api", 150, 4).reps(3, 4, 2, 3).rs("api-c", 2).rs("api-a", 2).V()) // api-a geri döndü
				s.tick(180, detSteady("api", 180, 4, "api-a", "api-c"))
			},
			want: []string{"g2 rollout succeeded api-a>api-b", "g3 rollout rolled_back api-b>api-c", "g4 rollback succeeded api-c>api-a"}},
		{name: "knownRevisionsMax geçmişten kısaysa GC'lenmiş dönüş rollout görünür (sınırın nedeni)",
			tune: func(r *V2Resolved, _ *V2Presence) { r.KnownRevisionsMax = 2 },
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).reps(3, 4, 2, 3).rs("api-b", 2).rs("api-c", 2).V())
				s.tick(120, detSteady("api", 120, 3, "api-c", "api-b"))
				s.tick(150, detDep("api", 150, 4).reps(3, 4, 2, 3).rs("api-c", 2).rs("api-a", 2).V())
				s.tick(180, detSteady("api", 180, 4, "api-a", "api-c"))
			},
			want: []string{"g2 rollout succeeded api-a>api-b", "g3 rollout succeeded api-b>api-c", "g4 rollout succeeded api-c>api-a"},
			check: func(t *testing.T, s *detSim) {
				if k := s.st(D, "api").KnownRevisions; len(k) > 2 {
					t.Fatalf("known_revisions sınırı aşıldı: %v", k)
				}
			}},
		{name: "SUPERSEDED: süren rollout'un ortasında yeni RS",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V())
				s.tick(90, detSteady("api", 90, 3, "api-c", "api-a", "api-b"))
			},
			want: []string{"g2 rollout superseded api-a>api-b", "g3 rollout succeeded api-b>api-c"},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !e.FinishedAt.Equal(s.ev(D, "api", 3).StartedAt) {
					t.Fatalf("superseded finished_at = yeni started_at olmalı: %+v", e)
				}
			}},
		{name: "STUCK (stuckAfter) sonra SUCCEEDED; stuck_at tarih olarak kalır",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(600, detRolling("api", 600, 2, 2, "api-a", "api-b"))
				s.tick(630, detRolling("api", 630, 2, 2, "api-a", "api-b"))
				s.tick(900, detSteady("api", 900, 2, "api-b", "api-a"))
			},
			want:   []string{"g2 rollout succeeded api-a>api-b stuck=timeout"},
			counts: map[string]int{"stuck": 1, "succeeded": 1},
			check: func(t *testing.T, s *detSim) {
				e := s.ev(D, "api", 2)
				if !e.StuckAt.Equal(detAt(630)) || !e.SucceededAt.Equal(detAt(900)) || !e.FinishedAt.Equal(detAt(900)) {
					t.Fatalf("zamanlar: %+v", e)
				}
			}},
		{name: "STUCK progress_deadline; START anındaki bayat koşul sayılmaz",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).og(1).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
				if e := s.ev(D, "api", 2); e.Status != V2StatusProgressing {
					s.t.Fatalf("observed_generation yetişmeden Progressing=false bayattır: %+v", e)
				}
				s.tick(60, detDep("api", 60, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
			},
			want: []string{"g2 rollout stuck api-a>api-b stuck=progress_deadline"}},
		{name: "rollout ortasında geri alma (undo): hedef eski RS'ye yakınsar → ROLLBACK",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).V()) // undo
				s.tick(90, detDep("api", 90, 3).rs("api-a", 3).rs("api-b", 0).V())
			},
			want:   []string{"g2 rollout rolled_back api-a>api-b", "g3 rollback succeeded api-b>api-a"},
			counts: map[string]int{"ride_along": 1},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 3); !e.StartedAt.Equal(detAt(60)) {
					t.Fatalf("geri alma, bekleyen artışın ilk örneğinde başlar: %v", e.StartedAt)
				}
				if e := s.ev(D, "api", 2); !e.FinishedAt.Equal(detAt(60)) {
					t.Fatalf("rolled_back finished_at = geri almanın started_at'i: %v", e.FinishedAt)
				}
			}},
		{name: "rollout ortasında HPA artışı olayla birlikte bekler, başarıda emilir",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(4, 5, 3, 4).rs("api-a", 1).rs("api-b", 3).V())
				s.tick(90, detDep("api", 90, 3).reps(4, 4, 4, 4).rs("api-b", 4).rs("api-a", 0).V())
			},
			want:   []string{"g2 rollout succeeded api-a>api-b"},
			counts: map[string]int{"ride_along": 1, "scale_only": 1, "start": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.Generation != 3 || st.PendingGeneration != 0 || st.OpenGeneration != 0 {
					t.Fatalf("bekleyen artış başarıda emilmeli: %+v", st)
				}
			}},
		{name: "paused: artış kapı süresini aşsa da bekler; resume rollout'u başlatır",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).rs("api-a", 3).paused().V())
				s.tick(630, detDep("api", 630, 2).rs("api-a", 3).paused().V())
				s.tick(660, detDep("api", 660, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).V())
			},
			want:   []string{"g3 rollout progressing api-a>api-b"},
			counts: map[string]int{"gate_timeout": 0, "scale_only": 0},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 3); !e.StartedAt.Equal(detAt(660)) {
					t.Fatalf("started_at = resume neslinin ilk örneği: %v", e.StartedAt)
				}
			}},
		{name: "observed_generation hiç yetişmez: kapı süresi dolunca olaysız emilir",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				for at := 30; at <= 330; at += 30 {
					s.tick(at, detDep("api", at, 2).og(1).rs("api-a", 3).V())
				}
			},
			want:   []string{},
			counts: map[string]int{"gate_timeout": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.Generation != 2 || st.PendingGeneration != 0 {
					t.Fatalf("zaman aşımında emilmeli: %+v", st)
				}
			}},
		{name: "eski pod var (tutma) + RS gecikmeli: START ilk örnek zamanını korur",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).reps(3, 3, 0, 3).rs("api-a", 3).V())
				s.tick(60, detRolling("api", 60, 2, 2, "api-a", "api-b"))
			},
			want: []string{"g2 rollout progressing api-a>api-b"},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !e.StartedAt.Equal(detAt(30)) {
					t.Fatalf("started_at = pending_started_at: %v", e.StartedAt)
				}
			}},
		{name: "tutma sinyali yokken emilen artış + geç RS kanıtı → geç START",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detSteady("api", 30, 2, "api-a"))
				s.tick(60, detRolling("api", 60, 2, 2, "api-a", "api-b"))
			},
			want:   []string{"g2 rollout progressing api-a>api-b"},
			counts: map[string]int{"late_evidence": 1, "scale_only": 1},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !e.StartedAt.Equal(detAt(60)) || !strings.Contains(e.Note, "geç") {
					t.Fatalf("geç START kendi örneğinde başlar ve not taşır: %+v", e)
				}
			}},
		{name: "nesil artışı olmadan yeniden etkin RS olay değildir",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-b", "api-a"))
				s.tick(30, detDep("api", 30, 1).rs("api-b", 3).rs("api-a", 1).V())
			},
			want:   []string{},
			counts: map[string]int{"reactivation_without_bump": 1}},
		{name: "RS owner ailesi yok: şablon değişimi pod sayılarından genel START",
			tune: func(_ *V2Resolved, p *V2Presence) { *p = V2Presence{ProgressingCondition: true} },
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 1).V())
				s.tick(30, detDep("api", 30, 2).reps(3, 4, 1, 3).V())
				s.tick(60, detDep("api", 60, 2).V())
			},
			want:   []string{"g2 rollout succeeded >"},
			counts: map[string]int{"generic_start": 1}},
		{name: "RS spec ailesi yok: bilinen RS'ye dönüş kapı süresi sonunda ROLLBACK",
			tune: func(_ *V2Resolved, p *V2Presence) { *p = V2Presence{RSOwner: true, ProgressingCondition: true} },
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 1).rs("api-b", 0).rs("api-a", 0).V())
				for at := 30; at <= 330; at += 30 {
					s.tick(at, detDep("api", at, 2).reps(3, 4, 1, 3).rs("api-b", 0).rs("api-a", 0).V())
				}
				s.tick(360, detDep("api", 360, 2).rs("api-b", 0).rs("api-a", 0).V())
			},
			want:   []string{"g2 rollback succeeded >"},
			counts: map[string]int{"generic_start": 1},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !e.StartedAt.Equal(detAt(30)) {
					t.Fatalf("gecikmeli karar started_at'i değiştirmez: %v", e.StartedAt)
				}
			}},
		{name: "silinip yeniden kurulan iş yükü: K tam okuma yokluk → yeni incarnation + initial",
			run: func(s *detSim) {
				web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") }
				s.tick(0, detSteady("api", 0, 1, "api-a"), web(0))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"), web(30))
				s.tick(60, web(60))
				s.tick(90, web(90))
				s.tick(120, web(120))
				s.tick(150, detSteady("api", 150, 1, "api-x"), web(150))
			},
			want:   []string{"g2 rollout superseded api-a>api-b", "g1 initial succeeded >api-x"},
			counts: map[string]int{"absent": 3, "gone": 1, "new_incarnation": 1, "initial_event": 1},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(D, "api", 2); !strings.Contains(e.Note, "okumada yok") || e.FinishedAt.IsZero() {
					t.Fatalf("kaybolan iş yükünün açık olayı kapanır: %+v", e)
				}
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(120)) {
					t.Fatalf("incarnation_at = ilk tam okuma dakikaya kesik (10:02): %v", st.IncarnationAt)
				}
				if _, ok := s.mem.Absent[V2Key{"cluster-a", "pay", D, "api"}]; ok {
					t.Fatal("yeniden görünen iş yükünün yokluk sayacı silinmeli")
				}
			}},
		{name: "yalnız yokluk kuralı: K tam okuma yok, nesil gerilemeden geri gelir → yeni incarnation",
			run: func(s *detSim) {
				web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") }
				s.tick(0, detSteady("api", 0, 2, "api-a"), web(0))
				s.tick(30, web(30))
				s.tick(60, web(60))
				s.tick(90, web(90))
				s.tick(120, detSteady("api", 120, 3, "api-z"), web(120))
			},
			want:   []string{"g3 initial succeeded >api-z"},
			counts: map[string]int{"absent": 3, "gone": 1, "new_incarnation": 1}},
		{name: "yokluk K−1 tam okuma: aynı incarnation, olay yok",
			run: func(s *detSim) {
				web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") }
				s.tick(0, detSteady("api", 0, 2, "api-a"), web(0))
				s.tick(30, web(30))
				s.tick(60, web(60))
				s.tick(90, detSteady("api", 90, 2, "api-a"), web(90))
			},
			want:   []string{},
			counts: map[string]int{"absent": 2, "gone": 0, "new_incarnation": 0}},
		{name: "bootstrap'ta controller gecikmesi: hedef bilinmezken etkin bilinen RS rollback sayılmaz",
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 2).og(1).rs("api-a", 3).rs("api-b", 0).V())
				s.tick(30, detDep("api", 30, 3).og(1).rs("api-a", 3).rs("api-b", 0).V())
				s.tick(60, detDep("api", 60, 3).rs("api-a", 3).rs("api-b", 0).V())
			},
			want: []string{},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-a" || st.Generation != 3 {
					t.Fatalf("yerleşince hedef sessizce benimsenmeli: %+v", st)
				}
			}},
		{name: "bootstrap maxSurge=0 rollout ortası: yeni RS henüz 0 iken hedef alınmaz; sonraki HPA olay değil",
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 2).reps(3, 3, 0, 3).rs("api-a", 3).rs("api-b", 0).V())
				s.tick(30, detDep("api", 30, 2).reps(3, 3, 1, 2).rs("api-a", 2).rs("api-b", 1).V())
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).reps(5, 5, 5, 5).rs("api-b", 5).rs("api-a", 0).V())
			},
			want:   []string{},
			counts: map[string]int{"adopt": 1, "scale_only": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-b" {
					t.Fatalf("hedef yeni RS olmalı: %+v", st)
				}
			}},
		{name: "hızlı yeniden kurulum: nesil geriledi → yeni incarnation (aynı dakikada +1 dk)",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 7, "api-a"))
				s.tick(30, detSteady("api", 30, 1, "api-a"))
			},
			want:   []string{"g1 initial succeeded >api-a"},
			counts: map[string]int{"new_incarnation": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(60)) {
					t.Fatalf("anahtar çakışmasın: incarnation_at eskiden sonra olmalı: %v", st.IncarnationAt)
				}
			}},
		{name: "_created varsa incarnation ondan; daha yeni _created = yeni incarnation; aynıysa nesil gerilemesi görmezden",
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 3).rs("api-a", 3).created(detAt(-3600)).V())
				s.tick(30, detDep("api", 30, 4).rs("api-a", 3).created(detAt(20)).V())
				s.tick(60, detDep("api", 60, 3).rs("api-a", 3).created(detAt(20)).V())
			},
			want:   []string{"g4 initial succeeded >api-a"},
			counts: map[string]int{"new_incarnation": 1, "generation_regressed": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(20)) || st.Generation != 4 {
					t.Fatalf("incarnation_at = _created; gerileme durumu değiştirmez: %+v", st)
				}
			}},
		{name: "bootstrap sonrası ilk kez görülen iş yükü → initial (karar 10)",
			wl: "web",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detSteady("api", 30, 1, "api-a"), detSteady("web", 30, 1, "web-a"))
			},
			want:   []string{"g1 initial succeeded >web-a"},
			counts: map[string]int{"baseline": 1, "initial_event": 1}},
		{name: "initialEvents=false → yeni iş yükü yalnız baseline",
			wl:   "web",
			tune: func(r *V2Resolved, _ *V2Presence) { r.InitialEvents = false },
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detSteady("api", 30, 1, "api-a"), detSteady("web", 30, 1, "web-a"))
			},
			want:   []string{},
			counts: map[string]int{"baseline": 2, "initial_event": 0}},
		{name: "StatefulSet: yeni update_revision rollout, bilinene dönüş rollback, ölçek emilir",
			kind: S, wl: "db",
			run: func(s *detSim) {
				s.tick(0, detSts("db", 0, 1, "db-r1", "db-r1").V())
				s.tick(30, detSts("db", 30, 2, "db-r1", "db-r2").reps(3, 3, 1, 3).ready(2).V())
				s.tick(60, detSts("db", 60, 2, "db-r2", "db-r2").V())
				s.tick(90, detSts("db", 90, 3, "db-r2", "db-r1").reps(3, 3, 1, 3).V())
				s.tick(120, detSts("db", 120, 3, "db-r1", "db-r1").V())
				s.tick(150, detSts("db", 150, 4, "db-r1", "db-r1").reps(5, 5, 5, 5).ready(5).V())
			},
			want:   []string{"g2 rollout rolled_back db-r1>db-r2", "g3 rollback succeeded db-r2>db-r1"},
			counts: map[string]int{"scale_only": 1}},
		{name: "DaemonSet hash'leriyle: tutma + yeni hash rollout, bilinen hash rollback",
			kind: DS, wl: "agent",
			run: func(s *detSim) {
				s.tick(0, detDs("agent", 0, 1, "h1").V())
				s.tick(30, detDs("agent", 30, 2, "h1").reps(3, 0, 0, 3).V())
				s.tick(60, detDs("agent", 60, 2, "h1", "h2").reps(3, 0, 1, 2).V())
				s.tick(90, detDs("agent", 90, 2, "h2").V())
				s.tick(120, detDs("agent", 120, 3, "h2").reps(3, 0, 0, 3).V())
				s.tick(150, detDs("agent", 150, 3, "h2", "h1").reps(3, 0, 1, 2).V())
				s.tick(180, detDs("agent", 180, 3, "h1").V())
			},
			want: []string{"g2 rollout rolled_back h1>h2", "g3 rollback succeeded h2>h1"},
			check: func(t *testing.T, s *detSim) {
				if e := s.ev(DS, "agent", 2); !e.StartedAt.Equal(detAt(30)) {
					t.Fatalf("DS START ilk artış örneğinde: %v", e.StartedAt)
				}
			}},
		{name: "DaemonSet hash'siz: artış + updated < desired → hemen genel START (§4.11)",
			kind: DS, wl: "agent",
			tune: func(_ *V2Resolved, p *V2Presence) { p.PodRevisionHash = false },
			run: func(s *detSim) {
				s.tick(0, detDs("agent", 0, 1).V())
				s.tick(30, detDs("agent", 30, 2).reps(3, 0, 1, 2).V())
				s.tick(60, detDs("agent", 60, 2).V())
			},
			want: []string{"g2 rollout succeeded >"}},
		{name: "kinds dışındaki tür yok sayılır",
			kind: DS, wl: "agent",
			tune: func(r *V2Resolved, _ *V2Presence) { r.Kinds = []string{V2KindDeployment} },
			run: func(s *detSim) {
				s.tick(0, detDs("agent", 0, 1, "h1").V())
				s.tick(30, detDs("agent", 30, 2, "h2").reps(3, 0, 0, 3).V())
			},
			want:   []string{},
			counts: map[string]int{"ignored_kind": 2},
			check: func(t *testing.T, s *detSim) {
				if len(s.states) != 0 {
					t.Fatalf("kapalı tür için durum yazılmamalı: %+v", s.states)
				}
			}},
		{name: "ignoreScale=false: ölçek artışı yalnız teşhiste görünür, satır YOK (§10.3.1 sözlüğü)",
			tune: func(r *V2Resolved, _ *V2Presence) { r.IgnoreScale = false },
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).reps(4, 4, 4, 4).rs("api-a", 4).V())
			},
			want:   []string{},
			counts: map[string]int{"scale_only_not_ignored": 1}},

		// ── v0.10.963 — P2.1 inceleme düzeltmeleri ─────────────────────────
		// R1 / P21-2: açık olay varken boşalan eski-eski RS "yeniden etkin" değildir.
		{name: "SUPERSEDED sonrası HPA artışı sahte ROLLBACK değil (eski-eski RS boşalıyor)",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V())
				s.tick(90, detDep("api", 90, 4).reps(4, 5, 3, 4).rs("api-a", 1).rs("api-b", 1).rs("api-c", 3).V()) // HPA 3→4
				s.tick(120, detDep("api", 120, 4).reps(4, 4, 4, 4).rs("api-a", 0).rs("api-b", 0).rs("api-c", 4).V())
				s.tick(600, detDep("api", 600, 5).reps(5, 5, 5, 5).rs("api-a", 0).rs("api-b", 0).rs("api-c", 5).V()) // sonraki HPA
			},
			want:   []string{"g2 rollout superseded api-a>api-b", "g3 rollout succeeded api-b>api-c"},
			counts: map[string]int{"ride_along": 1, "scale_only": 2},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-c" || st.Generation != 5 {
					t.Fatalf("hedef api-c kalmalı: %+v", st)
				}
			}},
		{name: "DaemonSet: superseded sonrası düğüm eklenmesi (desired artışı) sahte ROLLBACK değil",
			kind: DS, wl: "agent",
			run: func(s *detSim) {
				s.tick(0, detDs("agent", 0, 1, "h1").V())
				s.tick(30, detDs("agent", 30, 2, "h1", "h2").reps(3, 0, 1, 2).V())
				s.tick(60, detDs("agent", 60, 3, "h1", "h2", "h3").reps(3, 0, 1, 2).V())
				s.tick(90, detDs("agent", 90, 4, "h1", "h2", "h3").reps(4, 0, 1, 3).V())
				s.tick(120, detDs("agent", 120, 4, "h3").reps(4, 0, 4, 4).V())
			},
			want: []string{"g2 rollout superseded h1>h2", "g3 rollout succeeded h2>h3"},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(DS, "agent"); st.CurrentRevision != "h3" {
					t.Fatalf("hedef h3 kalmalı: %+v", st)
				}
			}},
		{name: "superseded + stuck + annotation artışı: iki eski RS etkinken ROLLBACK yok",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V())
				s.tick(700, detDep("api", 700, 3).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V())
				s.tick(730, detDep("api", 730, 4).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V())
			},
			want: []string{"g2 rollout superseded api-a>api-b", "g3 rollout stuck api-b>api-c stuck=timeout"}},
		{name: "açık rollout'ta 3 etkin RS ile undo: yakınsayınca ROLLBACK, başlangıç bekleyen artışın ilk örneği",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).reps(3, 4, 2, 3).rs("api-a", 0).rs("api-b", 2).rs("api-c", 2).V())
				s.tick(120, detDep("api", 120, 4).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 2).V()) // undo --to-revision api-a
				s.tick(150, detDep("api", 150, 4).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 1).rs("api-c", 1).V())
				s.tick(180, detSteady("api", 180, 4, "api-a", "api-b", "api-c"))
			},
			want: []string{"g2 rollout succeeded api-a>api-b", "g3 rollout rolled_back api-b>api-c", "g4 rollback succeeded api-c>api-a"},
			check: func(t *testing.T, s *detSim) {
				g3, g4 := s.ev(D, "api", 3), s.ev(D, "api", 4)
				if !g4.StartedAt.Equal(detAt(120)) || !g3.FinishedAt.Equal(detAt(120)) {
					t.Fatalf("geri alma bekleyen artışın ilk örneğinde başlar: g3 %+v g4 %+v", g3, g4)
				}
			}},
		// P21-3: bootstrap'ta rollout ortası + HPA, kapı süresi dolunca sahte rollback değil.
		{name: "bootstrap rollout ortası + HPA artışı + yavaş bitiş: olay yok, hedef sessizce benimsenir",
			run: func(s *detSim) {
				s.tick(0, detRolling("api", 0, 2, 2, "api-a", "api-b"))
				s.tick(30, detRolling("api", 30, 3, 3, "api-a", "api-b"))
				for at := 60; at <= 360; at += 30 {
					s.tick(at, detDep("api", at, 3).reps(3, 4, 2, 3).rs("api-a", 1).rs("api-b", 3).V())
				}
				s.tick(390, detSteady("api", 390, 3, "api-b", "api-a"))
			},
			want:   []string{},
			counts: map[string]int{"gate_timeout": 1, "generic_start": 0, "start": 0, "adopt": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-b" || st.Generation != 3 || st.PendingGeneration != 0 {
					t.Fatalf("artış emilmeli, hedef benimsenmeli: %+v", st)
				}
			}},
		{name: "bootstrap rollout ortası + HPA artışı, rollout hiç bitmez: olay yok (sahte rollback stuck olmaz)",
			run: func(s *detSim) {
				s.tick(0, detRolling("api", 0, 2, 2, "api-a", "api-b"))
				s.tick(30, detRolling("api", 30, 3, 3, "api-a", "api-b"))
				for at := 60; at <= 1200; at += 30 {
					s.tick(at, detDep("api", at, 3).reps(3, 4, 2, 3).rs("api-a", 1).rs("api-b", 3).V())
				}
			},
			want:   []string{},
			counts: map[string]int{"gate_timeout": 1, "generic_start": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "" {
					t.Fatalf("yerleşmeden hedef alınmaz: %+v", st)
				}
			}},
		// R3: geç START'ın durumu kaybolursa hedef ilerlemeli.
		{name: "geç START'ın durumu kaybolursa sonraki tik start_reused ile toparlar",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detSteady("api", 30, 2, "api-a")) // artış emildi (tutma yok)
				before := s.saveStates()
				s.tick(60, detRolling("api", 60, 2, 2, "api-a", "api-b"))
				s.loseState(before, D, "api")
				s.tick(90, detRolling("api", 90, 2, 2, "api-a", "api-b"))
				for at := 120; at <= 750; at += 30 {
					s.tick(at, detSteady("api", at, 2, "api-b", "api-a"))
				}
			},
			want:   []string{"g2 rollout succeeded api-a>api-b"},
			counts: map[string]int{"late_evidence": 1, "start_reused": 1, "evidence_without_bump": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-b" {
					t.Fatalf("durum hedefi api-b olmalı: %+v", st)
				}
			}},
		{name: "StatefulSet: geç START'ın durumu kaybolursa start_reused ile toparlar",
			kind: S, wl: "db",
			run: func(s *detSim) {
				s.tick(0, detSts("db", 0, 1, "db-1", "db-1").V())
				s.tick(30, detSts("db", 30, 2, "db-1", "db-1").V())
				before := s.saveStates()
				s.tick(60, detSts("db", 60, 2, "db-1", "db-2").reps(3, 3, 1, 3).ready(2).V())
				s.loseState(before, S, "db")
				s.tick(90, detSts("db", 90, 2, "db-1", "db-2").reps(3, 3, 2, 3).ready(2).V())
				s.tick(120, detSts("db", 120, 2, "db-2", "db-2").V())
			},
			want:   []string{"g2 rollout succeeded db-1>db-2"},
			counts: map[string]int{"late_evidence": 1, "start_reused": 1, "evidence_without_bump": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(S, "db"); st.CurrentRevision != "db-2" {
					t.Fatalf("durum hedefi db-2 olmalı: %+v", st)
				}
			}},
		// R4: mint yolunda durum kaybolursa ikinci 'initial' / hayalet incarnation yok.
		{name: "initial'in durumu kaybolur: sonraki tik (başka dakika) tek incarnation, tek initial",
			wl: "web",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				before := s.saveStates()
				s.tick(30, detSteady("api", 30, 1, "api-a"), detSteady("web", 30, 1, "web-a"))
				s.loseState(before, D, "web")
				s.tick(90, detSteady("api", 90, 1, "api-a"), detSteady("web", 90, 1, "web-a"))
			},
			want:   []string{"g1 initial succeeded >web-a"},
			counts: map[string]int{"lost_state": 1, "initial_event": 1},
			check: func(t *testing.T, s *detSim) {
				e, st := s.ev(D, "web", 1), s.st(D, "web")
				if !e.StartedAt.Equal(detAt(30)) || !st.IncarnationAt.Equal(e.IncarnationAt) || !st.IncarnationAt.Equal(detAt(0)) {
					t.Fatalf("incarnation ve started_at korunmalı: olay %+v durum %+v", e, st)
				}
			}},
		{name: "nesil gerilemesiyle yeni incarnation'ın durumu kaybolur: hayalet incarnation yok",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 5, "api-a"))
				before := s.saveStates()
				s.tick(30, detSteady("api", 30, 1, "api-a"))
				s.loseState(before, D, "api")
				s.tick(150, detSteady("api", 150, 1, "api-a"))
			},
			want:   []string{"g1 initial succeeded >api-a"},
			counts: map[string]int{"lost_state": 1, "new_incarnation": 1},
			check: func(t *testing.T, s *detSim) {
				if st, e := s.st(D, "api"), s.ev(D, "api", 1); !st.IncarnationAt.Equal(e.IncarnationAt) || !st.IncarnationAt.Equal(detAt(60)) || st.Generation != 1 {
					t.Fatalf("durum olayın incarnation'ında olmalı: %+v / %+v", st, e)
				}
			}},
		{name: "yoklukla yeni incarnation'ın durumu kaybolur: açık initial eski durumda asılı kalmaz",
			run: func(s *detSim) {
				web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") }
				s.tick(0, detSteady("api", 0, 2, "api-a"), web(0))
				s.tick(30, web(30))
				s.tick(60, web(60))
				s.tick(90, web(90))
				before := s.saveStates()
				s.tick(120, detDep("api", 120, 2).reps(3, 3, 3, 2).rs("api-a", 3).V(), web(120)) // yerleşik, available eksik
				s.loseState(before, D, "api")
				s.tick(180, detSteady("api", 180, 2, "api-a"), web(180))
			},
			want:   []string{"g2 initial succeeded >api-a"},
			counts: map[string]int{"lost_state": 1, "new_incarnation": 1, "succeeded": 1},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(120)) || st.OpenGeneration != 0 {
					t.Fatalf("durum yeni incarnation'da, açık olay yok: %+v", st)
				}
			}},
		{name: "initial'in durumu kaybolur + boşlukta rollout: tek incarnation, initial superseded",
			wl: "web",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				before := s.saveStates()
				s.tick(30, detSteady("api", 30, 1, "api-a"), detDep("web", 30, 1).reps(3, 3, 3, 2).rs("web-a", 3).V())
				s.loseState(before, D, "web")
				s.tick(90, detSteady("api", 90, 1, "api-a"), detDep("web", 90, 2).reps(3, 4, 2, 3).rs("web-a", 2).rs("web-b", 2).V())
				s.tick(120, detSteady("api", 120, 1, "api-a"), detSteady("web", 120, 2, "web-b", "web-a"))
			},
			want:   []string{"g1 initial superseded >web-a", "g2 rollout succeeded web-a>web-b"},
			counts: map[string]int{"lost_state": 1, "initial_event": 1},
			check: func(t *testing.T, s *detSim) {
				g1, g2 := s.ev(D, "web", 1), s.ev(D, "web", 2)
				if !g1.IncarnationAt.Equal(g2.IncarnationAt) || !g1.StartedAt.Equal(detAt(30)) || !s.st(D, "web").IncarnationAt.Equal(g1.IncarnationAt) {
					t.Fatalf("tek incarnation, started_at korunur: %+v / %+v", g1, g2)
				}
			}},
		// R5: _created varken 1 dk içinde yeniden kurulum tespiti dondurmaz.
		{name: "_created varken 1 dk içinde yeniden kurulum: nesil geriledi + daha yeni _created → yeni incarnation",
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 4).rs("api-a", 3).created(detAt(-10)).V())
				s.tick(60, detDep("api", 60, 1).rs("api-b", 3).created(detAt(40)).V())
				s.tick(90, detDep("api", 90, 2).reps(3, 4, 2, 3).rs("api-b", 2).rs("api-c", 2).created(detAt(40)).V())
			},
			want:   []string{"g1 initial succeeded >api-b", "g2 rollout progressing api-b>api-c"},
			counts: map[string]int{"new_incarnation": 1, "generation_regressed": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(40)) || !s.ev(D, "api", 2).IncarnationAt.Equal(detAt(40)) {
					t.Fatalf("yeni nesnenin rollout'u yeni incarnation'a ait: %+v", st)
				}
			}},
		// P21-6: StatefulSet revizyon serisi yoksa şablon değişimi sessizce emilmez.
		{name: "StatefulSet revizyon serisi yok: artış + eski revizyonlu pod → genel START",
			kind: S, wl: "db",
			run: func(s *detSim) {
				s.tick(0, detSts("db", 0, 1, "", "").V())
				s.tick(30, detSts("db", 30, 2, "", "").reps(3, 3, 1, 3).V())
				s.tick(60, detSts("db", 60, 2, "", "").V())
			},
			want:   []string{"g2 rollout succeeded >"},
			counts: map[string]int{"generic_start": 1, "scale_only": 0}},
		{name: "StatefulSet revizyon serisi yok: ölçek artışı (updated == status < spec) emilir",
			kind: S, wl: "db",
			run: func(s *detSim) {
				s.tick(0, detSts("db", 0, 1, "", "").V())
				s.tick(30, detSts("db", 30, 2, "", "").reps(5, 4, 4, 3).V())
			},
			want:   []string{},
			counts: map[string]int{"scale_only": 1, "generic_start": 0}},
		{name: "StatefulSet revizyonlu yavaş OrderedReady ölçek artışı kapı süresini aşar: olay yok",
			kind: S, wl: "db",
			run: func(s *detSim) {
				s.tick(0, detSts("db", 0, 1, "db-1", "db-1").reps(5, 5, 5, 5).ready(5).V())
				for at := 30; at <= 390; at += 30 {
					s.tick(at, detSts("db", at, 2, "db-1", "db-1").reps(10, 6, 6, 6).ready(6).V())
				}
				s.tick(420, detSts("db", 420, 2, "db-1", "db-1").reps(10, 10, 10, 10).ready(10).V())
			},
			want:   []string{},
			counts: map[string]int{"scale_only": 1, "generic_start": 0}},
		// P21-4: sağ kalan mutantları öldüren adanmış kurallar.
		{name: "progress_deadline: controller geride (og<gen) iken sonraki örnekte de bayat",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).og(1).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
				s.tick(60, detDep("api", 60, 2).og(1).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
			},
			want: []string{"g2 rollout progressing api-a>api-b"}},
		{name: "progress_deadline: og yetişmiş olsa da START örneğindeki koşul bayat; sonraki örnekte stuck",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
				if e := s.ev(D, "api", 2); e.Status != V2StatusProgressing {
					s.t.Fatalf("START örneğindeki Progressing=false bayattır: %+v", e)
				}
				s.tick(60, detDep("api", 60, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).pde().V())
			},
			want: []string{"g2 rollout stuck api-a>api-b stuck=progress_deadline"}},
		{name: "paused iken stuckAfter geçse de stuck yok",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detDep("api", 60, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).paused().V())
				s.tick(700, detDep("api", 700, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2).paused().V())
			},
			want:   []string{"g2 rollout progressing api-a>api-b"},
			counts: map[string]int{"stuck": 0}},
		{name: "initial olay ROLLBACK ile rolled_back olmaz",
			run: func(s *detSim) {
				s.tick(0, detSteady("web", 0, 1, "web-a"))
				s.tick(30, detSteady("web", 30, 1, "web-a"), detSteady("api", 30, 1, "api-b", "api-a"))
				s.tick(60, detSteady("web", 60, 1, "web-a"), detDep("api", 60, 2).reps(3, 4, 2, 3).rs("api-b", 2).rs("api-a", 2).V())
			},
			want: []string{"g1 initial succeeded >api-b", "g2 rollback progressing api-b>api-a"}},
		{name: "_created sonradan görünür, aynı dakika içinde: yeni incarnation değil (+1 dk toleransı)",
			run: func(s *detSim) {
				s.tick(30, detSteady("api", 30, 1, "api-a"))
				s.tick(60, detDep("api", 60, 1).rs("api-a", 3).created(detAt(10)).V())
			},
			want:   []string{},
			counts: map[string]int{"new_incarnation": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); !st.IncarnationAt.Equal(detAt(0)) {
					t.Fatalf("incarnation değişmemeli: %v", st.IncarnationAt)
				}
			}},
		{name: "açık olay varken durumda open_generation = olayın nesli",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
			},
			want: []string{"g2 rollout progressing api-a>api-b"},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.OpenGeneration != 2 {
					t.Fatalf("open_generation = 2 olmalı: %+v", st)
				}
			}},
		{name: "knownRevisionsMax: iki aday varken EN ESKİ atılır",
			tune: func(r *V2Resolved, _ *V2Presence) { r.KnownRevisionsMax = 2 },
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detDep("api", 90, 3).rs("api-c", 3).V()) // api-a ve api-b okumada yok
			},
			want: []string{"g2 rollout succeeded api-a>api-b", "g3 rollout succeeded api-b>api-c"},
			check: func(t *testing.T, s *detSim) {
				if k := s.st(D, "api").KnownRevisions; !reflect.DeepEqual(k, []string{"api-b", "api-c"}) {
					t.Fatalf("en eski (api-a) atılmalı: %v", k)
				}
			}},
		{name: "yinelenen gözlem (dedup kaçağı): büyük generation kazanır",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"), detSteady("api", 30, 1, "api-a"))
			},
			want:   []string{"g2 rollout progressing api-a>api-b"},
			counts: map[string]int{"duplicate_observation": 1}},
		{name: "artışsız yeni RS, olay güncel nesilde: yalnız teşhis, hedef değişmez",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
				s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
				s.tick(90, detSteady("api", 90, 2, "api-b", "api-a", "api-x"))
			},
			want:   []string{"g2 rollout succeeded api-a>api-b"},
			counts: map[string]int{"evidence_without_bump": 1, "late_evidence": 0},
			check: func(t *testing.T, s *detSim) {
				if st := s.st(D, "api"); st.CurrentRevision != "api-b" {
					t.Fatalf("hedef değişmemeli: %+v", st)
				}
			}},
		{name: "iki bilinmeyen RS: spec'i büyük olan hedef (ad sırası ağırlığı örtmez)",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 3).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-z", 0).rs("api-m", 2).V())
			},
			want: []string{"g3 rollout progressing api-a>api-m"}},
		{name: "nesil gerilemesi bekleyen nesle göre ölçülür",
			run: func(s *detSim) {
				s.tick(0, detSteady("api", 0, 1, "api-a"))
				s.tick(30, detDep("api", 30, 3).og(1).rs("api-a", 3).V()) // pending_generation 3
				s.tick(60, detSteady("api", 60, 2, "api-a"))
			},
			want:   []string{"g2 initial succeeded >api-a"},
			counts: map[string]int{"new_incarnation": 1}},
		{name: "new_revision boş olay ROLLBACK ile rolled_back olmaz (RS spec ailesi yok)",
			tune: func(_ *V2Resolved, p *V2Presence) { *p = V2Presence{RSOwner: true, ProgressingCondition: true} },
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 1).rs("api-b", 0).rs("api-a", 0).V())
				for at := 30; at <= 330; at += 30 {
					s.tick(at, detDep("api", at, 2).reps(3, 4, 1, 3).rs("api-b", 0).rs("api-a", 0).V())
				}
				s.tick(360, detDep("api", 360, 2).rs("api-b", 0).rs("api-a", 0).V())
				for at := 390; at <= 690; at += 30 {
					s.tick(at, detDep("api", at, 3).reps(3, 4, 1, 3).rs("api-b", 0).rs("api-a", 0).V())
				}
			},
			want: []string{"g2 rollback succeeded >", "g3 rollback progressing >"}},
		{name: "benimsenen hedef boş new_revision'ı doldurur (RS spec ailesi yok)",
			tune: func(_ *V2Resolved, p *V2Presence) { *p = V2Presence{RSOwner: true, ProgressingCondition: true} },
			run: func(s *detSim) {
				s.tick(0, detDep("api", 0, 1).rs("api-b", 0).rs("api-a", 0).V())
				for at := 30; at <= 330; at += 30 {
					s.tick(at, detDep("api", at, 2).reps(3, 4, 1, 3).rs("api-b", 0).rs("api-a", 0).V())
				}
				s.tick(360, detDep("api", 360, 2).rs("api-a", 0).V())
			},
			want:   []string{"g2 rollback succeeded >api-a"},
			counts: map[string]int{"adopt": 1}},
	}
	// v0.10.963 — P2.1: her kural iki kez koşar — ikincisinde taşınan satırlar
	// CH FINAL okumasındaki gibi döner (DEFAULT 0 → 1970, UTC dışı dilim).
	// Hiçbir kural satırı reddedilmemeli (yazıcı sözleşmesi kendi
	// sentinel'ini reddederse iş yükü donar: R2 / P21-1).
	for _, tc := range cases {
		for _, chRT := range []bool{false, true} {
			name := tc.name
			if chRT {
				name += " [CH gidiş-dönüş]"
			}
			t.Run(name, func(t *testing.T) {
				s := newDetSim(t)
				s.chRT = chRT
				if tc.tune != nil {
					tc.tune(&s.set, &s.pres)
				}
				tc.run(s)
				kind, wl := tc.kind, tc.wl
				if kind == "" {
					kind = D
				}
				if wl == "" {
					wl = "api"
				}
				if got := s.evs(kind, wl); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("olaylar\n got %q\nwant %q\nsayaçlar %v", got, tc.want, s.counts)
				}
				for k, v := range tc.counts {
					if s.counts[k] != v {
						t.Fatalf("sayaç %s = %d, beklenen %d (tümü %v)", k, s.counts[k], v, s.counts)
					}
				}
				if s.counts["rejected"] != 0 {
					t.Fatalf("sözleşme satır reddetti: %v", s.counts)
				}
				if tc.check != nil {
					tc.check(t, s)
				}
			})
		}
	}
}

func TestDetectV2_ChangeTypeFromImages(t *testing.T) {
	cases := []struct {
		name        string
		atStart     []string // START tikinde api-b imajları (nil = okunmadı)
		later       []string // sonraki tikte api-b imajları
		want        string
		wantImages  []string
		wantPrevImg []string
	}{
		{"imaj değişti → rollout", []string{"reg/checkout-api:1.5"}, nil, "rollout", []string{"reg/checkout-api:1.5"}, []string{"reg/checkout-api:1.4"}},
		{"aynı imaj → config", []string{"reg/checkout-api:1.4"}, nil, "config", []string{"reg/checkout-api:1.4"}, []string{"reg/checkout-api:1.4"}},
		{"imaj sonradan okunur, aynı → config'e iner", nil, []string{"reg/checkout-api:1.4"}, "config", []string{"reg/checkout-api:1.4"}, []string{"reg/checkout-api:1.4"}},
		{"imaj sonradan okunur, farklı → rollout kalır", nil, []string{"reg/checkout-api:1.5"}, "rollout", []string{"reg/checkout-api:1.5"}, []string{"reg/checkout-api:1.4"}},
		{"imaj hiç okunmaz → rollout (varsayılan), boş dizi", nil, nil, "rollout", nil, []string{"reg/checkout-api:1.4"}},
		{"sıra/tekrar normalize", []string{"reg/sidecar:2", "reg/checkout-api:1.4", "reg/sidecar:2"}, nil, "rollout",
			[]string{"reg/checkout-api:1.4", "reg/sidecar:2"}, []string{"reg/checkout-api:1.4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newDetSim(t)
			s.tick(0, detDep("api", 0, 1).rs("api-a", 3).img("api-a", "reg/checkout-api:1.4").V())
			b := detDep("api", 30, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2)
			if tc.atStart != nil {
				b.img("api-b", tc.atStart...)
			}
			s.tick(30, b.V())
			b = detDep("api", 60, 2).reps(3, 4, 2, 3).rs("api-a", 2).rs("api-b", 2)
			if tc.later != nil {
				b.img("api-b", tc.later...)
			}
			s.tick(60, b.V())
			e := s.ev(V2KindDeployment, "api", 2)
			if e.ChangeType != tc.want || !reflect.DeepEqual(e.Images, tc.wantImages) || !reflect.DeepEqual(e.PrevImages, tc.wantPrevImg) {
				t.Fatalf("got %s %v prev %v", e.ChangeType, e.Images, e.PrevImages)
			}
		})
	}
}

// ── SUCCEEDED eşdeğerliği (§4.5, kubectl rollout status) ─────────────────

func TestV2SucceededParity(t *testing.T) {
	pres := detAllPresent()
	cases := []struct {
		name string
		o    V2Observation
		want bool
	}{
		{"Deployment tamam", detDep("api", 0, 2).V(), true},
		{"Deployment observed < generation", detDep("api", 0, 2).og(1).V(), false},
		{"Deployment updated < spec", detDep("api", 0, 2).reps(3, 2, 2, 2).V(), false},
		{"Deployment eski pod var (status > updated; brief'te eksik terim)", detDep("api", 0, 2).reps(3, 4, 3, 3).V(), false},
		{"Deployment available < updated", detDep("api", 0, 2).reps(3, 3, 3, 2).V(), false},
		{"Deployment ProgressDeadlineExceeded", detDep("api", 0, 2).pde().V(), false},
		{"Deployment 0 replika", detDep("api", 0, 2).reps(0, 0, 0, 0).V(), true},
		{"StatefulSet tamam", detSts("db", 0, 2, "db-r2", "db-r2").V(), true},
		{"StatefulSet ready < replicas", detSts("db", 0, 2, "db-r2", "db-r2").ready(2).V(), false},
		{"StatefulSet update ≠ current", detSts("db", 0, 2, "db-r1", "db-r2").V(), false},
		{"StatefulSet observed < generation", detSts("db", 0, 2, "db-r2", "db-r2").og(1).V(), false},
		{"DaemonSet tamam", detDs("agent", 0, 2, "h2").V(), true},
		{"DaemonSet updated < desired", detDs("agent", 0, 2, "h2").reps(3, 0, 2, 3).V(), false},
		{"DaemonSet available < desired", detDs("agent", 0, 2, "h2").reps(3, 0, 3, 2).V(), false},
		// v0.10.963 — P2.1 (P21-4 M5): revizyon serisi yoksa güncellenen replika yedeği karar verir.
		{"StatefulSet revizyonsuz, updated < replicas", detSts("db", 0, 2, "", "").reps(3, 3, 2, 3).V(), false},
		{"StatefulSet revizyonsuz, updated = replicas", detSts("db", 0, 2, "", "").V(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := v2Succeeded(tc.o, pres); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// ── Adanmış sözleşmeler ───────────────────────────────────────────────────

// Olayın revizyonu güncel hedef değilse (ör. durum ile olay arasında yarım
// yazım) iş yükü yerleşse de olay SUCCEEDED olmaz: geri alınan / başka
// revizyona yakınsamış rollout "başarılı" sayılmaz.
func TestDetectV2_SucceededRequiresEventRevisionIsTarget(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
	for i := range s.states {
		s.states[i].CurrentRevision = "api-a" // olay api-b'yi açtı, durum eski hedefte kaldı
	}
	s.tick(60, detSteady("api", 60, 2, "api-a", "api-b"))
	if e := s.ev(V2KindDeployment, "api", 2); e.Status == V2StatusSucceeded {
		t.Fatalf("hedef olmayan revizyonun olayı başarılı sayılmamalı: %+v", e)
	}
}

func TestDetectV2_FirstRunBaselineState(t *testing.T) {
	s := newDetSim(t)
	out := s.tick(37, detSteady("api", 37, 5, "api-b", "api-a"), detSts("db", 37, 2, "db-r1", "db-r1").V(), detDs("agent", 37, 1, "h1").V())
	if len(out.Events) != 0 || len(out.States) != 3 {
		t.Fatalf("ilk koşu: 0 olay, 3 baseline beklenir: %d/%d", len(out.Events), len(out.States))
	}
	st := s.st(V2KindDeployment, "api")
	want := V2WorkloadState{ClusterID: "cluster-a", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "api",
		IncarnationAt: detAt(0), Generation: 5, ObservedGeneration: 5, CurrentRevision: "api-b",
		KnownRevisions: []string{"api-a", "api-b"}, FirstSeenAt: detAt(37), LastSeenAt: detAt(37), Version: st.Version}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("baseline\n got %+v\nwant %+v", st, want)
	}
	if st.Version == 0 {
		t.Fatal("açık istemci version'ı şart")
	}
}

func TestDetectV2_RestartKeepsPendingStartedAt(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	s.tick(30, detDep("api", 30, 2).og(1).rs("api-a", 3).V())
	if st := s.st(V2KindDeployment, "api"); st.PendingGeneration != 2 || !st.PendingStartedAt.Equal(detAt(30)) {
		t.Fatalf("bekleyen artış dayanıklı durumda olmalı: %+v", st)
	}
	s.failover() // bellek gider; durum tabloda (CH FINAL okuması) kalır
	s.tick(60, detRolling("api", 60, 2, 2, "api-a", "api-b"))
	if e := s.ev(V2KindDeployment, "api", 2); !e.StartedAt.Equal(detAt(30)) {
		t.Fatalf("failover pending_started_at'i yeniden damgalamamalı: %v", e.StartedAt)
	}
}

func TestDetectV2_PartialReadWritesNothing(t *testing.T) {
	s := newDetSim(t)
	web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") }
	s.tick(0, detSteady("api", 0, 1, "api-a"), web(0))
	before := s.mem
	out := s.snap(30, V2Snapshot{Complete: false, Presence: s.pres, Workloads: []V2Observation{web(30)}})
	if len(out.Events)+len(out.States) != 0 || out.Diag.Skipped != "partial" || out.Diag.Counts["skipped_partial"] != 1 {
		t.Fatalf("kısmi okuma hiçbir şey yazmamalı: %+v", out)
	}
	if !reflect.DeepEqual(out.Memory, before) {
		t.Fatalf("kısmi okuma yokluk saymamalı: %+v", out.Memory)
	}
	s.tick(60, web(60))
	s.tick(90, web(90))
	s.tick(120, detSteady("api", 120, 1, "api-a"), web(120)) // 2 tam okuma yokluk < K=3
	if s.counts["new_incarnation"] != 0 || len(s.evs(V2KindDeployment, "api")) != 0 {
		t.Fatalf("kısmi okuma yokluk sayılmış: %v", s.counts)
	}
}

func TestDetectV2_FamilyAbsentIsNotDeletion(t *testing.T) {
	s := newDetSim(t)
	db := func(at int) V2Observation { return detSts("db", at, 1, "db-r1", "db-r1").V() }
	s.tick(0, detSteady("api", 0, 1, "api-a"), db(0))
	for at := 30; at <= 150; at += 30 {
		s.tick(at, db(at)) // bütün Deployment ailesi yok (KSM yeniden başladı / scrape düştü)
	}
	s.tick(180, detSteady("api", 180, 1, "api-a"), db(180))
	if s.counts["family_absent"] == 0 || s.counts["absent"] != 0 || s.counts["new_incarnation"] != 0 || len(s.evs(V2KindDeployment, "api")) != 0 {
		t.Fatalf("tür ailesi yokluğu silinme sayılmamalı: %v", s.counts)
	}
}

func TestDetectV2_InvalidObservationsAreSkippedNotAbsent(t *testing.T) {
	s := newDetSim(t)
	web := func(at int) V2Observation { return detSteady("web", at, 1, "web-a") } // aile mevcut kalsın
	s.tick(0, detSteady("api", 0, 1, "api-a"), web(0))
	bad := func(at int, mut func(*V2Observation)) V2Observation {
		o := detRolling("api", at, 2, 2, "api-a", "api-b")
		mut(&o)
		return o
	}
	s.tick(30, bad(30, func(o *V2Observation) { o.SampleAt = time.Time{} }), web(30))
	s.tick(60, bad(60, func(o *V2Observation) { o.SampleAt = time.Unix(-5, 0) }), web(60))
	s.tick(90, bad(90, func(o *V2Observation) { o.Generation = 0 }), web(90))
	s.tick(120, bad(120, func(o *V2Observation) { o.SampleAt = time.Unix(0, 0) }), detDep("", 120, 1).V(), web(120))
	if s.counts["invalid_observation"] != 5 || len(s.events) != 0 {
		t.Fatalf("geçersiz gözlem atlanmalı: %v %v", s.counts, s.evs(V2KindDeployment, "api"))
	}
	s.tick(150, detSteady("api", 150, 1, "api-a"), web(150))
	if s.counts["absent"] != 0 || s.counts["new_incarnation"] != 0 {
		t.Fatalf("geçersiz gözlem yokluk sayılmamalı: %v", s.counts)
	}
}

// Yazıcı sözleşmesinin ikinci katmanı: CH'den okunan bozuk bir önceki satır
// (epoch started_at — TTL'in ilk birleşmede sileceği satır) güncellenecekse
// iş yükünün BÜTÜN satırları düşer ve sayılır; yarım durum yazılmaz.
func TestDetectV2_RejectsWorkloadAtomically(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"), detSteady("web", 0, 1, "web-a"))
	s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"), detSteady("web", 30, 1, "web-a"))
	for i := range s.events {
		s.events[i].StartedAt = time.Unix(0, 0).UTC()
	}
	out := s.tick(60, detDep("api", 60, 3).reps(3, 5, 1, 3).rs("api-a", 1).rs("api-b", 2).rs("api-c", 1).V(),
		detSteady("web", 60, 2, "web-a"))
	for _, e := range out.Events {
		if e.Workload == "api" {
			t.Fatalf("api satırları reddedilmeliydi: %+v", e)
		}
	}
	for _, st := range out.States {
		if st.Workload == "api" {
			t.Fatalf("api durumu da düşmeli (atomik): %+v", st)
		}
	}
	if out.Diag.Counts["rejected"] != 1 || len(out.Diag.Entries) == 0 || !strings.Contains(fmt.Sprint(out.Diag.Entries), "started_at") {
		t.Fatalf("red sayılmalı ve nedeni görünmeli: %+v", out.Diag)
	}
	if len(out.States) != 1 {
		t.Fatalf("öteki iş yükü etkilenmemeli: %+v", out.States)
	}
}

func TestDetectV2_NoChurn(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"), detSts("db", 0, 1, "db-r1", "db-r1").V(), detDs("agent", 0, 1, "h1").V())
	for at := 30; at <= 3600; at += 30 {
		out := s.tick(at, detSteady("api", at, 1, "api-a"), detSts("db", at, 1, "db-r1", "db-r1").V(), detDs("agent", at, 1, "h1").V())
		if len(out.Events)+len(out.States) != 0 {
			t.Fatalf("değişmeyen durum yazılmamalı (tik %d): %+v", at, out)
		}
	}
	// Aynı girdiyle aynı tik iki kez: ikincisi boş (idempotent).
	s.tick(3630, detRolling("api", 3630, 2, 2, "api-a", "api-b"))
	in := V2Input{ClusterID: "cluster-a", Now: detAt(3635), PrevStates: s.states, PrevEvents: s.events, Memory: s.mem,
		Snapshot: V2Snapshot{Complete: true, Presence: s.pres, Workloads: []V2Observation{detRolling("api", 3630, 2, 2, "api-a", "api-b"),
			detSts("db", 3630, 1, "db-r1", "db-r1").V(), detDs("agent", 3630, 1, "h1").V()}}}
	if out := DetectV2(s.set, in); len(out.Events)+len(out.States) != 0 {
		t.Fatalf("aynı tik yeniden koşunca satır üretmemeli: %+v", out)
	}
}

func TestDetectV2_DailyTouch(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	if out := s.tick(23*3600, detSteady("api", 23*3600, 1, "api-a")); len(out.States) != 0 {
		t.Fatal("gün dolmadan dokunulmaz")
	}
	out := s.tick(24*3600, detSteady("api", 24*3600, 1, "api-a"))
	if len(out.States) != 1 || !out.States[0].LastSeenAt.Equal(detAt(24*3600)) {
		t.Fatalf("günde bir dokunuş (TTL çapası): %+v", out.States)
	}
	if out := s.tick(24*3600+30, detSteady("api", 24*3600+30, 1, "api-a")); len(out.States) != 0 {
		t.Fatal("dokunuştan sonra yeniden yazılmaz")
	}
}

func TestDetectV2_VersionMonotonic(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	future := uint64(detAt(0).Add(24 * time.Hour).UnixNano())
	s.states[0].Version = future // saati geride kalan pod'a failover
	out := s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
	if len(out.States) != 1 || out.States[0].Version != future+1 {
		t.Fatalf("version önceki satırı geçmeli: %+v", out.States)
	}
	if out.Events[0].Version != uint64(detAt(35).UnixNano()) || !out.Events[0].UpdatedAt.Equal(detAt(35)) {
		t.Fatalf("yeni olayın version/updated_at'i tik zamanı: %+v", out.Events[0])
	}
}

// v0.10.963 — P2.1 (P21-4 M6): olay satırının version'ı da önceki satırı
// geçmeli — saati geride kalan pod'a failover'da RMT güncellemeyi kaybetmesin.
func TestDetectV2_EventVersionMonotonic(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
	future := uint64(detAt(0).Add(24 * time.Hour).UnixNano())
	for i := range s.events {
		s.events[i].Version = future
	}
	out := s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
	if len(out.Events) != 1 || out.Events[0].Status != V2StatusSucceeded || out.Events[0].Version != future+1 {
		t.Fatalf("olay version'ı önceki satırı geçmeli: %+v", out.Events)
	}
}

// v0.10.963 — P2.1 (P21-8): durum okuması hatalı/yarımsa tik atlanır; boş
// PrevStates "ilk-ever" sanılıp incarnation'lar yeniden basılmaz.
func TestDetectV2_PrevIncompleteSkipsTick(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
	mem := V2Memory{Absent: map[V2Key]int{{"cluster-a", "pay", V2KindDeployment, "gone"}: 1}}
	out := DetectV2(s.set, V2Input{ClusterID: "cluster-a", Now: detAt(305), PrevStates: nil, PrevEvents: s.events,
		PrevIncomplete: true, Memory: mem,
		Snapshot: V2Snapshot{Complete: true, Presence: s.pres, Workloads: []V2Observation{detRolling("api", 300, 2, 2, "api-a", "api-b")}}})
	if len(out.Events)+len(out.States) != 0 || out.Diag.Skipped != "prev_partial" || out.Diag.Counts["skipped_prev_partial"] != 1 ||
		!reflect.DeepEqual(out.Memory, mem) {
		t.Fatalf("yarım durum okumasında satır yazılmamalı, bellek korunmalı: %+v", out)
	}
	s.tick(330, detRolling("api", 330, 2, 2, "api-a", "api-b"))
	if st := s.st(V2KindDeployment, "api"); !st.IncarnationAt.Equal(detAt(0)) {
		t.Fatalf("incarnation yeniden basılmamalı: %v", st.IncarnationAt)
	}
	if got := s.evs(V2KindDeployment, "api"); !reflect.DeepEqual(got, []string{"g2 rollout progressing api-a>api-b"}) {
		t.Fatalf("açık rollout superseded olmamalı: %v", got)
	}
}

func TestDetectV2_OlderIncarnationOpenEventIsClosed(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	st := s.states[0]
	old := V2Event{ClusterID: "cluster-a", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "api",
		IncarnationAt: detAt(-3600), Generation: 9, StartedAt: detAt(-3000), Status: V2StatusProgressing,
		ChangeType: V2ChangeRollout, UpdatedAt: detAt(-3000), Version: 1}
	s.events = append(s.events, old)
	out := s.tick(30, detSteady("api", 30, 1, "api-a"))
	if len(out.Events) != 1 || out.Events[0].Status != V2StatusSuperseded || !out.Events[0].IncarnationAt.Equal(old.IncarnationAt) {
		t.Fatalf("eski incarnation'ın açık olayı kapanmalı: %+v", out.Events)
	}
	if !st.IncarnationAt.Equal(s.st(V2KindDeployment, "api").IncarnationAt) {
		t.Fatal("güncel incarnation değişmemeli")
	}
}

func TestDetectV2_KnownRevisionsBoundKeepsPresent(t *testing.T) {
	s := newDetSim(t)
	s.set.KnownRevisionsMax = 2
	s.tick(0, detSteady("api", 0, 1, "api-c", "api-a", "api-b"))
	if k := s.st(V2KindDeployment, "api").KnownRevisions; len(k) != 3 {
		t.Fatalf("mevcut RS'ler asla atılmaz (yoksa her tik 'yeni RS' olur): %v", k)
	}
	if s.counts["known_over_bound"] != 1 {
		t.Fatalf("sınır aşımı teşhiste görünmeli: %v", s.counts)
	}
	s.tick(30, detSteady("api", 30, 1, "api-c", "api-a", "api-b"))
	if len(s.evs(V2KindDeployment, "api")) != 0 {
		t.Fatal("sınır aşımı sahte olay üretmemeli")
	}
}

func TestDetectV2_Deterministic(t *testing.T) {
	s := newDetSim(t)
	all := func(at int, gen uint64) []V2Observation {
		return []V2Observation{
			detRolling("api", at, gen, gen, "api-a", "api-b"),
			detSteady("web", at, 1, "web-a"),
			detSts("db", at, gen, "db-r1", "db-r2").reps(3, 3, 1, 3).V(),
			detDs("agent", at, gen, "h1", "h2").reps(3, 0, 1, 3).V(),
		}
	}
	s.tick(0, detSteady("api", 0, 1, "api-a"), detSteady("web", 0, 1, "web-a"), detSts("db", 0, 1, "db-r1", "db-r1").V(), detDs("agent", 0, 1, "h1").V())
	obs := append(all(30, 2), detSteady("new", 30, 1, "new-a"))
	in := V2Input{ClusterID: "cluster-a", Now: detAt(35), PrevStates: s.states, PrevEvents: s.events, Memory: s.mem,
		Snapshot: V2Snapshot{Complete: true, Presence: s.pres, Workloads: obs}}
	before := fmt.Sprintf("%#v", obs)
	base := DetectV2(s.set, in)
	if len(base.Events) < 4 {
		t.Fatalf("senaryo olay üretmeli: %d", len(base.Events))
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 25; i++ {
		sh := in
		sh.Snapshot.Workloads = append([]V2Observation(nil), obs...)
		r.Shuffle(len(sh.Snapshot.Workloads), func(a, b int) {
			sh.Snapshot.Workloads[a], sh.Snapshot.Workloads[b] = sh.Snapshot.Workloads[b], sh.Snapshot.Workloads[a]
		})
		for j := range sh.Snapshot.Workloads {
			o := &sh.Snapshot.Workloads[j]
			o.ReplicaSets = append([]V2ReplicaSet(nil), o.ReplicaSets...)
			r.Shuffle(len(o.ReplicaSets), func(a, b int) { o.ReplicaSets[a], o.ReplicaSets[b] = o.ReplicaSets[b], o.ReplicaSets[a] })
			o.RevisionHashes = append([]string(nil), o.RevisionHashes...)
			r.Shuffle(len(o.RevisionHashes), func(a, b int) { o.RevisionHashes[a], o.RevisionHashes[b] = o.RevisionHashes[b], o.RevisionHashes[a] })
		}
		sh.PrevStates = append([]V2WorkloadState(nil), s.states...)
		r.Shuffle(len(sh.PrevStates), func(a, b int) { sh.PrevStates[a], sh.PrevStates[b] = sh.PrevStates[b], sh.PrevStates[a] })
		sh.PrevEvents = append([]V2Event(nil), s.events...)
		r.Shuffle(len(sh.PrevEvents), func(a, b int) { sh.PrevEvents[a], sh.PrevEvents[b] = sh.PrevEvents[b], sh.PrevEvents[a] })
		if got := DetectV2(s.set, sh); !reflect.DeepEqual(got, base) {
			t.Fatalf("girdi sırası çıktıyı değiştirdi\n got %+v\nwant %+v", got, base)
		}
	}
	// Girdi DEĞİŞTİRİLMEZ.
	if fmt.Sprintf("%#v", obs) != before {
		t.Fatal("DetectV2 girdiyi değiştirdi")
	}
}

func TestDetectV2_OtherClusterRowsIgnored(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSteady("api", 0, 1, "api-a"))
	foreign := s.states[0]
	foreign.ClusterID = "cluster-b"
	foreign.Generation = 99
	s.states = append(s.states, foreign)
	out := s.tick(30, detSteady("api", 30, 1, "api-a"))
	if len(out.Events)+len(out.States) != 0 || s.counts["new_incarnation"] != 0 || s.counts["absent"] != 0 || out.Memory.Absent != nil {
		t.Fatalf("başka kümenin satırı karışmamalı (yokluk da sayılmaz): %+v", out)
	}
}

func TestV2Carry(t *testing.T) {
	e1 := detValidEvent()
	e2 := e1
	e2.Status, e2.Version = V2StatusSucceeded, e1.Version+1
	s1 := detValidState()
	s2 := s1
	s2.Generation, s2.Version = 3, s1.Version+1
	stale := s1
	stale.Version = s1.Version - 1
	states, events := V2Carry([]V2WorkloadState{s1}, []V2Event{e1}, V2Output{Events: []V2Event{e2}, States: []V2WorkloadState{s2}})
	if len(states) != 1 || states[0].Generation != 3 || len(events) != 1 || events[0].Status != V2StatusSucceeded {
		t.Fatalf("çıktı öncekini ezmeli: %+v %+v", states, events)
	}
	states, _ = V2Carry(states, events, V2Output{States: []V2WorkloadState{stale}})
	if states[0].Generation != 3 {
		t.Fatal("düşük version RMT'deki gibi kaybetmeli")
	}
}

// Now sıfır/epoch-öncesiyse updated_at ve version damgalanamaz (sıfır
// zamanın UnixNano'su taşar → çöp version): bütün tik atlanır (fail-closed).
func TestDetectV2_InvalidNowSkipsTick(t *testing.T) {
	mem := V2Memory{Absent: map[V2Key]int{{"cluster-a", "pay", V2KindDeployment, "gone"}: 2}}
	for _, now := range []time.Time{{}, time.Unix(0, 0), time.Unix(-60, 0)} {
		out := DetectV2(DefaultSettings().ResolvedV2(), V2Input{ClusterID: "cluster-a", Now: now, Memory: mem,
			Snapshot: V2Snapshot{Complete: true, Presence: detAllPresent(), Workloads: []V2Observation{detSteady("api", 0, 1, "api-a")}}})
		if len(out.Events)+len(out.States) != 0 || out.Diag.Skipped != "invalid_now" || !reflect.DeepEqual(out.Memory, mem) {
			t.Fatalf("Now=%v ile satır yayılmamalı, bellek korunmalı: %+v", now, out)
		}
	}
}

// v0.10.982 — P2.2 incelemesi (P2.1 çekirdek hatası): yeni revizyon nesil
// artışından ÖNCE görünürse (tikin sorguları farklı scrape okur ya da KSM
// informer kayması) evidence_without_bump yolu onu known_revisions'a
// katıyordu; artış geldiği tik "bilinen revizyon" → sahte ROLLBACK ve
// önceki başarılı rollout rolled_back. Bilinmeyen kalmalı → rollout.
func TestDetectV2_EvidenceBeforeBumpIsNotRollback(t *testing.T) {
	t.Run("Deployment", func(t *testing.T) {
		s := newDetSim(t)
		s.tick(0, detSteady("api", 0, 1, "api-a"))
		s.tick(30, detRolling("api", 30, 2, 2, "api-a", "api-b"))
		s.tick(60, detSteady("api", 60, 2, "api-b", "api-a"))
		// Kayma: nesil ölçüleri eski scrape'ten (g2), RS listesi yeniden (api-c var).
		s.tick(90, detDep("api", 90, 2).rs("api-a", 0).rs("api-b", 3).rs("api-c", 1).V())
		if k := s.st(V2KindDeployment, "api").KnownRevisions; v2Has(k, "api-c") {
			t.Fatalf("artışsız görülen revizyon known'a katılmamalı: %v", k)
		}
		s.tick(120, detDep("api", 120, 3).og(3).reps(3, 4, 2, 3).rs("api-a", 0).rs("api-b", 2).rs("api-c", 2).V())
		want := []string{"g2 rollout succeeded api-a>api-b", "g3 rollout progressing api-b>api-c"}
		if got := s.evs(V2KindDeployment, "api"); !reflect.DeepEqual(got, want) {
			t.Fatalf("olaylar %v, istenen %v", got, want)
		}
		if s.counts["evidence_without_bump"] != 1 {
			t.Fatalf("teşhis: %v", s.counts)
		}
	})
	t.Run("StatefulSet", func(t *testing.T) {
		s := newDetSim(t)
		s.tick(0, detSts("db", 0, 1, "db-1", "db-1").V())
		s.tick(30, detSts("db", 30, 2, "db-1", "db-2").ready(2).V())
		s.tick(60, detSts("db", 60, 2, "db-2", "db-2").V())
		s.tick(90, detSts("db", 90, 2, "db-2", "db-3").V()) // kayma: update_revision nesilden önce
		s.tick(120, detSts("db", 120, 3, "db-2", "db-3").ready(2).V())
		want := []string{"g2 rollout succeeded db-1>db-2", "g3 rollout progressing db-2>db-3"}
		if got := s.evs(V2KindStatefulSet, "db"); !reflect.DeepEqual(got, want) {
			t.Fatalf("olaylar %v, istenen %v", got, want)
		}
	})
}
