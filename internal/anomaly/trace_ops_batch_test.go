package anomaly

// trace_ops_batch_test.go — v0.10.1039: batch servislerde trace_op
// error_spike'ı SAYIM VE PAY ister (saf susturma).
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// Ölçülen sınıf: 20× yük + SABİT hata yüzdesi → hata SAYISI da 20× → sayım
// kuralı (cur ≥ 3 × pencere-normalize taban) "error_spike 20×" yazar, terfi
// kapısı (PeakRatio ≥ 15) onu Problem'e taşırdı.
//
// İnceleme kararı: pay kuralı sayım kuralının YERİNE değil YANINA (VE). İlk
// taslakta yerine geçiyordu ve yeni olay da ÜRETİYORDU (cari hacim 24 sa
// ortalamasının altındayken pay artar, sayım artmaz). Pinler: "yeni kabul"
// vakası, ve özellik tablosu (batch-yeni ⊆ batch-eski; batch olmayan çiftler
// ilk 50'de hiç yer kaybetmez).
//
// MUTASYON KANITLARI (çalıştırıldı, geri alındı — rapor v0.10.1039):
//   (a) Go'daki batch pay kapısı silinince "batch, 20× yük, sabit pay" vakası
//       error_spike'a döner → TestClassifyTraceOpsBatch ve
//       TestTraceOpHavingAgreesWithGo kızarır; SQL'de pay koşulu silinince
//       TestTraceOpQueryBatchBranchInHaving + TestTraceOpHavingAgreesWithGo.
//   (b) pay kuralı sayımın YERİNE geçirilince "yeni kabul" vakası ve özellik
//       tablosu kızarır; kapı new_error'a genişletilince iki "batch
//       new_error" vakası kızarır.

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// traceOpWindowRatio — üretimdeki oran: 5 dk cari / 24 sa taban.
var traceOpWindowRatio = float64(5*time.Minute) / float64(24*time.Hour)

func batchSens(p ...string) chstore.AnomalySensitivityConfig {
	if p == nil {
		p = []string{}
	}
	return chstore.AnomalySensitivityConfig{BatchServicePatterns: &p}
}

// Normal 5 dk: 1.000 çağrı, 30 hata (%3). 24 saatlik taban = 288 pencere.
const (
	normCalls5m = 1_000
	normErrs5m  = 30
	baseCalls   = normCalls5m * 288 // 288.000
	baseErrs    = normErrs5m * 288  // 8.640
)

