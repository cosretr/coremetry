package evaluator

// db_health.go — v0.10.1073: veritabanı SAĞLIK kuralı (db-health).
//
// Operatör (2026-10-03): "Dün akşam CRM database'inde sorun oldu ama
// problemlerde P1 gelmedi." Prod olayı 2026-10-02 ~21:30–22:30: veritabanının
// hata oranı ~%0 → ~%10 (~15 dk), p99'u ms → ~5 s; çağıranların p99'u 8 s,
// ardından 55k HTTP 503. VERİTABANI öznesinde Problem açılmadı (db-capacity
// gauge, db-slow-stmt ifade, db_p99_ms yerleşikleri çağıran başına ve
// varsayılan kapalı).
//
// Kural (vidalar `db_slow_query` blobunun `health` alanında):
//   - 2 ardışık 5 dk kovada İHLAL: db hata % ≥ ErrorPct (5, MUTLAK) YA DA db
//     p99 ≥ P99Ms (2000) VE ≥ P99RiseFactor (3) × aynı veritabanının 24 sa
//     önceki aynı kovası (GÖRELİ — v0.10.1069: filo geneli mutlak gecikme
//     eşiği gürültü; dünkü kova yoksa p99 boyutu o kova için KAPALI); kova
//     başına ≥ MinCalls (100) çağrı VE ≥ MinCallers (2) etkilenen çağıran
//     (≥ MinCallerCalls (10) çağrılı; batch çağıranlar okumada düşer).
//   - Şiddet: hata % ≥ 2×ErrorPct ya da (ihlal eden) p99 ≥ 2×P99Ms → critical
//     (oran ≥ 2 → computePriority P1), değilse warning. Tazelemede şiddet
//     warning → critical yükselirse yeniden bildirim (P1 kanalları sayfalasın).
//   - KAPANIŞ: son iki TAMAMLANMIŞ kova temiz (iki boyut da ihlalsiz ya da
//     kova MinCalls altında / verisiz) VE bu iki ardışık okumada (tikte) böyle.
//
// Kova çiftleri: tik 1 dk, kova 5 dk. AÇILIŞ cari (yarım) kova çağrı tabanını
// geçtiyse (önceki, cari) çiftine bakabilir — olay erken yakalansın; KAPANIŞ
// yalnız tamamlanmış kovalara bakar — yarım kovanın verisiyle açılıp aynı
// veriyle kapanma (flap) olmasın.
//
// Yumuşak-hata yönü (aiops §5): ayar hiç okunamadı / ana okuma / referans
// okuması düştü → açık problemler TAZELENİR, hiçbir şey açılmaz/kapanmaz
// (yoksa bayat süpürme olay ortasında P1'i "source silent" diye kapatırdı).
// Kural kapatılınca açık problemler "rule disabled" gerekçesiyle kapanır
// (v0.10.1069 emsali; süpürmeye bırakılmaz).
//
// Karar SAF (dbHealthDecide, tablo-testli); G/Ç yalnız evaluateDBHealth'te.
// Okuma tik başına bir ana sorgu (chstore.DBHealthBuckets) + yalnız p99
// adayı varken bir referans sorgusu (chstore.DBHealthReferenceP99).

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	dbHealthBucket = 5 * time.Minute
	// dbHealthRefShift — p99 referansı: aynı kova, bir gün önce.
	dbHealthRefShift = 24 * time.Hour
	// dbHealthClearReads — kapanış için gereken ardışık "temiz" okuma (tik).
	dbHealthClearReads = 2
	// dbHealthDisabledNote — kural kapatılınca açık satırın DÜRÜST gerekçesi.
	dbHealthDisabledNote = "· resolved: rule disabled (db-health kapalı — Settings → Anomaly → Database health)"
)

type dbHealthKey struct{ System, Instance, DBName string }

func dbHealthKeyOf(ruleID string) (dbHealthKey, bool) {
	s, i, d, ok := chstore.ParseDBHealthRuleID(ruleID)
	return dbHealthKey{System: s, Instance: i, DBName: d}, ok
}

// dbHealthErrBreach / dbHealthP99Breach — boyut ihlalleri (çağrı/çağıran
// kapısından bağımsız). p99 GÖRELİ: referans yoksa daima false.
func dbHealthErrBreach(b chstore.DBHealthBucket, cfg chstore.DBHealthConfig) bool {
	return b.Calls > 0 && b.ErrorPct() >= cfg.ErrorPct
}

