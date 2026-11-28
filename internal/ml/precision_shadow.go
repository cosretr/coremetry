package ml

// precision_shadow.go — v0.10.2486 (precision programı ADIM 2,
// docs/anomaly/precision-program.md B.2 + Kararlar 2026-11-27): otomatik
// gölge kuralı.
//
// OY KURALININ TEK İSTİSNASI. 2026-10-17 kuralı ("oylar çalışma zamanını
// sürmez") yalnız bu dosya için ve yalnız iki kaynakla (olay oyu, onaylı
// vaka) kısmen tersine çevrildi (DECISIONS 2026-11-28). Karne
// (chstore.MLScorecard) ml paketinde YALNIZ burada okunur;
// vote_runtime_guard_test.go izin listesinde tek girdi bu dosyadır.
// 👍/👎 (CoSRE), triyaj ve örtük sinyaller hiçbir kipte kurala girmez.
//
// KİPLER (ayar ml.precisionShadow.mode; varsayılan shadow):
//   - off:    değerlendirme yok; durum blobu olduğu gibi kalır.
//   - shadow: günde bir değerlendirme YALNIZ "düşerdi / dönerdi" kaydı ve
//     audit yazar. demoted() boş küme döner: hiçbir olay, kapı kararı,
//     Problem ya da bildirim değişmez (precision_shadow_pin_test.go pinler).
//     Durum makinesi SANAL koşar (frenler, bekleme süreleri aynen) — rapor
//     `on` kipinde olacak olanı gösterir.
//   - on:     aynı karar gerçek olur. Gölgedeki dedektörün olayı yazılır,
//     kanıtına `detShadow` damgalanır, Elenenler'de "dedektör gölgede
//     (karne)" nedeniyle görünür; grup kapısının üye süzgecinde
//     (DecideGroupPromotion, singleArmCheck) ve servis düzeyi seçimde
//     (decideSvcShadow girdisi) sayılmaz. `on` operatör onayı ister.
//
// KARAR (saf, DecidePrecisionShadow) — gölgede olmayan dedektör:
//   1. kapsam dışı (muaf, Problem açmayan, ML dışı)        → blocked:exempt
//   2. operatör pini                                        → keep
//   3. n = 0 ya da Wilson üst ≥ demoteUpper                 → keep
//   4. n < minLabels                                        → blocked:few_labels
//   5. < 2 etiketleyen                                      → blocked:few_labellers
//   6. < 5 etiket günü                                      → blocked:few_days
//   7. en kötü servis payı > 0,6                            → blocked:single_service
//   8. kuyruk etiketleri alt sınırı ≥ 0,60 (yanlılık)       → blocked:queue_bias
//   9. son geri dönüşten < 14 gün                           → blocked:redemote_cooldown
//  10. son 2 sa açık P1'e bağlı olay                        → blocked:open_p1
//  11. bugün maxPerDay düşüş yapıldı                        → blocked:daily_cap
//  12. (gölgedeki + 1) > maxShare × açık ML dedektörü       → blocked:share_cap
//  13. aksi halde                                          → would_demote (shadow) / demote (on)
// Gölgedeki dedektör:
//   1. kapsam dışı oldu ya da "on" pini                     → would_restore / restore
//   2. gölgede < 7 gün                                      → blocked:min_shadow_days
//   3. gölgeye alındıktan sonra < 10 yeni etiket            → keep
//   4. yeni etiketlerin Wilson alt ≥ restoreLower           → would_restore / restore
//   5. aksi halde                                          → keep
//
// Yanlılık kuralı (belgenin hata kipi tablosu): "Kuyruk etiketlerinin alt
// sınırı ≥ 0,60 ise organik etiketler düşüşü tetiklemez." Asgari kuyruk n'i
// belgede yok; Wilson alt sınırı küçük n'de zaten düşük kalır (7/7 → 0,65),
// bu yüzden kural yazıldığı gibi uygulanır (kuyruk n > 0 şartıyla).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Karar sonuçları.
const (
	PrecKeep         = "keep"
	PrecWouldDemote  = "would_demote"
	PrecDemote       = "demote"
	PrecWouldRestore = "would_restore"
	PrecRestore      = "restore"
	precBlocked      = "blocked:"
)

