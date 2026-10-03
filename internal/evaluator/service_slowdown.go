package evaluator

// service_slowdown.go — v0.10.1091: "yaygın yavaşlama" hızlı yolu
// (`svc-slowdown:<servis>`).
//
// Operatör (prod, iki gün üst üste): "Dün söylediğim CRM sorunu yine oldu, bir
// sürü anomali geldi ama P1 problem gelmedi." ~10 dk'lık ağır olay: bir
// servisin birçok operasyonunda p99 ms'lerden 15–20 s'ye çıktı, trafik ~%40
// düştü; 20 operasyon anomalisi (×700–×2000) açılıp 5–10 dk'da "temizlendi",
// P1 Problem yok — bu hafta eklenen her kural 2 ardışık kova / 10 dk istiyor,
// operasyon anomalileri bilinçli P3. Onay: "Onay".
//
// Kural (vidalar anomaly_sensitivity.serviceSlowdown; lider tikinde):
//   - OPERASYON KOLU — TEK tamamlanmış 5 dk kovada (sürdürme YOK) aynı
//     servisin ≥ MinOps (3) farklı operasyonunun HER BİRİ: ≥ MinCallsPerOp
//     (30) çağrı, p99 ≥ MinP99Ms (5000 ms), p99 ≥ RiseFactor (20) × kendi
//     24 sa HAVUZLANMIŞ p95 tabanı (cari kova hariç; dünkü kısa olay tabanı
//     şişirmesin) VE YAVAŞ PAY: kova p95'i ≥ MinP99Ms/2 (inceleme A — tek
//     yavaş trace'in iç içe span'leri üç operasyonun p99'unu birden şişirir,
//     p95'ini değil; ~%5'ten fazla çağrı yavaş olmalı); servisin kovadaki
//     toplam çağrısı ≥ MinCallsTotal (100); servis batch DEĞİL.
//   - TRAFİK ÇÖKÜŞÜ KOLU (aynı pas, aynı problem id) — servisin kova çağrısı ≤
//     (1 − DropPct/100) × önceki saatin kova başı ortalaması (vars. %40 düşüş)
//     VE servis p99'u ≥ 3 × 24 sa p95 tabanı VE önceki saatin kova başı
//     ortalaması ≥ MinCallsTotal VE (inceleme B) cari kovada ≥ MinCallsTotal
//     çağrı VE servis p99'u ≥ MinP99Ms; servis batch değil.
//   - Problem SERVİS başına tek: id = kural id = "svc-slowdown:<servis>",
//     kategori SLOWDOWN (metrik p99_ms), şiddet DAİMA critical. Operasyon
//     kolunda Value = en yüksek operasyon p99'u, Threshold = O operasyonun 24 sa
//     tabanı (oran ≥ RiseFactor ≥ 3 → kural ateşlediğinde DAİMA computePriority
//     P1; operatör: tetiklenince her zaman P1 — 5.5 s / 120 ms dahil); yalnız
//     çöküş kolunda Value = servis p99'u, Threshold = servisin 24 sa p99 tabanı
//     (≥ 3× → P1). Problem eşiği GÖRÜNTÜ eşiği DEĞİL: detay grafiğinin çizgisi
//     ve yavaş trace süzgeci ayardaki MinP99Ms'i kullanır (sunucu dizi
//     cevabının `threshold` alanı, api/alert_rule_series.go). Açıkken ölçü
//     yalnız YÜKSELİR (oranı daha büyük ölçü gelince) — öncelik olay ortasında
//     P1'den düşmez; açıklama her kovada günceldir.
//   - TAZELEME / KAPANIŞ — en yeni tamamlanmış kova kuralı tuttukça tazelenir;
//     ClearBuckets (2) ARDIŞIK temiz tamamlanmış kovada kapanır (histerezis).
//     Kesik okumada (satır tavanı) görünmeyen servis temiz SAYILMAZ.
//
// Neden tek kova burada güvenli (diğer kurallar 2 kova istiyor): bağımsız
// tabanlar birlikte — mutlak (p99 ≥ 5 s), göreli (≥ 20× KENDİ tabanı), hacim
// (≥ 30 çağrı / operasyon, ≥ 100 / servis), YAVAŞ PAY (p95 ≥ 2.5 s) — ve
// GENİŞLİK (≥ 3 operasyon aynı anda). p99 tek başına yetmez: iç içe span'leri
// olan TEK yavaş trace (sunucu + iç + istemci operasyonu) ~40 çağrılık üç
// operasyonun p99'unu birden çeker; p95 tabanı bunu ayırır (≥ ~%6 yavaş çağrı).
//
// Ölçüm sıklığı (aiops §2 soru 1): okuma TAMAMLANMIŞ KOVA BAŞINA bir kez (iki MV
// sorgusu, chstore/service_slowdown.go); aynı kovanın sonraki tiklerinde karar
// bellekten sunulur ve açık satırlar her tik tazelenir — bayat süpürme "kaynak
// sustu" demez. Fırtına tavanı: tik başına ≤ MaxNewPerTick (10) açılış, en kötü
// önce; kalanlar aynı kovanın sonraki tiklerinde açılır.
//
// Yumuşak-hata yönü (aiops §5, v0.10.1073 emsali): ayar bu süreçte doğrulanmadı
// / okuma düştü / anlık görüntü yok → açık problemler DEĞİŞMEDEN tazelenir,
// hiçbir şey açılmaz/kapanmaz (bayat süpürme olay ortasında P1'i kapatmasın).
// Kural kapalı → açık satırlar "rule disabled" gerekçesiyle kapanır.
//
// Operasyon anomalileri (trace_op_latency) BAĞIMSIZ çalışmaya devam eder —
// bastırılmaz; bu kural onların üstüne servis düzeyinde tek P1 ekler.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