func dbHealthP99Breach(b chstore.DBHealthBucket, cfg chstore.DBHealthConfig) bool {
	return b.HasRef && b.RefP99Ms > 0 && b.P99Ms >= cfg.P99Ms && b.P99Ms >= cfg.P99RiseFactor*b.RefP99Ms
}

// dbHealthBreach — açılış kovası: boyut ihlali + çağrı + etkilenen çağıran.
func dbHealthBreach(b chstore.DBHealthBucket, ok bool, cfg chstore.DBHealthConfig) bool {
	return ok && b.Calls >= cfg.MinCalls && b.AffectedCallers >= uint64(cfg.MinCallers) &&
		(dbHealthErrBreach(b, cfg) || dbHealthP99Breach(b, cfg))
}

// dbHealthClear — kapanış kovası (yalnız TAMAMLANMIŞ kovalar): verisiz,
// çağrı tabanı altında (düşük hacimde sonsuz tutma → 4 sa sonra bayat-critical
// P1 olmasın) ya da iki boyut da ihlalsiz.
func dbHealthClear(b chstore.DBHealthBucket, ok bool, cfg chstore.DBHealthConfig) bool {
	if !ok || b.Calls < cfg.MinCalls {
		return true
	}
	return !dbHealthErrBreach(b, cfg) && !dbHealthP99Breach(b, cfg)
}

// dbHealthVerdict — bir veritabanının bu tikteki kararı.
type dbHealthVerdict struct {
	Key   dbHealthKey
	ID    string
	Fire  bool // açılış çiftinin iki kovası da ihlal
	Clear bool // son iki tamamlanmış kova temiz (ve okuma kesik değil)
	// Latest — açılış çiftinin en yeni VERİLİ kovası (ölçü + gerekçe).
	Latest    chstore.DBHealthBucket
	HasLatest bool
	Metric    string
	Value     float64
	Threshold float64
	Severity  string
}

// dbHealthMeasure — SAF: kovanın baskın boyutu (değer/taban oranı en yüksek
// İHLAL EDEN boyut; Value/Threshold dürüst oran taşısın) ve şiddeti.
func dbHealthMeasure(b chstore.DBHealthBucket, cfg chstore.DBHealthConfig) (metric string, value, threshold float64, severity string) {
	errPct := b.ErrorPct()
	p99On := dbHealthP99Breach(b, cfg)
	metric, value, threshold = chstore.DBHealthMetricErrorPct, errPct, cfg.ErrorPct
	if p99On && (!dbHealthErrBreach(b, cfg) || b.P99Ms/cfg.P99Ms > errPct/cfg.ErrorPct) {
		metric, value, threshold = chstore.DBHealthMetricP99Ms, b.P99Ms, cfg.P99Ms
	}
	severity = "warning"
	if errPct >= 2*cfg.ErrorPct || (p99On && b.P99Ms >= 2*cfg.P99Ms) {
		severity = "critical"
	}
	return metric, value, threshold, severity
}

