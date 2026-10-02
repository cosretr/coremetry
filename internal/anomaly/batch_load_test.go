package anomaly

// batch_load_test.go — v0.10.1039: batch servislerde yük (request_rate)
// anomali değil — davranış motoru + metrik dedektörü.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// Ölçülen yol: `orders-batch` gibi bir serviste 20× istek artışı, hata
// yüzdesi SABİT → davranış motoru request_rate için behavior_change yazar
// (Problems'ta P1, promoteStrongAnomalies ile anomaly-auto: Problem'e
// terfi). Metrik dedektörü request_rate'i izliyorsa anomaly:<svc>:
// request_rate de açılır.
//
// EN KÖTÜ SONUÇ AŞIRI SUSTURMA: aşağıdaki her "batch ama SİNYAL" vakası
// (error_rate rejim kayması, p99) bu korkunun pinidir.
//
// MUTASYON KANITLARI (çalıştırıldı, geri alındı — rapor v0.10.1039):
//   (a) behaviorFleetCandidates'teki `batchLoadSkipped` kapısı silinince
//       "batch + 20× yük" vakası request_rate adayı üretir → kızarır.
//   (b) batchLoadSkipped `metric == batchLoadMetric &&` koşulu atılıp tüm
//       metriklere genişletilince "batch + error_rate rejim kayması" ve
//       batchLoadSkipped tablosunun error_rate/p99 satırları kızarır;
//       batchLoadResolutions'ın tam RuleID eşleşmesi önek eşleşmesine
//       gevşetilince (batch servisin TÜM anomali satırları kapanır)
//       TestBatchLoadResolutions kızarır. scan()'deki faz-1 kapısı silinince
//       TestScanWiresBatchLoadGate (kaynak pini) kızarır.

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// behaviorFleetRows — 4 haftalık taban (aynı HOW kovası, her hafta farklı
// gün, 12 dilim) + cutoff'tan sonra `recentN` dilimlik "şimdi" penceresi.
// Taban: 30.000 span / 5 dk (100 istek/sn), hata %3, p99 100 ms; küçük
// salınım MAD'i sıfırdan uzak tutar.
func behaviorFleetRows(cutoff int64, recentN int, recentSpans, recentErrs uint64, recentP99 float64) []behaviorRow {
	const how = 10
	var out []behaviorRow
	for w := behaviorTestFullRepeats; w >= 1; w-- {
		day := cutoff - int64(w)*7*86400
		for i := 0; i < 12; i++ {
			jit := float64(i%5-2) * 0.02
			spans := uint64(30_000 * (1 + jit))
			out = append(out, behaviorRow{
				Unix: day + int64(i)*300, HOW: how,
				Spans: spans, Errs: spans * 3 / 100, P99Ms: 100 * (1 + jit),
			})
		}
	}
	for i := 0; i < recentN; i++ {
		out = append(out, behaviorRow{
			Unix: cutoff + int64(i)*300, HOW: how,
			Spans: recentSpans, Errs: recentErrs, P99Ms: recentP99,
		})
	}
	return out
}

func candidateMetrics(cands []behaviorCandidate, svc string) map[string]bool {
	out := map[string]bool{}
	for _, c := range cands {
		if c.Service == svc {
			out[c.Metric] = true
		}
	}
	return out
}