func TestClassifyTraceOpsBatch(t *testing.T) {
	def := chstore.AnomalySensitivityConfig{} // alan yok → ["-batch"]
	off := batchSens()                        // kural kapalı
	wr := traceOpWindowRatio

	type tc struct {
		name      string
		sens      chstore.AnomalySensitivityConfig
		in        traceOpBucket
		wantKind  string // "" = olay YOK
		wantRatio float64
	}
	row := func(svc string, curErrs, curCalls, bErrs, bCalls uint64) traceOpBucket {
		return traceOpBucket{Service: svc, Operation: "ProcessChunk",
			CurErrs: curErrs, CurCalls: curCalls, BaseErrs: bErrs, BaseCalls: bCalls}
	}
	// countRatio — bugünkü sayım kuralının raporladığı oran (batch'te de aynı).
	countRatio := func(cur, bErrs uint64) float64 { return float64(cur) / float64(uint64(float64(bErrs)*wr)) }
	tests := []tc{
		// ── ASIL VAKA: 20× yük, sabit %3 ──
		{"batch, 20× çağrı + 20× hata (pay sabit) → olay YOK", def,
			row("orders-batch", 600, 20_000, baseErrs, baseCalls), "", 0},
		{"aynı sayılar batch OLMAYAN serviste → bugünkü gibi error_spike", def,
			row("payments-api", 600, 20_000, baseErrs, baseCalls), "error_spike", countRatio(600, baseErrs)},
		{"kural kapalı → batch adlı servis de sıradan (error_spike)", off,
			row("orders-batch", 600, 20_000, baseErrs, baseCalls), "error_spike", countRatio(600, baseErrs)},

		// ── SAF SUSTURMA: pay artsa da sayım kuralı geçmiyorsa olay YOK ──
		// Taban %0,3 pay, pencere başına ~100 bin çağrı; cari 300 / 20.000 =
		// %1,5 → pay 5× ama sayım 300 < 3 × ~300. İlk taslak bunu AÇIYORDU.
		{"yeni kabul: batch, pay 5× ama sayım kuralı geçmiyor → olay YOK", def,
			row("orders-batch", 300, 20_000, 86_400, 28_800_000), "", 0},
		{"yeni kabul: batch olmayan aynı sayılar → bugünkü gibi YOK", def,
			row("payments-api", 300, 20_000, 86_400, 28_800_000), "", 0},

		// ── sayım VE pay → SİNYAL, oran SAYIM oranı (UI sayıları yanında yazıyor) ──
		{"batch, pay 3.5× + sayım 70× → error_spike, sayım oranıyla", def,
			row("orders-batch", 2_100, 20_000, baseErrs, baseCalls), "error_spike", countRatio(2_100, baseErrs)},
		{"batch, pay 3.5× yük OLMADAN (sayım 3.5×) → error_spike", def,
			row("orders-batch", 105, 1_000, baseErrs, baseCalls), "error_spike", countRatio(105, baseErrs)},
		{"batch, pay 2.9× (sayım 58×) → olay YOK", def,
			row("orders-batch", 1_740, 20_000, baseErrs, baseCalls), "", 0},
		// Tam sınır, ikili kesirlerle (yuvarlama yok): taban 1/4, cari 3/4.
		{"batch, pay TAM 3× → error_spike (>=)", def,
			row("orders-batch", 750, 1_000, 1_000, 4_000), "error_spike", countRatio(750, 1_000)},
		// BİLİNEN BEDEL (DECISIONS v0.10.1039): taban payı ≥ %33 → pay üçe
		// katlanamaz → error_spike hiç açılmaz; tam çöküşü error_rate ve
		// new_error yakalar.
		{"bilinen bedel: batch, taban payı %40, %100 hata + 20× yük → YOK", def,
			row("orders-batch", 20_000, 20_000, 115_200, 288_000), "", 0},
		{"bilinen bedel: aynı sayılar batch olmayan serviste → error_spike", def,
			row("payments-api", 20_000, 20_000, 115_200, 288_000), "error_spike", countRatio(20_000, 115_200)},

		// ── new_error batch'te de AYNEN (hiç hata vermeyen iş hata veriyor) ──
		{"batch new_error (tabanda hata yok) → olay", def,
			row("orders-batch", 600, 20_000, 0, baseCalls), "new_error", 600},
		{"batch new_error, taban çağrı da yok → olay", def,
			row("orders-batch", 40, 400, 0, 0), "new_error", 40},
		// Savunma kolu: taban hatası var ama taban çağrısı 0 (hata ⊆ çağrı,
		// pratikte olmaz) → pay ölçülemez, sayım kararı kalır.
		{"batch, taban çağrı 0 → sayım kuralı", def,
			row("orders-batch", 600, 20_000, baseErrs, 0), "error_spike", countRatio(600, baseErrs)},

		// ── tabanlar batch'te de geçerli ──
		{"batch, pay 10× ama hata < 10 → YOK (sayım tabanı)", def,
			row("orders-batch", 9, 30, baseErrs, baseCalls), "", 0},
		{"batch, pay < %1 → YOK (pay tabanı)", def,
			row("orders-batch", 150, 20_000, 1, baseCalls), "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTraceOps([]traceOpBucket{tt.in}, wr, tt.sens.IsBatchService)
			if tt.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("olay beklenmiyordu: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tt.wantKind {
				t.Fatalf("kind=%s bekleniyordu, got=%+v", tt.wantKind, got)
			}
			if d := got[0].Ratio - tt.wantRatio; d > 1e-9 || d < -1e-9 {
				t.Fatalf("ratio=%v, beklenen %v", got[0].Ratio, tt.wantRatio)
			}
		})
	}

	// nil yüklem = kural kapalı = v0.10.1039 öncesi davranış.
	surge := []traceOpBucket{row("orders-batch", 600, 20_000, baseErrs, baseCalls)}
	if got := classifyTraceOps(surge, wr, nil); len(got) != 1 || got[0].Kind != "error_spike" {
		t.Fatalf("nil yüklemle bugünkü sayım kuralı bekleniyordu: %+v", got)
	}
}

