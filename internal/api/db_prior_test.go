package api

// db_prior_test.go — v0.10.1025 (Databases dilim 3). /api/databases
// ?compare=prior'un iki saf parçası:
//
//   - mergeDBPrior: prior sayaçlar mevcut satırlara TAM (system, instance,
//     dbName) kimliğiyle iner — sırayla değil, kısmi anahtarla değil;
//     prior'da ikizi olmayan satır sıfır Prior* ile kalır (omitempty →
//     JSON'da yok → rozet yok) ve yalnız prior'da olan satır İCAT EDİLMEZ.
//     messaging_prior_test.go'nun (v0.8.364) ikizi; v0.9.433'ten beri
//     testsizdi.
//   - dbListPriorWindow: MV yolunda paylaşılan chstore.PriorWindow (ortak
//     kova yok), ham env yolunda birebir süre kaydırması.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func dbRow(system, instance, dbName string, spans, errs uint64, avg, p50, p99 float64) chstore.DBInstance {
	return chstore.DBInstance{
		System: system, Instance: instance, DBName: dbName,
		SpanCount: spans, ErrorCount: errs,
		AvgMs: avg, P50Ms: p50, P99Ms: p99,
	}
}

type dbPriorWant struct {
	spans, errs   uint64
	avg, p50, p99 float64
}

func TestMergeDBPrior(t *testing.T) {
	cases := []struct {
		name  string
		cur   []chstore.DBInstance
		prior []chstore.DBInstance
		want  []dbPriorWant
	}{
		{
			name:  "tam kimlik eşleşmesi her prior sayacı kopyalar",
			cur:   []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 100, 5, 12, 8, 90)},
			prior: []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 80, 2, 10, 7, 70)},
			want:  []dbPriorWant{{80, 2, 10, 7, 70}},
		},
		{
			name:  "prior'da olmayan satır sıfır Prior* ile kalır",
			cur:   []chstore.DBInstance{dbRow("oracle", "db-a", "NEWDB", 10, 0, 1, 1, 2)},
			prior: []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 80, 2, 10, 7, 70)},
			want:  []dbPriorWant{{0, 0, 0, 0, 0}},
		},
		{
			// Bir host'ta N veritabanı (v0.9.821): aynı (system, instance)
			// iki farklı db_name ile ASLA çapraz eşleşmez.
			name: "dbName kimliğin parçası — aynı host'taki iki veritabanı karışmaz",
			cur: []chstore.DBInstance{
				dbRow("postgresql", "pg-1", "orders", 100, 0, 5, 4, 9),
				dbRow("postgresql", "pg-1", "billing", 200, 0, 6, 5, 11),
			},
			prior: []chstore.DBInstance{
				dbRow("postgresql", "pg-1", "billing", 150, 3, 7, 6, 13),
			},
			want: []dbPriorWant{{0, 0, 0, 0, 0}, {150, 3, 7, 6, 13}},
		},
		{
			name:  "instance kimliğin parçası",
			cur:   []chstore.DBInstance{dbRow("postgresql", "pg-2", "orders", 10, 0, 1, 1, 2)},
			prior: []chstore.DBInstance{dbRow("postgresql", "pg-1", "orders", 80, 2, 10, 7, 70)},
			want:  []dbPriorWant{{0, 0, 0, 0, 0}},
		},
		{
			name:  "system kimliğin parçası",
			cur:   []chstore.DBInstance{dbRow("mysql", "db-a", "orders", 10, 0, 1, 1, 2)},
			prior: []chstore.DBInstance{dbRow("postgresql", "db-a", "orders", 80, 2, 10, 7, 70)},
			want:  []dbPriorWant{{0, 0, 0, 0, 0}},
		},
		{
			// Receiver satırı (metric_points) db_name TAŞIMAZ (""); MV ise
			// eksik db.name'i 'default'a çözer. İkisi aynı host'ta yan yana
			// dursa da receiver satırı span prior'unu ödünç almaz.
			name: "receiver satırı ('' dbName) 'default' span prior'unu almaz",
			cur: []chstore.DBInstance{
				{System: "oracle", Instance: "db-a", Source: chstore.DBSourceReceiver},
			},
			prior: []chstore.DBInstance{dbRow("oracle", "db-a", "default", 80, 2, 10, 7, 70)},
			want:  []dbPriorWant{{0, 0, 0, 0, 0}},
		},
		{
			name: "prior sırası önemsiz — birleşme anahtarla, indeksle değil",
			cur: []chstore.DBInstance{
				dbRow("oracle", "db-a", "A", 1, 0, 1, 1, 1),
				dbRow("oracle", "db-a", "B", 2, 0, 2, 2, 2),
			},
			prior: []chstore.DBInstance{
				dbRow("oracle", "db-a", "B", 20, 1, 22, 21, 29),
				dbRow("oracle", "db-a", "A", 10, 0, 11, 10, 19),
			},
			want: []dbPriorWant{{10, 0, 11, 10, 19}, {20, 1, 22, 21, 29}},
		},
		{
			name:  "boş prior no-op",
			cur:   []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 100, 5, 12, 8, 90)},
			prior: []chstore.DBInstance{},
			want:  []dbPriorWant{{0, 0, 0, 0, 0}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nCur := len(tc.cur)
			mergeDBPrior(tc.cur, tc.prior, 1) // 1 = tamamlanmış pencere, ölçek yok
			// Yalnız prior'da olan satır İCAT EDİLMEZ: merge yerinde
			// çalışır, dilim boyu sabit.
			if len(tc.cur) != nCur {
				t.Fatalf("satır sayısı %d → %d — prior-only satır icat edildi", nCur, len(tc.cur))
			}
			for i, w := range tc.want {
				got := tc.cur[i]
				if got.PriorSpanCount != w.spans || got.PriorErrorCount != w.errs ||
					got.PriorAvgMs != w.avg || got.PriorP50Ms != w.p50 || got.PriorP99Ms != w.p99 {
					t.Errorf("satır %d: prior (spans=%d errs=%d avg=%v p50=%v p99=%v), beklenen %+v",
						i, got.PriorSpanCount, got.PriorErrorCount, got.PriorAvgMs, got.PriorP50Ms, got.PriorP99Ms, w)
				}
			}
		})
	}
}

