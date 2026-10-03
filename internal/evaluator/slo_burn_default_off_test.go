package evaluator

// slo_burn_default_off_test.go — v0.10.1081 (operatör: "SLO burn rate problem
// olmasın, çıkar. SLO ile ilgili beklentim yok."). v0.10.1069 göçünün emsali.
//
// Pinlenen sözleşmeler:
//  1. Açık (open + acknowledged) slo:* problemleri dürüst gerekçeyle kapanır
//     ("slo burn problems disabled by default v0.10.1081"), Value ezilmez;
//     başka kuralın problemi açık kalır.
//  2. TEK audit satırı (aktör system), kapatılan id'lerle.
//  3. İdempotent: işaret yazıldıktan sonra ikinci koşu hiçbir şey yapmaz.
//  4. Hata yönü: işaret okunamazsa ya da bir kapatma düşerse işaret yazılmaz;
//     yeniden deneme çift kapatma / çift audit üretmez.
//  5. Bayrak zaten AÇIKSA hiçbir şey kapatılmaz, yalnız işaret.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// fakeSLOOff — sloOffStore'un bellek içi eşi (fakeOffStore'un problem ve
// ayar yarısı; kural yüzeyi yok).
type fakeSLOOff struct {
	settings  map[string][]byte
	problems  map[string]chstore.Problem
	audits    []chstore.AuditEntry
	getErr    error
	upsertErr map[string]error
}

func newFakeSLOOff(ps ...chstore.Problem) *fakeSLOOff {
	f := &fakeSLOOff{settings: map[string][]byte{}, problems: map[string]chstore.Problem{}, upsertErr: map[string]error{}}
	for _, p := range ps {
		f.problems[p.ID] = p
	}
	return f
}

func (f *fakeSLOOff) GetSetting(_ context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.settings[key], nil
}

func (f *fakeSLOOff) PutSetting(_ context.Context, key string, v []byte) error {
	f.settings[key] = v
	return nil
}

func (f *fakeSLOOff) OpenProblemsSnapshot(context.Context) (*chstore.OpenProblems, error) {
	var open []chstore.Problem
	for _, p := range f.problems {
		if p.Status == "open" || p.Status == "acknowledged" {
			open = append(open, p)
		}
	}
	return chstore.NewOpenProblems(open), nil
}

func (f *fakeSLOOff) UpsertProblem(_ context.Context, p chstore.Problem) error {
	if err := f.upsertErr[p.ID]; err != nil {
		return err
	}
	f.problems[p.ID] = p
	return nil
}

func (f *fakeSLOOff) AppendAudit(_ context.Context, e chstore.AuditEntry) error {
	f.audits = append(f.audits, e)
	return nil
}

func sloOffFixture() *fakeSLOOff {
	return newFakeSLOOff(
		chstore.Problem{ID: "s1", RuleID: "slo:slo-a:warning", RuleName: "SLO burn-rate warning — checkout", Service: "svc-checkout", Status: "open", Value: 7.2, Threshold: 6, Description: "Burn rate above warning threshold for SLO \"checkout\""},
		chstore.Problem{ID: "s2", RuleID: "slo:slo-b:critical", RuleName: "SLO burn-rate critical — ledger", Service: "svc-ledger", Status: "acknowledged", Value: 18, Threshold: 14.4, Assignee: "op-1"},
		chstore.Problem{ID: "s3", RuleID: "slo:slo-a:critical", Service: "svc-checkout", Status: "resolved"},
		chstore.Problem{ID: "u1", RuleID: "user-checkout-p99", Service: "svc-checkout", Status: "open"},
		chstore.Problem{ID: "d1", RuleID: "db-health:couchbase@db-host-1/orders", Service: "db:couchbase@orders", Status: "open"},
	)
}

