package ml

// publish_gate_enforce_test.go — gürültü kapılarının uygulama yolu
// (v0.10.2343 A2; v0.10.2473: kip operatör ayarı off | shadow | on). Tablo
// testleri: az hacimli / küçük mutlak farklı / görünür sapması olmayan olay
// on kipinde tutulur (liste durumu + Problem adaylığı), sağlıklı hacimli
// gerçek sapma geçer, K10 hiçbir kipte uygulanmaz, shadow yalnız kaydeder,
// off hiçbir şey değerlendirmez, etki kuralı aileleri etkilenmez. Servis
// adları SENTETİK; zamanlar sabit UTC.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// applyRecordContractShadow — shadow kipinde yazım (v0.10.2472 davranışı);
// kayıt sözleşmesi testlerinin ortak çağrısı.
func applyRecordContractShadow(ev chstore.MLEvent) chstore.MLEvent {
	return applyRecordContract(ev, chstore.MLGatesShadow)
}

// decideAt — tek grubun terfi kararı, verilen anda (süre şartı için).
func decideAt(t *testing.T, evs []chstore.MLEvent, in GroupGateInput, now time.Time) GroupDecision {
	t.Helper()
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 {
		t.Fatalf("tek grup bekleniyordu: %d", len(gs))
	}
	return DecideGroupPromotion(gs[0], evs, in, now)
}

var gateModes = []string{chstore.MLGatesOff, chstore.MLGatesShadow, chstore.MLGatesOn}

// gateLowVolume — sağlıklı sapma, yalnız hacim az (40 çağrı < 100).
func gateLowVolume() EventRecord {
	r := gateLatRec()
	r.Volume = f64(40)
	r.Title = "op-x p95 135 ms (tespit anında 100 ms; beklenen 43 ms; 40 çağrı)"
	return r
}

// gateTinyDiff — 15 dakika bant dışında ama fark 17 ms (< 25 ms): bant dar.
func gateTinyDiff() EventRecord {
	r := gateLatRec()
	r.Baseline.Hi = 50
	r.Snapshot = series1m(recAt(6, 28), 75, 0, 50, func(i int) float64 {
		if i >= 30 && i < 45 {
			return 60
		}
		return 43
	})
	r.Snapshot.Aggregation = "p95"
	r.PeakValue, r.ValueAtDetection = 60, 60
	r.Title = "op-x p95 60 ms (tespit anında 60 ms; beklenen 43 ms; 412 çağrı)"
	return r
}

// gateWeakEvidence — sağlıklı hacimli gerçek sapma, tek bağımsız kanıt (K10 satırı kalır).
func gateWeakEvidence() EventRecord {
	r := gateLatRec()
	r.Evidence = r.Evidence[:2]
	return r
}