// Merge CURRENT sayaçlara asla dokunmaz — yalnız Prior*.
func TestMergeDBPriorLeavesCurrentIntact(t *testing.T) {
	cur := []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 100, 5, 12, 8, 90)}
	cur[0].P95Ms = 40
	cur[0].Callers = []string{"svc-a"}
	prior := []chstore.DBInstance{dbRow("oracle", "db-a", "LEDGER", 80, 2, 10, 7, 70)}
	mergeDBPrior(cur, prior, 0.5)
	got := cur[0]
	if got.SpanCount != 100 || got.ErrorCount != 5 || got.AvgMs != 12 || got.P50Ms != 8 ||
		got.P95Ms != 40 || got.P99Ms != 90 || len(got.Callers) != 1 {
		t.Errorf("merge current sayaçları değiştirdi: %+v", got)
	}
}

// v0.10.1025 (inceleme R1) — canlı kenar: yalnız prior SAYAÇLARI kapsama
// oranıyla ölçeklenir; gecikmeler dokunulmaz; sıfır olmayan prior hata
// sayısı tabanla 1'de kalır (0 olsaydı "önce 0" yazardı), ölçülmüş sıfır
// sıfır kalır.
func TestMergeDBPriorScalesOnlyCounters(t *testing.T) {
	cur := []chstore.DBInstance{
		dbRow("oracle", "db-a", "LEDGER", 18, 3, 12, 8, 90),
		dbRow("oracle", "db-a", "CARDS", 18, 0, 12, 8, 90),
		dbRow("oracle", "db-a", "QUIET", 18, 1, 12, 8, 90),
	}
	prior := []chstore.DBInstance{
		dbRow("oracle", "db-a", "LEDGER", 20, 4, 10, 7, 70),
		dbRow("oracle", "db-a", "CARDS", 20, 0, 10, 7, 70),
		dbRow("oracle", "db-a", "QUIET", 20, 1, 10, 7, 70),
	}
	mergeDBPrior(cur, prior, 0.375)
	want := []dbPriorWant{
		{8, 2, 10, 7, 70}, // 20×0,375 = 7,5 → 8; 4×0,375 = 1,5 → 2
		{8, 0, 10, 7, 70}, // ölçülmüş sıfır hata sıfır kalır
		{8, 1, 10, 7, 70}, // 1×0,375 = 0,375 → TABAN 1
	}
	for i, w := range want {
		g := cur[i]
		if g.PriorSpanCount != w.spans || g.PriorErrorCount != w.errs ||
			g.PriorAvgMs != w.avg || g.PriorP50Ms != w.p50 || g.PriorP99Ms != w.p99 {
			t.Errorf("satır %d: prior (spans=%d errs=%d avg=%v p50=%v p99=%v), beklenen %+v",
				i, g.PriorSpanCount, g.PriorErrorCount, g.PriorAvgMs, g.PriorP50Ms, g.PriorP99Ms, w)
		}
	}
}

// Messaging ikizi: dört sayaç ölçeklenir (spans, errors, produce, consume),
// gecikmeler değil.
func TestMergeMessagingPriorScalesOnlyCounters(t *testing.T) {
	cur := []chstore.MessagingInstance{msgRow("kafka", "(default)", "orders", 75, 3, 40, 35, 12, 8, 90)}
	prior := []chstore.MessagingInstance{msgRow("kafka", "(default)", "orders", 100, 4, 50, 50, 10, 7, 70)}
	mergeMessagingPrior(cur, prior, 0.75)
	g := cur[0]
	if g.PriorSpanCount != 75 || g.PriorErrorCount != 3 || g.PriorProduceCount != 38 || g.PriorConsumeCount != 38 {
		t.Errorf("ölçekli sayaçlar: spans=%d errs=%d produce=%d consume=%d, beklenen 75 3 38 38",
			g.PriorSpanCount, g.PriorErrorCount, g.PriorProduceCount, g.PriorConsumeCount)
	}
	if g.PriorAvgMs != 10 || g.PriorP50Ms != 7 || g.PriorP99Ms != 70 {
		t.Errorf("gecikmeler ölçeklenmiş: %+v", g)
	}
}

