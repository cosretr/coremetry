package ml

// precision_shadow_pin_test.go — v0.10.2486 (precision programı B.2): varsayılan
// ayarla (mode shadow) otomatik gölge kuralı HİÇBİR çalışma zamanı kararını
// değiştirmez — olay kanıtı, grup kararı, tek sinyal kolu, servis düzeyi
// karar (Problem + bildirim bunlardan doğar) v0.10.2485 ile bayt bayt aynı.
// Durum blobunda gölge kaydı (gerçek, sanal, pin) olsa bile. Bekçinin kör
// olmadığını `on` kipindeki fark kanıtlar. Servis adları SENTETİK.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// pinFixture — iki sinyalli grup (trace + log), tek sinyal kolu güçlü üye
// ve servis düzeyi olaylar; hepsinin dedektörü durum blobunda "gölgede".
func pinFixture() ([]chstore.MLEvent, chstore.MLPrecisionState) {
	evs := []chstore.MLEvent{
		gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150),
		gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 14),
		gateEv("s", detOpLatency, "billing-api", 0, 40, 100, 900),
		svcEv("b1", detSvcBreadth, "payments-api", 0, 1000, 2500),
		svcEv("e1", detSvcErrorRate, "ledger-api", 0, 0.01, 0.05),
	}
	st := chstore.MLPrecisionState{Detectors: map[string]chstore.MLPrecisionDetState{
		detOpLatency:  {Demoted: true, DemotedAt: 1},
		detErrorRate:  {Demoted: true, Virtual: true, DemotedAt: 1},
		detSvcBreadth: {Demoted: true, DemotedAt: 1},
	}}
	return evs, st
}

func pinRun(evs []chstore.MLEvent, demoted map[string]bool, now time.Time) (string, string, string) {
	kept := append([]chstore.MLEvent(nil), evs...)
	groups, counts := decideGroups(kept, nil, 10*time.Minute, GroupGateInput{Demoted: demoted}, now)
	svc := decideSvcShadow(demotedOut(publishedCandidates(kept, nil), demoted), nil, groups, chstore.DefaultProblemPriority(), now)
	stampGroupDecisions(kept, groups)
	stampSvcShadow(kept, svc)
	stampDetShadow(kept, demoted, now)
	g, _ := json.Marshal(struct {
		G []GroupDecision
		C shadowCounts
	}{decisionsOf(groups), counts})
	s, _ := json.Marshal(svc)
	k, _ := json.Marshal(kept)
	return string(g), string(s), string(k)
}

func decisionsOf(gs []decidedGroup) []GroupDecision {
	out := make([]GroupDecision, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.D)
	}
	return out
}

