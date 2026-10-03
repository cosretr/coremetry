package evaluator

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1083 — db-health MUTLAK HATA SAYISI kolu. Operatör: "Oracle hataları
// da problemse hâlâ düşmüyor" → onay "Yap". Span tarafındaki ORA patlaması tüm
// veritabanının çağrıları içinde %5'e ulaşmıyordu (yüksek hacimli DB'de 340
// hata ≈ %0.2): hata % kolu hiç ateşlemiyordu. Kol: iki ardışık 5 dk kovanın
// her birinde hata SAYISI ≥ 50 VE ≥ 3 × dünkü aynı kova (dünkü kova yoksa
// KAPALI) VE ≥ 2 çağıranın her biri ≥ 10 hata; 2 × 50 → critical → P1.

// dbhErrRow — yüksek hacimli (hata % düşük, p99 düşük) kova; hata sayısı kolu
// için: errs hata, errCallers ≥10-hatalı çağıran, dünkü kova (ref, hasRef).
func dbhErrRow(bucketAgo int, errs, errCallers, ref uint64, hasRef bool) chstore.DBHealthBucket {
	b := dbhRow(bucketAgo, 100000, errs, 50, 0)
	b.ErrorCallers = errCallers
	b.RefErrors, b.HasErrRef = ref, hasRef
	return b
}