// Fren nedenleri (blocked:<neden>).
const (
	PrecBlockExempt        = "exempt"
	PrecBlockFewLabels     = "few_labels"
	PrecBlockFewLabellers  = "few_labellers"
	PrecBlockFewDays       = "few_days"
	PrecBlockSingleService = "single_service"
	PrecBlockQueueBias     = "queue_bias"
	PrecBlockCooldown      = "redemote_cooldown"
	PrecBlockOpenP1        = "open_p1"
	PrecBlockDailyCap      = "daily_cap"
	PrecBlockShareCap      = "share_cap"
	PrecBlockMinShadow     = "min_shadow_days"
)

// PrecisionInput — kararın bütün girdisi (I/O yok).
type PrecisionInput struct {
	Score chstore.MLDetectorScore
	// Since — gölgedeyken toplanan etiketler (yalnız gölgedeki dedektörde;
	// nil = okunamadı / yok → geri dönüş yok).
	Since        *chstore.MLDetectorScore
	Cfg          chstore.MLPrecisionShadow
	State        chstore.MLPrecisionDetState
	DemotedToday int  // bu değerlendirmede ve bugün yapılan düşüş
	DemotedTotal int  // şu an gölgedeki dedektör
	EnabledML    int  // kurala girebilen açık ML dedektörü
	OpenP1       bool // son 2 sa açık P1'e bağlı olayı var
	Now          time.Time
}

// PrecisionDecision — kararın çıktısı.
type PrecisionDecision struct {
	Detector string `json:"detector"`
	Outcome  string `json:"outcome"`
	Reason   string `json:"reason"`
	// Signal — düşme sinyali tuttu (n > 0 ve üst < demoteUpper); fren
	// kayıtları yalnız bu durumda rapora yazılır.
	Signal bool `json:"signal,omitempty"`
}

// Blocked — SAF: fren nedeni ("" = fren değil).
func (d PrecisionDecision) Blocked() string {
	b, _ := strings.CutPrefix(d.Outcome, precBlocked)
	if b == d.Outcome {
		return ""
	}
	return b
}

// trPossessive — SAF: Türkçe sayı + iyelik eki ("6'sı", "24'ü", "10'u").
func trPossessive(n int64) string {
	suf := func(v int64) string {
		switch {
		case v == 0:
			return "'ı"
		case v%1000000 == 0:
			return "'u" // milyon
		case v%1000 == 0:
			return "'i" // bin
		case v%100 == 0:
			return "'ü" // yüz
		case v%10 == 0:
			return [...]string{"", "'u", "'si", "'u", "'ı", "'si", "'ı", "'i", "'i", "'ı"}[(v/10)%10]
		}
		return [...]string{"", "'i", "'si", "'ü", "'ü", "'i", "'sı", "'si", "'i", "'u"}[v%10]
	}
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%d%s", n, suf(n))
}

func pctTR(v float64) string { return fmt.Sprintf("%%%d", int(math.Round(v*100))) }

// precisionSentence — SAF: gerekçe cümlesi, örn. "30 günde 24 etiket, 6'sı
// doğru; üst sınır %45".
func precisionSentence(s chstore.MLDetectorScore, window int) string {
	return fmt.Sprintf("%d günde %d etiket, %s doğru; üst sınır %s", window, s.Samples, trPossessive(s.Hits), pctTR(s.WilsonHi))
}