func TestPrecisionShadowDefaultChangesNothing(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	if m := cfg.PrecisionShadowMode(); m != chstore.MLPrecisionModeShadow {
		t.Fatalf("varsayılan kip shadow olmalı: %s", m)
	}
	if m := chstore.NormalizeMLConfig(chstore.MLConfig{}).PrecisionShadowMode(); m != chstore.MLPrecisionModeShadow {
		t.Fatalf("boş blob → shadow: %s", m)
	}
	// Tik yolu: kip on değilken durum blobu OKUNMAZ (store nil → okunsaydı panik).
	r := &Recorder{}
	cfg.Enabled = true
	if d := r.precisionDemoted(context.Background(), cfg); d != nil {
		t.Fatalf("shadow kipinde gölge kümesi nil olmalı: %v", d)
	}
	off := cfg
	off.PrecisionShadow = &chstore.MLPrecisionShadow{Mode: chstore.MLPrecisionModeOff}
	if d := r.precisionDemoted(context.Background(), chstore.NormalizeMLConfig(off)); d != nil {
		t.Fatalf("off kipinde gölge kümesi nil olmalı: %v", d)
	}
	evs, st := pinFixture()
	// Pin'ler de shadow kipinde davranış değiştirmez.
	ps := cfg.PrecisionShadowSettings()
	ps.Pins = map[string]string{detSvcErrorRate: chstore.MLPrecisionPinShadow, "log_volume": chstore.MLPrecisionPinShadow}
	if set := chstore.MLPrecisionDemotedSet(ps, st); set != nil {
		t.Fatalf("shadow kipinde küme: %v", set)
	}
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	g0, s0, k0 := pinRun(evs, nil, now)                                   // v0.10.2485 girdisi (alan yok)
	g1, s1, k1 := pinRun(evs, chstore.MLPrecisionDemotedSet(ps, st), now) // varsayılan kip
	if g0 != g1 || s0 != s1 || k0 != k1 {
		t.Fatalf("varsayılan kipte karar değişti:\n%s\n%s\n%s\n%s", g0, g1, s0, s1)
	}
	// Ön koşul: fikstür gerçekten Problem açan kararlar içeriyor.
	var dec struct{ G []GroupDecision }
	_ = json.Unmarshal([]byte(g0), &dec)
	open := 0
	for _, d := range dec.G {
		if d.WouldOpen {
			open++
		}
	}
	if open < 2 || len(s0) < 10 {
		t.Fatalf("fikstür kör: %s %s", g0, s0)
	}
	// Bekçi kör değil: on kipinde aynı durum kararları değiştirir.
	pon := ps
	pon.Mode = chstore.MLPrecisionModeOn
	set := chstore.MLPrecisionDemotedSet(pon, st)
	if !set[detOpLatency] || !set[detSvcBreadth] || set[detErrorRate] || set[detSvcErrorRate] {
		t.Fatalf("on kümesi (sanal ve muaf hariç): %v", set)
	}
	g2, s2, k2 := pinRun(evs, set, now)
	if g2 == g0 || s2 == s0 || k2 == k0 {
		t.Fatal("on kipinde karar değişmeliydi (bekçi kör)")
	}
	var svc map[string]SvcShadow
	_ = json.Unmarshal([]byte(s2), &svc)
	if _, ok := svc["b1"]; ok {
		t.Errorf("gölgedeki svc_breadth servis kararına girmemeli: %+v", svc)
	}
	if !svc["e1"].WouldOpen {
		t.Errorf("muaf svc_error_rate aynen açardı: %+v", svc)
	}
}

// on kipi: gölgedeki dedektörün üyesi hiçbir kolda sayılmaz.
func TestPrecisionDemotedMemberNotCounted(t *testing.T) {
	two := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 14)}
	if d := decideFor(t, two, GroupGateInput{}); !d.WouldOpen {
		t.Fatalf("ön koşul: iki sinyal açar %+v", d)
	}
	d := decideFor(t, two, GroupGateInput{Demoted: map[string]bool{detErrorRate: true}})
	if d.WouldOpen || len(d.Signals) != 1 {
		t.Errorf("gölgedeki log üyesi sinyal saymamalı: %+v", d)
	}
	strong := []chstore.MLEvent{gateEv("s", detOpLatency, "billing-api", -40, 0, 100, 900)}
	if d := decideFor(t, strong, GroupGateInput{}); !d.SingleWouldOpen {
		t.Fatalf("ön koşul: tek sinyal kolu açar %+v", d)
	}
	if c := singleArmCheck(strong[0], GroupGateInput{Demoted: map[string]bool{detOpLatency: true}}, gBase); c.Unmet != SingleUnmetDemoted {
		t.Errorf("tek sinyal kolu: %+v", c)
	}
	if got := decideFor(t, strong, GroupGateInput{Demoted: map[string]bool{detOpLatency: true}}); got.WouldOpen {
		t.Errorf("gölgedeki dedektör tek başına açmamalı: %+v", got)
	}
	if !reflect.DeepEqual(decideFor(t, two, GroupGateInput{Demoted: map[string]bool{}}), decideFor(t, two, GroupGateInput{})) {
		t.Error("boş küme = alan yok")
	}
}
