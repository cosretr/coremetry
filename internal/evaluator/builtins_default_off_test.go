package evaluator

// builtins_default_off_test.go — v0.10.1069 (operatör, prod Alert rules
// sayfası: "Built-in alertleri kaldıralım, çok false pozitif geliyor.").
//
// Pinlenen sözleşmeler:
//  1. Göç yalnız YERLEŞİK kuralları kapatır; kullanıcı kuralına dokunmaz.
//  2. Kapatılan kuralların açık (open + acknowledged) problemleri dürüst
//     gerekçeyle kapanır ("rule disabled by default v0.10.1069"), Value
//     ezilmez; kullanıcı kuralının problemi açık kalır.
//  3. TEK audit satırı, kapatılan id'lerin listesiyle.
//  4. İdempotent: işaret yazıldıktan sonra ikinci koşu hiçbir şey yapmaz.
//  5. Operatörün sonradan elle açtığı kural yeniden KAPATILMAZ.
//  6. Hata yönü: işaret okunamazsa ya da bir yazım düşerse işaret yazılmaz,
//     yeniden deneme çift audit / çift kapatma üretmez.
//  7. Yeni kurulum: dilim kapalı ekilir → göç hiçbir şey kapatmaz, audit
//     yazmaz, yalnız işaret bırakır.
//  8. Devre dışı kural değerlendirilmez (evaluableRules + collectMeasureKeys).

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// fakeOffStore — builtinsOffStore'un bellek içi eşi.
type fakeOffStore struct {
	settings map[string][]byte
	rules    map[string]chstore.AlertRule
	problems map[string]chstore.Problem
	audits   []chstore.AuditEntry

	getErr    error
	putErr    error
	upsertErr map[string]error // problem id → hata
	disables  int
}

func newFakeOffStore(rules []chstore.AlertRule, problems []chstore.Problem) *fakeOffStore {
	f := &fakeOffStore{
		settings:  map[string][]byte{},
		rules:     map[string]chstore.AlertRule{},
		problems:  map[string]chstore.Problem{},
		upsertErr: map[string]error{},
	}
	for _, r := range rules {
		f.rules[r.ID] = r
	}
	for _, p := range problems {
		f.problems[p.ID] = p
	}
	return f
}

func (f *fakeOffStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.settings[key], nil
}

func (f *fakeOffStore) PutSetting(_ context.Context, key string, v []byte) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.settings[key] = v
	return nil
}

func (f *fakeOffStore) InvalidateAlertRulesCache() {}

func (f *fakeOffStore) ListAlertRules(context.Context) ([]chstore.AlertRule, error) {
	out := make([]chstore.AlertRule, 0, len(f.rules))
	for _, r := range f.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeOffStore) SetAlertRuleEnabled(_ context.Context, id string, enabled bool) error {
	r, ok := f.rules[id]
	if !ok {
		return errors.New("no such rule")
	}
	r.Enabled = enabled
	f.rules[id] = r
	f.disables++
	return nil
}

func (f *fakeOffStore) OpenProblemsSnapshot(context.Context) (*chstore.OpenProblems, error) {
	var open []chstore.Problem
	for _, p := range f.problems {
		if p.Status == "open" || p.Status == "acknowledged" {
			open = append(open, p)
		}
	}
	return chstore.NewOpenProblems(open), nil
}

func (f *fakeOffStore) UpsertProblem(_ context.Context, p chstore.Problem) error {
	if err := f.upsertErr[p.ID]; err != nil {
		return err
	}
	f.problems[p.ID] = p
	return nil
}

func (f *fakeOffStore) AppendAudit(_ context.Context, e chstore.AuditEntry) error {
	f.audits = append(f.audits, e)
	return nil
}

// fixture — sentetik filo: iki açık yerleşik (biri bayraksız eski satır),
// bir zaten kapalı yerleşik, bir kullanıcı kuralı; açık/ack/çözülmüş
// problemler karışık.
func offFixture() *fakeOffStore {
	rules := []chstore.AlertRule{
		{ID: "builtin-warn-http-p99-3s", Name: "HTTP P99 latency >3s (sustained 10 min)", Enabled: true, BuiltIn: true},
		{ID: "builtin-http-p99-5s", Name: "HTTP P99 latency >5s (5 min)", Enabled: true, BuiltIn: false}, // bayraksız eski satır — önek yakalar
		{ID: "builtin-db-error-5pct", Name: "DB error rate", Enabled: false, BuiltIn: true},
		{ID: "user-checkout-p99", Name: "checkout p99", Enabled: true, BuiltIn: false},
	}
	problems := []chstore.Problem{
		{ID: "p1", RuleID: "builtin-warn-http-p99-3s", RuleName: "HTTP P99 latency >3s (sustained 10 min)", Service: "svc-alpha", Status: "open", Value: 3872.6, Threshold: 3000, Description: "p99 high"},
		{ID: "p2", RuleID: "builtin-http-p99-5s", RuleName: "HTTP P99 latency >5s (5 min)", Service: "svc-beta", Status: "acknowledged", Value: 6100, Threshold: 5000, Assignee: "op-1"},
		{ID: "p3", RuleID: "builtin-warn-http-p99-3s", Service: "svc-gamma", Status: "resolved"},
		{ID: "p4", RuleID: "user-checkout-p99", Service: "svc-alpha", Status: "open"},
		{ID: "p5", RuleID: "anomaly:svc-alpha:p99_ms", Service: "svc-alpha", Status: "open"},
	}
	return newFakeOffStore(rules, problems)
}

