package evaluator

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1073 — veritabanı sağlık kuralı (db-health). Operatör: "Dün akşam
// CRM database'inde sorun oldu ama problemlerde P1 gelmedi." Saf karar
// tablosu (her kapı, göreli p99, histerezis, 5 dk sınırı, düşük hacim, kesik
// okuma), şiddet/öncelik/yükselme bildirimi, gerekçe cümlesi, keep-last-good,
// kural kapatma, tik sırası ve yumuşak-hata pinleri.

var dbhCur = time.Date(2026, 10, 2, 21, 45, 0, 0, time.UTC)

const dbhID = "db-health:oracle@db-host-01/crm-db"

func dbhRow(bucketAgo int, calls, errs uint64, p99 float64, affected uint64) chstore.DBHealthBucket {
	return chstore.DBHealthBucket{
		DBSystem: "oracle", Instance: "db-host-01", DBName: "crm-db",
		Bucket: dbhCur.Add(-time.Duration(bucketAgo) * dbHealthBucket),
		Calls:  calls, Errors: errs, P99Ms: p99,
		Callers: affected + 1, AffectedCallers: affected,
		TopCallers: []string{"svc-a", "svc-b", "svc-c"},
	}
}

// withRef — dünkü aynı kovanın p99'u (göreli p99 boyutu için).
func withRef(b chstore.DBHealthBucket, ref float64) chstore.DBHealthBucket {
	b.RefP99Ms, b.HasRef = ref, true
	return b
}

func dbhRows(rs ...chstore.DBHealthBucket) []chstore.DBHealthBucket { return rs }

func TestDBHealthDecideFireGates(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	cases := []struct {
		name string
		rows []chstore.DBHealthBucket
		fire bool
	}{
		{"iki tam kova hata %10, 2 çağıran → açılır",
			dbhRows(dbhRow(2, 1000, 100, 50, 2), dbhRow(1, 1000, 100, 50, 2)), true},
		{"cari kova tabanı geçti → çift (önceki, cari)",
			dbhRows(dbhRow(1, 1000, 100, 50, 2), dbhRow(0, 400, 40, 50, 2)), true},
		{"yalnız TEK kova ihlal → açılmaz",
			dbhRows(dbhRow(2, 1000, 0, 50, 0), dbhRow(1, 1000, 100, 50, 2)), false},
		{"çağrı tabanın altında → açılmaz",
			dbhRows(dbhRow(2, 99, 50, 50, 2), dbhRow(1, 99, 50, 50, 2)), false},
		{"tek etkilenen çağıran → açılmaz",
			dbhRows(dbhRow(2, 1000, 100, 50, 1), dbhRow(1, 1000, 100, 50, 1)), false},
		// Batch çağıranlar ve < MinCallerCalls çağrılı çağıranlar okumada (SQL)
		// etkilenen sayısından düşer: geriye kalan tek çağıran kapıyı geçemez.
		{"batch / düşük hacimli çağıranlar düştükten sonra 1 çağıran → açılmaz",
			dbhRows(dbhRow(2, 1000, 100, 50, 1), dbhRow(1, 1000, 100, 50, 1)), false},
		{"p99 dünün 12.5 katı ve ≥ 2 s → açılır",
			dbhRows(withRef(dbhRow(2, 1000, 0, 5000, 3), 400), withRef(dbhRow(1, 1000, 0, 2500, 3), 200)), true},
		{"p99 ≥ 2 s ama dünün yalnız 2.5 katı (raporlama DB'si) → açılmaz",
			dbhRows(withRef(dbhRow(2, 1000, 0, 5000, 3), 2000), withRef(dbhRow(1, 1000, 0, 5000, 3), 2000)), false},
		{"p99 yüksek ama dünkü kova yok → p99 boyutu kapalı, açılmaz",
			dbhRows(dbhRow(2, 1000, 0, 5000, 3), dbhRow(1, 1000, 0, 5000, 3)), false},
		{"p99 dünün 15 katı ama mutlak tabanın altında → açılmaz",
			dbhRows(withRef(dbhRow(2, 1000, 0, 1500, 3), 100), withRef(dbhRow(1, 1000, 0, 1500, 3), 100)), false},
		{"bir kova hata, diğeri göreli p99 → iki kova da ihlal → açılır",
			dbhRows(dbhRow(2, 1000, 80, 100, 2), withRef(dbhRow(1, 1000, 0, 3000, 2), 300)), true},
		{"yarım cari kova tabanın altında → son iki tamamlanmış kova",
			dbhRows(dbhRow(2, 1000, 100, 50, 2), dbhRow(1, 1000, 100, 50, 2), dbhRow(0, 20, 0, 10, 0)), true},
	}
	for _, c := range cases {
		got := dbHealthDecide(c.rows, cfg, dbhCur, nil, false)
		if len(got) != 1 {
			t.Fatalf("%s: 1 karar bekleniyor, %d", c.name, len(got))
		}
		if got[0].Fire != c.fire {
			t.Errorf("%s: fire=%v, istenen %v", c.name, got[0].Fire, c.fire)
		}
	}
}