const (
	// svcSlowSettle — kova bittikten sonra okumadan önce beklenen pay (geç
	// gelen span'ler MV'ye düşsün; async insert birkaç saniye).
	svcSlowSettle = 30 * time.Second
	// svcSlowRuleName — Problem'in kural adı (özet cümlesi bununla başlar).
	svcSlowRuleName = "Yaygın yavaşlama"
	// svcSlowRuleNameCollapse — YALNIZ trafik çöküşü koluyla açılan satırın adı
	// (inceleme E): detay grafiği bu adda "op tabanı" çizgisini çizmez — orada
	// eşik operasyon tabanı değil. FE ikizi alertMetricSeries.ts
	// SVC_SLOWDOWN_COLLAPSE_RULE_NAME (testle pinli).
	svcSlowRuleNameCollapse = "Yaygın yavaşlama · trafik çöküşü"
	// svcSlowMetric — Problem metriği: servis p99'u (kategori SLOWDOWN; detay
	// sayfasının alarm grafiği bu seriyi çizer).
	svcSlowMetric = "p99_ms"
	// svcSlowDisabledNote — kural kapatılınca açık satırın DÜRÜST gerekçesi.
	svcSlowDisabledNote = "· resolved: rule disabled (yaygın yavaşlama kapalı — Settings → Anomaly)"
	// svcSlowTopOps — açıklamadaki en yavaş operasyon sayısı.
	svcSlowTopOps = 3
)

// svcSlowLatestBucket — SAF: kararın kovası = en yeni TAMAMLANMIŞ 5 dk kova
// (svcSlowSettle payıyla).
func svcSlowLatestBucket(now time.Time) time.Time {
	return now.Add(-svcSlowSettle).Truncate(chstore.SvcSlowdownBucket).Add(-chstore.SvcSlowdownBucket)
}

// svcSlowOp — tabanları geçen bir operasyon (BaseMs = 24 sa p95 tabanı).
type svcSlowOp struct {
	Operation        string
	CurP99Ms, BaseMs float64
	CurCalls         uint64
}