func TestBehaviorBatchLoadGate(t *testing.T) {
	const cutoff = int64(1_700_000_000)
	def := chstore.DefaultAnomalySensitivity() // batch kalıpları: ["-batch"]
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty // kural KAPALI

	surge := behaviorFleetRows(cutoff, 8, 600_000, 18_000, 100)    // 20× yük, hata %3 sabit, p99 sabit
	errShift := behaviorFleetRows(cutoff, 8, 30_000, 4_500, 100)   // yük sabit, hata %3 → %15
	p99Shift := behaviorFleetRows(cutoff, 8, 30_000, 900, 250)     // yük/hata sabit, p99 2.5×
	surgeErr := behaviorFleetRows(cutoff, 8, 600_000, 90_000, 100) // 20× yük VE hata %15
	collapse := behaviorFleetRows(cutoff, 8, 3_000, 90, 100)       // yük 1/10 (düşüş yönü)

	tests := []struct {
		name string
		cfg  chstore.AnomalySensitivityConfig
		svc  string
		rows []behaviorRow
		want map[string]bool // aday üreten metrikler
	}{
		// ── ASIL VAKA ──
		{"batch + 20× yük → aday YOK", def, "orders-batch", surge, map[string]bool{}},
		{"aynı yük batch olmayan serviste → request_rate adayı (bugünkü gibi)", def, "payments-api", surge, map[string]bool{"request_rate": true}},
		{"kural kapalı → batch adı sıradan servis gibi", off, "orders-batch", surge, map[string]bool{"request_rate": true}},
		// İki yön de yük: batch işinin durması/bitmesi de olay değil.
		{"batch + yük çöküşü → aday YOK", def, "orders-batch", collapse, map[string]bool{}},
		{"batch olmayan + yük çöküşü → request_rate adayı", def, "payments-api", collapse, map[string]bool{"request_rate": true}},

		// ── AŞIRI SUSTURMA PİNLERİ: batch'te gerçek sinyal SUSMAZ ──
		{"batch + error_rate rejim kayması → error_rate adayı", def, "orders-batch", errShift, map[string]bool{"error_rate": true}},
		{"batch + p99 kayması → p99 adayı", def, "orders-batch", p99Shift, map[string]bool{"p99_ms": true}},
		{"batch + 20× yük + hata %15 → error_rate adayı (yük değil)", def, "orders-batch", surgeErr, map[string]bool{"error_rate": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cands, _ := behaviorFleetCandidates(map[string][]behaviorRow{tt.svc: tt.rows}, nil, cutoff, tt.cfg, nil)
			got := candidateMetrics(cands, tt.svc)
			if len(got) != len(tt.want) {
				t.Fatalf("aday metrikleri %v, beklenen %v", got, tt.want)
			}
			for m := range tt.want {
				if !got[m] {
					t.Fatalf("aday metrikleri %v, beklenen %v", got, tt.want)
				}
			}
		})
	}

	// Kıtlık sayımı kapıdan ETKİLENMEZ (servis başına bir kez, metrikten bağımsız).
	short := []behaviorRow{{Unix: cutoff - 86400, HOW: 10, Spans: 100}, {Unix: cutoff, HOW: 10, Spans: 100}}
	_, sBatch := behaviorFleetCandidates(map[string][]behaviorRow{"orders-batch": short}, nil, cutoff, def, nil)
	_, sPlain := behaviorFleetCandidates(map[string][]behaviorRow{"payments-api": short}, nil, cutoff, def, nil)
	if sBatch != sPlain || sBatch == 0 {
		t.Fatalf("kıtlık sayımı batch kapısından etkilendi: batch=%d sıradan=%d", sBatch, sPlain)
	}
}

func TestBatchLoadSkipped(t *testing.T) {
	def := chstore.DefaultAnomalySensitivity()
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty
	tests := []struct {
		cfg     chstore.AnomalySensitivityConfig
		svc, m  string
		skipped bool
	}{
		{def, "orders-batch", "request_rate", true},
		{def, "ORDERS-BATCH-worker", "request_rate", true},
		// Aşırı susturma pinleri — yalnız YÜK susar.
		{def, "orders-batch", "error_rate", false},
		{def, "orders-batch", "p99_ms", false},
		{def, "orders-batch", "service_silent", false},
		{def, "payments-api", "request_rate", false},
		{off, "orders-batch", "request_rate", false},
	}
	for _, tt := range tests {
		if got := batchLoadSkipped(tt.cfg, tt.svc, tt.m); got != tt.skipped {
			t.Errorf("batchLoadSkipped(%s, %s) = %v, beklenen %v", tt.svc, tt.m, got, tt.skipped)
		}
	}
}