func TestDBListPriorScale(t *testing.T) {
	m := func(mi, s int) time.Time { return time.Date(2026, 9, 30, 10, mi, s, 0, time.UTC) }
	// Canlı 15 dk: from 10:02:30, to = now = 10:17:30.
	if got := dbListPriorScale(m(2, 30), m(17, 30), m(17, 30), ""); got != 0.875 {
		t.Errorf("MV yolu canlı kenar ölçeği %v, beklenen 0.875 (17,5 / 20 dk)", got)
	}
	// Ham yol: pencere birebir [from, to], to = now → ölçek yok.
	if got := dbListPriorScale(m(2, 30), m(17, 30), m(17, 30), "uat"); got != 1 {
		t.Errorf("ham yol ölçeği %v, beklenen 1", got)
	}
	// Geçmiş pencere: iki yol da 1.
	if got := dbListPriorScale(m(0, 0), m(15, 0), m(40, 0), ""); got != 1 {
		t.Errorf("geçmiş pencere ölçeği %v, beklenen 1", got)
	}
}

func TestDBListPriorWindow(t *testing.T) {
	from := time.Date(2026, 9, 30, 10, 3, 0, 0, time.UTC)
	to := time.Date(2026, 9, 30, 11, 3, 0, 0, time.UTC)

	t.Run("MV yolu (env yok) = paylaşılan PriorWindow, hizalı ve ortak kovasız", func(t *testing.T) {
		pFrom, pTo := dbListPriorWindow(from, to, "")
		wFrom, wTo := chstore.PriorWindow(from, to)
		if !pFrom.Equal(wFrom) || !pTo.Equal(wTo) {
			t.Fatalf("[%v, %v) ≠ PriorWindow [%v, %v)", pFrom, pTo, wFrom, wTo)
		}
		// Current ilk kovası 10:00; prior onun önünde bitmeli.
		if !pTo.Equal(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("prior sonu %v — current'ın 10:00 kovası prior'a giriyor", pTo)
		}
	})

	t.Run("ham env yolu = birebir süre kaydırması (kova ızgarası yok)", func(t *testing.T) {
		pFrom, pTo := dbListPriorWindow(from, to, "uat")
		if !pTo.Equal(from) || pTo.Sub(pFrom) != to.Sub(from) {
			t.Errorf("env prior [%v, %v) — beklenen [from − %v, from)", pFrom, pTo, to.Sub(from))
		}
	})
}

// Kaynak pini: iki liste ucu da prior penceresini paylaşılan türetimden
// alıyor; eski `from.Add(-dur), from` çifti geri gelmesin. Yorumlar
// süzülür (eski formül şerhte TARİHÇE olarak anılıyor).
func TestListHandlersUseSharedPriorWindow(t *testing.T) {
	b, err := os.ReadFile("api_databases.go")
	if err != nil {
		t.Fatal(err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		code.WriteString(line + "\n")
	}
	src := code.String()
	for _, want := range []string{
		"pFrom, pTo := dbListPriorWindow(from, to, env)",
		"s.store.GetDatabasesRollup(ctx, pFrom, pTo, env)",
		"pFrom, pTo := chstore.PriorWindow(from, to)",
		"s.store.GetMessagingRollup(ctx, pFrom, pTo)",
		// v0.10.1025 inceleme R1/R3/R4 — kapılar ve ölçek.
		"s.store.DBListPriorReadable(ctx, pFrom, pTo, now, env != \"\", ov.SpanHorizonDays)",
		"mergeDBPrior(ov.Rows, priorRows, dbListPriorScale(from, to, now, env))",
		"s.store.MessagingPriorReadable(ctx, pFrom, pTo, now)",
		"mergeMessagingPrior(ov.Rows, priorRows, chstore.PriorCoverage(from, to, now))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("api_databases.go %q içermiyor", want)
		}
	}
	if strings.Contains(src, "from.Add(-dur), from") {
		t.Error("eski hizasız prior penceresi (`from.Add(-dur), from`) geri gelmiş — ortak kova")
	}
	// Kapı okumadan ÖNCE: eksik bir prior okunup çizilmesin.
	for _, pair := range [][2]string{
		{"s.store.DBListPriorReadable(", "s.store.GetDatabasesRollup("},
		{"s.store.MessagingPriorReadable(", "s.store.GetMessagingRollup("},
	} {
		if i, j := strings.Index(src, pair[0]), strings.Index(src, pair[1]); i < 0 || j < 0 || i > j {
			t.Errorf("%s, %s'dan ÖNCE çağrılmalı", pair[0], pair[1])
		}
	}
}