// TestTraceOpBatchIsPureSuppression — ÖZELLİK TABLOSU. Bir ızgara üzerindeki
// her (cari hata, cari çağrı, taban hata, taban çağrı) çifti için:
//  1. batch-yeni ⊆ batch-eski: kural açıkken kalan her olay, kural kapalıyken
//     de BİREBİR aynı alanlarla (kind, ratio, BaselineErrors) vardı;
//  2. batch olmayan servis: kural açık/kapalı sonuç birebir aynı;
//  3. hepsi tek çağrıda (ilk 50 tavanı ısırır): kural kapalıyken ilk 50'de
//     olan her batch OLMAYAN çift, kural açıkken de ilk 50'de.
func TestTraceOpBatchIsPureSuppression(t *testing.T) {
	wr := traceOpWindowRatio
	on := chstore.AnomalySensitivityConfig{}.IsBatchService // varsayılan ["-batch"]
	var all []traceOpBucket
	i := 0
	for _, ce := range []uint64{10, 30, 105, 300, 600, 2_100, 5_000, 20_000} {
		for _, cc := range []uint64{300, 1_000, 20_000, 200_000} {
			for _, be := range []uint64{0, 5, 288, 8_640, 86_400, 115_200} {
				for _, bc := range []uint64{0, 2_880, 288_000, 28_800_000} {
					if ce > cc || be > bc && bc > 0 {
						continue // hata ⊆ çağrı (bc 0 savunma kolu ayrıca kalır)
					}
					for _, svc := range []string{"orders-batch", "payments-api"} {
						i++
						all = append(all, traceOpBucket{Service: svc, Operation: fmt.Sprintf("op-%04d", i),
							CurErrs: ce, CurCalls: cc, BaseErrs: be, BaseCalls: bc})
					}
				}
			}
		}
	}
	nonEmpty := 0
	for _, r := range all {
		old := classifyTraceOps([]traceOpBucket{r}, wr, nil)
		neu := classifyTraceOps([]traceOpBucket{r}, wr, on)
		if len(old) > 0 {
			nonEmpty++
		}
		if len(neu) > len(old) {
			t.Fatalf("kural YENİ olay üretti: %+v → %+v", r, neu)
		}
		if len(neu) == 1 && !reflect.DeepEqual(neu[0], old[0]) {
			t.Fatalf("kalan olay alanları değişti:\neski %+v\nyeni %+v", old[0], neu[0])
		}
		if !on(r.Service) && !reflect.DeepEqual(old, neu) {
			t.Fatalf("batch olmayan servis etkilendi: %+v", r)
		}
	}
	if nonEmpty < 100 {
		t.Fatalf("ızgara anlamsız: yalnız %d olay", nonEmpty)
	}

	// İlk 50 yarışı yalnız spike'lar arasında anlamlı: new_error'lar sıralamada
	// önde ve kuraldan etkilenmiyor, ızgaranın tamamıyla ilk 50'yi onlar doldurur.
	var spikes []traceOpBucket
	for _, r := range all {
		if r.BaseErrs > 0 {
			spikes = append(spikes, r)
		}
	}
	oldTop := classifyTraceOps(spikes, wr, nil)
	newTop := classifyTraceOps(spikes, wr, on)
	if len(oldTop) != 50 {
		t.Fatalf("ilk 50 tavanı ısırmıyor (%d) — yer kaybı testi anlamsız", len(oldTop))
	}
	inNew := map[string]bool{}
	for _, a := range newTop {
		inNew[a.Service+"/"+a.Operation] = true
	}
	displaced := 0
	for _, a := range oldTop {
		key := a.Service + "/" + a.Operation
		if !on(a.Service) && !inNew[key] {
			t.Fatalf("batch olmayan çift ilk 50'den düştü: %+v", a)
		}
		if on(a.Service) && !inNew[key] {
			displaced++
		}
	}
	if displaced == 0 {
		t.Fatal("eski ilk 50'de susturulan batch çifti yok — yer kaybı testi anlamsız")
	}
}