// DecidePrecisionShadow — SAF: tek dedektörün günlük kararı (tablo dosya başında).
func DecidePrecisionShadow(in PrecisionInput) PrecisionDecision {
	s, c := in.Score, in.Cfg
	d := PrecisionDecision{Detector: s.Detector}
	on := c.Mode == chstore.MLPrecisionModeOn
	pick := func(shadow, live string) string {
		if on {
			return live
		}
		return shadow
	}
	block := func(why, text string) PrecisionDecision {
		d.Outcome, d.Reason = precBlocked+why, text
		return d
	}
	inScope, scopeWhy := chstore.MLPrecisionRuleScope(s.Detector)
	pin := c.Pins[s.Detector]
	now := in.Now.Unix()

	if in.State.Demoted {
		switch {
		case !inScope:
			d.Outcome, d.Reason = pick(PrecWouldRestore, PrecRestore), "kural dışı: "+scopeWhy
			return d
		case pin == chstore.MLPrecisionPinOn:
			d.Outcome, d.Reason = pick(PrecWouldRestore, PrecRestore), "operatör açık sabitledi"
			return d
		}
		days := float64(now-in.State.DemotedAt) / 86400
		if days < chstore.MLPrecisionMinShadowDays {
			return block(PrecBlockMinShadow, fmt.Sprintf("gölgede %d gün (en az %d)", int(days), chstore.MLPrecisionMinShadowDays))
		}
		if in.Since == nil || in.Since.Samples < chstore.MLPrecisionRestoreMinNew {
			n := int64(0)
			if in.Since != nil {
				n = in.Since.Samples
			}
			d.Outcome, d.Reason = PrecKeep, fmt.Sprintf("gölgede: %d yeni etiket (en az %d)", n, chstore.MLPrecisionRestoreMinNew)
			return d
		}
		sentence := fmt.Sprintf("gölgede %d yeni etiket, %s doğru; alt sınır %s", in.Since.Samples, trPossessive(in.Since.Hits), pctTR(in.Since.WilsonLo))
		if in.Since.WilsonLo >= c.RestoreLower {
			d.Outcome, d.Reason = pick(PrecWouldRestore, PrecRestore), sentence
			return d
		}
		d.Outcome, d.Reason = PrecKeep, sentence+fmt.Sprintf(" (dönüş için %s)", pctTR(c.RestoreLower))
		return d
	}

	if !inScope {
		return block(PrecBlockExempt, scopeWhy)
	}
	sentence := precisionSentence(s, c.WindowDays)
	if pin != "" {
		d.Outcome, d.Reason = PrecKeep, "operatör sabitledi ("+pin+"); "+sentence
		return d
	}
	if s.Samples <= 0 || s.WilsonHi >= c.DemoteUpper {
		d.Outcome, d.Reason = PrecKeep, sentence
		return d
	}
	d.Signal = true
	switch {
	case s.Samples < int64(c.MinLabels):
		return block(PrecBlockFewLabels, fmt.Sprintf("%s — etiket az (en az %d)", sentence, c.MinLabels))
	case s.Labellers < chstore.MLPrecisionMinLabellers:
		return block(PrecBlockFewLabellers, fmt.Sprintf("%s — %d kişi etiketledi (en az %d)", sentence, s.Labellers, chstore.MLPrecisionMinLabellers))
	case s.LabelDays < chstore.MLPrecisionMinLabelDays:
		return block(PrecBlockFewDays, fmt.Sprintf("%s — etiketler %d günden (en az %d)", sentence, s.LabelDays, chstore.MLPrecisionMinLabelDays))
	case s.WorstSvcShare > chstore.MLPrecisionMaxSvcShare:
		return block(PrecBlockSingleService, fmt.Sprintf("%s — tek servis baskın (yanlışların %s'i tek serviste)", sentence, pctTR(s.WorstSvcShare)))
	case s.Queue.N > 0 && s.Queue.WilsonLo >= chstore.MLPrecisionQueueBiasLower:
		return block(PrecBlockQueueBias, fmt.Sprintf("%s — rastgele kuyruk etiketleri iyi (alt sınır %s)", sentence, pctTR(s.Queue.WilsonLo)))
	case in.State.RestoredAt > 0 && float64(now-in.State.RestoredAt)/86400 < chstore.MLPrecisionRedemoteDays:
		return block(PrecBlockCooldown, fmt.Sprintf("%s — geri döneli %d gün (en az %d)", sentence, (now-in.State.RestoredAt)/86400, chstore.MLPrecisionRedemoteDays))
	case in.OpenP1:
		return block(PrecBlockOpenP1, sentence+" — son 2 saatte açık P1 Problem'e bağlı olayı var")
	case in.DemotedToday >= c.MaxPerDay:
		return block(PrecBlockDailyCap, fmt.Sprintf("%s — bugünkü düşüş sınırı dolu (%d)", sentence, c.MaxPerDay))
	case float64(in.DemotedTotal+1) > c.MaxShare*float64(in.EnabledML):
		return block(PrecBlockShareCap, fmt.Sprintf("%s — açık ML dedektörlerinin en çok %s'i gölgede olabilir (%d / %d)", sentence, pctTR(c.MaxShare), in.DemotedTotal, in.EnabledML))
	}
	d.Outcome, d.Reason = pick(PrecWouldDemote, PrecDemote), sentence
	return d
}