// svcSlowVerdict — bir servisin bu kovadaki kararı.
type svcSlowVerdict struct {
	Service      string
	ID           string
	Bucket       time.Time
	Fire         bool
	OpFire       bool
	CollapseFire bool
	// Ops — tabanları geçen operasyonlar, p99 azalan (OpFire false olsa da).
	Ops []svcSlowOp
	// TotalCalls — servisin kova çağrısı (servis özeti; yoksa operasyon toplamı
	// — alt sınır).
	TotalCalls uint64
	Svc        chstore.SvcSlowServiceRow
	HasSvc     bool
	Value      float64
	Threshold  float64
}

// svcSlowOpQualifies — SAF: operasyon satırı kuralın tabanlarını geçiyor mu
// (SQL HAVING'in Go ikizi; nihai hüküm burada): hacim, mutlak p99, kat (24 sa
// p95 tabanına göre) ve yavaş pay (kova p95'i ≥ MinP99Ms/2).
func svcSlowOpQualifies(r chstore.SvcSlowOpRow, cfg chstore.ServiceSlowdownConfig) bool {
	minCalls := uint64(cfg.MinCallsPerOp)
	return r.CurCalls >= minCalls && r.BaseCalls >= minCalls &&
		r.BaseP95Ms > 0 && r.CurP99Ms >= cfg.MinP99Ms && r.CurP99Ms >= cfg.RiseFactor*r.BaseP95Ms &&
		r.CurP95Ms >= chstore.SvcSlowP95Floor(cfg)
}

// svcSlowCollapse — SAF: trafik çöküşü kolu (SQL HAVING'in Go ikizi, aynı
// çapraz çarpım). v0.10.1091 inceleme B: cari kovada ≥ MinCallsTotal çağrı VE
// servis p99'u ≥ MinP99Ms (sessiz serviste tek 300 ms'lik istek açmasın).
func svcSlowCollapse(s chstore.SvcSlowServiceRow, cfg chstore.ServiceSlowdownConfig) bool {
	hour := float64(s.HourCalls)
	return s.HourCalls >= uint64(cfg.MinCallsTotal)*chstore.SvcSlowHourBuckets &&
		float64(s.CurCalls)*100*chstore.SvcSlowHourBuckets <= (100-cfg.DropPct)*hour &&
		s.CurCalls >= uint64(cfg.MinCallsTotal) && s.CurP99Ms >= cfg.MinP99Ms &&
		s.BaseP95Ms > 0 && s.CurP99Ms >= chstore.SvcSlowCollapseP99Factor*s.BaseP95Ms
}