func gateRecEv(t *testing.T, id, det string, rec EventRecord) chstore.MLEvent {
	t.Helper()
	if vs := ValidateRecord(rec); len(vs) != 0 {
		t.Fatalf("%s fikstürü doğrulayıcıdan kaldı: %+v", id, vs)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	e := gateEv(id, det, "checkout", 0, 30, 100, 500) // güçlü + uzun: tek sinyal kolunu açar
	e.Class, e.PublishState, e.Record = rec.Class, PublishPublished, string(raw)
	return e
}

func gateNames(gs []RecordGate) []string {
	out := []string{}
	for _, g := range gs {
		out = append(out, g.Name)
	}
	return out
}

func recGates(t *testing.T, ev chstore.MLEvent) []RecordGate {
	t.Helper()
	var rec EventRecord
	if err := json.Unmarshal([]byte(ev.Record), &rec); err != nil {
		t.Fatal(err)
	}
	return rec.Gates
}

// Tablo: kip × olay. on → yalnız üç gürültü kapısı tutar; shadow → kayıt;
// off → değerlendirme yok. Tutulan olay ana listenin durumunda (held) ve
// grup kapısında aday değil.
func TestNoiseGatesByMode(t *testing.T) {
	now := gBase.Add(40 * time.Minute)
	for _, c := range []struct {
		name string
		rec  EventRecord
		held []string // on kipinde kalan uygulanan kapılar (boş = geçer)
	}{
		{"az hacim (40 çağrı)", gateLowVolume(), []string{GateMinVolume}},
		{"küçük mutlak fark (17 ms)", gateTinyDiff(), []string{GateMinAbsDiff}},
		{"tek kovalık sıçrama, az hacim", gateSingleBucket(), []string{GateVisibleDeviation, GateMinVolume}},
		{"sağlıklı hacim, gerçek sapma", gateLatRec(), nil},
		{"sağlıklı hacim, tek bağımsız kanıt (K10 uygulanmaz)", gateWeakEvidence(), nil},
	} {
		for _, mode := range gateModes {
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				in := gateRecEv(t, "g1", detOpLatency, c.rec)
				out := applyRecordContract(in, mode)
				if out.Class != in.Class {
					t.Fatalf("sınıf hiçbir kipte değişmez: %q", out.Class)
				}
				gates := recGates(t, out)
				wantHeld := mode == chstore.MLGatesOn && len(c.held) > 0
				if (out.PublishState == PublishHeld) != wantHeld || (!wantHeld && out.PublishState != PublishPublished) {
					t.Fatalf("publish_state=%q, tutulmalı=%v", out.PublishState, wantHeld)
				}
				switch mode {
				case chstore.MLGatesOff:
					if len(gates) != 0 || ShadowHeld(out) {
						t.Fatalf("off: kapı değerlendirilmemeli: %+v", gates)
					}
				default:
					if len(gates) != 6 {
						t.Fatalf("%s: 6 kapı satırı kayda yazılmalı: %d", mode, len(gates))
					}
					if got := gateNames(HeldGates(out)); !slices.Equal(got, append([]string{}, c.held...)) {
						t.Fatalf("kalan kapılar %v, istenen %v", got, c.held)
					}
					if ShadowHeld(out) != (len(c.held) > 0) {
						t.Fatalf("Elenenler üyeliği: %v", ShadowHeld(out))
					}
				}
				// Problem adaylığı: on → son yazımın kararı; shadow / off → herkes aday
				var pub map[string]PubVerdict
				if mode == chstore.MLGatesOn {
					m := pubVerdicts{}
					m.observe(out, now)
					pub = m
				}
				if problemCandidate(out, pub) == wantHeld {
					t.Fatalf("aday=%v, tutulan=%v", problemCandidate(out, pub), wantHeld)
				}
				// tek sinyal kolu: güçlü + uzun olay, tutulmadıysa açar
				if d := decideAt(t, []chstore.MLEvent{out}, GroupGateInput{Published: pub}, now); d.WouldOpen == wantHeld {
					t.Fatalf("tek sinyal kolu WouldOpen=%v (%s)", d.WouldOpen, d.Reason)
				}
				// ≥ 2 sinyal kolu: tutulan üye ikinci sinyal sayılmaz
				partner := gateEv("g2", detErrorRate, "checkout", 5, 30, 10, 12) // zayıf: tek başına açmaz
				if pub != nil {
					pub[partner.ID] = PubVerdict{}
				}
				d := decideAt(t, []chstore.MLEvent{out, partner}, GroupGateInput{Published: pub}, now)
				if d.WouldOpen == wantHeld || (wantHeld && len(d.Signals) != 1) {
					t.Fatalf("grup WouldOpen=%v sinyaller=%v (%s)", d.WouldOpen, d.Signals, d.Reason)
				}
			})
		}
	}
}