// PrecisionDayInput — günlük planın girdisi (I/O yok).
type PrecisionDayInput struct {
	Rows    []chstore.MLDetectorScore
	Since   map[string]chstore.MLDetectorScore // gölgedeki dedektör → gölgeye alındıktan sonraki etiketler
	Cfg     chstore.MLPrecisionShadow
	Enabled map[string]bool // dedektör açık mı (ana anahtar dahil)
	OpenP1  map[string]bool
	Fed     map[string]chstore.MLDetectorProblemsFed
	State   chstore.MLPrecisionState
	Day     string
	Now     time.Time
}

// PlanPrecisionDay — SAF: bir günün değerlendirmesi → yeni durum + kararlar
// + yeni kayıtlar. Sıra deterministik: önce gölgedekiler (geri dönüş),
// sonra adaylar Wilson üst sınırı artan (en kötü önce; günlük sınır en
// kötüye gider).
func PlanPrecisionDay(in PrecisionDayInput) (chstore.MLPrecisionState, []PrecisionDecision, []chstore.MLPrecisionRecord) {
	on := in.Cfg.Mode == chstore.MLPrecisionModeOn
	st := in.State
	dets := map[string]chstore.MLPrecisionDetState{}
	for k, v := range st.Detectors {
		// Kip geçişi: on → shadow gerçek kaydı sanala çevirir; shadow → on
		// sanal kayıtları siler (on'da frenler baştan, günde 1 ile işler).
		if v.Demoted && on && v.Virtual {
			v.Demoted, v.Virtual, v.DemotedAt, v.Reason = false, false, 0, ""
		} else if v.Demoted && !on {
			v.Virtual = true
		}
		dets[k] = v
	}
	byDet := map[string]chstore.MLDetectorScore{}
	for _, r := range in.Rows {
		byDet[r.Detector] = r
	}
	enabled := 0
	for _, n := range chstore.MLDetectorNames {
		if ok, _ := chstore.MLPrecisionRuleScope(n); ok && in.Enabled[n] {
			enabled++
		}
	}
	demotedTotal, demotedToday := 0, 0
	for _, v := range dets {
		if v.Demoted {
			demotedTotal++
			if v.DemotedAt > 0 && time.Unix(v.DemotedAt, 0).In(in.Now.Location()).Format("2006-01-02") == in.Day {
				demotedToday++
			}
		}
	}
	// Değerlendirilecek dedektörler: karnede satırı olanlar + gölgedekiler.
	names := map[string]bool{}
	for d := range byDet {
		names[d] = true
	}
	for d, v := range dets {
		if v.Demoted {
			names[d] = true
		}
	}
	order := make([]string, 0, len(names))
	for d := range names {
		order = append(order, d)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := dets[order[i]].Demoted, dets[order[j]].Demoted
		if a != b {
			return a
		}
		ha, hb := byDet[order[i]].WilsonHi, byDet[order[j]].WilsonHi
		if ha != hb {
			return ha < hb
		}
		return order[i] < order[j]
	})
	var decs []PrecisionDecision
	var recs []chstore.MLPrecisionRecord
	now := in.Now.Unix()
	for _, det := range order {
		score, ok := byDet[det]
		if !ok {
			score = chstore.MLDetectorScore{Detector: det}
		}
		cur := dets[det]
		pi := PrecisionInput{Score: score, Cfg: in.Cfg, State: cur, DemotedToday: demotedToday, DemotedTotal: demotedTotal,
			EnabledML: enabled, OpenP1: in.OpenP1[det], Now: in.Now}
		if s, ok := in.Since[det]; ok && cur.Demoted {
			pi.Since = &s
		}
		d := DecidePrecisionShadow(pi)
		decs = append(decs, d)
		switch d.Outcome {
		case PrecWouldDemote, PrecDemote:
			cur = chstore.MLPrecisionDetState{Demoted: true, Virtual: !on, DemotedAt: now, RestoredAt: cur.RestoredAt, Reason: d.Reason, At: now}
			demotedTotal++
			demotedToday++
		case PrecWouldRestore, PrecRestore:
			cur = chstore.MLPrecisionDetState{RestoredAt: now, Reason: d.Reason, At: now}
			demotedTotal--
		}
		if cur != (chstore.MLPrecisionDetState{}) {
			dets[det] = cur
		}
		if precisionRecorded(d) {
			lo, hi, n, c := score.WilsonLo, score.WilsonHi, score.Samples, score.Hits
			if (d.Outcome == PrecWouldRestore || d.Outcome == PrecRestore) && pi.Since != nil {
				lo, hi, n, c = pi.Since.WilsonLo, pi.Since.WilsonHi, pi.Since.Samples, pi.Since.Hits
			}
			f := in.Fed[det]
			recs = append(recs, chstore.MLPrecisionRecord{Day: in.Day, At: now, Detector: det, Outcome: d.Outcome, Reason: d.Reason,
				N: n, Correct: c, Wrong: n - c, Lower: lo, Upper: hi, Window: in.Cfg.WindowDays, Mode: in.Cfg.Mode,
				ProblemsFed: f.Fed, ProblemsReal: f.Real})
		}
	}
	st.Detectors = dets
	st.LastEvalDay, st.LastEvalAt, st.Mode, st.LastError = in.Day, now, in.Cfg.Mode, ""
	st.Records = chstore.PruneMLPrecisionRecords(append(recs, st.Records...), in.Now)
	return st, decs, recs
}