func TestDBHealthDecideClear(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	open := []string{dbhID}
	cases := []struct {
		name      string
		rows      []chstore.DBHealthBucket
		open      []string
		truncated bool
		clear     bool
	}{
		{"iki tamamlanmış kova temiz → temiz",
			dbhRows(dbhRow(2, 1000, 10, 100, 0), dbhRow(1, 1000, 0, 100, 0)), open, false, true},
		{"histerezis: bir kova ihlal bir kova temiz → temiz değil",
			dbhRows(dbhRow(2, 1000, 100, 100, 2), dbhRow(1, 1000, 0, 100, 0)), open, false, false},
		{"yarım cari kova temiz ama tamamlanmış b2 ihlal → temiz DEĞİL (kapanış yalnız tamamlanmış kovalardan)",
			dbhRows(dbhRow(2, 1000, 100, 100, 2), dbhRow(1, 1000, 0, 100, 0), dbhRow(0, 500, 0, 100, 0)), open, false, false},
		{"düşük hacim: eşik üstü ama iki kova da MinCalls altında → temiz (sonsuz tutma yok)",
			dbhRows(dbhRow(2, 40, 20, 100, 2), dbhRow(1, 40, 20, 100, 2)), open, false, true},
		{"ihlal sürüyor ama çağıran kapısı tutmuyor → temiz değil (tut)",
			dbhRows(dbhRow(2, 1000, 100, 100, 1), dbhRow(1, 1000, 100, 100, 1)), open, false, false},
		{"açık DB okumada hiç yok (HAVING açık id'leri geçirir → verisiz) → temiz",
			nil, open, false, true},
		{"açık DB okumada yok ama okuma kesik → temiz değil",
			nil, open, true, false},
		{"kesik okumada temiz satırlar da temiz sayılmaz",
			dbhRows(dbhRow(2, 1000, 0, 100, 0), dbhRow(1, 1000, 0, 100, 0)), open, true, false},
		{"p99 yüksek ama referans yok → p99 boyutu kapalı → temiz",
			dbhRows(dbhRow(2, 1000, 0, 5000, 3), dbhRow(1, 1000, 0, 5000, 3)), open, false, true},
		{"göreli p99 sürüyor → temiz değil",
			dbhRows(withRef(dbhRow(2, 1000, 0, 5000, 3), 400), withRef(dbhRow(1, 1000, 0, 5000, 3), 400)), open, false, false},
	}
	for _, c := range cases {
		got := dbHealthDecide(c.rows, cfg, dbhCur, c.open, c.truncated)
		if len(got) != 1 || got[0].ID != dbhID {
			t.Fatalf("%s: tek karar (%s) bekleniyor: %+v", c.name, dbhID, got)
		}
		if got[0].Clear != c.clear {
			t.Errorf("%s: clear=%v, istenen %v", c.name, got[0].Clear, c.clear)
		}
	}
}

// 5 dk sınırı: yarım kovayla AÇILAN problem, aynı veriyle bir sonraki tikte
// (aynı kova ya da kova döndükten sonra) KAPANMAZ.
func TestDBHealthBoundaryNoFlap(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	rows := dbhRows(dbhRow(2, 1000, 0, 100, 0), dbhRow(1, 1000, 100, 50, 2), dbhRow(0, 300, 30, 50, 2))
	v := dbHealthDecide(rows, cfg, dbhCur, nil, false)[0]
	if !v.Fire || v.Clear {
		t.Fatalf("yarım kovayla açılmalı, temiz olmamalı: %+v", v)
	}
	if again := dbHealthDecide(rows, cfg, dbhCur.Add(time.Minute).Truncate(dbHealthBucket), []string{dbhID}, false)[0]; again.Clear {
		t.Error("aynı kova, aynı veri: kapanmamalı")
	}
	if next := dbHealthDecide(rows, cfg, dbhCur.Add(dbHealthBucket), []string{dbhID}, false)[0]; next.Clear {
		t.Error("kova döndü (eski yarım kova artık tamamlanmış ve ihlalde): kapanmamalı")
	}
}