// goldenLegacyTraceOpSQL — v0.10.1039 ÖNCESİ trace_ops.go'nun sorgu metni,
// BİREBİR kopya. Kalıp listesi boşken kurucu bunu üretmeli.
const goldenLegacyTraceOpSQL = `
		SELECT service_name, name,
		       sumIf(errs,  is_cur = 1) AS cur_errs,
		       sumIf(errs,  is_cur = 0) AS base_errs,
		       sumIf(calls, is_cur = 1) AS cur_calls
		FROM (
		  SELECT service_name, name,
		         time_bucket >= ? AS is_cur,
		         countIfMerge(error_count_state) AS errs,
		         countMerge(span_count_state)    AS calls
		  FROM operation_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY service_name, name, is_cur
		)
		GROUP BY service_name, name
		-- v0.9.327 — the coarse filter now carries the SAME floors the Go
		-- classifier applies. It used to be deliberately looser, which meant
		-- the LIMIT 200 filled with pairs Go would then reject: the ranking
		-- was spent on rows that could never qualify. Same lesson as the
		-- inbox status narrow (v0.9.322) — the LIMIT has to bite on rows
		-- that can actually survive.
		HAVING cur_errs >= ? AND cur_calls > 0
		   AND cur_errs >= ? * cur_calls
		   AND ((base_errs = 0) OR (cur_errs >= ? * base_errs * ?))
		ORDER BY cur_errs DESC
		LIMIT 200
		SETTINGS max_execution_time = 25`

func traceOpTimes() (time.Time, time.Time, time.Time) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	cur := now.Add(-5 * time.Minute)
	return cur, cur.Add(-24 * time.Hour), now
}

// TestTraceOpQueryLegacyIdentity — kural kapalıyken (boş liste) SQL metni
// ve argümanlar bugünküyle AYNI: batch olmayan dünyada hiçbir şey değişmez.
func TestTraceOpQueryLegacyIdentity(t *testing.T) {
	cur, base, now := traceOpTimes()
	cond, cargs := batchSens().BatchServiceSQL("service_name")
	if cond != "" || cargs != nil {
		t.Fatalf("boş liste koşul üretti: %q %v", cond, cargs)
	}
	q, args := traceOpQuery(cur, base, now, traceOpWindowRatio, cond, cargs)
	if q != goldenLegacyTraceOpSQL {
		t.Fatalf("boş listede SQL metni değişti:\n%s", q)
	}
	want := []any{cur, base, now, traceOpMinErrs, traceOpMinErrShare, traceOpMinRatio, traceOpWindowRatio}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar\n%v\nbeklenen\n%v", args, want)
	}
}