// svcSlowCandidates — SAF: operasyon kolunda ≥ MinOps operasyonu tabanları
// geçen servisler (servis özeti bunların toplam çağrısını getirir), sıralı.
func svcSlowCandidates(ops []chstore.SvcSlowOpRow, cfg chstore.ServiceSlowdownConfig) []string {
	n := map[string]int{}
	for _, r := range ops {
		if svcSlowOpQualifies(r, cfg) {
			n[r.Service]++
		}
	}
	out := make([]string, 0, len(n))
	for s, c := range n {
		if c >= cfg.MinOps {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// svcSlowClassify — SAF, tablo-testli: iki okumanın satırları → servis başına
// karar (yalnız satırı olan servisler). isBatch nil = batch yüklemi yok.
func svcSlowClassify(bucket time.Time, ops []chstore.SvcSlowOpRow, svcs []chstore.SvcSlowServiceRow,
	cfg chstore.ServiceSlowdownConfig, isBatch func(string) bool) map[string]svcSlowVerdict {
	out := map[string]svcSlowVerdict{}
	get := func(s string) svcSlowVerdict {
		v, ok := out[s]
		if !ok {
			v = svcSlowVerdict{Service: s, ID: chstore.SvcSlowdownRuleID(s), Bucket: bucket}
		}
		return v
	}
	var opCalls = map[string]uint64{}
	for _, r := range ops {
		if !svcSlowOpQualifies(r, cfg) {
			continue
		}
		v := get(r.Service)
		v.Ops = append(v.Ops, svcSlowOp{Operation: r.Operation, CurP99Ms: r.CurP99Ms, BaseMs: r.BaseP95Ms, CurCalls: r.CurCalls})
		opCalls[r.Service] += r.CurCalls
		out[r.Service] = v
	}
	for _, s := range svcs {
		v := get(s.Service)
		v.Svc, v.HasSvc = s, true
		out[s.Service] = v
	}
	for s, v := range out {
		sort.SliceStable(v.Ops, func(i, j int) bool {
			if v.Ops[i].CurP99Ms != v.Ops[j].CurP99Ms {
				return v.Ops[i].CurP99Ms > v.Ops[j].CurP99Ms
			}
			return v.Ops[i].Operation < v.Ops[j].Operation
		})
		// Toplam: servis özeti; yoksa (ya da daha küçükse — farklı okuma anı)
		// tabanı geçen operasyonların toplamı, gerçek toplamın alt sınırı.
		v.TotalCalls = opCalls[s]
		if v.HasSvc && v.Svc.CurCalls > v.TotalCalls {
			v.TotalCalls = v.Svc.CurCalls
		}
		batch := isBatch != nil && isBatch(s)
		v.OpFire = !batch && len(v.Ops) >= cfg.MinOps && v.TotalCalls >= uint64(cfg.MinCallsTotal)
		v.CollapseFire = !batch && v.HasSvc && svcSlowCollapse(v.Svc, cfg)
		v.Fire = v.OpFire || v.CollapseFire
		switch {
		case v.OpFire:
			// Value = en yavaş operasyonun p99'u, Threshold = O operasyonun kendi
			// tabanı: oran ≥ RiseFactor (≥ 3, vars. 20) → kural ateşlediğinde
			// DAİMA ≥ 2× → P1 (operatör: kural tetiklenince her zaman P1).
			v.Value, v.Threshold = v.Ops[0].CurP99Ms, v.Ops[0].BaseMs
		case v.CollapseFire:
			v.Value, v.Threshold = v.Svc.CurP99Ms, v.Svc.BaseP95Ms
		}
		out[s] = v
	}
	return out
}

// svcSlowFmtMs — "18.2 s" / "120 ms" (açıklama biçimi; saniyede tek ondalık).
func svcSlowFmtMs(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1f s", ms/1000)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

// svcSlowTrafficDelta — SAF: kova çağrısının önceki saatin kova başı
// ortalamasına göre yüzde değişimi; ok=false: taban yok.
func svcSlowTrafficDelta(s chstore.SvcSlowServiceRow) (float64, bool) {
	if s.HourCalls == 0 {
		return 0, false
	}
	avg := float64(s.HourCalls) / chstore.SvcSlowHourBuckets
	return (float64(s.CurCalls)/avg - 1) * 100, true
}

// svcSlowTrafficText — "trafik −%40" / "trafik +%12" ("" = taban yok).
func svcSlowTrafficText(v svcSlowVerdict) string {
	if !v.HasSvc {
		return ""
	}
	d, ok := svcSlowTrafficDelta(v.Svc)
	if !ok {
		return ""
	}
	r := int(math.Round(d))
	if r < 0 {
		return fmt.Sprintf("trafik −%%%d", -r)
	}
	return fmt.Sprintf("trafik +%%%d", r)
}

// svcSlowOpLabel — operasyon adı, uzunsa kısaltılmış.
func svcSlowOpLabel(op string) string {
	const max = 80
	if len([]rune(op)) > max {
		return string([]rune(op)[:max]) + "…"
	}
	return op
}

// svcSlowReason — TEK düz Türkçe cümle (+ en yavaş ≤ 3 operasyon). loc =
// kova damgasının dilimi (operatör dilimi, tzdefault).
func svcSlowReason(v svcSlowVerdict, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	stamp := v.Bucket.In(loc).Format("02.01 15:04")
	var parts []string
	if len(v.Ops) > 0 && (v.OpFire || !v.CollapseFire) {
		parts = append(parts, fmt.Sprintf("%d operasyon, p99 en çok %s (taban %s)",
			len(v.Ops), svcSlowFmtMs(v.Ops[0].CurP99Ms), svcSlowFmtMs(v.Ops[0].BaseMs)))
	}
	if t := svcSlowTrafficText(v); t != "" {
		parts = append(parts, t)
	}
	if v.CollapseFire && !v.OpFire && v.Svc.BaseP95Ms > 0 {
		parts = append(parts, fmt.Sprintf("servis p99 %s (taban %s, %.1f×)",
			svcSlowFmtMs(v.Svc.CurP99Ms), svcSlowFmtMs(v.Svc.BaseP95Ms), v.Svc.CurP99Ms/v.Svc.BaseP95Ms))
	}
	if len(parts) == 0 {
		parts = append(parts, "eşiklerin altında")
	}
	s := fmt.Sprintf("%s servisinde yaygın yavaşlama: %s — %s kovası.", v.Service, strings.Join(parts, ", "), stamp)
	if len(v.Ops) > 0 {
		top := v.Ops
		if len(top) > svcSlowTopOps {
			top = top[:svcSlowTopOps]
		}
		items := make([]string, len(top))
		for i, o := range top {
			items[i] = fmt.Sprintf("%s %s (taban %s)", svcSlowOpLabel(o.Operation), svcSlowFmtMs(o.CurP99Ms), svcSlowFmtMs(o.BaseMs))
		}
		s += " En yavaş: " + strings.Join(items, ", ") + "."
	}
	return s
}

// svcSlowRatio — Value/Threshold (0 eşikte 0).
func svcSlowRatio(value, threshold float64) float64 {
	if threshold <= 0 {
		return 0
	}
	return value / threshold
}

// svcSlowRuleNameOf — kararın kural adı: operasyon kolu ateşlediyse temel ad,
// yalnız çöküş kolu ise çöküş adı.
func svcSlowRuleNameOf(v svcSlowVerdict) string {
	if v.OpFire || !v.CollapseFire {
		return svcSlowRuleName
	}
	return svcSlowRuleNameCollapse
}

// svcSlowProblem — SAF: açılacak Problem satırı. StartedAt = kovanın başı
// (olay o kovada başladı; bildirim ~5 dk sonra gelir).
func svcSlowProblem(v svcSlowVerdict, loc *time.Location) chstore.Problem {
	return chstore.Problem{
		ID: v.ID, RuleID: v.ID,
		RuleName:    svcSlowRuleNameOf(v),
		Severity:    "critical",
		Service:     v.Service,
		Kind:        chstore.ProblemKindService,
		Metric:      svcSlowMetric,
		Value:       v.Value,
		Threshold:   v.Threshold,
		Comparator:  ">=",
		Status:      "open",
		Description: svcSlowReason(v, loc),
		StartedAt:   v.Bucket.UnixNano(),
	}
}

// svcSlowRefreshed — SAF: kuralı hâlâ tutan açık satırın tazelenmiş KOPYASI.
// Açıklama her kovada güncel; ölçü yalnız oranı büyüdüyse değişir (öncelik
// olay ortasında P1'den düşmesin); şiddet daima critical (asla inmez).
func svcSlowRefreshed(q chstore.Problem, v svcSlowVerdict, loc *time.Location) chstore.Problem {
	q.Description = svcSlowReason(v, loc)
	// Ad yapışkan: operasyon kolu bir kez ateşlediyse temel ad kalır (grafik
	// op tabanı çizgisini çizer); yalnız çöküşle sürerse çöküş adı.
	if v.OpFire || q.RuleName == svcSlowRuleName {
		q.RuleName = svcSlowRuleName
	} else {
		q.RuleName = svcSlowRuleNameCollapse
	}
	if svcSlowRatio(v.Value, v.Threshold) >= svcSlowRatio(q.Value, q.Threshold) {
		q.Metric, q.Value, q.Threshold = svcSlowMetric, v.Value, v.Threshold
	}
	q.Severity = "critical"
	return q
}

// svcSlowOpenOrder — SAF: açılış adayları en kötü önce (oran azalan, eşitlikte
// servis adı).
func svcSlowOpenOrder(vs []svcSlowVerdict) {
	sort.SliceStable(vs, func(i, j int) bool {
		ri, rj := svcSlowRatio(vs[i].Value, vs[i].Threshold), svcSlowRatio(vs[j].Value, vs[j].Threshold)
		if ri != rj {
			return ri > rj
		}
		return vs[i].Service < vs[j].Service
	})
}

// svcSlowOpenRows — SAF: anlık görüntüdeki açık/onaylı svc-slowdown satırları.
func svcSlowOpenRows(all []*chstore.Problem) []*chstore.Problem {
	var out []*chstore.Problem
	for _, p := range all {
		if p == nil || p.ID == "" || !strings.HasPrefix(p.RuleID, chstore.RuleSvcSlowdownPrefix) {
			continue
		}
		if p.Status != "open" && p.Status != "acknowledged" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// svcSlowServiceOf — kural id'sinden servis.
func svcSlowServiceOf(p *chstore.Problem) string {
	return strings.TrimPrefix(p.RuleID, chstore.RuleSvcSlowdownPrefix)
}

// svcSlowReads — iki okuma (test dikişi: sahte okuyucu).
type svcSlowReads struct {
	ops  func() ([]chstore.SvcSlowOpRow, bool, error)
	svcs func(services []string) ([]chstore.SvcSlowServiceRow, bool, error)
}

// svcSlowActions — bir tikin yazımları (I/O çağıranda).
type svcSlowActions struct {
	Keep    []chstore.Problem // değişmeden tazele (bayat süpürme görmesin)
	Refresh []chstore.Problem // ölçü/açıklama güncel
	Resolve []chstore.Problem
	Open    []chstore.Problem // + incident + bildirim
}

// svcSlowMemo — kova başına ölçüm belleği + ardışık temiz kova sayaçları +
// tavan dışı kalan açılış adayları. Bellekte ve yalnız liderde (db-health
// emsali): lider değişimi bir sonraki tikte yeni ölçüm yapar, kapanışı en çok
// ClearBuckets kova geciktirir.
type svcSlowMemo struct {
	mu      sync.Mutex
	bucket  time.Time
	clean   map[string]int
	pending []svcSlowVerdict
	// failBucket / fails — inceleme C: okuma hatası kova başına EN ÇOK bir kez
	// yeniden denenir (iki deneme); sonra kova değişene dek okuma yok, açıklar
	// yalnız tazelenir — 10 s'lik bütçe her tik evaluateAll'a binmesin.
	failBucket time.Time
	fails      int
}

// svcSlowMaxAttempts — kova başına okuma denemesi (ilk + bir yeniden deneme).
const svcSlowMaxAttempts = 2

// errSvcSlowGaveUp — kova için deneme hakkı bitti (log seli olmasın diye
// çağıran bunu sessizce geçer).
var errSvcSlowGaveUp = errors.New("svc-slowdown: bu kova için okuma denemesi bitti")

// keepAll — karar verilemeyen tikin yazımları: açıklar değişmeden.
func keepAll(open []*chstore.Problem) svcSlowActions {
	var a svcSlowActions
	for _, p := range open {
		a.Keep = append(a.Keep, *p)
	}
	return a
}

// step — tikin kararı. Yeni tamamlanmış kova varsa iki okuma + karar
// (açıklara: tazele / histerezis / kapat), yoksa açıklar değişmeden tazelenir.
// Okuma hatası → keepAll + hata (bellek İLERLEMEZ — sonraki tik yeniden okur).
// Açılışlar tik başına ≤ MaxNewPerTick; kalanlar aynı kovanın sonraki
// tiklerine.
func (m *svcSlowMemo) step(now time.Time, cfg chstore.ServiceSlowdownConfig, isBatch func(string) bool,
	open []*chstore.Problem, rd svcSlowReads, loc *time.Location) (svcSlowActions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clean == nil {
		m.clean = map[string]int{}
	}
	openBySvc := make(map[string]*chstore.Problem, len(open))
	for _, p := range open {
		openBySvc[svcSlowServiceOf(p)] = p
	}
	for s := range m.clean {
		if openBySvc[s] == nil {
			delete(m.clean, s)
		}
	}
	var act svcSlowActions
	bucket := svcSlowLatestBucket(now)
	if !bucket.Equal(m.failBucket) {
		m.failBucket, m.fails = bucket, 0
	}
	if bucket.Equal(m.bucket) {
		act = keepAll(open)
	} else if m.fails >= svcSlowMaxAttempts {
		return keepAll(open), errSvcSlowGaveUp
	} else {
		ops, opsTrunc, err := rd.ops()
		if err != nil {
			m.fails++
			return keepAll(open), fmt.Errorf("operasyon okuması (deneme %d/%d): %w", m.fails, svcSlowMaxAttempts, err)
		}
		cands := svcSlowCandidates(ops, cfg)
		seen := make(map[string]bool, len(cands)+len(openBySvc))
		for _, s := range cands {
			seen[s] = true
		}
		for s := range openBySvc {
			if !seen[s] {
				cands = append(cands, s)
			}
		}
		sort.Strings(cands)
		svcs, svcTrunc, err := rd.svcs(cands)
		if err != nil {
			m.fails++
			return keepAll(open), fmt.Errorf("servis okuması (deneme %d/%d): %w", m.fails, svcSlowMaxAttempts, err)
		}
		truncated := opsTrunc || svcTrunc
		verdicts := svcSlowClassify(bucket, ops, svcs, cfg, isBatch)
		m.bucket, m.pending = bucket, nil
		for _, p := range open {
			svc := svcSlowServiceOf(p)
			v, ok := verdicts[svc]
			switch {
			case ok && v.Fire:
				delete(m.clean, svc)
				act.Refresh = append(act.Refresh, svcSlowRefreshed(*p, v, loc))
			case truncated:
				// Kesik okuma: görünmeyen servis bilinmiyor, temiz değil.
				act.Keep = append(act.Keep, *p)
			default:
				m.clean[svc]++
				if m.clean[svc] >= cfg.ClearBuckets {
					delete(m.clean, svc)
					q := *p
					chstore.MarkResolved(&q, now.UnixNano())
					act.Resolve = append(act.Resolve, q)
				} else {
					act.Keep = append(act.Keep, *p)
				}
			}
		}
		for s, v := range verdicts {
			if v.Fire && openBySvc[s] == nil {
				m.pending = append(m.pending, v)
			}
		}
		svcSlowOpenOrder(m.pending)
		if truncated {
			log.Printf("[evaluator] svc-slowdown: okuma satır tavanına dayandı — bu kovada açık problem kapatılmaz")
		}
	}
	// Açılışlar (tavanlı, en kötü önce). Arada açılmış olan atlanır.
	n := 0
	rest := m.pending[:0]
	for _, v := range m.pending {
		if openBySvc[v.Service] != nil {
			continue
		}
		if n >= cfg.MaxNewPerTick {
			rest = append(rest, v)
			continue
		}
		act.Open = append(act.Open, svcSlowProblem(v, loc))
		n++
	}
	if len(rest) > 0 {
		log.Printf("[evaluator] svc-slowdown: tik tavanı %d — %d açılış sonraki tike", cfg.MaxNewPerTick, len(rest))
	}
	m.pending = rest
	return act, nil
}

// svcSlowDisabledResolutions — SAF: kural kapalıyken açık satırların kapanmış
// KOPYALARI, dürüst gerekçeyle (gerekçe iki kez eklenmez; Value ezilmez).
func svcSlowDisabledResolutions(open []*chstore.Problem, nowNs int64) []chstore.Problem {
	return dbHealthResolutionsWith(open, svcSlowDisabledNote, nowNs)
}

// evaluateServiceSlowdown — evaluateAll pası (db-health'ten SONRA, runtime'dan
// ÖNCE; yaş eskalasyonu ve bayat süpürmeden ÖNCE). Lider tikinde koşar; açık
// problemler tik başına tek anlık görüntüden.
func (e *Evaluator) evaluateServiceSlowdown(ctx context.Context) {
	snap, err := e.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		log.Printf("[evaluator] svc-slowdown: açık problem anlık görüntüsü alınamadı, tik atlanıyor: %v", err)
		return
	}
	open := svcSlowOpenRows(snap.All())
	// Keep-last-good: blob 30 sn'de bir tazelenir, okuma hatası son değeri
	// korur. Bu süreçte HİÇ doğrulanmadıysa yayınlanan değer tahmindir (kural
	// kapalıysa açık satırları kapatır, batch listesi boştur) → karar yok.
	if !e.store.AnomalySensitivityConfirmed() {
		e.applySvcSlow(ctx, keepAll(open))
		return
	}
	sens := e.store.AnomalySensitivityForDetectors()
	cfg := sens.ServiceSlowdown
	now := time.Now()
	if !cfg.On() {
		e.applySvcSlow(ctx, svcSlowActions{Resolve: svcSlowDisabledResolutions(open, now.UnixNano())})
		return
	}
	// İki okumanın TOPLAM bütçesi ~10 s (inceleme C); okuma yoksa bağlam boşta.
	rctx, cancel := context.WithTimeout(ctx, chstore.SvcSlowReadTimeout)
	defer cancel()
	act, err := e.svcSlow.step(now, cfg, sens.IsBatchService, open, svcSlowReads{
		ops: func() ([]chstore.SvcSlowOpRow, bool, error) {
			return e.store.ServiceSlowdownOps(rctx, svcSlowLatestBucket(now), cfg, sens)
		},
		svcs: func(services []string) ([]chstore.SvcSlowServiceRow, bool, error) {
			return e.store.ServiceSlowdownServices(rctx, svcSlowLatestBucket(now), cfg, sens, services)
		},
	}, tzdefault.Location())
	if err != nil && !errors.Is(err, errSvcSlowGaveUp) {
		log.Printf("[evaluator] svc-slowdown read: %v — açık problemler tazelendi, karar yok", err)
	}
	e.applySvcSlow(ctx, act)
}

// applySvcSlow — tikin yazımları: tazele / kapat / aç (+ incident + bildirim).
func (e *Evaluator) applySvcSlow(ctx context.Context, act svcSlowActions) {
	for _, q := range append(act.Keep, act.Refresh...) {
		if err := e.store.UpsertProblem(ctx, q); err != nil {
			log.Printf("[evaluator] svc-slowdown refresh %s: %v", q.ID, err)
		}
	}
	for _, q := range act.Resolve {
		if err := e.store.UpsertProblem(ctx, q); err != nil {
			log.Printf("[evaluator] svc-slowdown resolve %s: %v", q.ID, err)
			continue
		}
		e.countResolved()
		log.Printf("[evaluator] PROBLEM RESOLVED (svc.slowdown): %s", q.ID)
	}
	for _, p := range act.Open {
		if err := e.store.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator] svc-slowdown open %s: %v", p.ID, err)
			continue
		}
		e.countOpened()
		log.Printf("[evaluator] PROBLEM OPENED (svc.slowdown): %s", p.Description)
		if _, err := e.store.AttachProblemToIncident(ctx, p); err != nil {
			log.Printf("[evaluator] svc-slowdown incident attach: %v", err)
		}
		if e.notifier != nil {
			go e.notifier.SendProblemAlert(context.Background(), p)
		}
	}
}