// K10 (≥ 2 bağımsız kanıt) HİÇBİR kipte uygulanmaz: operatör kararı 2026-11-19,
// tek etki olayı Problem açar. Satır kayda yazılmaya devam eder.
func TestK10NeverEnforced(t *testing.T) {
	now := gBase.Add(40 * time.Minute)
	if dropGates[GateEvidence] || dropGates[GateImpact] || dropGates[GateSeriesConsistency] || len(dropGates) != 3 {
		t.Fatalf("uygulanan kapılar yalnız hacim / mutlak fark / görünür sapma: %v", dropGates)
	}
	for _, mode := range gateModes {
		in := gateRecEv(t, "k1", detOpLatency, gateWeakEvidence())
		out := applyRecordContract(in, mode)
		if out.PublishState != PublishPublished || out.Class != ClassServiceAnomaly {
			t.Fatalf("%s: tek kanıtlı olay yayımlanır, sınıfı inmez: %q %q", mode, out.PublishState, out.Class)
		}
		if mode != chstore.MLGatesOff {
			g := gateByName(recGates(t, out), GateEvidence)
			if g.Passed || g.Value != 1 || g.Threshold != gateMinIndependent {
				t.Fatalf("%s: K10 satırı kayda yazılmaya devam etmeli: %+v", mode, g)
			}
		}
	}
	// enforceVerdict hiçbir sonuçta sınıfa dokunmaz
	for _, o := range []gateOutcome{{Evaluated: true, WouldClass: ClassInfo}, {Evaluated: true, Drop: true, WouldClass: ClassInfo}, {Evaluated: true, Failed: []string{GateEvidence}}} {
		if e := enforceVerdict(chstore.MLEvent{Class: ClassServiceAnomaly, PublishState: PublishPublished}, o); e.Class != ClassServiceAnomaly {
			t.Fatalf("sınıf değişti: %+v → %q", o, e.Class)
		}
	}
	// on kipinde tek sinyal kolu kanıt sayısına bakmaz: tek kanıtlı ve kaydı
	// olmayan (0 kanıt) güçlü olay Problem açar
	weak := applyRecordContract(gateRecEv(t, "k2", detOpLatency, gateWeakEvidence()), chstore.MLGatesOn)
	bare := gateEv("k3", detOpLatency, "payments", 0, 30, 100, 500)
	pub := pubVerdicts{}
	pub.observe(weak, now)
	pub.observe(applyRecordContract(bare, chstore.MLGatesOn), now)
	for _, e := range []chstore.MLEvent{weak, bare} {
		if d := decideAt(t, []chstore.MLEvent{e}, GroupGateInput{Published: pub}, now); !d.WouldOpen || !d.SingleWouldOpen {
			t.Fatalf("%s: tek olay Problem açmalı (K10 yok): %s", e.ID, d.Reason)
		}
	}
}