// precisionRecorded — SAF: rapora yazılan karar: durum değişikliği ya da
// düşme sinyali tutup frene takılan.
func precisionRecorded(d PrecisionDecision) bool {
	switch d.Outcome {
	case PrecWouldDemote, PrecDemote, PrecWouldRestore, PrecRestore:
		return true
	}
	return d.Signal && d.Blocked() != ""
}

// precisionAudited — SAF: audit'e düşen karar (yalnız durum değişikliği).
func precisionAudited(outcome string) bool {
	switch outcome {
	case PrecWouldDemote, PrecDemote, PrecWouldRestore, PrecRestore:
		return true
	}
	return false
}

// PrecisionAuditActor — otomatik kararların audit aktörü.
const PrecisionAuditActor = "system:ml-precision"

// precisionAuditEntry — SAF: kaydın audit satırı (belgenin details alanları).
func precisionAuditEntry(r chstore.MLPrecisionRecord) chstore.AuditEntry {
	details, _ := json.Marshal(map[string]any{"detector": r.Detector, "n": r.N, "correct": r.Correct, "wrong": r.Wrong,
		"lower": r.Lower, "upper": r.Upper, "window": r.Window, "reason": r.Reason, "mode": r.Mode,
		"problemsFed": r.ProblemsFed, "problemsReal": r.ProblemsReal})
	return chstore.AuditEntry{Time: r.At * int64(time.Second), ActorID: PrecisionAuditActor, ActorEmail: PrecisionAuditActor, ActorRole: "system",
		Action: "ml.precision." + r.Outcome, TargetKind: "ml_detector", TargetID: r.Detector, Details: string(details)}
}