// dbHealthDecide — SAF: (db, kova) satırları + cari kova başı + açık problem
// id'leri → veritabanı başına karar. Açık olup okumada HİÇ görünmeyen
// veritabanı (HAVING açık id'leri hep geçirir → gerçekten verisiz) kesik
// olmayan okumada temiz sayılır. Kesik okumada hiçbir şey temiz değildir
// (eksik satır = bilinmiyor). Çıktı kural id'sine göre sıralı.
func dbHealthDecide(rows []chstore.DBHealthBucket, cfg chstore.DBHealthConfig, cur time.Time, openIDs []string, truncated bool) []dbHealthVerdict {
	byID := map[string]map[int64]chstore.DBHealthBucket{}
	for _, r := range rows {
		id := r.RuleID()
		m := byID[id]
		if m == nil {
			m = map[int64]chstore.DBHealthBucket{}
			byID[id] = m
		}
		m[r.Bucket.Unix()] = r
	}
	for _, id := range openIDs {
		if _, ok := byID[id]; !ok {
			byID[id] = map[int64]chstore.DBHealthBucket{}
		}
	}
	b0t := cur.Unix()
	b1t := cur.Add(-dbHealthBucket).Unix()
	b2t := cur.Add(-2 * dbHealthBucket).Unix()
	out := make([]dbHealthVerdict, 0, len(byID))
	for id, m := range byID {
		k, ok := dbHealthKeyOf(id)
		if !ok {
			continue
		}
		older, newer := b2t, b1t
		if b0, ok := m[b0t]; ok && b0.Calls >= cfg.MinCalls {
			older, newer = b1t, b0t
		}
		oldB, oldOK := m[older]
		newB, newOK := m[newer]
		c2, c2ok := m[b2t]
		c1, c1ok := m[b1t]
		v := dbHealthVerdict{
			Key: k, ID: id,
			Fire:  dbHealthBreach(oldB, oldOK, cfg) && dbHealthBreach(newB, newOK, cfg),
			Clear: !truncated && dbHealthClear(c2, c2ok, cfg) && dbHealthClear(c1, c1ok, cfg),
		}
		switch {
		case newOK:
			v.Latest, v.HasLatest = newB, true
		case oldOK:
			v.Latest, v.HasLatest = oldB, true
		}
		if v.HasLatest {
			v.Metric, v.Value, v.Threshold, v.Severity = dbHealthMeasure(v.Latest, cfg)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// dbHealthRefCandidates — SAF: p99 referansı gereken veritabanları (bir
// kovası mutlak tabana ulaşan), sıralı ve tekrarsız. Boşsa referans okuması yok.
func dbHealthRefCandidates(rows []chstore.DBHealthBucket, cfg chstore.DBHealthConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.P99Ms < cfg.P99Ms {
			continue
		}
		if id := r.RuleID(); !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// dbHealthApplyRefs — SAF: referans p99'larını satırlara işler.
func dbHealthApplyRefs(rows []chstore.DBHealthBucket, refs map[chstore.DBHealthRefKey]float64) {
	for i := range rows {
		if p, ok := refs[chstore.DBHealthRefKey{RuleID: rows[i].RuleID(), Bucket: rows[i].Bucket.Unix()}]; ok && p > 0 {
			rows[i].RefP99Ms, rows[i].HasRef = p, true
		}
	}
}

// dbHealthOpenOrder — SAF: açılış adaylarını önce critical, sonra oran
// (Value/Threshold) azalan sıraya dizer; fırtına tavanı en kötüleri açar.
func dbHealthOpenOrder(vs []dbHealthVerdict) {
	sort.SliceStable(vs, func(i, j int) bool {
		ci, cj := vs[i].Severity == "critical", vs[j].Severity == "critical"
		if ci != cj {
			return ci
		}
		return vs[i].Value/vs[i].Threshold > vs[j].Value/vs[j].Threshold
	})
}

func dbHealthSevRank(s string) int {
	switch s {
	case "critical":
		return 2
	case "warning":
		return 1
	}
	return 0
}

// dbHealthSeverityRose — SAF: tazeleme şiddeti yükseltti mi (ör. warning →
// critical). Yükseldiyse yeniden bildirim: yalnız-P1 kanalları olay
// tırmanınca sayfalasın (dedup katmanı yalnız gerçek şiddet artışını geçirir).
func dbHealthSeverityRose(old, cur string) bool { return dbHealthSevRank(cur) > dbHealthSevRank(old) }

// dbHealthOpenRows — SAF: anlık görüntüdeki açık/onaylı db-health satırları.
func dbHealthOpenRows(all []*chstore.Problem) []*chstore.Problem {
	var out []*chstore.Problem
	for _, p := range all {
		if p == nil || p.ID == "" || !strings.HasPrefix(p.RuleID, chstore.RuleDBHealthPrefix) {
			continue
		}
		if p.Status != "open" && p.Status != "acknowledged" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// dbHealthDisabledResolutions — SAF: kural kapalıyken açık satırların
// kapanmış KOPYALARI, dürüst gerekçeyle (anlık görüntü satırı değişmez).
func dbHealthDisabledResolutions(open []*chstore.Problem, nowNs int64) []chstore.Problem {
	out := make([]chstore.Problem, 0, len(open))
	for _, p := range open {
		q := *p
		if !strings.Contains(q.Description, dbHealthDisabledNote) {
			q.Description = strings.TrimRight(q.Description, " ") + " " + dbHealthDisabledNote
		}
		chstore.MarkResolved(&q, nowNs)
		out = append(out, q)
	}
	return out
}

// fmtNum — ondalık sıfırsız sayı ("5", "2.5").
func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// dbHealthLabel — insan etiketi: gerçek db.name varsa "<db> (<instance>)",
// yoksa instance.
func dbHealthLabel(k dbHealthKey) string {
	if chstore.DBHealthHasDBName(k.DBName) && k.DBName != k.Instance {
		return k.DBName + " (" + k.Instance + ")"
	}
	if chstore.DBHealthHasDBName(k.DBName) {
		return k.DBName
	}
	return k.Instance
}

// dbHealthReason — TEK düz Türkçe cümle: ihlal eden boyut(lar) + etkilenen
// çağıran sayısı + en çok çağıran ≤3 servis.
func dbHealthReason(k dbHealthKey, b chstore.DBHealthBucket, cfg chstore.DBHealthConfig) string {
	var dims []string
	if dbHealthErrBreach(b, cfg) {
		dims = append(dims, fmt.Sprintf("hata oranı %%%.1f (eşik %%%s)", b.ErrorPct(), fmtNum(cfg.ErrorPct)))
	}
	if dbHealthP99Breach(b, cfg) {
		dims = append(dims, fmt.Sprintf("p99 %s (eşik %s; dün aynı saatte %s, %.1f×)",
			fmtMs(b.P99Ms), fmtMs(cfg.P99Ms), fmtMs(b.RefP99Ms), b.P99Ms/b.RefP99Ms))
	}
	if len(dims) == 0 {
		dims = append(dims, fmt.Sprintf("hata oranı %%%.1f, p99 %s (eşiklerin altında)", b.ErrorPct(), fmtMs(b.P99Ms)))
	}
	s := fmt.Sprintf("%s %s veritabanında %s, %d çağıran servis etkilendi",
		strings.ToLower(k.System), dbHealthLabel(k), strings.Join(dims, " ve "), b.AffectedCallers)
	if len(b.TopCallers) > 0 {
		top := b.TopCallers
		if len(top) > 3 {
			top = top[:3]
		}
		s += " (" + strings.Join(top, ", ") + ")"
	}
	return s + fmt.Sprintf(" — %d çağrı / 5 dk.", b.Calls)
}

// dbHealthSubject — (özne, tür): chstore.DBHealthSubject; kodlanamazsa
// instance, Kind=service (db_capacity emsali — çözülmemiş dal bugünkü dal).
func dbHealthSubject(k dbHealthKey) (service, kind string) {
	if id := chstore.DBHealthSubject(k.System, k.Instance, k.DBName); id != "" {
		return id, chstore.ProblemKindDB
	}
	return k.Instance, chstore.ProblemKindService
}

// dbHealthCfgMemo — keep-last-good ayar + ardışık temiz okuma sayacı. Ayar
// okuması düşerse SON BAŞARILI değer kullanılır; süreçte hiç okunamadıysa
// pas yalnız tazeler (varsayılan yayınlamak, operatörün kapattığı kuralı o
// tik geri açardı — aiops §11). Sayaç bellekte, yalnız liderde: lider
// değişimi kapanışı en çok bir tik geciktirir.
type dbHealthCfgMemo struct {
	mu    sync.Mutex
	last  *chstore.DBHealthConfig
	clear map[string]int
}

func (m *dbHealthCfgMemo) resolve(c chstore.DBSlowQueryConfig, err error) (chstore.DBHealthConfig, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		h := chstore.NormalizeDBHealth(chstore.DBHealthConfig{})
		if c.Health != nil {
			h = chstore.NormalizeDBHealth(*c.Health)
		}
		m.last = &h
		return h, true
	}
	if m.last != nil {
		return *m.last, true
	}
	return chstore.DBHealthConfig{}, false
}

// clearStep — bu okumada temiz mi; dbHealthClearReads ardışık temiz okumada
// true (kapat). Temiz olmayan okuma sayacı sıfırlar.
func (m *dbHealthCfgMemo) clearStep(id string, clear bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clear == nil {
		m.clear = map[string]int{}
	}
	if !clear {
		delete(m.clear, id)
		return false
	}
	m.clear[id]++
	if m.clear[id] >= dbHealthClearReads {
		delete(m.clear, id)
		return true
	}
	return false
}

// prune — açık olmayan id'lerin sayaçlarını at (başka yoldan kapanan satır).
func (m *dbHealthCfgMemo) prune(open map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.clear {
		if !open[id] {
			delete(m.clear, id)
		}
	}
}

// evaluateDBHealth — evaluateAll pası (db-slow-stmt'ten SONRA). Lider
// tikinde koşar; açık problemler tik başına tek anlık görüntüden.
func (e *Evaluator) evaluateDBHealth(ctx context.Context) {
	raw, rerr := e.store.LoadDBSlowQuery(ctx)
	cfg, ok := e.dbHealthCfg.resolve(raw, rerr)
	if rerr != nil {
		log.Printf("[evaluator] db-health: ayar okunamadı (son iyi değer: %v): %v", ok, rerr)
	}
	snap, err := e.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		log.Printf("[evaluator] db-health: açık problem anlık görüntüsü alınamadı, tik atlanıyor: %v", err)
		return
	}
	open := dbHealthOpenRows(snap.All())
	now := time.Now()
	if !ok {
		e.keepDBHealth(ctx, open)
		return
	}
	if !cfg.On() {
		e.resolveDBHealthDisabled(ctx, open, now)
		return
	}
	openIDs := make([]string, 0, len(open))
	openSet := make(map[string]bool, len(open))
	for _, p := range open {
		openIDs = append(openIDs, p.ID)
		openSet[p.ID] = true
	}
	sort.Strings(openIDs)
	e.dbHealthCfg.prune(openSet)

	cur := now.Truncate(dbHealthBucket)
	rows, truncated, err := e.store.DBHealthBuckets(ctx, cur.Add(-2*dbHealthBucket), cur.Add(dbHealthBucket),
		cfg, e.store.AnomalySensitivityForDetectors(), openIDs)
	if err != nil {
		log.Printf("[evaluator] db-health read: %v — açık problemler tazelendi, karar yok", err)
		e.keepDBHealth(ctx, open)
		return
	}
	refStart := cur.Add(-dbHealthRefShift - 2*dbHealthBucket)
	refs, err := e.store.DBHealthReferenceP99(ctx, refStart, refStart.Add(3*dbHealthBucket), dbHealthRefShift,
		dbHealthRefCandidates(rows, cfg))
	if err != nil {
		// Referanssız p99 boyutu KAPALI sayılırdı → açık p99 problemi sahte
		// "temiz" görünüp kapanırdı. Ana okuma hatasıyla aynı yön: yalnız tazele.
		log.Printf("[evaluator] db-health p99 referans okuması: %v — açık problemler tazelendi, karar yok", err)
		e.keepDBHealth(ctx, open)
		return
	}
	dbHealthApplyRefs(rows, refs)
	if truncated {
		log.Printf("[evaluator] db-health: okuma %d satır tavanına dayandı — bu tik hiçbir problem kapatılmaz", len(rows))
	}

	var toOpen []dbHealthVerdict
	for _, v := range dbHealthDecide(rows, cfg, cur, openIDs, truncated) {
		existing := snap.ByID(v.ID)
		if existing == nil || existing.ID == "" {
			if v.Fire {
				toOpen = append(toOpen, v)
			}
			continue
		}
		e.reconcileDBHealth(ctx, *existing, v, cfg, now)
	}
	e.openDBHealth(ctx, toOpen, cfg, now)
}

// reconcileDBHealth — açık bir satırı tazeler / kapatır. q anlık görüntü
// satırının KOPYASI (paylaşımlı memo değişmesin).
func (e *Evaluator) reconcileDBHealth(ctx context.Context, q chstore.Problem, v dbHealthVerdict, cfg chstore.DBHealthConfig, now time.Time) {
	q.Service, q.Kind = dbHealthSubject(v.Key)
	if e.dbHealthCfg.clearStep(v.ID, v.Clear) {
		chstore.MarkResolved(&q, now.UnixNano())
		if err := e.store.UpsertProblem(ctx, q); err != nil {
			log.Printf("[evaluator] db-health resolve %s: %v", v.ID, err)
			return
		}
		e.countResolved()
		log.Printf("[evaluator] PROBLEM RESOLVED (db.health): %s", v.ID)
		return
	}
	rose := false
	if v.Fire {
		prev := q.Severity
		q.Metric, q.Value, q.Threshold = v.Metric, v.Value, v.Threshold
		q.Severity = effectiveSeverity(v.Severity, time.Since(time.Unix(0, q.StartedAt)), e.escalationCfg(ctx))
		q.Description = dbHealthReason(v.Key, v.Latest, cfg)
		rose = dbHealthSeverityRose(prev, q.Severity)
	}
	// Fire değilse histerezis bandı (ya da ilk temiz okuma): satır tazelenir
	// (bayat süpürme "kaynak sustu" demesin), ölçü/gerekçe son ihlalde kalır.
	if err := e.store.UpsertProblem(ctx, q); err != nil {
		log.Printf("[evaluator] db-health refresh %s: %v", v.ID, err)
		return
	}
	if rose && e.notifier != nil {
		log.Printf("[evaluator] db-health severity rose → %s: %s", q.Severity, v.ID)
		go e.notifier.SendProblemAlert(context.Background(), q)
	}
}

// keepDBHealth — karar verilemeyen tikte açık satırları olduğu gibi tazeler.
func (e *Evaluator) keepDBHealth(ctx context.Context, open []*chstore.Problem) {
	for _, p := range open {
		if err := e.store.UpsertProblem(ctx, *p); err != nil {
			log.Printf("[evaluator] db-health keep %s: %v", p.ID, err)
		}
	}
}

// resolveDBHealthDisabled — kural kapalı: açık satırlar "rule disabled"
// gerekçesiyle kapanır (bayat süpürmenin yanlış "source silent"i değil).
func (e *Evaluator) resolveDBHealthDisabled(ctx context.Context, open []*chstore.Problem, now time.Time) {
	for _, q := range dbHealthDisabledResolutions(open, now.UnixNano()) {
		if err := e.store.UpsertProblem(ctx, q); err != nil {
			log.Printf("[evaluator] db-health resolve (rule disabled) %s: %v", q.ID, err)
			continue
		}
		e.countResolved()
		log.Printf("[evaluator] PROBLEM RESOLVED (db.health, rule disabled): %s", q.ID)
	}
}

// openDBHealth — yeni açılışlar, tik başına en çok MaxNewPerTick (en kötü
// önce). Tavan dışı kalanlar bir sonraki tikte hâlâ ihlaldeyse açılır.
func (e *Evaluator) openDBHealth(ctx context.Context, toOpen []dbHealthVerdict, cfg chstore.DBHealthConfig, now time.Time) {
	dbHealthOpenOrder(toOpen)
	if len(toOpen) > cfg.MaxNewPerTick {
		log.Printf("[evaluator] db-health: %d açılış adayı, tik tavanı %d — kalanı sonraki tike", len(toOpen), cfg.MaxNewPerTick)
		toOpen = toOpen[:cfg.MaxNewPerTick]
	}
	for _, v := range toOpen {
		p := dbHealthProblem(v, cfg, now)
		if err := e.store.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator] db-health open %s: %v", p.ID, err)
			continue
		}
		e.countOpened()
		log.Printf("[evaluator] PROBLEM OPENED (db.health): %s", p.Description)
		if _, err := e.store.AttachProblemToIncident(ctx, p); err != nil {
			log.Printf("[evaluator] db-health incident attach: %v", err)
		}
		if e.notifier != nil {
			go e.notifier.SendProblemAlert(context.Background(), p)
		}
	}
}

// dbHealthProblem — SAF: açılacak Problem satırı.
func dbHealthProblem(v dbHealthVerdict, cfg chstore.DBHealthConfig, now time.Time) chstore.Problem {
	service, kind := dbHealthSubject(v.Key)
	return chstore.Problem{
		ID: v.ID, RuleID: v.ID,
		RuleName:    "DB health · " + strings.ToLower(v.Key.System),
		Severity:    v.Severity,
		Service:     service,
		Kind:        kind,
		Metric:      v.Metric,
		Value:       v.Value,
		Threshold:   v.Threshold,
		Comparator:  ">=",
		Status:      "open",
		Description: dbHealthReason(v.Key, v.Latest, cfg),
		StartedAt:   now.UnixNano(),
	}
}