// TestTraceOpQueryBatchBranchInHaving — batch kolu SQL'in HAVING'inde (Go'da
// değil): sabit-paylı batch çiftleri LIMIT 200'ü dolduramaz.
func TestTraceOpQueryBatchBranchInHaving(t *testing.T) {
	cur, base, now := traceOpTimes()
	sens := chstore.AnomalySensitivityConfig{} // varsayılan ["-batch"]
	cond, cargs := sens.BatchServiceSQL("service_name")
	q, args := traceOpQuery(cur, base, now, traceOpWindowRatio, cond, cargs)

	iHaving := strings.Index(q, "HAVING ")
	iOrder := strings.Index(q, "ORDER BY cur_errs DESC")
	iLimit := strings.Index(q, "\n\t\tLIMIT 200") // SQL yorumundaki "the LIMIT 200" değil
	if iHaving < 0 || iOrder < iHaving || iLimit < iOrder {
		t.Fatalf("HAVING → ORDER BY → LIMIT sırası bozuk:\n%s", q)
	}
	having := q[iHaving:iOrder]
	if strings.Count(having, cond) != 1 {
		t.Fatalf("batch koşulu HAVING'de tam bir kez bekleniyordu:\n%s", having)
	}
	// SAYIM VE (batch değil YA DA pay): sayım koşulu her serviste aynen, pay
	// yalnız ek eleme.
	for _, frag := range []string{
		"(base_errs = 0)",
		"OR (cur_errs >= ? * base_errs * ?\n",
		"AND (NOT (" + cond + " AND base_calls > 0)",
		"OR cur_errs / cur_calls >= ? * (base_errs / base_calls))))",
	} {
		if !strings.Contains(having, frag) {
			t.Fatalf("HAVING %q taşımıyor:\n%s", frag, having)
		}
	}
	// Batch koşulu HAVING DIŞINDA yok (ör. Go'ya süzülecek ayrı bir kolon).
	if strings.Count(q, cond) != 1 {
		t.Fatal("batch koşulu HAVING dışında da geçiyor")
	}
	// Taban çağrısı aynı iç alt sorgudan; ek tarama yok.
	if !strings.Contains(q, "sumIf(calls, is_cur = 0) AS base_calls") || strings.Count(q, "FROM operation_summary_5m") != 1 {
		t.Fatal("base_calls aynı geçişten okunmuyor")
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d, argüman %d", strings.Count(q, "?"), len(args))
	}
	want := []any{cur, base, now, traceOpMinErrs, traceOpMinErrShare,
		traceOpMinRatio, traceOpWindowRatio, "-batch", traceOpMinRatio}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar\n%v\nbeklenen\n%v", args, want)
	}
}

// TestTraceOpsReadsPublishedSettings — kalıplar tik başına CH'den değil,
// metrik dedektörünün okuduğu atomic ayardan gelir.
func TestTraceOpsReadsPublishedSettings(t *testing.T) {
	b, err := os.ReadFile("trace_ops.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func DetectTraceOpAnomalies(")
	if i < 0 {
		t.Fatal("DetectTraceOpAnomalies bulunamadı")
	}
	body := src[i:]
	// ForDetectors: atomic ayar; doğrulanmamışsa batch kuralı devre dışı.
	if !strings.Contains(body, "store.AnomalySensitivityForDetectors()") {
		t.Fatal("trace_op atomic ayarı (ForDetectors) okumuyor")
	}
	if strings.Contains(body, "GetAnomalySensitivity(") || strings.Contains(body, "GetSetting(") {
		t.Fatal("trace_op tik başına ayarı CH'den okuyor")
	}
	if !strings.Contains(body, "classifyTraceOps(buckets, windowRatio, isBatch)") {
		t.Fatal("Go kemeri batch yüklemini almıyor")
	}
}