var offNow = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

func TestBuiltinsDefaultOff_DisablesOnlyBuiltinsAndResolves(t *testing.T) {
	f := offFixture()
	res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow)
	if err != nil {
		t.Fatalf("göç hata verdi: %v", err)
	}
	if !res.Ran {
		t.Fatal("ilk koşu Ran=false — işaret yokken göç koşmalıydı")
	}
	wantOff := []string{"builtin-http-p99-5s", "builtin-warn-http-p99-3s"}
	if strings.Join(res.Disabled, ",") != strings.Join(wantOff, ",") {
		t.Fatalf("kapatılan %v, beklenen %v", res.Disabled, wantOff)
	}
	for id, r := range f.rules {
		if isBuiltinRule(r) && r.Enabled {
			t.Errorf("%s yerleşik ama açık kaldı", id)
		}
	}
	if !f.rules["user-checkout-p99"].Enabled {
		t.Error("kullanıcı kuralı kapatıldı — göç yalnız yerleşiklere dokunmalı")
	}

	// Problemler: p1 (open) + p2 (acknowledged) kapanır; p3 zaten kapalı,
	// p4 kullanıcı kuralı, p5 anomali — dokunulmaz.
	for _, id := range []string{"p1", "p2"} {
		p := f.problems[id]
		if p.Status != "resolved" || p.ResolvedAt == nil || *p.ResolvedAt != offNow.UnixNano() {
			t.Errorf("%s kapanmadı: status=%q resolvedAt=%v", id, p.Status, p.ResolvedAt)
		}
		if !strings.Contains(p.Description, "auto-resolved: rule disabled by default v0.10.1069") {
			t.Errorf("%s gerekçe yok: %q", id, p.Description)
		}
	}
	if f.problems["p1"].Value != 3872.6 {
		t.Errorf("Value ezildi (v0.9.977 kapatma yolu Value'ya dokunmaz): %v", f.problems["p1"].Value)
	}
	if f.problems["p2"].Assignee != "op-1" {
		t.Error("tam satır taşınmadı — Assignee kayboldu")
	}
	if f.problems["p4"].Status != "open" || f.problems["p5"].Status != "open" {
		t.Error("yerleşik olmayan kuralın problemi kapatıldı")
	}
	if len(res.Resolved) != 2 {
		t.Errorf("Resolved = %d, beklenen 2", len(res.Resolved))
	}

	// Tek audit satırı, id listesi + sayı.
	if len(f.audits) != 1 {
		t.Fatalf("audit satırı %d, beklenen 1", len(f.audits))
	}
	a := f.audits[0]
	if a.Action != builtinsDefaultOffAction || a.TargetKind != "alert_rule" || a.ActorID != "system" {
		t.Errorf("audit şekli yanlış: %+v", a)
	}
	var det struct {
		Version          string   `json:"version"`
		Disabled         []string `json:"disabled"`
		ResolvedProblems int      `json:"resolvedProblems"`
	}
	if err := json.Unmarshal([]byte(a.Details), &det); err != nil {
		t.Fatalf("audit details JSON değil: %v", err)
	}
	if det.Version != "v0.10.1069" || strings.Join(det.Disabled, ",") != strings.Join(wantOff, ",") || det.ResolvedProblems != 2 {
		t.Errorf("audit details: %+v", det)
	}

	// İşaret yazıldı.
	var mk builtinsDefaultOffMarker
	if err := json.Unmarshal(f.settings[builtinsDefaultOffKey], &mk); err != nil || mk.Version != "v0.10.1069" {
		t.Fatalf("işaret yok/bozuk: %s (%v)", f.settings[builtinsDefaultOffKey], err)
	}
}

func TestBuiltinsDefaultOff_IdempotentAndRespectsReEnable(t *testing.T) {
	f := offFixture()
	if _, err := applyBuiltinsDefaultOff(context.Background(), f, offNow); err != nil {
		t.Fatal(err)
	}
	// Operatör Alert rules sayfasından bir yerleşiği yeniden açtı ve kural
	// yeni bir problem açtı.
	_ = f.SetAlertRuleEnabled(context.Background(), "builtin-warn-http-p99-3s", true)
	f.problems["p9"] = chstore.Problem{ID: "p9", RuleID: "builtin-warn-http-p99-3s", Service: "svc-delta", Status: "open"}
	disablesBefore, auditsBefore := f.disables, len(f.audits)

	res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ran || len(res.Disabled) != 0 || len(res.Resolved) != 0 {
		t.Fatalf("ikinci koşu iş yaptı: %+v", res)
	}
	if !f.rules["builtin-warn-http-p99-3s"].Enabled {
		t.Error("operatörün elle açtığı kural yeniden kapatıldı")
	}
	if f.problems["p9"].Status != "open" {
		t.Error("yeniden açılan kuralın yeni problemi kapatıldı")
	}
	if f.disables != disablesBefore || len(f.audits) != auditsBefore {
		t.Errorf("ikinci koşu yazım yaptı: disables %d→%d audits %d→%d", disablesBefore, f.disables, auditsBefore, len(f.audits))
	}
}