// Etki kuralı aileleri (servis hata oranı, servis yanıt süresi, etki kuralıyla
// açılmış consumer lag) gürültü kapılarından muaf: satırlar kayda yazılır ama
// olay hiçbir kipte tutulmaz ve karar beklemeden (boş harita, ilk tik) adaydır.
// Etki damgası olmayan lag olayı muaf DEĞİL.
func TestImpactRuleFamiliesNotGated(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	raw, _ := json.Marshal(gateSingleBucket()) // görünür sapma + hacim kapısında kalır
	lag := func(evidence string) chstore.MLEvent {
		return lagGateEv("x", now, 4*time.Minute, 600, 165000, evidence)
	}
	for _, c := range []struct {
		name   string
		ev     chstore.MLEvent
		exempt bool
	}{
		{"servis hata oranı", svcEv("x", detSvcErrorRate, "orders", 0, 0.01, 0.05), true},
		{"servis yanıt süresi", svcEv("x", detSvcResponseTime, "orders", 0, 200, 900), true},
		{"uç nokta etkisi", svcEv("x", detEndpointImpact, "checkout", 0, 0.002, 0.14), true}, // v0.10.2474
		{"consumer lag, etki kuralı", lag(lagImpactEvidence(true, 1200, 4)), true},
		{"consumer lag, etki damgası yok", lag(`{"source":"vm","dwell":2}`), false},
		{"endpoint gecikmesi", gateEv("x", detOpLatency, "orders", 0, 30, 100, 500), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.ev.Class, c.ev.PublishState, c.ev.Record = ClassServiceAnomaly, PublishPublished, string(raw)
			if gateHoldExempt(c.ev) != c.exempt {
				t.Fatalf("muafiyet=%v", gateHoldExempt(c.ev))
			}
			for _, mode := range gateModes {
				out := applyRecordContract(c.ev, mode)
				wantHeld := mode == chstore.MLGatesOn && !c.exempt
				if (out.PublishState == PublishHeld) != wantHeld || out.Class != ClassServiceAnomaly {
					t.Fatalf("%s: publish_state=%q class=%q", mode, out.PublishState, out.Class)
				}
				if mode == chstore.MLGatesOff {
					continue
				}
				if g := gateByName(recGates(t, out), GateMinVolume); g.Passed || g.Value != 40 {
					t.Fatalf("%s: kapı satırı kayda yazılmaya devam etmeli: %+v", mode, g)
				}
				if ShadowHeld(out) == c.exempt || (len(HeldGates(out)) == 0) != c.exempt {
					t.Fatalf("%s: Elenenler üyeliği muafiyetle çelişiyor", mode)
				}
			}
			// boş karar haritası (ilk tik / kip yeni açıldı): muaf olay aday, diğeri bekler
			if problemCandidate(c.ev, map[string]PubVerdict{}) != c.exempt {
				t.Fatalf("ilk tik adaylığı=%v", !c.exempt)
			}
		})
	}
	// servis düzeyi kapının girdisi: etki kuralı olayları süzülmez
	evs := []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders", 0, 0.01, 0.05), svcEv("e2", detSvcResponseTime, "payments", 0, 200, 900),
		svcEv("e3", detSvcBreadth, "checkout", 0, 1, 4), svcEv("e4", detSvcBreadth, "search", 0, 1, 4)}
	if got := publishedCandidates(evs, nil); len(got) != 4 {
		t.Fatal("nil = aynen")
	}
	got := publishedCandidates(evs, map[string]PubVerdict{"e3": {State: PublishHeld}, "e4": {State: PublishPublished}})
	if len(got) != 3 || got[0].ID != "e1" || got[1].ID != "e2" || got[2].ID != "e4" {
		t.Fatalf("%v", got)
	}
}

func a2ByID(evs []chstore.MLEvent) map[string]chstore.MLEvent {
	m := map[string]chstore.MLEvent{}
	for _, e := range evs {
		m[e.ID] = e
	}
	return m
}

// Replay fikstürleri on kipinde: yalnız tek kovalık sıçrama tutulur; tek
// kanıtlı olay (K10), geçersiz kayıt ve kayıtsız olay shadow'daki gibi kalır.
func TestPublishGatesOnWritesVerdict(t *testing.T) {
	evs := a2Fixture(t)
	in, got := a2ByID(evs), map[string]chstore.MLEvent{}
	for _, e := range evs {
		got[e.ID] = applyRecordContract(e, chstore.MLGatesOn)
	}
	for _, c := range []struct{ id, state string }{
		{"a1000000000000001", PublishPublished}, // tüm kapılar geçti
		{"a1000000000000002", PublishHeld},      // görünür sapma kaldı → held
		{"a1000000000000003", PublishPublished}, // tek bağımsız kanıt: K10 uygulanmaz
		{"a1000000000000004", PublishPublished}, // hacim kaydı
		{"a1000000000000005", PublishInvalid},   // doğrulayıcı
		{"a1000000000000006", ""},               // kayıt yok: dokunulmaz
	} {
		if e := got[c.id]; e.PublishState != c.state || e.Class != in[c.id].Class {
			t.Errorf("%s: state=%q class=%q, istenen %q / %q", c.id, e.PublishState, e.Class, c.state, in[c.id].Class)
		}
	}
	// taşınan held, karar değişince geri alınır
	pub := in["a1000000000000001"]
	pub.PublishState = PublishHeld
	if e := applyRecordContract(pub, chstore.MLGatesOn); e.PublishState != PublishPublished {
		t.Fatalf("geri alınmadı: %q", e.PublishState)
	}
	// tutulan olay Elenenler'de (ShadowHeld) ve kartta okunur
	if !ShadowHeld(got["a1000000000000002"]) || ShadowHeld(got["a1000000000000001"]) || ShadowHeld(got["a1000000000000003"]) {
		t.Fatal("ShadowHeld")
	}
	h := got["a1000000000000002"]
	if v := CardRecordView(&h, CardInput{}); v == nil || v.PublishState != PublishHeld {
		t.Fatalf("tutulan olayın kartı okunmalı: %+v", v)
	}
}