// TestTraceOpHavingAgreesWithGo — SQL HAVING ile Go sınıflandırıcısı AYNI
// fikstür tablosunda AYNI çiftleri geçirir (`clickhouse local`; ikili yoksa
// atlanır). Fikstür, sayım kolunun pencere-normalize tamsayı kesme farkından
// (v0.10.1039 öncesi, batch'ten bağımsız) uzak tutuldu.
func TestTraceOpHavingAgreesWithGo(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — Go↔SQL HAVING karşılaştırması atlandı")
	}
	sens := batchSens("-batch", "etl-")
	rows := []traceOpBucket{
		{Service: "orders-batch", Operation: "flat", CurErrs: 600, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "orders-batch", Operation: "share35", CurErrs: 2_100, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "orders-batch", Operation: "share29", CurErrs: 1_740, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "orders-batch", Operation: "exact3", CurErrs: 750, CurCalls: 1_000, BaseErrs: 1_000, BaseCalls: 4_000},
		{Service: "orders-batch", Operation: "new", CurErrs: 600, CurCalls: 20_000, BaseErrs: 0, BaseCalls: baseCalls},
		{Service: "orders-batch", Operation: "nobasecalls", CurErrs: 600, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: 0},
		{Service: "ETL-loader", Operation: "flat", CurErrs: 600, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "payments-api", Operation: "flat", CurErrs: 600, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "payments-api", Operation: "calm", CurErrs: 40, CurCalls: 1_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "payments-api", Operation: "new", CurErrs: 40, CurCalls: 1_000, BaseErrs: 0, BaseCalls: baseCalls},
		{Service: "orders-batch", Operation: "tiny", CurErrs: 9, CurCalls: 30, BaseErrs: baseErrs, BaseCalls: baseCalls},
		// İlk taslağın YENİ kabul ettiği çift (pay 5×, sayım ~1×).
		{Service: "orders-batch", Operation: "admitted", CurErrs: 300, CurCalls: 20_000, BaseErrs: 86_400, BaseCalls: 28_800_000},
		{Service: "payments-api", Operation: "admitted", CurErrs: 300, CurCalls: 20_000, BaseErrs: 86_400, BaseCalls: 28_800_000},
		// Taban payı %40: pay üçe katlanamaz (bilinen bedel).
		{Service: "orders-batch", Operation: "highbase", CurErrs: 20_000, CurCalls: 20_000, BaseErrs: 115_200, BaseCalls: 288_000},
	}
	cur, base, now := traceOpTimes()
	cond, cargs := sens.BatchServiceSQL("service_name")
	q, args := traceOpQuery(cur, base, now, traceOpWindowRatio, cond, cargs)
	having := q[strings.Index(q, "HAVING ")+len("HAVING ") : strings.Index(q, "ORDER BY cur_errs DESC")]
	pred := inlineTraceOpArgs(t, having, args[3:]) // ilk üçü alt sorgunun zaman sınırları

	vals := make([]string, len(rows))
	for i, r := range rows {
		vals[i] = fmt.Sprintf("('%s', '%s', %d, %d, %d, %d)", r.Service, r.Operation, r.CurErrs, r.BaseErrs, r.CurCalls, r.BaseCalls)
	}
	sql := "SELECT service_name, name FROM values('service_name String, name String, cur_errs UInt64, base_errs UInt64, cur_calls UInt64, base_calls UInt64', " +
		strings.Join(vals, ", ") + ") WHERE " + pred + " ORDER BY service_name, name FORMAT TSVRaw"
	out, err := exec.Command(bin, "local", "--query", sql).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s\n%s", err, out, sql)
	}
	sqlSet := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" {
			sqlSet[strings.Replace(ln, "\t", "/", 1)] = true
		}
	}
	goSet := map[string]bool{}
	for _, a := range classifyTraceOps(rows, traceOpWindowRatio, sens.IsBatchService) {
		goSet[a.Service+"/"+a.Operation] = true
	}
	if !reflect.DeepEqual(sqlSet, goSet) {
		t.Fatalf("SQL ve Go farklı çiftleri geçiriyor\nSQL: %v\nGo:  %v", sqlSet, goSet)
	}
	// Fikstürün kendisi anlamlı mı: sabit-paylı batch satırları elendi,
	// batch olmayan aynı satır geçti.
	for k, want := range map[string]bool{
		"orders-batch/flat": false, "ETL-loader/flat": false, "payments-api/flat": true,
		"orders-batch/share35": true, "orders-batch/new": true, "orders-batch/exact3": true,
		"orders-batch/share29": false, "orders-batch/nobasecalls": true,
		"orders-batch/admitted": false, "payments-api/admitted": false, "orders-batch/highbase": false,
	} {
		if goSet[k] != want {
			t.Fatalf("%s: geçti=%v, beklenen %v", k, goSet[k], want)
		}
	}
}

func inlineTraceOpArgs(t *testing.T, q string, args []any) string {
	t.Helper()
	var b strings.Builder
	i := 0
	for _, r := range q {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		if i >= len(args) {
			t.Fatalf("argüman eksik")
		}
		if s, ok := args[i].(string); ok {
			b.WriteString("'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'")
		} else {
			b.WriteString(fmt.Sprint(args[i])) // sürücünün sayısal bind biçimi
		}
		i++
	}
	if i != len(args) {
		t.Fatalf("fazla argüman %d/%d", i, len(args))
	}
	return b.String()
}