func TestDBHealthCountArmFireGates(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	cases := []struct {
		name string
		rows []chstore.DBHealthBucket
		fire bool
	}{
		{"170 + 170 hata, dün 10, 3 çağıran → açılır (hata % yalnız %0.17)",
			dbhRows(dbhErrRow(2, 170, 3, 10, true), dbhErrRow(1, 170, 3, 10, true)), true},
		{"hata sayısı tabanın altında (40 < 50) → açılmaz",
			dbhRows(dbhErrRow(2, 40, 3, 0, true), dbhErrRow(1, 40, 3, 0, true)), false},
		{"dünkü kova YOK → kol kapalı, açılmaz",
			dbhRows(dbhErrRow(2, 170, 3, 0, false), dbhErrRow(1, 170, 3, 0, false)), false},
		{"dünkü kova çok yüksek (170 < 3 × 100) → açılmaz",
			dbhRows(dbhErrRow(2, 170, 3, 100, true), dbhErrRow(1, 170, 3, 100, true)), false},
		{"dün hiç hata yoktu (kova var, 0) → kat şartı tutar, açılır",
			dbhRows(dbhErrRow(2, 60, 2, 0, true), dbhErrRow(1, 60, 2, 0, true)), true},
		// Çağıran kapısı: ≥ 10 hatalı çağıran sayısı SQL'de (c_err_affected);
		// < 10 hatalı çağıranlar ErrorCallers'a girmez — tek çağıran kalır.
		{"yalnız 1 çağıranın ≥10 hatası var (diğerleri <10) → açılmaz",
			dbhRows(dbhErrRow(2, 170, 1, 10, true), dbhErrRow(1, 170, 1, 10, true)), false},
		{"yalnız TEK kova ihlal → açılmaz",
			dbhRows(dbhErrRow(2, 10, 3, 10, true), dbhErrRow(1, 170, 3, 10, true)), false},
		{"çağrı tabanı (MinCalls) bu kolda da geçerli → açılmaz",
			func() []chstore.DBHealthBucket {
				a, b := dbhErrRow(2, 60, 2, 0, true), dbhErrRow(1, 60, 2, 0, true)
				a.Calls, b.Calls = 90, 90
				return dbhRows(a, b)
			}(), false},
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

// Kritik: 170 ≥ 2 × 50 → critical, Value = hata sayısı, Threshold = 50 →
// computePriority P1 (3.4×). 80 hata → warning → P1 DEĞİL.
func TestDBHealthCountArmSeverityAndPriority(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	v := dbHealthDecide(dbhRows(dbhErrRow(2, 170, 3, 10, true), dbhErrRow(1, 170, 3, 10, true)), cfg, dbhCur, nil, false)[0]
	if !v.Fire || v.Severity != "critical" || v.Metric != chstore.DBHealthMetricErrorCount || v.Value != 170 || v.Threshold != 50 {
		t.Fatalf("kritik hata sayısı kararı: %+v", v)
	}
	p := dbHealthProblem(v, cfg, dbhCur)
	out := chstore.EnrichProblemsWithPriority([]chstore.Problem{p})
	if out[0].Priority != "P1" {
		t.Errorf("critical 170/50 → P1 bekleniyor, %s (%s)", out[0].Priority, out[0].PriorityReason)
	}
	if out[0].Category != chstore.CategoryError {
		t.Errorf("kategori %q", out[0].Category)
	}
	w := dbHealthDecide(dbhRows(dbhErrRow(2, 80, 3, 10, true), dbhErrRow(1, 80, 3, 10, true)), cfg, dbhCur, nil, false)[0]
	if !w.Fire || w.Severity != "warning" || w.Metric != chstore.DBHealthMetricErrorCount {
		t.Fatalf("80 hata → warning: %+v", w)
	}
	if wp := chstore.EnrichProblemsWithPriority([]chstore.Problem{dbHealthProblem(w, cfg, dbhCur)}); wp[0].Priority == "P1" {
		t.Errorf("warning 80/50 P1 olmamalı: %s", wp[0].Priority)
	}
}

// Baskın boyut: hata % ve hata sayısı birlikte ihlaldeyse oranı yüksek olan
// Value/Threshold olur; hata sayısı oranı daha yüksekse o.
func TestDBHealthCountArmMeasureDominance(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	b := dbhRow(0, 1000, 60, 50, 2) // %6 (1.2×) ve 60 hata (1.2×) — eşit: hata % kalır
	b.ErrorCallers, b.HasErrRef = 2, true
	if m, _, _, _ := dbHealthMeasure(b, cfg); m != chstore.DBHealthMetricErrorPct {
		t.Errorf("eşit oranda hata %% kalmalı: %s", m)
	}
	b.Errors = 200 // %20 (4×) vs 200 hata (4×) — yine eşit
	b.Calls = 1000
	if m, _, _, sev := dbHealthMeasure(b, cfg); m != chstore.DBHealthMetricErrorPct || sev != "critical" {
		t.Errorf("eşit oran: %s %s", m, sev)
	}
	c := dbhErrRow(0, 300, 3, 10, true) // %0.3 (eşik altı), 300 hata (6×)
	if m, v, th, sev := dbHealthMeasure(c, cfg); m != chstore.DBHealthMetricErrorCount || v != 300 || th != 50 || sev != "critical" {
		t.Errorf("hata sayısı baskın: %s %v/%v %s", m, v, th, sev)
	}
}

// Kapanış/histerezis diğer kollarla AYNI: hata sayısı boyutu sürerken temiz
// değil (çağıran kapısı kapanışa girmez); dünkü kova yoksa boyut kapalı →
// temiz; taban altına inince temiz.
func TestDBHealthCountArmClear(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	open := []string{dbhID}
	cases := []struct {
		name  string
		rows  []chstore.DBHealthBucket
		clear bool
	}{
		{"hata sayısı sürüyor → temiz değil", dbhRows(dbhErrRow(2, 170, 3, 10, true), dbhErrRow(1, 170, 3, 10, true)), false},
		{"sürüyor ama çağıran kapısı tutmuyor → yine temiz değil (tut)", dbhRows(dbhErrRow(2, 170, 1, 10, true), dbhErrRow(1, 170, 1, 10, true)), false},
		{"histerezis: bir kova ihlal bir kova temiz → temiz değil", dbhRows(dbhErrRow(2, 170, 3, 10, true), dbhErrRow(1, 5, 0, 10, true)), false},
		{"dünkü kova yok → boyut kapalı → temiz", dbhRows(dbhErrRow(2, 170, 3, 0, false), dbhErrRow(1, 170, 3, 0, false)), true},
		{"taban altına indi → temiz", dbhRows(dbhErrRow(2, 20, 0, 10, true), dbhErrRow(1, 5, 0, 10, true)), true},
	}
	for _, c := range cases {
		got := dbHealthDecide(c.rows, cfg, dbhCur, open, false)
		if len(got) != 1 || got[0].Clear != c.clear {
			t.Errorf("%s: %+v, istenen clear=%v", c.name, got, c.clear)
		}
	}
}

// Referans adayı: p99 tabanı YA DA hata sayısı tabanı — yalnız bunlar için
// dünkü okuma; hata referansı çağrısı olan dünkü kovada (hata 0 dahil).
func TestDBHealthCountArmRefCandidatesAndApply(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	loud := dbhRow(1, 100000, 60, 50, 0)
	quiet := dbhRow(1, 100000, 10, 50, 0)
	quiet.DBName = "billing-db"
	rows := []chstore.DBHealthBucket{loud, quiet}
	if got := dbHealthRefCandidates(rows, cfg); len(got) != 1 || got[0] != dbhID {
		t.Errorf("yalnız hata sayısı tabanına ulaşan DB referans ister: %v", got)
	}
	refs := map[chstore.DBHealthRefKey]chstore.DBHealthRef{
		{RuleID: dbhID, Bucket: loud.Bucket.Unix()}: {Calls: 90000, Errors: 0},
	}
	dbHealthApplyRefs(rows, refs)
	if !rows[0].HasErrRef || rows[0].RefErrors != 0 || rows[0].HasRef || rows[1].HasErrRef {
		t.Errorf("hatasız dünkü kova geçerli hata referansı; p99'suz satır p99 referansı değil: %+v", rows)
	}
}

// Gerekçe cümlesi (operatör şablonu): "<sys> <db> veritabanında 10 dakikada
// 340 hata (eşik …50; dün aynı saatte 20), 3 çağıran servis etkilendi".
func TestDBHealthCountArmReasonSentence(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	v := dbHealthDecide(dbhRows(dbhErrRow(2, 170, 3, 10, true), dbhErrRow(1, 170, 3, 10, true)), cfg, dbhCur, nil, false)[0]
	got := dbHealthProblem(v, cfg, dbhCur).Description
	want := "oracle crm-db (db-host-01) veritabanında 10 dakikada 340 hata (eşik 5 dk'da 50; dün aynı saatte 20), 3 çağıran servis etkilendi (svc-a, svc-b, svc-c) — 100000 çağrı / 5 dk."
	if got != want {
		t.Errorf("gerekçe:\n got %s\nwant %s", got, want)
	}
	// Tek verili kova (çift toplamı yok) → 5 dk'lık cümle.
	one := dbHealthReason(v.Key, dbhErrRow(0, 120, 2, 0, true), dbHealthSpan{}, cfg)
	if !strings.Contains(one, "5 dakikada 120 hata (eşik 5 dk'da 50; dün aynı saatte 0), 2 çağıran") {
		t.Errorf("tek kova gerekçesi: %s", one)
	}
}

// Çiftin yenisi yarım CARİ kovaysa (çağrı tabanını geçtiği için açılış ona
// bakıyor) süre "10 dakika" diye uydurulmaz: "son iki 5 dk kovasında".
func TestDBHealthCountArmReasonPartialBucket(t *testing.T) {
	cfg := chstore.DefaultDBHealth()
	v := dbHealthDecide(dbhRows(dbhErrRow(1, 170, 3, 10, true), dbhErrRow(0, 120, 3, 10, true)), cfg, dbhCur, nil, false)[0]
	if !v.Fire || !v.Span.Partial {
		t.Fatalf("yarım cari kovayla açılmalı: %+v", v)
	}
	got := dbHealthProblem(v, cfg, dbhCur).Description
	if !strings.Contains(got, "son iki 5 dk kovasında 290 hata (eşik 5 dk'da 50; dün aynı saatte 20)") || strings.Contains(got, "10 dakikada") {
		t.Errorf("gerekçe: %s", got)
	}
}