// ── Günlük değerlendirme (lider) ────────────────────────────────────────

// precisionJob — süreç-içi koşu bayrağı (tik goroutine'ini bloklamaz).
type precisionJob struct{ running atomic.Bool }

const precisionEvalTimeout = 2 * time.Minute

// maybePrecisionDaily — zamanlı lider tikinin sonunda: kip off değilse ve
// bugün değerlendirilmediyse arka planda bir kez koşar (idempotent: durum
// blobundaki gün).
func (r *Recorder) maybePrecisionDaily(ctx context.Context, cfg chstore.MLConfig, now time.Time) {
	pcfg := cfg.PrecisionShadowSettings()
	if pcfg.Mode == chstore.MLPrecisionModeOff || !r.precision.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.precision.running.Store(false)
		ectx, cancel := context.WithTimeout(ctx, precisionEvalTimeout)
		defer cancel()
		if err := r.runPrecisionDay(ectx, cfg, pcfg, now); err != nil {
			log.Printf("[ml] precision shadow: %v", err)
		}
	}()
}

func (r *Recorder) runPrecisionDay(ctx context.Context, cfg chstore.MLConfig, pcfg chstore.MLPrecisionShadow, now time.Time) error {
	loc := OperatorLocation(r.store.AnomalySensitivityForDetectors())
	now = now.In(loc)
	day := now.Format("2006-01-02")
	st, _, err := r.store.GetMLPrecisionState(ctx)
	if err != nil {
		return err
	}
	if st.LastEvalDay == day {
		return nil
	}
	window := time.Duration(pcfg.WindowDays) * 24 * time.Hour
	rows, err := r.store.MLScorecard(ctx, now.Add(-window))
	if err != nil {
		return r.precisionFail(ctx, st, err)
	}
	since := map[string]chstore.MLDetectorScore{}
	byAt := map[int64][]string{}
	for det, v := range st.Detectors {
		if v.Demoted && v.DemotedAt > 0 {
			byAt[v.DemotedAt] = append(byAt[v.DemotedAt], det)
		}
	}
	for at, dets := range byAt { // gölgedeki dedektör başına "yeni etiketler" (düşüş günü başına bir okuma; %25 tavanı küçük tutar)
		srows, serr := r.store.MLScorecard(ctx, time.Unix(at, 0))
		if serr != nil {
			return r.precisionFail(ctx, st, serr)
		}
		for _, s := range srows {
			for _, d := range dets {
				if s.Detector == d {
					since[d] = s
				}
			}
		}
	}
	openP1 := map[string]bool{}
	if p1, perr := r.store.OpenP1ProblemIDs(ctx, now); perr == nil {
		links, lerr := r.store.MLRecentProblemLinks(ctx, now.Add(-chstore.MLPrecisionOpenP1Window))
		if lerr != nil {
			return r.precisionFail(ctx, st, lerr)
		}
		for _, l := range links {
			if p1[l.ProblemID] {
				openP1[l.Detector] = true
			}
		}
	} else {
		return r.precisionFail(ctx, st, perr)
	}
	fed, ferr := r.store.MLProblemsFed(ctx, now.Add(-window))
	if ferr != nil {
		fed = nil // rapor alanı; karar girdisi değil
	}
	enabled := map[string]bool{}
	for _, n := range chstore.MLDetectorNames {
		enabled[n] = cfg.DetectorOn(n)
	}
	next, _, recs := PlanPrecisionDay(PrecisionDayInput{Rows: rows, Since: since, Cfg: pcfg, Enabled: enabled, OpenP1: openP1,
		Fed: fed, State: st, Day: day, Now: now})
	if err := r.store.SaveMLPrecisionState(ctx, next); err != nil {
		return err
	}
	for _, rec := range recs {
		if !precisionAudited(rec.Outcome) {
			continue
		}
		if aerr := r.store.AppendAudit(ctx, precisionAuditEntry(rec)); aerr != nil {
			log.Printf("[ml] precision audit %s: %v", rec.Detector, aerr)
		}
	}
	return nil
}