// TestBatchLoadResolutions — açık batch request_rate problemi açık
// gerekçeyle kapanır; başka hiçbir satıra dokunulmaz; snapshot'taki satır
// değiştirilmez; kapanan satır aynı tikte kümeleme adayı olmaz.
func TestBatchLoadResolutions(t *testing.T) {
	now := time.Now()
	started := now.Add(-5 * time.Minute).UnixNano()
	mk := func(id, svc, metric, status string) chstore.Problem {
		return chstore.Problem{
			ID: id, RuleID: "anomaly:" + svc + ":" + metric, RuleName: "Anomaly · x",
			Service: svc, Metric: metric, Status: status, Severity: "critical",
			Value: 2000, Threshold: 100, Description: "Request rate spiked on " + svc + ".",
			StartedAt: started,
		}
	}
	ext := mk("p-ext", "orders-batch", "request_rate", "open")
	ext.Kind = chstore.ProblemKindExternal
	promoted := mk("p-auto", "orders-batch", "anomaly_ratio", "open")
	promoted.RuleID = "anomaly-auto:abc123"
	cluster := mk("p-cl", "orders-batch", "request_rate", "open")
	cluster.RuleID = "anomaly-cluster:orders-batch"
	rows := []chstore.Problem{
		mk("p-rr", "orders-batch", "request_rate", "open"),
		mk("p-ack", "nightly-batch", "request_rate", "acknowledged"),
		mk("p-er", "orders-batch", "error_rate", "open"),
		mk("p-p99", "orders-batch", "p99_ms", "open"),
		mk("p-silent", "orders-batch", "service_silent", "open"),
		mk("p-plain", "payments-api", "request_rate", "open"),
		ext, promoted, cluster,
	}
	snap := chstore.NewOpenProblems(rows)
	nowNs := now.UnixNano()

	got := batchLoadResolutions(snap.All(), chstore.DefaultAnomalySensitivity(), nowNs)
	ids := map[string]chstore.Problem{}
	for _, q := range got {
		ids[q.ID] = q
	}
	if len(ids) != 2 || ids["p-rr"].ID == "" || ids["p-ack"].ID == "" {
		t.Fatalf("yalnız iki batch request_rate satırı kapanmalıydı, got %v", keysOf(ids))
	}
	for _, q := range got {
		if q.Status != "resolved" || q.ResolvedAt == nil || *q.ResolvedAt != nowNs {
			t.Fatalf("%s kapanmış olarak işaretlenmedi: %+v", q.ID, q)
		}
		if !strings.Contains(q.Description, "batch servis — yük sinyali anomali sayılmaz") {
			t.Fatalf("%s gerekçesi dürüst değil: %q", q.ID, q.Description)
		}
		if strings.Contains(q.Description, "source silent") || strings.Contains(q.Description, "signal loss") {
			t.Fatalf("%s 'sustu' gerekçesi taşıyor: %q", q.ID, q.Description)
		}
		// Value (anomali anındaki değer) KORUNUR (v0.9.977).
		if q.Value != 2000 || q.Threshold != 100 {
			t.Fatalf("%s değer/eşik ezildi: %v/%v", q.ID, q.Value, q.Threshold)
		}
	}
	// Snapshot'taki satır değişmedi (5 sn'lik memo başka okuyuculara da gider).
	if p := snap.ByID("p-rr"); p == nil || p.Status != "open" || strings.Contains(p.Description, "batch servis") {
		t.Fatalf("snapshot satırı yerinde değiştirildi: %+v", p)
	}
	// İdempotent gerekçe: zaten damgalı açıklama ikinci kez damgalanmaz.
	again := batchLoadResolutions([]*chstore.Problem{&got[0]}, chstore.DefaultAnomalySensitivity(), nowNs)
	if len(again) == 1 && strings.Count(again[0].Description, batchLoadResolveNote) != 1 {
		t.Fatal("gerekçe iki kez eklendi")
	}

	// Kural kapalı → hiçbir şey kapanmaz.
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty
	if r := batchLoadResolutions(snap.All(), off, nowNs); len(r) != 0 {
		t.Fatalf("kural kapalıyken %d satır kapandı", len(r))
	}
	// Snapshot okunamadı (nil) → hiçbir şey kapanmaz.
	var nilSnap *chstore.OpenProblems
	if r := batchLoadResolutions(nilSnap.All(), chstore.DefaultAnomalySensitivity(), nowNs); len(r) != 0 {
		t.Fatal("nil snapshot'ta satır kapandı")
	}

	// Aynı tik kümeleme: kapanan batch satırı join-on-open adayı OLMAZ,
	// batch olmayan request_rate satırı aday kalır.
	tracked := map[string]bool{"request_rate": true, "error_rate": true, "p99_ms": true}
	cands := recentOpenCandidates(snap.All(), now, clusterJoinWindow, batchLoadResolvingKeys(got), tracked)
	seen := map[string]bool{}
	for _, c := range cands {
		seen[c.Service+"/"+c.Metric] = true
	}
	if seen["orders-batch/request_rate"] || seen["nightly-batch/request_rate"] {
		t.Fatalf("kapanan batch satırı kümeleme adayı kaldı: %v", seen)
	}
	if !seen["payments-api/request_rate"] || !seen["orders-batch/error_rate"] {
		t.Fatalf("batch dışı / yük dışı satırlar adaylıktan düştü: %v", seen)
	}
}