func TestBuiltinsDefaultOff_NewInstallWritesOnlyMarker(t *testing.T) {
	// Yeni kurulum: seedBuiltinRules dilimi aynen eker — hepsi kapalı.
	rules := make([]chstore.AlertRule, 0, len(builtins))
	for _, r := range builtins {
		if r.Enabled {
			t.Fatalf("%s varsayılan açık gemiye biniyor", r.ID)
		}
		rules = append(rules, r)
	}
	f := newFakeOffStore(rules, nil)
	res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Disabled) != 0 || len(res.Resolved) != 0 || len(f.audits) != 0 {
		t.Fatalf("yeni kurulumda iş yapıldı: %+v audits=%d", res, len(f.audits))
	}
	if f.settings[builtinsDefaultOffKey] == nil {
		t.Fatal("işaret yazılmadı — sonraki boot'ta operatörün açtığı kural kapatılabilirdi")
	}
}

func TestBuiltinsDefaultOff_FailureDirection(t *testing.T) {
	t.Run("işaret okunamadı → hiçbir şey yapma", func(t *testing.T) {
		f := offFixture()
		f.getErr = errors.New("ch down")
		res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow)
		if err == nil {
			t.Fatal("hata dönmeliydi")
		}
		if res.Ran || f.disables != 0 || len(f.audits) != 0 || f.problems["p1"].Status != "open" {
			t.Fatalf("işaret okunamazken göç koştu: %+v", res)
		}
	})

	t.Run("problem yazımı düştü → işaret yok, yeniden deneme tek audit", func(t *testing.T) {
		f := offFixture()
		f.upsertErr["p2"] = errors.New("insert failed")
		if _, err := applyBuiltinsDefaultOff(context.Background(), f, offNow); err == nil {
			t.Fatal("hata dönmeliydi")
		}
		if f.settings[builtinsDefaultOffKey] != nil {
			t.Fatal("yarım koşu işaret yazdı — kalan problem sonsuza dek açık kalırdı")
		}
		if f.problems["p2"].Status == "resolved" {
			t.Fatal("düşen yazım kapanmış görünüyor")
		}
		// Yeniden deneme: kural zaten kapalı, p1 zaten kapalı; yalnız p2.
		delete(f.upsertErr, "p2")
		res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Disabled) != 0 || len(res.Resolved) != 1 || res.Resolved[0].ID != "p2" {
			t.Fatalf("yeniden deneme: %+v", res)
		}
		if f.problems["p2"].Status != "resolved" || f.settings[builtinsDefaultOffKey] == nil {
			t.Fatal("yeniden deneme bitirmedi")
		}
	})

	t.Run("işaret yazımı düştü → sonraki koşu iş yapmadan işareti yazar", func(t *testing.T) {
		f := offFixture()
		f.putErr = errors.New("insert failed")
		if _, err := applyBuiltinsDefaultOff(context.Background(), f, offNow); err == nil {
			t.Fatal("hata dönmeliydi")
		}
		f.putErr = nil
		res, err := applyBuiltinsDefaultOff(context.Background(), f, offNow.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Disabled) != 0 || len(res.Resolved) != 0 || len(f.audits) != 1 {
			t.Fatalf("yeniden deneme çift iş: %+v audits=%d", res, len(f.audits))
		}
	})
}

// Devre dışı kural hiçbir ölçüm/problem üretmez: tik döngüsü onu
// görmez (evaluableRules) ve toplu MV ön-okuması onun için sorgu planlamaz
// (collectMeasureKeys). Gemideki yerleşik küme tümüyle kapalıyken tik
// SIFIR metrik sorgusu planlar.
func TestDisabledRuleNeverEvaluates(t *testing.T) {
	user := chstore.AlertRule{ID: "user-a", Metric: "error_rate", Comparator: ">", Threshold: 5, WindowSec: 600, Enabled: true}
	off := chstore.AlertRule{ID: "user-b", Metric: "http_p99_ms", Comparator: ">", Threshold: 3000, WindowSec: 600, Enabled: false}
	got := evaluableRules([]chstore.AlertRule{off, user, off})
	if len(got) != 1 || got[0].ID != "user-a" {
		t.Fatalf("evaluableRules = %+v", got)
	}

	measures, counts := collectMeasureKeys(builtins)
	if len(measures) != 0 || len(counts) != 0 {
		t.Fatalf("kapalı yerleşikler için ölçüm planlandı: %v %v", measures, counts)
	}
	if len(evaluableRules(builtins)) != 0 {
		t.Fatal("kapalı yerleşik tik döngüsüne girdi")
	}
}