// precisionFail — okuma hatası: gün işaretlenmez (sonraki tik yeniden dener),
// hata durum blobunda görünür.
func (r *Recorder) precisionFail(ctx context.Context, st chstore.MLPrecisionState, err error) error {
	st.LastError = err.Error()
	_ = r.store.SaveMLPrecisionState(ctx, st)
	return err
}

// ── `on` kipinin uygulama yolu ──────────────────────────────────────────

// precisionDemoted — tik başı: `on` kipinde gölgedeki dedektörler; diğer
// kiplerde nil (durum blobu OKUNMAZ — varsayılan kipte tik birebir aynı).
func (r *Recorder) precisionDemoted(ctx context.Context, cfg chstore.MLConfig) map[string]bool {
	pcfg := cfg.PrecisionShadowSettings()
	if pcfg.Mode != chstore.MLPrecisionModeOn {
		return nil
	}
	st, _, err := r.store.GetMLPrecisionState(ctx)
	if err != nil {
		log.Printf("[ml] precision state: %v (gölge uygulanmadı)", err)
		return nil
	}
	return chstore.MLPrecisionDemotedSet(pcfg, st)
}

// detShadowKey — gölgedeki dedektörün olay kanıtı anahtarı (şemasız;
// groupShadow emsali). Elenenler bu anahtarla listeler.
const detShadowKey = "detShadow"

// DetShadowReason — Elenenler nedeni.
const DetShadowReason = "dedektör gölgede (karne)"

// DetShadow — kanıttaki damga.
type DetShadow struct {
	Reason string `json:"reason"`
	At     int64  `json:"at"`
}

// demotedOut — SAF: gölgedeki dedektörün olaylarını çıkarır (servis düzeyi
// seçimin girdisi). demoted boşsa girdi AYNEN döner.
func demotedOut(evs []chstore.MLEvent, demoted map[string]bool) []chstore.MLEvent {
	if len(demoted) == 0 {
		return evs
	}
	out := make([]chstore.MLEvent, 0, len(evs))
	for _, e := range evs {
		if !demoted[e.Detector] {
			out = append(out, e)
		}
	}
	return out
}

// stampDetShadow — SAF: gölgedeki dedektörün yazılacak olaylarına damga.
func stampDetShadow(kept []chstore.MLEvent, demoted map[string]bool, now time.Time) {
	if len(demoted) == 0 {
		return
	}
	for i := range kept {
		if demoted[kept[i].Detector] {
			kept[i].Evidence = mergeEvidence(kept[i].Evidence, map[string]any{detShadowKey: DetShadow{Reason: DetShadowReason, At: now.Unix()}})
		}
	}
}

// DetShadowOf — SAF: kanıttan gölge damgası.
func DetShadowOf(evidence string) (DetShadow, bool) {
	if evidence == "" || !strings.Contains(evidence, detShadowKey) {
		return DetShadow{}, false
	}
	var w map[string]json.RawMessage
	if json.Unmarshal([]byte(evidence), &w) != nil {
		return DetShadow{}, false
	}
	raw, ok := w[detShadowKey]
	if !ok {
		return DetShadow{}, false
	}
	var d DetShadow
	if json.Unmarshal(raw, &d) != nil {
		return DetShadow{}, false
	}
	return d, true
}
