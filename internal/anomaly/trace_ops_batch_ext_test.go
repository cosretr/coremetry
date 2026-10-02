package anomaly

// trace_ops_batch_ext_test.go — v0.10.1043: seyrek koşan batch işinde
// "yeni hata" uzun tabana bakar.
//
// Operatör (prod): "Batch'te 'yeni hata' gürültüsü: son 24 saatte hiç
// koşmamış bir iş her koşuda 'yeni hata' diye açılıyor."
//
// Ölçülen sınıf: haftalık bir iş (orders-batch) her koşuda %2 hata verir.
// 24 sa taban penceresinde iş HİÇ koşmadığı için base_errs = 0 → new_error,
// ratio = cari hata SAYISI (300 hata = "×300") → Problems'ta P1, terfi
// kapısında critical anomaly-auto. Her koşu, her hafta.
//
// Kural: batch + base_errs = 0 → 8 günlük uzun tabana bak. Uzun taban hata
// görmüşse taban payı = ext_errs / (ext_calls + base_calls); cari pay − taban
// payı ≥ 25 puan → bugünkü new_error (mutlak artış kaçışı); cari PAY ≥ 3 ×
// taban payı → error_spike (pay oranıyla); değilse olay yok. Uzun tabanda da
// hata yoksa new_error AYNEN. Batch olmayan servis ve kural kapalı (boş
// liste) → birebir bugünkü.
//
// MUTASYON KANITLARI (çalıştırıldı, geri alındı — rapor v0.10.1043):
//   (a) batch kısıtı, üç katmanın her biri ayrı ayrı:
//       - sınıflandırıcıdaki `isBatch != nil && isBatch(r.Service)` silinince
//         iki "batch olmayan" + "kural kapalı" vakası ve özellik tablosu kızarır;
//       - aday seçimindeki isBatch silinince TestTraceOpBatchExtCandidates,
//         davranış testinin "aday yok", "yalnız aday çift" ve "okuma hatası"
//         vakaları ve clickhouse-local testi kızarır;
//       - uzun okumanın SQL'inden batch koşulu (ve argümanları) silinince
//         TestTraceOpBatchExtQueryShape ve clickhouse-local testi kızarır.
//   (b) okuma hatası adayları susturunca (hata dalında adaylara uzun taban
//       yazılır) "okuma hatası" ve "rows.Err" vakaları kızarır; rows.Err'de
//       yarım harita uygulanınca "rows.Err" vakası kızarır.
//   (c) üst sınır `< ?` → `<= ?` (taban başındaki kova iki kez sayılır) →
//       TestTraceOpBatchExtQueryShape ve clickhouse-local testi kızarır.
//   (d) mutlak artış kaçışı silinince dört kaçış vakası (%40 → %100 dahil),
//       özellik tablosu ve clickhouse-local testi kızarır.
//   (e) paydadan base_calls düşürülünce "dün temiz koşu" vakası, özellik
//       tablosu ve clickhouse-local testi kızarır.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Haftalık iş: koşu başına 1.000 çağrı, %2 hata; uzun tabanda 7 koşu.
const (
	extCalls7 = 7_000
	extErrs7  = 140 // %2
)

func extRow(svc string, curErrs, curCalls, bErrs, bCalls, eErrs, eCalls uint64) traceOpBucket {
	return traceOpBucket{Service: svc, Operation: "ProcessChunk",
		CurErrs: curErrs, CurCalls: curCalls, BaseErrs: bErrs, BaseCalls: bCalls,
		ExtErrs: eErrs, ExtCalls: eCalls}
}