func TestDBHealthClearStepNeedsTwoReads(t *testing.T) {
	var m dbHealthCfgMemo
	if m.clearStep(dbhID, true) {
		t.Fatal("ilk temiz okumada kapanmamalı")
	}
	if m.clearStep(dbhID, false) {
		t.Fatal("temiz olmayan okuma kapatmaz")
	}
	if m.clearStep(dbhID, true) {
		t.Fatal("sayaç sıfırlanmış olmalı")
	}
	if !m.clearStep(dbhID, true) {
		t.Fatal("iki ardışık temiz okumada kapanmalı")
	}
	m.clearStep("db-health:x@y/z", true)
	m.prune(map[string]bool{})
	if m.clearStep("db-health:x@y/z", true) {
		t.Error("prune sonrası sayaç sıfırdan başlamalı")
	}
}

func TestDBHealthDecideDeterministicPerDB(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	a := dbhRow(1, 1000, 100, 50, 2)
	b := a
	b.DBName = "billing-db"
	got := dbHealthDecide([]chstore.DBHealthBucket{a, b}, cfg, dbhCur, nil, false)
	if len(got) != 2 || got[0].ID > got[1].ID {
		t.Fatalf("veritabanı başına bir karar, kural id sıralı: %+v", got)
	}
}

func TestDBHealthRefCandidatesAndApply(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	slow := dbhRow(1, 1000, 0, 2500, 2)
	fast := dbhRow(1, 1000, 0, 100, 2)
	fast.DBName = "billing-db"
	rows := []chstore.DBHealthBucket{slow, fast, dbhRow(0, 10, 0, 3000, 0)}
	if got := dbHealthRefCandidates(rows, cfg); len(got) != 1 || got[0] != dbhID {
		t.Errorf("yalnız mutlak tabana ulaşan DB referans ister: %v", got)
	}
	refs := map[chstore.DBHealthRefKey]float64{{RuleID: dbhID, Bucket: slow.Bucket.Unix()}: 400}
	dbHealthApplyRefs(rows, refs)
	if !rows[0].HasRef || rows[0].RefP99Ms != 400 || rows[1].HasRef || rows[2].HasRef {
		t.Errorf("referans yalnız eşleşen (id, kova)'ya işlenmeli: %+v", rows)
	}
}

func TestDBHealthMeasureSeverityAndValue(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	cases := []struct {
		name             string
		b                chstore.DBHealthBucket
		metric, sev      string
		value, threshold float64
	}{
		{"hata %10 / eşik 5 → critical", dbhRow(0, 1000, 100, 50, 2), chstore.DBHealthMetricErrorPct, "critical", 10, 5},
		{"hata %7 → warning", dbhRow(0, 1000, 70, 50, 2), chstore.DBHealthMetricErrorPct, "warning", 7, 5},
		{"göreli p99 5 s / eşik 2 s → critical", withRef(dbhRow(0, 1000, 0, 5000, 2), 400), chstore.DBHealthMetricP99Ms, "critical", 5000, 2000},
		{"iki boyut: oranı yüksek olan değer olur", withRef(dbhRow(0, 1000, 60, 6000, 2), 500), chstore.DBHealthMetricP99Ms, "critical", 6000, 2000},
		{"referanssız p99 şiddete/değere girmez", dbhRow(0, 1000, 60, 9000, 2), chstore.DBHealthMetricErrorPct, "warning", 6, 5},
	}
	for _, c := range cases {
		m, v, th, sev := dbHealthMeasure(c.b, cfg)
		if m != c.metric || sev != c.sev || v != c.value || th != c.threshold {
			t.Errorf("%s: %s %v/%v %s", c.name, m, v, th, sev)
		}
	}
}