func TestSLOBurnDefaultOff_ResolvesAndAudits(t *testing.T) {
	f := sloOffFixture()
	res, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow)
	if err != nil {
		t.Fatalf("göç hata verdi: %v", err)
	}
	if !res.Ran || len(res.Resolved) != 2 {
		t.Fatalf("Ran=%v resolved=%d, beklenen true/2", res.Ran, len(res.Resolved))
	}
	for _, id := range []string{"s1", "s2"} {
		p := f.problems[id]
		if p.Status != "resolved" || p.ResolvedAt == nil || *p.ResolvedAt != offNow.UnixNano() {
			t.Errorf("%s kapanmadı: status=%q", id, p.Status)
		}
		if !strings.Contains(p.Description, "auto-resolved: slo burn problems disabled by default v0.10.1081") {
			t.Errorf("%s gerekçe yok: %q", id, p.Description)
		}
	}
	if f.problems["s1"].Value != 7.2 || f.problems["s2"].Assignee != "op-1" {
		t.Error("tam satır taşınmadı (Value / Assignee)")
	}
	for _, id := range []string{"u1", "d1"} {
		if f.problems[id].Status != "open" {
			t.Errorf("%s slo dışı ama kapatıldı", id)
		}
	}
	if len(f.audits) != 1 {
		t.Fatalf("%d audit satırı, tek bekleniyordu", len(f.audits))
	}
	a := f.audits[0]
	if a.Action != sloBurnDefaultOffAction || a.ActorID != "system" {
		t.Errorf("audit eylem/aktör: %q / %q", a.Action, a.ActorID)
	}
	ids := strings.Split(a.TargetID, ",")
	if len(ids) != 2 {
		t.Errorf("audit hedefi iki id taşımalı: %q", a.TargetID)
	}
	var m sloBurnDefaultOffMarker
	if err := json.Unmarshal(f.settings[sloBurnDefaultOffKey], &m); err != nil {
		t.Fatalf("işaret yok/bozuk: %v", err)
	}
	if m.Version != "v0.10.1081" || m.ResolvedProblems != 2 {
		t.Errorf("işaret: %+v", m)
	}
}

func TestSLOBurnDefaultOff_Idempotent(t *testing.T) {
	f := sloOffFixture()
	if _, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow); err != nil {
		t.Fatal(err)
	}
	// Operatör sonra bayrağı açtı ve yeni bir burn problemi açıldı: göç bir
	// daha koşmaz, problemi kapatmaz, ikinci audit yazmaz.
	f.problems["s9"] = chstore.Problem{ID: "s9", RuleID: "slo:slo-c:critical", Service: "svc-pay", Status: "open"}
	res, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ran {
		t.Fatal("işaret varken göç yeniden koştu")
	}
	if f.problems["s9"].Status != "open" {
		t.Fatal("işaretten sonra açılan burn problemi kapatıldı")
	}
	if len(f.audits) != 1 {
		t.Fatalf("ikinci koşu audit yazdı (%d)", len(f.audits))
	}
}

func TestSLOBurnDefaultOff_FailureLeavesNoMarker(t *testing.T) {
	f := sloOffFixture()
	f.getErr = errors.New("ch down")
	if _, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow); err == nil {
		t.Fatal("işaret okunamazken hata dönmeliydi")
	}
	if f.problems["s1"].Status != "open" || len(f.audits) != 0 {
		t.Fatal("işaret okunamazken kör koştu")
	}
	f.getErr = nil

	f.upsertErr["s2"] = errors.New("write failed")
	if _, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow); err == nil {
		t.Fatal("kapatma düşünce hata dönmeliydi")
	}
	if _, ok := f.settings[sloBurnDefaultOffKey]; ok {
		t.Fatal("yarım koşu işaret yazdı")
	}
	// Yeniden deneme: yalnız kalan kapanır, s1 ikinci kez kapatılmaz.
	delete(f.upsertErr, "s2")
	res, err := applySLOBurnDefaultOff(context.Background(), f, false, offNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolved) != 1 || res.Resolved[0].ID != "s2" {
		t.Fatalf("yeniden deneme %v kapattı, yalnız s2 bekleniyordu", res.Resolved)
	}
	if strings.Count(f.problems["s1"].Description, "auto-resolved") != 1 {
		t.Errorf("s1 gerekçesi çiftlendi: %q", f.problems["s1"].Description)
	}
}

func TestSLOBurnDefaultOff_SkipsWhenEnabled(t *testing.T) {
	f := sloOffFixture()
	res, err := applySLOBurnDefaultOff(context.Background(), f, true, offNow)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || len(res.Resolved) != 0 {
		t.Fatalf("bayrak açıkken kapatma yapıldı: %v", res.Resolved)
	}
	if f.problems["s1"].Status != "open" || len(f.audits) != 0 {
		t.Fatal("bayrak açıkken problem kapandı ya da audit yazıldı")
	}
	var m sloBurnDefaultOffMarker
	if err := json.Unmarshal(f.settings[sloBurnDefaultOffKey], &m); err != nil || !m.SkippedEnabled {
		t.Fatalf("işaret yazılmalı ve SkippedEnabled taşımalı: %+v %v", m, err)
	}
}