func nearlyEqual(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func TestClassifyTraceOpsBatchExtBaseline(t *testing.T) {
	def := chstore.AnomalySensitivityConfig{} // alan yok → ["-batch"]
	wr := traceOpWindowRatio

	type tc struct {
		name      string
		isBatch   func(string) bool
		in        traceOpBucket
		wantKind  string // "" = olay YOK
		wantRatio float64
		wantBase  uint64
	}
	tests := []tc{
		// ── ASIL VAKA: haftalık iş, 24 sa boş, her zamanki %2 ──
		{"batch, 24 sa boş, uzun taban %2, cari %2 → olay YOK", def.IsBatchService,
			extRow("orders-batch", 20, 1_000, 0, 0, extErrs7, extCalls7), "", 0, 0},
		{"operatörün '×300'ü: 300 hata / 15.000 çağrı (%2), uzun taban %2 → olay YOK", def.IsBatchService,
			extRow("orders-batch", 300, 15_000, 0, 0, extErrs7, extCalls7), "", 0, 0},
		{"batch, uzun taban %2, cari %7 → error_spike, PAY oranı 3.5, prev = %2 × 1.000", def.IsBatchService,
			extRow("orders-batch", 70, 1_000, 0, 0, extErrs7, extCalls7), "error_spike", 3.5, 20},
		{"batch, uzun taban %2, cari %5.9 (2.95×) → olay YOK", def.IsBatchService,
			extRow("orders-batch", 59, 1_000, 0, 0, extErrs7, extCalls7), "", 0, 0},
		{"batch, uzun taban %2, cari %4 (2×) → olay YOK", def.IsBatchService,
			extRow("orders-batch", 40, 1_000, 0, 0, extErrs7, extCalls7), "", 0, 0},
		// Tam sınır, ikili kesirlerle (yuvarlama yok): taban 1/16, cari 3/16
		// (mutlak artış 12.5 puan — kaçışın altında).
		{"batch, pay TAM 3× → error_spike (>=)", def.IsBatchService,
			extRow("orders-batch", 1_875, 10_000, 0, 0, 1_000, 16_000), "error_spike", 3, 625},
		{"batch, payın hemen altı (1874/10000 vs 3/16) → olay YOK", def.IsBatchService,
			extRow("orders-batch", 1_874, 10_000, 0, 0, 1_000, 16_000), "", 0, 0},

		// ── MUTLAK ARTIŞ KAÇIŞI (≥ 25 puan → bugünkü new_error) ──
		// Haftalık işte uzun taban TEK koşu; kötü koşu tabanı zehirlemesin.
		{"kaçış: geçen hafta %40, bu hafta %100 (2.5×) → new_error", def.IsBatchService,
			extRow("orders-batch", 1_000, 1_000, 0, 0, 400, 1_000), "new_error", 1_000, 0},
		{"kaçış: %60 → %100 → new_error", def.IsBatchService,
			extRow("orders-batch", 1_000, 1_000, 0, 0, 600, 1_000), "new_error", 1_000, 0},
		{"kronik %60 → %60 → olay YOK", def.IsBatchService,
			extRow("orders-batch", 600, 1_000, 0, 0, 600, 1_000), "", 0, 0},
		{"bilinen boşluk: %40 → %60 (+20 puan, 1.5×) → olay YOK", def.IsBatchService,
			extRow("orders-batch", 600, 1_000, 0, 0, 400, 1_000), "", 0, 0},
		// Tam sınır, ikili kesirlerle: taban 1/4, cari 1/2 → fark TAM 0.25.
		{"kaçış sınırı: TAM +25 puan → new_error (>=)", def.IsBatchService,
			extRow("orders-batch", 500, 1_000, 0, 0, 1_000, 4_000), "new_error", 500, 0},
		{"kaçışın hemen altı: +24.9 puan (2×) → olay YOK", def.IsBatchService,
			extRow("orders-batch", 499, 1_000, 0, 0, 1_000, 4_000), "", 0, 0},
		// Kaçış 3×'ten ÖNCE: büyük mutlak sıçrama bugünkü gibi yüksek sesle.
		{"kaçış önceliği: %2 → %30 (15×, +28 puan) → new_error", def.IsBatchService,
			extRow("orders-batch", 300, 1_000, 0, 0, extErrs7, extCalls7), "new_error", 300, 0},

		// ── PAYDA 24 sa tabanın TEMİZ çağrısını taşır ──
		// Dün 1M temiz çağrı, altı gün önce 10/1.000 hata, bugün %2.5:
		// taban ≈ %0.001 → ateşler (payda yalnız uzun tabansa %1 → susardı).
		{"dün temiz koşu (1M çağrı) → taban payı ≈ %0.001, %2.5 → error_spike", def.IsBatchService,
			extRow("orders-batch", 25, 1_000, 0, 1_000_000, 10, 1_000), "error_spike",
			0.025 / (10.0 / 1_001_000.0), 0},
		{"aynı iş 24 sa'de koşmadı (base_calls 0) → taban %1, %2.5 → olay YOK", def.IsBatchService,
			extRow("orders-batch", 25, 1_000, 0, 0, 10, 1_000), "", 0, 0},
		{"24 sa'de temiz koşu (1.000 çağrı), uzun taban %2, cari %2 → olay YOK", def.IsBatchService,
			extRow("orders-batch", 20, 1_000, 0, 1_000, extErrs7, extCalls7), "", 0, 0},

		// ── new_error AYNEN: gerçek ilk hata susmaz ──
		{"batch, uzun taban BOŞ (okunmadı / okunamadı / tavan dışı / veri yok) → new_error", def.IsBatchService,
			extRow("orders-batch", 70, 1_000, 0, 0, 0, 0), "new_error", 70, 0},
		{"batch, uzun tabanda çağrı var hata YOK → new_error", def.IsBatchService,
			extRow("orders-batch", 70, 1_000, 0, 0, 0, extCalls7), "new_error", 70, 0},
		// Savunma: hata ⊆ çağrı, pratikte olmaz → pay ölçülemez, bugünkü karar.
		{"batch, uzun taban hata var çağrı 0 (savunma) → new_error", def.IsBatchService,
			extRow("orders-batch", 70, 1_000, 0, 0, extErrs7, 0), "new_error", 70, 0},

		// ── batch OLMAYAN / kural kapalı: aynı uzun taban alanlarıyla bile bugünkü ──
		{"batch olmayan, uzun taban %2, cari %2 → new_error (bugünkü)", def.IsBatchService,
			extRow("payments-api", 20, 1_000, 0, 0, extErrs7, extCalls7), "new_error", 20, 0},
		{"batch olmayan, uzun taban %2, cari %7 → new_error (bugünkü)", def.IsBatchService,
			extRow("payments-api", 70, 1_000, 0, 0, extErrs7, extCalls7), "new_error", 70, 0},
		{"kural kapalı (nil yüklem) → batch adlı servis de new_error", nil,
			extRow("orders-batch", 20, 1_000, 0, 0, extErrs7, extCalls7), "new_error", 20, 0},

		// ── tabanlar uzun taban dalında da geçerli ──
		{"batch, 9 hata (sayım tabanı) → YOK", def.IsBatchService,
			extRow("orders-batch", 9, 100, 0, 0, 0, 0), "", 0, 0},
		{"batch, pay < %1 (pay tabanı) → YOK", def.IsBatchService,
			extRow("orders-batch", 15, 2_000, 0, 0, 1, extCalls7), "", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyTraceOps([]traceOpBucket{tt.in}, wr, tt.isBatch)
			if tt.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("olay beklenmiyordu: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tt.wantKind {
				t.Fatalf("kind=%s bekleniyordu, got=%+v", tt.wantKind, got)
			}
			if !nearlyEqual(got[0].Ratio, tt.wantRatio) {
				t.Fatalf("ratio=%v, beklenen %v", got[0].Ratio, tt.wantRatio)
			}
			if got[0].BaselineErrors != tt.wantBase {
				t.Fatalf("BaselineErrors=%d, beklenen %d", got[0].BaselineErrors, tt.wantBase)
			}
			if got[0].CurrentErrors != tt.in.CurErrs || got[0].CurrentCalls != tt.in.CurCalls {
				t.Fatalf("cari sayılar değişti: %+v", got[0])
			}
		})
	}

	// base_errs > 0 → uzun taban alanları hiçbir dalı etkilemez (sayım + pay
	// kuralı v0.10.1039'daki gibi).
	for _, r := range []traceOpBucket{
		extRow("orders-batch", 2_100, 20_000, baseErrs, baseCalls, extErrs7, extCalls7),
		extRow("orders-batch", 600, 20_000, baseErrs, baseCalls, extErrs7, extCalls7),
	} {
		zero := r
		zero.ExtErrs, zero.ExtCalls = 0, 0
		if a, b := classifyTraceOps([]traceOpBucket{r}, wr, def.IsBatchService), classifyTraceOps([]traceOpBucket{zero}, wr, def.IsBatchService); !reflect.DeepEqual(a, b) {
			t.Fatalf("base_errs > 0 iken uzun taban kararı değiştirdi:\n%+v\n%+v", a, b)
		}
	}
}