// Kritik %10 vs taban %5 → computePriority P1 (critical + oran ≥ 2×).
func TestDBHealthCriticalReachesP1(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	v := dbHealthDecide(dbhRows(dbhRow(2, 1000, 100, 50, 2), dbhRow(1, 1000, 100, 50, 2)), cfg, dbhCur, nil, false)[0]
	p := dbHealthProblem(v, cfg, dbhCur)
	out := chstore.EnrichProblemsWithPriority([]chstore.Problem{p})
	if out[0].Priority != "P1" {
		t.Errorf("critical %%10 vs %%5 → P1 bekleniyor, %s (%s)", out[0].Priority, out[0].PriorityReason)
	}
	warn := dbHealthDecide(dbhRows(dbhRow(2, 1000, 70, 50, 2), dbhRow(1, 1000, 70, 50, 2)), cfg, dbhCur, nil, false)[0]
	wp := chstore.EnrichProblemsWithPriority([]chstore.Problem{dbHealthProblem(warn, cfg, dbhCur)})
	if wp[0].Priority == "P1" {
		t.Errorf("warning %%7 P1 olmamalı: %s", wp[0].Priority)
	}
	if p.Kind != chstore.ProblemKindDB || p.Service != "db:oracle@crm-db" || p.RuleID != p.ID || p.RuleID != dbhID {
		t.Errorf("problem satırı: %+v", p)
	}
	if out[0].Category != chstore.CategoryError {
		t.Errorf("kategori %q", out[0].Category)
	}
	if got := chstore.ProblemNotifyKind(p); got != chstore.NotifyKindProblem {
		t.Errorf("bildirim türü %q", got)
	}
}

// Şiddet yükselince (warning → critical) yeniden bildirim.
func TestDBHealthSeverityRoseNotifies(t *testing.T) {
	for _, c := range []struct {
		old, cur string
		want     bool
	}{
		{"warning", "critical", true}, {"info", "warning", true}, {"critical", "critical", false},
		{"critical", "warning", false}, {"warning", "warning", false},
	} {
		if got := dbHealthSeverityRose(c.old, c.cur); got != c.want {
			t.Errorf("%s → %s: %v, istenen %v", c.old, c.cur, got, c.want)
		}
	}
	src, err := os.ReadFile("db_health.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (e *Evaluator) reconcileDBHealth(")
	j := strings.Index(s, "func (e *Evaluator) keepDBHealth(")
	if i < 0 || j < i || !strings.Contains(s[i:j], "if rose && e.notifier != nil {") ||
		!strings.Contains(s[i:j], "go e.notifier.SendProblemAlert(context.Background(), q)") {
		t.Error("tazeleme yolunda şiddet yükselince SendProblemAlert çağrılmalı")
	}
}

func TestDBHealthReasonSentence(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	k := dbHealthKey{System: "oracle", Instance: "db-host-01", DBName: "crm-db"}
	r := dbHealthReason(k, dbhRow(0, 1000, 102, 50, 2), cfg)
	want := "oracle crm-db (db-host-01) veritabanında hata oranı %10.2 (eşik %5), 2 çağıran servis etkilendi (svc-a, svc-b, svc-c) — 1000 çağrı / 5 dk."
	if r != want {
		t.Errorf("gerekçe:\n got %s\nwant %s", r, want)
	}
	both := dbHealthReason(dbHealthKey{System: "oracle", Instance: "db-host-01", DBName: "default"}, withRef(dbhRow(0, 1000, 100, 5200, 3), 400), cfg)
	for _, w := range []string{"oracle db-host-01 veritabanında", "hata oranı %10.0 (eşik %5) ve p99 5.20 s (eşik 2.00 s; dün aynı saatte 400 ms, 13.0×)", "3 çağıran"} {
		if !strings.Contains(both, w) {
			t.Errorf("gerekçede %q yok: %s", w, both)
		}
	}
}

func TestDBHealthOpenOrderStormCap(t *testing.T) {
	vs := []dbHealthVerdict{
		{Key: dbHealthKey{DBName: "a"}, Severity: "warning", Value: 7, Threshold: 5},
		{Key: dbHealthKey{DBName: "b"}, Severity: "critical", Value: 10, Threshold: 5},
		{Key: dbHealthKey{DBName: "c"}, Severity: "critical", Value: 30, Threshold: 5},
	}
	dbHealthOpenOrder(vs)
	if vs[0].Key.DBName != "c" || vs[1].Key.DBName != "b" || vs[2].Key.DBName != "a" {
		t.Errorf("sıra: %v %v %v", vs[0].Key.DBName, vs[1].Key.DBName, vs[2].Key.DBName)
	}
}