// TestResolveBatchLoadSkippedUntilSettingsConfirmed — v0.10.1039 (inceleme):
// ayar bu süreçte hiç BAŞARIYLA okunmadıysa (hiç yüklenmemiş ya da yalnız
// hata almış → yayınlanan değer varsayılan TAHMİNİ) kapatma geçişi HİÇBİR
// ŞEY kapatmaz. Store bağlantısız: geçiş UpsertProblem'e uzanırsa test
// çöker — kapının upsert'ten önce durduğunun kanıtı.
func TestResolveBatchLoadSkippedUntilSettingsConfirmed(t *testing.T) {
	store := &chstore.Store{}
	d := &Detector{store: store}
	snap := chstore.NewOpenProblems([]chstore.Problem{{
		ID: "p-rr", RuleID: "anomaly:orders-batch:request_rate", Service: "orders-batch",
		Metric: "request_rate", Status: "open", Severity: "critical",
	}})
	// Varsayılan liste batch servisi seçiyor — kapı olmasa satır kapanırdı.
	if len(batchLoadResolutions(snap.All(), chstore.DefaultAnomalySensitivity(), 1)) != 1 {
		t.Fatal("fikstür: varsayılan listeyle satır kapanmalıydı")
	}
	if store.AnomalySensitivityConfirmed() {
		t.Fatal("bağlantısız Store doğrulanmış görünüyor")
	}
	if got := d.resolveBatchLoadProblems(context.Background(), snap, chstore.DefaultAnomalySensitivity()); len(got) != 0 {
		t.Fatalf("doğrulanmamış ayarla kapatma yapıldı: %v", got)
	}
	// scan'in okuduğu görünüm de batch kuralını devre dışı gösteriyor (kemer).
	if store.AnomalySensitivityForDetectors().IsBatchService("orders-batch") {
		t.Fatal("doğrulanmamış ayarda karar noktaları batch kuralını etkin görüyor")
	}
}

func keysOf(m map[string]chstore.Problem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestScanWiresBatchLoadGate — scan() kablosu: kapı faz 1'de karardan
// ÖNCE, kapatma geçişi kümelemeden ÖNCE ve kapananlar `resolving`'e
// giriyor. Yeni CH okuması YOK (geçiş snapshot'tan çalışır).
func TestScanWiresBatchLoadGate(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) scan(")
	if i < 0 {
		t.Fatal("scan bulunamadı")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	// Tik okuması ForDetectors: doğrulanmamış ayarda batch kuralı devre dışı.
	if !strings.Contains(body, "sens := d.store.AnomalySensitivityForDetectors()") {
		t.Fatal("scan batch kuralını doğrulanmamış ayardan da uygulayabiliyor (ForDetectors okunmuyor)")
	}
	gate := strings.Index(body, "if batchLoadSkipped(sens, svc, m) {")
	eval := strings.Index(body, "evaluateAnomaly(m, buckets")
	pass := strings.Index(body, "batchResolved := d.resolveBatchLoadProblems(ctx, snap, sens)")
	merge := strings.Index(body, "resolving[k] = true")
	join := strings.Index(body, "recentOpenCandidates(")
	if gate < 0 || eval < 0 || pass < 0 || merge < 0 || join < 0 {
		t.Fatalf("kablo parçaları bulunamadı (kapı %d, karar %d, geçiş %d, birleşim %d, join %d)", gate, eval, pass, merge, join)
	}
	if !(gate < eval && eval < pass && pass < merge && merge < join) {
		t.Fatal("sıra bozuk: kapı → karar → kapatma geçişi → resolving → join-on-open bekleniyordu")
	}

	b, err := os.ReadFile("batch_load.go")
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(string(b), "")
	for _, forbidden := range []string{"OpenProblemsSnapshot(", "FindOpenProblem(", "Query(", "GetAnomalySensitivity("} {
		if strings.Contains(code, forbidden) {
			t.Fatalf("batch_load.go %s çağırıyor — geçiş tikin snapshot'ı ve atomic ayarıyla çalışmalı", forbidden)
		}
	}
}