// TestTraceOpBatchExtNeverAddsEvents — ÖZELLİK TABLOSU. Her ızgara satırı
// için uzun taban alanları dolu (yeni) ve sıfır (bugünkü) sonuç kıyaslanır:
//  1. olay sayısı ASLA artmaz (kural yalnız susturur ya da türü değiştirir);
//  2. batch olmayan servis, base_errs > 0 ya da uzun tabanda hata yok →
//     sonuç BİREBİR bugünkü;
//  3. kural kapalı (nil yüklem) → her satırda birebir bugünkü;
//  4. kalan her uzun-taban olayı: bugün new_error'du; mutlak artış ≥ 25 puansa
//     BİREBİR bugünkü new_error, değilse error_spike, oranı tam pay oranı
//     (taban payı = ext_errs / (ext_calls + base_calls)) ve ≥ 3, cari sayılar
//     aynı.
func TestTraceOpBatchExtNeverAddsEvents(t *testing.T) {
	wr := traceOpWindowRatio
	on := chstore.AnomalySensitivityConfig{}.IsBatchService
	n, changed, suppressed, escaped := 0, 0, 0, 0
	for _, svc := range []string{"orders-batch", "payments-api"} {
		for _, ce := range []uint64{9, 10, 30, 70, 300, 2_000} {
			for _, cc := range []uint64{500, 1_000, 15_000} {
				for _, be := range []uint64{0, 5, 8_640} {
					for _, bc := range []uint64{0, 288_000} {
						for _, ee := range []uint64{0, 1, extErrs7, 3_000} {
							for _, ec := range []uint64{0, extCalls7, 100_000} {
								if ce > cc || (be > bc && bc > 0) || (ee > ec && ec > 0) {
									continue // hata ⊆ çağrı (çağrı 0 savunma kolları kalır)
								}
								n++
								r := traceOpBucket{Service: svc, Operation: "op", CurErrs: ce, CurCalls: cc,
									BaseErrs: be, BaseCalls: bc, ExtErrs: ee, ExtCalls: ec}
								zero := r
								zero.ExtErrs, zero.ExtCalls = 0, 0
								old := classifyTraceOps([]traceOpBucket{zero}, wr, on)
								neu := classifyTraceOps([]traceOpBucket{r}, wr, on)
								if !reflect.DeepEqual(classifyTraceOps([]traceOpBucket{r}, wr, nil), classifyTraceOps([]traceOpBucket{zero}, wr, nil)) {
									t.Fatalf("kural kapalıyken uzun taban kararı etkiledi: %+v", r)
								}
								if len(neu) > len(old) {
									t.Fatalf("kural YENİ olay üretti: %+v → %+v", r, neu)
								}
								if !on(svc) || be > 0 || ee == 0 || ec == 0 {
									if !reflect.DeepEqual(old, neu) {
										t.Fatalf("bugünkü karar değişti: %+v\neski %+v\nyeni %+v", r, old, neu)
									}
									continue
								}
								if len(old) == 1 && old[0].Kind != "new_error" {
									t.Fatalf("aday bugün new_error değildi: %+v", old)
								}
								curShare := float64(ce) / float64(cc)
								extShare := float64(ee) / float64(ec+bc)
								if len(old) == 1 && curShare-extShare >= traceOpBatchExtAbsRise {
									escaped++
									if !reflect.DeepEqual(old, neu) {
										t.Fatalf("kaçış bugünkü new_error'u üretmedi: %+v\neski %+v\nyeni %+v", r, old, neu)
									}
									continue
								}
								if len(old) == 1 && len(neu) == 0 {
									suppressed++
								}
								if len(neu) == 1 {
									changed++
									want := curShare / extShare
									if neu[0].Kind != "error_spike" || neu[0].Ratio != want || want < traceOpMinRatio-1e-9 ||
										neu[0].CurrentErrors != ce || neu[0].CurrentCalls != cc {
										t.Fatalf("uzun-taban olayı yanlış: %+v → %+v (pay oranı %v)", r, neu[0], want)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if suppressed == 0 || changed == 0 || escaped == 0 {
		t.Fatalf("ızgara anlamsız: %d satır, %d susturuldu, %d error_spike'a döndü, %d kaçış", n, suppressed, changed, escaped)
	}

	// İlk 50 yarışı: new_error → error_spike'a dönen batch olayı sıralamada
	// new_error grubundan spike grubuna İNER; hiçbir batch olmayan çift ilk
	// 50'den düşmemeli (yalnız yer kazanabilir).
	var all []traceOpBucket
	for i := 0; i < 15; i++ { // batch, uzun taban %2, cari %10 → ×5 error_spike
		all = append(all, traceOpBucket{Service: "orders-batch", Operation: fmt.Sprintf("conv-%02d", i),
			CurErrs: uint64(100 + i), CurCalls: uint64(100+i) * 10, ExtErrs: extErrs7, ExtCalls: extCalls7})
	}
	for i := 0; i < 20; i++ { // batch olmayan new_error
		all = append(all, traceOpBucket{Service: "payments-api", Operation: fmt.Sprintf("new-%02d", i),
			CurErrs: uint64(20 + i), CurCalls: 1_000})
	}
	// Batch olmayan spike'lar, oranlar ×4…×23. Eski ilk 50 = 35 new_error +
	// en yüksek 15 spike; yenide 20 new_error + 18 spike (×6…×23) + ×5'lik
	// payments + dönüşen batch'ler — ×4'lük spike zaten eski ilk 50'de değildi.
	for i := 0; i < 20; i++ {
		all = append(all, traceOpBucket{Service: "payments-api", Operation: fmt.Sprintf("spike-%02d", i),
			CurErrs: uint64(120 + 30*i), CurCalls: 20_000, BaseErrs: 8_640, BaseCalls: 2_880_000})
	}
	zeroAll := make([]traceOpBucket, len(all))
	for i, r := range all {
		r.ExtErrs, r.ExtCalls = 0, 0
		zeroAll[i] = r
	}
	oldTop := classifyTraceOps(zeroAll, wr, on)
	newTop := classifyTraceOps(all, wr, on)
	if len(oldTop) != 50 || len(newTop) != 50 {
		t.Fatalf("ilk 50 tavanı ısırmıyor (%d/%d) — yer kaybı testi anlamsız", len(oldTop), len(newTop))
	}
	inNew := map[string]bool{}
	converted := 0
	for _, a := range newTop {
		inNew[a.Service+"/"+a.Operation] = true
		if a.Service == "orders-batch" && a.Kind == "error_spike" {
			converted++
		}
	}
	for _, a := range oldTop {
		if !on(a.Service) && !inNew[a.Service+"/"+a.Operation] {
			t.Fatalf("batch olmayan çift ilk 50'den düştü: %+v", a)
		}
	}
	if converted == 0 {
		t.Fatal("dönüşen batch olayı ilk 50'de yok — sıralama testi anlamsız")
	}
}

// TestTraceOpBatchExtCandidates — aday = BUGÜN new_error olacak batch çifti;
// sıra cari hata çoktan aza; tavan; kural kapalı → aday yok.
func TestTraceOpBatchExtCandidates(t *testing.T) {
	on := chstore.AnomalySensitivityConfig{}.IsBatchService
	rows := []traceOpBucket{
		{Service: "orders-batch", Operation: "small", CurErrs: 20, CurCalls: 1_000},
		{Service: "orders-batch", Operation: "big", CurErrs: 300, CurCalls: 15_000},
		{Service: "orders-batch", Operation: "below-count", CurErrs: 9, CurCalls: 100},
		{Service: "orders-batch", Operation: "below-share", CurErrs: 15, CurCalls: 2_000},
		{Service: "orders-batch", Operation: "has-base", CurErrs: 600, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls},
		{Service: "payments-api", Operation: "new", CurErrs: 400, CurCalls: 1_000},
		{Service: "ETL-batch-loader", Operation: "new", CurErrs: 20, CurCalls: 1_000},
	}
	pairs, uncovered := traceOpBatchExtCandidates(rows, on)
	want := []traceOpPair{{"orders-batch", "big"}, {"ETL-batch-loader", "new"}, {"orders-batch", "small"}}
	if !reflect.DeepEqual(pairs, want) || uncovered != 0 {
		t.Fatalf("adaylar %v (kapsam dışı %d), beklenen %v", pairs, uncovered, want)
	}
	if p, u := traceOpBatchExtCandidates(rows, nil); p != nil || u != 0 {
		t.Fatalf("kural kapalıyken aday üretildi: %v %d", p, u)
	}

	var many []traceOpBucket
	for i := 0; i < traceOpBatchExtMaxPairs+7; i++ {
		many = append(many, traceOpBucket{Service: "orders-batch", Operation: fmt.Sprintf("op-%03d", i),
			CurErrs: uint64(100 + i), CurCalls: uint64(100+i) * 50})
	}
	pairs, uncovered = traceOpBatchExtCandidates(many, on)
	if len(pairs) != traceOpBatchExtMaxPairs || uncovered != 7 {
		t.Fatalf("tavan: %d kapsandı, %d dışarıda", len(pairs), uncovered)
	}
	// En çok hata sayan 50 kapsanır (en gürültülü new_error'lar).
	if pairs[0].Operation != fmt.Sprintf("op-%03d", traceOpBatchExtMaxPairs+6) || pairs[len(pairs)-1].Operation != "op-007" {
		t.Fatalf("tavan sırası cari hata çoktan aza değil: ilk %v son %v", pairs[0], pairs[len(pairs)-1])
	}
}

// TestTraceOpBatchExtQueryShape — uzun okumanın SQL'i: aynı MV, yarı açık
// zaman sınırı, aday çiftler BİREBİR, batch koşulu, LIMIT, max_execution_time.
func TestTraceOpBatchExtQueryShape(t *testing.T) {
	_, baseStart, now := traceOpTimes()
	extStart, extEnd, ok := traceOpBatchExtWindow(now, baseStart)
	if !ok {
		t.Fatal("5 dk pencerede uzun taban kurulmalıydı")
	}
	if !extStart.Equal(now.Add(-8*24*time.Hour)) || !extEnd.Equal(baseStart) {
		t.Fatalf("uzun taban [%v, %v), beklenen [şimdi−8g, taban başı %v)", extStart, extEnd, baseStart)
	}
	cond, cargs := chstore.AnomalySensitivityConfig{}.BatchServiceSQL("service_name")
	pairs := []traceOpPair{{"orders-batch", "ProcessChunk"}, {"ETL-batch-loader", "Load"}}
	q, args := traceOpBatchExtQuery(extStart, extEnd, pairs, cond, cargs)

	for _, frag := range []string{
		"countIfMerge(error_count_state) AS ext_errs",
		"countMerge(span_count_state)    AS ext_calls",
		"FROM operation_summary_5m",
		// Yarı açık: alt sınır dahil, üst sınır (= ana sorgunun taban başı) HARİÇ.
		"WHERE time_bucket >= ? AND time_bucket < ?",
		"AND (service_name, name) IN (tuple(?, ?), tuple(?, ?))",
		"AND " + cond + "\n",
		"GROUP BY service_name, name",
		"LIMIT 50\n",
		"SETTINGS max_execution_time = 10",
	} {
		if !strings.Contains(q, frag) {
			t.Fatalf("uzun okuma %q taşımıyor:\n%s", frag, q)
		}
	}
	if strings.Count(q, cond) != 1 || strings.Count(q, "FROM ") != 1 || strings.Contains(q, "FROM spans") {
		t.Fatalf("uzun okuma tek MV + tek batch koşulu olmalı:\n%s", q)
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d, argüman %d", strings.Count(q, "?"), len(args))
	}
	want := []any{extStart, baseStart, "orders-batch", "ProcessChunk", "ETL-batch-loader", "Load", "-batch"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("argümanlar\n%v\nbeklenen\n%v", args, want)
	}
	// LIMIT = aday tavanı (GROUP BY çift ⊆ aday listesi → LIMIT hiçbir satırı
	// kesemez); tavan belgelenen 50 (DECISIONS v0.10.1043).
	if traceOpBatchExtMaxPairs != 50 {
		t.Fatalf("aday tavanı %d — belgelenen 50 ve SQL'deki LIMIT ile birlikte güncelle", traceOpBatchExtMaxPairs)
	}

	// Taban başı uzun taban başına eşit ya da daha eskiyse pencere boş: okuma
	// YOK. (Gerçek pencere kesimi — 13 × pencere ≥ 8 g — davranış testinde.)
	if _, _, ok := traceOpBatchExtWindow(now, now.Add(-traceOpBatchExtLookback)); ok {
		t.Fatal("boş [x, x) pencere kuruldu")
	}
	if _, _, ok := traceOpBatchExtWindow(now, now.Add(-traceOpBatchExtLookback-traceOpBucketLen)); ok {
		t.Fatal("taban 8 günü aşarken uzun taban penceresi kuruldu")
	}
}

// ── sahte bağlantı ──────────────────────────────────────────────────────

type fakeTraceOpQuery struct {
	q    string
	args []any
}

// fakeTraceOpConn — ana sorguya `main`'i, uzun okumaya `ext`'i (YALNIZ
// istenen çiftler için — SQL kısıtını taklit eder), örnek-trace sorgusuna
// boş sonuç döner; gönderilen her sorguyu saklar.
type fakeTraceOpConn struct {
	main       []traceOpBucket
	ext        map[traceOpPair][2]uint64
	extErr     error // Query hatası
	extRowsErr error // tarama sonrası rows.Err
	queries    []fakeTraceOpQuery
}

func (f *fakeTraceOpConn) Query(_ context.Context, q string, args ...any) (driver.Rows, error) {
	f.queries = append(f.queries, fakeTraceOpQuery{q, args})
	switch {
	case strings.Contains(q, "AS ext_errs"):
		if f.extErr != nil {
			return nil, f.extErr
		}
		n := strings.Count(q, "tuple(?, ?)")
		var rows [][]any
		for i := 0; i < n; i++ {
			p := traceOpPair{args[2+2*i].(string), args[3+2*i].(string)}
			if e, ok := f.ext[p]; ok {
				rows = append(rows, []any{p.Service, p.Operation, e[0], e[1]})
			}
		}
		return &fakeTraceOpRows{vals: rows, err: f.extRowsErr}, nil
	case strings.Contains(q, "AS is_cur"):
		withBase := strings.Contains(q, "AS base_calls")
		var rows [][]any
		for _, b := range f.main {
			r := []any{b.Service, b.Operation, b.CurErrs, b.BaseErrs, b.CurCalls}
			if withBase {
				r = append(r, b.BaseCalls)
			}
			rows = append(rows, r)
		}
		return &fakeTraceOpRows{vals: rows}, nil
	case strings.Contains(q, "FROM spans"):
		return &fakeTraceOpRows{}, nil
	}
	return nil, fmt.Errorf("beklenmeyen sorgu: %s", q)
}

func (f *fakeTraceOpConn) extQueries() []fakeTraceOpQuery {
	var out []fakeTraceOpQuery
	for _, q := range f.queries {
		if strings.Contains(q.q, "AS ext_errs") {
			out = append(out, q)
		}
	}
	return out
}

type fakeTraceOpRows struct {
	driver.Rows
	vals [][]any
	i    int
	err  error
}

func (r *fakeTraceOpRows) Next() bool {
	if r.i < len(r.vals) {
		r.i++
		return true
	}
	return false
}

func (r *fakeTraceOpRows) Scan(dest ...any) error {
	row := r.vals[r.i-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan: %d hedef, %d değer", len(dest), len(row))
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(row[i]))
	}
	return nil
}

func (r *fakeTraceOpRows) Close() error { return nil }
func (r *fakeTraceOpRows) Err() error   { return r.err }

// fakeNow — hizalı şimdi 12:00:00; 5 dk pencere [11:55, 12:00).
var fakeNow = time.Date(2026, 10, 2, 12, 2, 30, 0, time.UTC)

func runFakeTraceOps(t *testing.T, conn *fakeTraceOpConn, sens chstore.AnomalySensitivityConfig, window time.Duration) ([]TraceOpAnomaly, []string) {
	t.Helper()
	var logs []string
	out, err := detectTraceOps(context.Background(), conn, sens, fakeNow, window,
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	if err != nil {
		t.Fatal(err)
	}
	return out, logs
}

func kinds(out []TraceOpAnomaly) map[string]string {
	m := map[string]string{}
	for _, a := range out {
		m[a.Service+"/"+a.Operation] = a.Kind
	}
	return m
}

func TestDetectTraceOpsBatchExtBehaviour(t *testing.T) {
	def := chstore.AnomalySensitivityConfig{} // ["-batch"]
	weekly := traceOpBucket{Service: "orders-batch", Operation: "ProcessChunk", CurErrs: 20, CurCalls: 1_000}
	worse := traceOpBucket{Service: "orders-batch", Operation: "ProcessChunk", CurErrs: 70, CurCalls: 1_000}
	api := traceOpBucket{Service: "payments-api", Operation: "Get", CurErrs: 40, CurCalls: 1_000}
	spike := traceOpBucket{Service: "orders-batch", Operation: "Spike", CurErrs: 2_100, CurCalls: 20_000, BaseErrs: baseErrs, BaseCalls: baseCalls}
	usual := map[traceOpPair][2]uint64{
		{"orders-batch", "ProcessChunk"}: {extErrs7, extCalls7},
		{"payments-api", "Get"}:          {extErrs7, extCalls7}, // SQL bunu zaten döndürmezdi; sızarsa da karar değişmemeli
	}

	t.Run("liste boş → ana sorgu birebir eski, uzun okuma YOK", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{weekly, api}, ext: usual}
		out, logs := runFakeTraceOps(t, conn, batchSens(), 5*time.Minute)
		if conn.queries[0].q != goldenLegacyTraceOpSQL {
			t.Fatalf("boş listede ana sorgu değişti:\n%s", conn.queries[0].q)
		}
		if len(conn.extQueries()) != 0 || len(logs) != 0 {
			t.Fatalf("boş listede uzun okuma kuruldu: %d sorgu, log %v", len(conn.extQueries()), logs)
		}
		if k := kinds(out); k["orders-batch/ProcessChunk"] != "new_error" || k["payments-api/Get"] != "new_error" {
			t.Fatalf("bugünkü new_error bekleniyordu: %v", k)
		}
	})

	t.Run("batch new_error adayı yok → uzun okuma YOK", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{api, spike}, ext: usual}
		out, _ := runFakeTraceOps(t, conn, def, 5*time.Minute)
		if len(conn.extQueries()) != 0 {
			t.Fatal("aday yokken uzun okuma kuruldu (batch olmayan new_error ya da batch spike tetiklememeli)")
		}
		if k := kinds(out); k["payments-api/Get"] != "new_error" || k["orders-batch/Spike"] != "error_spike" {
			t.Fatalf("bugünkü kararlar bekleniyordu: %v", k)
		}
	})

	t.Run("batch adayı, uzun taban %2, cari %2 → olay YOK; tek okuma, yalnız aday çift", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{weekly, api}, ext: usual}
		out, logs := runFakeTraceOps(t, conn, def, 5*time.Minute)
		ext := conn.extQueries()
		if len(ext) != 1 {
			t.Fatalf("tam bir uzun okuma bekleniyordu, %d", len(ext))
		}
		// Çift argümanları: yalnız batch adayı; batch olmayan new_error YOK.
		if got := ext[0].args[2:4]; !reflect.DeepEqual(got, []any{"orders-batch", "ProcessChunk"}) || strings.Count(ext[0].q, "tuple(?, ?)") != 1 {
			t.Fatalf("uzun okuma çiftleri %v", ext[0].args)
		}
		// Sınır pini: uzun taban [şimdi−8g, ana sorgunun taban başı); ana sorgu
		// [taban başı, şimdi) okur — aynı değer, yarı açık, örtüşme yok.
		mainArgs := conn.queries[0].args
		aligned := fakeNow.Truncate(traceOpBucketLen)
		if !ext[0].args[1].(time.Time).Equal(mainArgs[1].(time.Time)) ||
			!ext[0].args[0].(time.Time).Equal(aligned.Add(-traceOpBatchExtLookback)) ||
			!mainArgs[2].(time.Time).Equal(aligned) {
			t.Fatalf("sınırlar: uzun [%v, %v) ana taban başı %v şimdi %v", ext[0].args[0], ext[0].args[1], mainArgs[1], mainArgs[2])
		}
		if k := kinds(out); len(k) != 1 || k["payments-api/Get"] != "new_error" {
			t.Fatalf("yalnız batch olmayan new_error kalmalıydı: %v", k)
		}
		if len(logs) != 0 {
			t.Fatalf("başarılı okumada log: %v", logs)
		}
	})

	t.Run("batch adayı, cari %7 → error_spike, pay oranı + dürüst prev", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{worse}, ext: usual}
		out, _ := runFakeTraceOps(t, conn, def, 5*time.Minute)
		if len(out) != 1 || out[0].Kind != "error_spike" || !nearlyEqual(out[0].Ratio, 3.5) || out[0].BaselineErrors != 20 {
			t.Fatalf("error_spike ×3.5, prev 20 bekleniyordu: %+v", out)
		}
	})

	t.Run("uzun tabanda veri yok (gerçek ilk hata) → new_error", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{weekly}, ext: map[traceOpPair][2]uint64{}}
		out, logs := runFakeTraceOps(t, conn, def, 5*time.Minute)
		if len(conn.extQueries()) != 1 || len(out) != 1 || out[0].Kind != "new_error" || out[0].Ratio != 20 || len(logs) != 0 {
			t.Fatalf("new_error bekleniyordu: %+v log %v", out, logs)
		}
	})

	// Bugünkü sonuç = kural kapalıyken (boş liste) aynı satırların sonucu.
	todayFor := func(rows ...traceOpBucket) []TraceOpAnomaly {
		out, _ := runFakeTraceOps(t, &fakeTraceOpConn{main: rows}, batchSens(), 5*time.Minute)
		return out
	}

	t.Run("uzun okuma HATASI → adaylar bugünkü new_error, TEK log", func(t *testing.T) {
		conn := &fakeTraceOpConn{main: []traceOpBucket{weekly, api}, ext: usual, extErr: errors.New("ch down")}
		out, logs := runFakeTraceOps(t, conn, def, 5*time.Minute)
		if !reflect.DeepEqual(out, todayFor(weekly, api)) {
			t.Fatalf("okuma hatasında bugünkü sonuç bekleniyordu:\n%+v\n%+v", out, todayFor(weekly, api))
		}
		if len(logs) != 1 || !strings.Contains(logs[0], "okunamadı") || !strings.Contains(logs[0], "ch down") || !strings.Contains(logs[0], "1 aday") {
			t.Fatalf("tek log satırı bekleniyordu: %v", logs)
		}
	})

	t.Run("uzun okuma rows.Err → yarım sonuç UYGULANMAZ, TEK log", func(t *testing.T) {
		other := traceOpBucket{Service: "orders-batch", Operation: "Other", CurErrs: 30, CurCalls: 1_000}
		ext := map[traceOpPair][2]uint64{
			{"orders-batch", "ProcessChunk"}: {extErrs7, extCalls7},
			{"orders-batch", "Other"}:        {extErrs7, extCalls7},
		}
		conn := &fakeTraceOpConn{main: []traceOpBucket{weekly, other}, ext: ext, extRowsErr: errors.New("bağlantı koptu")}
		out, logs := runFakeTraceOps(t, conn, def, 5*time.Minute)
		if !reflect.DeepEqual(out, todayFor(weekly, other)) {
			t.Fatalf("yarım okumada bugünkü sonuç bekleniyordu: %+v", out)
		}
		if len(logs) != 1 || !strings.Contains(logs[0], "2 aday") {
			t.Fatalf("tek log satırı bekleniyordu: %v", logs)
		}
	})

	t.Run("tavan aşımı → kapsanmayanlar bugünkü new_error, tek sayı logu", func(t *testing.T) {
		var main []traceOpBucket
		ext := map[traceOpPair][2]uint64{}
		for i := 0; i < traceOpBatchExtMaxPairs+10; i++ {
			b := traceOpBucket{Service: "orders-batch", Operation: fmt.Sprintf("op-%03d", i),
				CurErrs: uint64(100 + i), CurCalls: uint64(100+i) * 50} // %2
			main = append(main, b)
			ext[traceOpPair{b.Service, b.Operation}] = [2]uint64{extErrs7, extCalls7} // her zamanki %2
		}
		conn := &fakeTraceOpConn{main: main, ext: ext}
		out, logs := runFakeTraceOps(t, conn, def, 5*time.Minute)
		eq := conn.extQueries()
		if len(eq) != 1 || strings.Count(eq[0].q, "tuple(?, ?)") != traceOpBatchExtMaxPairs {
			t.Fatalf("tek okuma, %d çift bekleniyordu", traceOpBatchExtMaxPairs)
		}
		// Kapsanmayan 10 = en az hata sayan 10 (op-000..op-009); bugünkü new_error.
		k := kinds(out)
		if len(k) != 10 {
			t.Fatalf("10 kapsanmayan new_error bekleniyordu, %d: %v", len(k), k)
		}
		for i := 0; i < 10; i++ {
			if k[fmt.Sprintf("orders-batch/op-%03d", i)] != "new_error" {
				t.Fatalf("op-%03d bugünkü new_error olmalıydı: %v", i, k)
			}
		}
		if len(logs) != 1 || !strings.Contains(logs[0], "60 aday") || !strings.Contains(logs[0], "tavan 50") || !strings.Contains(logs[0], "10 çift") {
			t.Fatalf("tek sayı logu bekleniyordu: %v", logs)
		}
	})

	// Pencere kesimi: taban başı = şimdi − 13 × pencere (pencere ≥ 2 sa) →
	// 13 × pencere ≥ 8 g olunca uzun pencere boş. 5 dk hizalı: 14 sa 45 dk
	// (191 sa 45 dk) okur, 14 sa 50 dk (192 sa 50 dk) okumaz.
	for _, c := range []struct {
		window time.Duration
		reads  bool
	}{{14*time.Hour + 45*time.Minute, true}, {14*time.Hour + 50*time.Minute, false}} {
		t.Run(fmt.Sprintf("pencere %v → uzun okuma %v", c.window, c.reads), func(t *testing.T) {
			conn := &fakeTraceOpConn{main: []traceOpBucket{weekly}, ext: map[traceOpPair][2]uint64{}}
			out, _ := runFakeTraceOps(t, conn, def, c.window)
			if (len(conn.extQueries()) == 1) != c.reads || len(conn.extQueries()) > 1 {
				t.Fatalf("uzun okuma sayısı %d, okuma beklentisi %v", len(conn.extQueries()), c.reads)
			}
			if len(out) != 1 || out[0].Kind != "new_error" {
				t.Fatalf("bugünkü new_error bekleniyordu: %+v", out)
			}
		})
	}
}