// Kural kapatılınca açık/onaylı satırlar "rule disabled" gerekçesiyle kapanır
// (bayat süpürmenin "source silent"i değil); anlık görüntü satırı değişmez.
func TestDBHealthDisabledResolutions(t *testing.T) {
	all := []*chstore.Problem{
		{ID: dbhID, RuleID: dbhID, Status: "open", Description: "x", Value: 10},
		{ID: "db-health:oracle@h/d", RuleID: "db-health:oracle@h/d", Status: "acknowledged", Description: "y"},
		{ID: "db-slow-stmt:oracle:d:1", RuleID: "db-slow-stmt", Status: "open"},
		{ID: "db-health:oracle@h/r", RuleID: "db-health:oracle@h/r", Status: "resolved"},
		nil,
	}
	open := dbHealthOpenRows(all)
	if len(open) != 2 {
		t.Fatalf("yalnız açık/onaylı db-health satırları: %d", len(open))
	}
	res := dbHealthDisabledResolutions(open, dbhCur.UnixNano())
	for _, q := range res {
		if q.Status != "resolved" || q.ResolvedAt == nil || !strings.Contains(q.Description, "rule disabled") {
			t.Errorf("kapanış: %+v", q)
		}
	}
	if res[0].Value != 10 {
		t.Error("kapanış Value'yu ezmemeli (v0.9.977)")
	}
	if all[0].Status != "open" {
		t.Error("anlık görüntü satırı değişmemeli")
	}
	if again := dbHealthDisabledResolutions([]*chstore.Problem{&res[0]}, dbhCur.UnixNano()); strings.Count(again[0].Description, "rule disabled") != 1 {
		t.Error("gerekçe iki kez eklenmemeli")
	}
}

// keep-last-good: okuma hatasında son iyi değer; hiç okunmadıysa karar yok.
func TestDBHealthConfigKeepLastGood(t *testing.T) {
	var m dbHealthCfgMemo
	if _, ok := m.resolve(chstore.DBSlowQueryConfig{}, errors.New("ch down")); ok {
		t.Fatal("hiç okunmamış ayarla karar verilmemeli")
	}
	off := false
	h := chstore.DefaultDBHealth()
	h.Enabled, h.ErrorPct = &off, 8
	got, ok := m.resolve(chstore.DBSlowQueryConfig{Health: &h}, nil)
	if !ok || got.On() || got.ErrorPct != 8 {
		t.Fatalf("okunan değer: %+v %v", got, ok)
	}
	again, ok := m.resolve(chstore.DBSlowQueryConfig{}, errors.New("timeout"))
	if !ok || again.On() || again.ErrorPct != 8 {
		t.Errorf("hata hâlinde son iyi değer (kapalı, %%8) korunmalı: %+v", again)
	}
}

// Yumuşak-hata yönü: ayar hiç okunamadı / ana okuma / referans okuması
// düştü → açık problemler TAZELENİR (bayat süpürme olay ortasında P1'i
// "source silent" diye kapatmasın); kural kapalı → "rule disabled" kapanışı.
func TestDBHealthSoftFailKeepsOpenProblems(t *testing.T) {
	b, err := os.ReadFile("db_health.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "func (e *Evaluator) evaluateDBHealth(")
	j := strings.Index(s, "func (e *Evaluator) reconcileDBHealth(")
	body := s[i:j]
	if n := strings.Count(body, "e.keepDBHealth(ctx, open)\n\t\treturn"); n != 3 {
		t.Errorf("ayar yok + ana okuma + referans okuması hatası → tazele ve dön (3 dal), %d bulundu", n)
	}
	if !strings.Contains(body, "if !cfg.On() {\n\t\te.resolveDBHealthDisabled(ctx, open, now)") {
		t.Error("kural kapalıyken açık satırlar rule-disabled gerekçesiyle kapanmalı")
	}
}

// Tik sırası: db-health, db-slow-stmt'ten SONRA, runtime'dan ÖNCE; aynı
// lider-kilitli evaluateAll içinde.
func TestDBHealthTickOrder(t *testing.T) {
	b, err := os.ReadFile("evaluator.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	slow := strings.Index(src, "e.evaluateDBSlowStatements(ctx)")
	health := strings.Index(src, "e.evaluateDBHealth(ctx)")
	runtime := strings.Index(src, "e.evaluateRuntimePods(ctx)")
	all := strings.Index(src, "func (e *Evaluator) evaluateAll(")
	if slow < 0 || health < 0 || runtime < 0 || all < 0 {
		t.Fatalf("çağrılar bulunamadı: slow=%d health=%d runtime=%d all=%d", slow, health, runtime, all)
	}
	if !(all < slow && slow < health && health < runtime) {
		t.Errorf("sıra bozuk: evaluateAll=%d slow=%d health=%d runtime=%d", all, slow, health, runtime)
	}
	if strings.Count(src, "e.evaluateDBHealth(ctx)") != 1 {
		t.Error("db-health tik başına bir kez çağrılmalı")
	}
}