// on kipinde açılan Problem'ler = shadow'da açılanlar − tutulan olayınki.
func TestPublishGatesOnPromotion(t *testing.T) {
	evs := a2Fixture(t)
	now := gBase.Add(40 * time.Minute)
	pub := pubVerdicts{}
	for _, e := range evs {
		pub.observe(applyRecordContract(e, chstore.MLGatesOn), now)
	}
	opened := func(in GroupGateInput) []string {
		var out struct {
			Open []string `json:"open"`
		}
		if err := json.Unmarshal(a2Outputs(t, evs, in), &out); err != nil {
			t.Fatal(err)
		}
		return out.Open
	}
	shadow, on := opened(GroupGateInput{}), opened(GroupGateInput{Published: pub})
	var want []string
	for _, p := range shadow {
		if !strings.Contains(p, "cart-api") { // a1000000000000002: tek kovalık sıçrama → held
			want = append(want, p)
		}
	}
	if len(want) != len(shadow)-1 || !slices.Equal(on, want) {
		t.Fatalf("on kipinde yalnız tutulan olayın Problem'i düşmeli:\nshadow=%v\non=%v", shadow, on)
	}
	for _, must := range []string{"anomaly-auto:group:a1000000000000001:checkout-api", "anomaly-auto:group:b1:checkout", "anomaly-auto:svc:orders", "payments-api"} {
		if !strings.Contains(strings.Join(on, "\n"), must) {
			t.Errorf("açılmalı: %s\n%v", must, on)
		}
	}
}

// Karar yoksa (yeni olay, ilk tik) etki kuralı dışındaki olay aday değil;
// kararı gelen tik adaydır.
func TestCandidateWaitsForVerdict(t *testing.T) {
	ev := gateEv("s1", detOpLatency, "orders", 0, 30, 100, 500)
	for _, c := range []struct {
		name string
		pub  map[string]PubVerdict
		want bool
	}{
		{"kip on değil (nil) → aday", nil, true},
		{"karar yok (ilk tik) → aday değil", map[string]PubVerdict{}, false},
		{"yayımlandı → aday", map[string]PubVerdict{"s1": {State: PublishPublished}}, true},
		{"kaydı yok (durum boş) → aday", map[string]PubVerdict{"s1": {}}, true},
		{"geçersiz kayıt → shadow'daki gibi aday", map[string]PubVerdict{"s1": {State: PublishInvalid}}, true},
		{"held → aday değil", map[string]PubVerdict{"s1": {State: PublishHeld}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if d := decideAt(t, []chstore.MLEvent{ev}, GroupGateInput{Published: c.pub}, gBase.Add(40*time.Minute)); d.WouldOpen != c.want {
				t.Fatalf("WouldOpen=%v istenen %v (%s)", d.WouldOpen, c.want, d.Reason)
			}
		})
	}
}