// ── clickhouse-local: gerçek SQL anlamı ─────────────────────────────────

// chLocalTraceOpConn — sorguları `clickhouse local`'da, operation_summary_5m
// biçiminde (aynı state tipleri) kurulmuş bir fikstür üstünde koşar.
// Örnek-trace sorgusu (FROM spans) boş döner.
type chLocalTraceOpConn struct {
	t       *testing.T
	bin     string
	fixture string
	queries []fakeTraceOpQuery
}

func (c *chLocalTraceOpConn) Query(_ context.Context, q string, args ...any) (driver.Rows, error) {
	c.queries = append(c.queries, fakeTraceOpQuery{q, args})
	if strings.Contains(q, "FROM spans") {
		return &fakeTraceOpRows{}, nil
	}
	sql := c.fixture + inlineCHArgs(c.t, q, args) + " FORMAT TSVRaw"
	out, err := exec.Command(c.bin, "local", "--multiquery", "--query", sql).CombinedOutput()
	if err != nil {
		c.t.Fatalf("clickhouse local: %v\n%s\n%s", err, out, sql)
	}
	var rows [][]any
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		row := []any{f[0], f[1]}
		for _, s := range f[2:] {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				c.t.Fatalf("sayı değil %q: %s", s, ln)
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	return &fakeTraceOpRows{vals: rows}, nil
}

// inlineCHArgs — inlineTraceOpArgs + time.Time (UTC DateTime literali).
func inlineCHArgs(t *testing.T, q string, args []any) string {
	t.Helper()
	conv := make([]any, len(args))
	for i, a := range args {
		if tm, ok := a.(time.Time); ok {
			conv[i] = chTimeLit(tm)
		} else {
			conv[i] = a
		}
	}
	var b strings.Builder
	i := 0
	for _, r := range q {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		if i >= len(conv) {
			t.Fatalf("argüman eksik: %d yer tutucu fazla", i+1-len(conv))
		}
		switch v := conv[i].(type) {
		case chTimeLit:
			b.WriteString("toDateTime('" + time.Time(v).UTC().Format("2006-01-02 15:04:05") + "', 'UTC')")
		case string:
			b.WriteString("'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'")
		default:
			b.WriteString(fmt.Sprint(v))
		}
		i++
	}
	if i != len(args) {
		t.Fatalf("argüman %d/%d", i, len(args))
	}
	return b.String()
}

type chTimeLit time.Time

// TestTraceOpBatchExtAgreesWithClickHouse — detectTraceOps'un GERÇEK yolu
// (ana sorgu → aday → uzun okuma → sınıflandırma) `clickhouse local`'daki
// operation_summary_5m biçimli fikstürde. Sınır kovaları: uzun tabanın ilk
// kovası (dahil), son kovası (taban başından hemen önce, dahil), 8 günden bir
// kova önce (hariç). İkili yoksa atlanır (CI).
func TestTraceOpBatchExtAgreesWithClickHouse(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — uzun taban SQL'i canlı CH ile doğrulanmadı")
	}
	aligned := fakeNow.Truncate(traceOpBucketLen) // 12:00
	cur := aligned.Add(-traceOpBucketLen)         // 11:55 — cari kova
	baseStart := cur.Add(-24 * time.Hour)         // taban başı
	extStart := aligned.Add(-traceOpBatchExtLookback)

	type bucket struct {
		svc, op     string
		at          time.Time
		calls, errs int
	}
	week := cur.Add(-7 * 24 * time.Hour)
	fx := []bucket{
		// Haftalık iş, her zamanki %2 → SUSAR.
		{"orders-batch", "weekly", cur, 1_000, 20},
		{"orders-batch", "weekly", week, 1_000, 20},
		// Aynı iş, %7 → error_spike ×3.5.
		{"orders-batch", "worse", cur, 1_000, 70},
		{"orders-batch", "worse", week, 1_000, 20},
		// Geçen hafta temiz koşmuş → gerçek ilk hata, new_error.
		{"orders-batch", "first-fail", cur, 1_000, 20},
		{"orders-batch", "first-fail", week, 1_000, 0},
		// Sınırlar: son uzun kova (taban başından 5 dk önce) DAHİL → susar.
		{"orders-batch", "edge-last", cur, 1_000, 20},
		{"orders-batch", "edge-last", baseStart.Add(-traceOpBucketLen), 1_000, 20},
		// İlk uzun kova (şimdi − 8 g) DAHİL → susar.
		{"orders-batch", "edge-first", cur, 1_000, 20},
		{"orders-batch", "edge-first", extStart, 1_000, 20},
		// 8 günden bir kova önce HARİÇ → new_error.
		{"orders-batch", "edge-before", cur, 1_000, 20},
		{"orders-batch", "edge-before", extStart.Add(-traceOpBucketLen), 1_000, 20},
		// Taban başındaki kova TABANA ait (uzun tabana değil): base_errs > 0
		// → aday değil; v0.10.1039 sayım+pay kuralı (pay sabit) → olay yok.
		{"orders-batch", "base-edge", cur, 1_000, 20},
		{"orders-batch", "base-edge", baseStart, 1_000, 20},
		// Mutlak artış kaçışı: geçen hafta %40, bu hafta %100 → new_error.
		{"orders-batch", "escape", cur, 1_000, 1_000},
		{"orders-batch", "escape", week, 1_000, 400},
		// Dün temiz koşu (100.000 çağrı, 0 hata — ana sorgunun base_calls'ı),
		// altı gün önce 10/1.000: taban payı 10/101.000 → %2.5 ateşler
		// (payda yalnız uzun tabansa %1 → susardı).
		{"orders-batch", "clean-yesterday", cur, 1_000, 25},
		{"orders-batch", "clean-yesterday", cur.Add(-12 * time.Hour), 100_000, 0},
		{"orders-batch", "clean-yesterday", cur.Add(-6 * 24 * time.Hour), 1_000, 10},
		// Batch OLMAYAN aynı desen → bugünkü new_error (uzun okumaya girmez).
		{"payments-api", "weekly", cur, 1_000, 20},
		{"payments-api", "weekly", week, 1_000, 20},
	}
	var ins []string
	for _, b := range fx {
		ins = append(ins, fmt.Sprintf(
			"INSERT INTO operation_summary_5m SELECT '%s', '%s', toDateTime('%s', 'UTC'), countState(), countIfState(number < %d) FROM numbers(%d);",
			b.svc, b.op, b.at.UTC().Format("2006-01-02 15:04:05"), b.errs, b.calls))
	}
	fixture := `CREATE TABLE operation_summary_5m (
		service_name String, name String, time_bucket DateTime('UTC'),
		span_count_state AggregateFunction(count),
		error_count_state AggregateFunction(countIf, UInt8)
	) ENGINE = AggregatingMergeTree ORDER BY (service_name, name, time_bucket);
	` + strings.Join(ins, "\n") + "\n"

	conn := &chLocalTraceOpConn{t: t, bin: bin, fixture: fixture}
	var logs []string
	out, err := detectTraceOps(context.Background(), conn, chstore.AnomalySensitivityConfig{}, fakeNow, 5*time.Minute,
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]TraceOpAnomaly{}
	for _, a := range out {
		got[a.Service+"/"+a.Operation] = a
	}
	want := map[string]string{
		"orders-batch/worse":           "error_spike",
		"orders-batch/first-fail":      "new_error",
		"orders-batch/edge-before":     "new_error",
		"orders-batch/escape":          "new_error",
		"orders-batch/clean-yesterday": "error_spike",
		"payments-api/weekly":          "new_error",
	}
	if len(got) != len(want) {
		t.Fatalf("olaylar %v, beklenen %v (log %v)", kinds(out), want, logs)
	}
	for k, kind := range want {
		if got[k].Kind != kind {
			t.Fatalf("%s: %q, beklenen %q (tümü %v)", k, got[k].Kind, kind, kinds(out))
		}
	}
	if w := got["orders-batch/worse"]; !nearlyEqual(w.Ratio, 3.5) || w.BaselineErrors != 20 {
		t.Fatalf("worse: ×%v prev %d, beklenen ×3.5 prev 20", w.Ratio, w.BaselineErrors)
	}
	if e := got["orders-batch/escape"]; e.Ratio != 1_000 || e.BaselineErrors != 0 {
		t.Fatalf("escape: bugünkü new_error (×1000, prev 0) bekleniyordu: %+v", e)
	}
	if c := got["orders-batch/clean-yesterday"]; !nearlyEqual(c.Ratio, 0.025/(10.0/101_000.0)) {
		t.Fatalf("clean-yesterday: ×%v, beklenen pay oranı %v", c.Ratio, 0.025/(10.0/101_000.0))
	}
	// Tek uzun okuma; çiftler yalnız batch new_error adayları (base-edge ve
	// batch olmayan çift YOK).
	var ext []fakeTraceOpQuery
	for _, q := range conn.queries {
		if strings.Contains(q.q, "AS ext_errs") {
			ext = append(ext, q)
		}
	}
	if len(ext) != 1 || strings.Count(ext[0].q, "tuple(?, ?)") != 8 || len(logs) != 0 {
		t.Fatalf("tek uzun okuma, 8 aday çift bekleniyordu: %d okuma, log %v", len(ext), logs)
	}

	// SQL kısıtları doğrudan: batch olmayan çift ve taban başındaki kova uzun
	// okumaya SIZMAZ — çifti elle listeye koysan bile.
	cond, cargs := chstore.AnomalySensitivityConfig{}.BatchServiceSQL("service_name")
	q, args := traceOpBatchExtQuery(extStart, baseStart, []traceOpPair{
		{"payments-api", "weekly"}, {"orders-batch", "base-edge"}, {"orders-batch", "edge-first"},
	}, cond, cargs)
	rs, _ := conn.Query(context.Background(), q, args...)
	direct := map[string][2]uint64{}
	for rs.Next() {
		var s, n string
		var e, c uint64
		if err := rs.Scan(&s, &n, &e, &c); err != nil {
			t.Fatal(err)
		}
		direct[s+"/"+n] = [2]uint64{e, c}
	}
	if !reflect.DeepEqual(direct, map[string][2]uint64{"orders-batch/edge-first": {20, 1_000}}) {
		t.Fatalf("uzun okuma kısıtları: %v", direct)
	}
}