// Kip → karar haritası: on'da dolu (ilk tik "ısınma": o tik kapanış yok),
// shadow / off'ta nil (terfi kapısı süzmez) ve harita bırakılır.
func TestPublishVerdictsByMode(t *testing.T) {
	now := gBase
	r := &Recorder{}
	for _, mode := range []string{chstore.MLGatesOff, chstore.MLGatesShadow} {
		if pub, warm := r.publishVerdicts(now, mode); pub != nil || warm || r.pub != nil {
			t.Fatalf("%s: harita olmamalı", mode)
		}
		r.observePublish(chstore.MLEvent{ID: "a", PublishState: PublishHeld}, now) // yazılmaz
	}
	pub, warm := r.publishVerdicts(now, chstore.MLGatesOn)
	if pub == nil || !warm || len(pub) != 0 {
		t.Fatalf("on ilk tik: boş harita + ısınma: %v %v", pub, warm)
	}
	r.observePublish(chstore.MLEvent{ID: "a", PublishState: PublishHeld}, now)
	pub, warm = r.publishVerdicts(now.Add(5*time.Minute), chstore.MLGatesOn)
	if warm || pub["a"].State != PublishHeld {
		t.Fatalf("on ikinci tik: karar okunmalı, ısınma bitmeli: %v %v", pub, warm)
	}
	if pub, _ := r.publishVerdicts(now, chstore.MLGatesShadow); pub != nil || r.pub != nil {
		t.Fatal("shadow'a dönünce harita bırakılmalı")
	}
	if !strings.Contains(PublishGatesCriterion, "K10") || !strings.Contains(PublishGatesCriterion, "hiçbir kipte uygulanmaz") {
		t.Fatal("ölçüt metni K10'un uygulanmadığını söylemeli")
	}
}

func TestPubVerdictsPrune(t *testing.T) {
	m := pubVerdicts{}
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	m.observe(chstore.MLEvent{ID: "old"}, t0)
	m.observe(chstore.MLEvent{ID: "new"}, t0.Add(20*time.Hour))
	m.prune(t0.Add(25 * time.Hour))
	if _, ok := m["old"]; ok || len(m) != 1 {
		t.Fatalf("%v", m)
	}
}

// Gölge raporu: tutulacak olaylardan Problem'le çakışan / dönüşen + örnekler.
func TestGateShadowK16Fields(t *testing.T) {
	es := map[string]gateEntry{
		"d1": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 10, service: "orders"},
		"d2": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 30, service: "orders", problem: true},
		"d3": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 20, service: "payments", problem: true, escalated: true},
		"d4": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 40, service: "checkout"},
		"p1": {detector: "op_latency", o: gateOutcome{Evaluated: true}, at: 50, service: "orders", problem: true},
	}
	r := foldGateShadow(es)["op_latency"]
	if r.WouldDrop != 4 || r.WouldPublish != 1 || r.WouldDropProblem != 2 || r.WouldDropEscalated != 1 {
		t.Fatalf("%+v", r)
	}
	if strings.Join(r.DropExamples, ",") != "d3,d2,d4" {
		t.Fatalf("örnek sırası: %v", r.DropExamples)
	}
	if line := gateShadowLine(map[string]chstore.MLGateShadow{"op_latency": r}, chstore.MLGatesShadow); !strings.Contains(line, "would_drop_problem=2 would_drop_escalated=1 mode=shadow") {
		t.Fatal(line)
	}
}

func TestMarkGateLinksSticky(t *testing.T) {
	var s stats
	s.observeGate("d1", "op_latency", "orders", gateOutcome{Evaluated: true, Drop: true})
	s.observeGate("p1", "op_latency", "orders", gateOutcome{Evaluated: true})
	if !s.gateShadowHasDrop() {
		t.Fatal("drop var")
	}
	s.markGateLinks(map[string]bool{"d1": true, "p1": true}, map[string]bool{"orders": true})
	s.observeGate("d1", "op_latency", "orders", gateOutcome{Evaluated: true, Drop: true}) // sonraki tik
	r := s.gateShadowReport(time.Now())["op_latency"]
	if r.WouldDropProblem != 1 || r.WouldDropEscalated != 1 {
		t.Fatalf("işaretler kalıcı olmalı: %+v", r)
	}
	g := []decidedGroup{{G: MLGroup{Members: []string{"a", "b"}}, D: GroupDecision{WouldOpen: true}}, {G: MLGroup{Members: []string{"c"}}, D: GroupDecision{WouldOpen: true, Finished: true}}}
	ids := escalatedIDs(g, map[string]SvcShadow{"e": {WouldOpen: true}, "f": {}})
	if !ids["a"] || !ids["b"] || ids["c"] || !ids["e"] || ids["f"] {
		t.Fatalf("%v", ids)
	}
}
