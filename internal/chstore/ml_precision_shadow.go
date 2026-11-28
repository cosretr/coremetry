package chstore

// ml_precision_shadow.go — v0.10.2486 (precision programı ADIM 2,
// docs/anomaly/precision-program.md B.2; operatör kararları 2026-11-27):
// otomatik gölge kuralının AYARI, DURUM blobu ve kuralın iki küçük CH okuması.
// Karar işlevi (saf) ve günlük değerlendirme internal/ml/precision_shadow.go.
//
//   - Ayar: `ml` blobu altında `precisionShadow` {mode: off|shadow|on,
//     minLabels, demoteUpper, restoreLower, windowDays, maxPerDay, maxShare,
//     pins}. Varsayılan mode=shadow: kural YALNIZ "düşerdi" kaydı yazar,
//     hiçbir olay / kapı / Problem / bildirim değişmez. `on` ayrı operatör
//     onayı ister (PUT onay adımı, api/ml_precision_shadow.go).
//   - Durum: system_settings `ml_precision_state` (ml_tick_status emsali;
//     tek yazıcı ML lideri). Kayıtlı AYAR otomatik yeniden yazılmaz.
//   - Okumalar: (1) son 2 sa olaylarının Problem bağı (açık P1 freni),
//     (2) pencerede dedektörün beslediği Problem ve bunların onaylı, gürültü
//     olmayan vakalı olanları (operatörün `on` öncesi gördüğü sayı). İkisi de
//     oy OKUMAZ (anomaly_verdicts yok).

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
)

// Kural kipleri.
const (
	MLPrecisionModeOff    = "off"
	MLPrecisionModeShadow = "shadow"
	MLPrecisionModeOn     = "on"
)

// Sabitlemeler (pins) — operatörün dedektör başına kararı.
const (
	MLPrecisionPinOn     = "on"     // kural bu dedektörü hiç düşürmez
	MLPrecisionPinShadow = "shadow" // operatör gölgeye aldı (yalnız `on` kipinde etkili)
)

// Belgenin sabit eşikleri (ayar şeklinde yok; Kararlar tablosu 4).
const (
	MLPrecisionMinLabellers    = 2             // etiketler ≥ 2 farklı kişiden
	MLPrecisionMinLabelDays    = 5             // ve ≥ 5 farklı günden
	MLPrecisionMaxSvcShare     = 0.6           // A1c tek servis payı ≤ 0,6
	MLPrecisionQueueBiasLower  = 0.60          // kuyruk etiketlerinin alt sınırı ≥ 0,60 → organik düşüş yok
	MLPrecisionRestoreMinNew   = 10            // geri dönüş: gölgedeyken ≥ 10 yeni etiket
	MLPrecisionMinShadowDays   = 7             // gölgede en az 7 gün
	MLPrecisionRedemoteDays    = 14            // geri dönüşten sonra 14 gün yeniden düşme yok
	MLPrecisionOpenP1Window    = 2 * time.Hour // son 2 sa açık P1 bağı → o gün düşüş yok
	MLPrecisionRecordRetention = 30 * 24 * time.Hour
)

// MLPrecisionShadow — `ml.precisionShadow` ayarı.
type MLPrecisionShadow struct {
	Mode         string            `json:"mode"`
	MinLabels    int               `json:"minLabels"`
	DemoteUpper  float64           `json:"demoteUpper"`
	RestoreLower float64           `json:"restoreLower"`
	WindowDays   int               `json:"windowDays"`
	MaxPerDay    int               `json:"maxPerDay"`
	MaxShare     float64           `json:"maxShare"`
	Pins         map[string]string `json:"pins"`
}

// DefaultMLPrecisionShadow — belgenin Kararlar tablosu; kip shadow (yalnız rapor).
func DefaultMLPrecisionShadow() MLPrecisionShadow {
	return MLPrecisionShadow{Mode: MLPrecisionModeShadow, MinLabels: MLPrecisionMinLabels, DemoteUpper: 0.50, RestoreLower: 0.60,
		WindowDays: 30, MaxPerDay: 1, MaxShare: 0.25, Pins: map[string]string{}}
}

// NormalizeMLPrecisionShadow — SAF: sınırlar; bilinmeyen kip shadow (güvenli
// yön: davranış değişmez), bilinmeyen dedektör ya da değerli pin silinir.
func NormalizeMLPrecisionShadow(p MLPrecisionShadow) MLPrecisionShadow {
	d := DefaultMLPrecisionShadow()
	switch p.Mode {
	case MLPrecisionModeOff, MLPrecisionModeShadow, MLPrecisionModeOn:
	default:
		p.Mode = d.Mode
	}
	p.MinLabels = clampInt(p.MinLabels, 10, 1000, d.MinLabels)
	p.WindowDays = clampInt(p.WindowDays, 7, 90, d.WindowDays)
	p.MaxPerDay = clampInt(p.MaxPerDay, 1, 10, d.MaxPerDay)
	if !(p.DemoteUpper > 0 && p.DemoteUpper <= 0.9) {
		p.DemoteUpper = d.DemoteUpper
	}
	if !(p.RestoreLower > 0 && p.RestoreLower <= 1) || p.RestoreLower < p.DemoteUpper {
		p.RestoreLower = math.Max(d.RestoreLower, p.DemoteUpper)
	}
	if !(p.MaxShare > 0 && p.MaxShare <= 1) {
		p.MaxShare = d.MaxShare
	}
	pins := map[string]string{}
	for det, v := range p.Pins {
		if !mlKnownDetector(det) || (v != MLPrecisionPinOn && v != MLPrecisionPinShadow) {
			continue
		}
		pins[det] = v
	}
	p.Pins = pins
	return p
}

func mlKnownDetector(det string) bool {
	for _, n := range MLDetectorNames {
		if n == det {
			return true
		}
	}
	return false
}

// PrecisionShadowSettings — SAF: yürürlükteki ayar (blobda yoksa varsayılan).
func (c MLConfig) PrecisionShadowSettings() MLPrecisionShadow {
	if c.PrecisionShadow == nil {
		return DefaultMLPrecisionShadow()
	}
	return NormalizeMLPrecisionShadow(*c.PrecisionShadow)
}

// PrecisionShadowMode — SAF: yürürlükteki kip.
func (c MLConfig) PrecisionShadowMode() string { return c.PrecisionShadowSettings().Mode }

// MLPrecisionRuleScope — SAF: dedektör otomatik kurala girer mi. Muaf (etki
// kuralı aileleri, metric_kafka) ve Problem açmayan (MLOffFeedDetector)
// dedektörler girmez; ML dışı aileler zaten bu listede yok.
func MLPrecisionRuleScope(det string) (bool, string) {
	switch {
	case !mlKnownDetector(det):
		return false, "ML dedektörü değil"
	case MLPrecisionExempt(det):
		return false, "muaf (etki kuralı ya da metric_kafka)"
	case MLOffFeedDetector(det):
		return false, "Problem açmaz (yalnız hacimle ölçülür)"
	}
	return true, ""
}

// ── Durum blobu ─────────────────────────────────────────────────────────

const mlPrecisionStateKey = "ml_precision_state"

// Dedektör durumu (rozet).
const (
	MLPrecStatusOn          = "on"           // açık
	MLPrecStatusWouldDemote = "would_demote" // shadow kipinde "düşerdi"
	MLPrecStatusShadow      = "shadow"       // on kipinde gölgede
	MLPrecStatusPinned      = "pinned"       // operatör sabitledi
)

// MLPrecisionDetState — bir dedektörün kural durumu. Demoted: kural (ya da
// shadow kipinde sanal olarak) gölgeye aldı. Virtual: shadow kipinde alındı
// — davranışı HİÇBİR kipte değiştirmez; on kipinin ilk değerlendirmesi
// sanal kayıtları siler (frenler yeniden, günde 1 ile işler).
type MLPrecisionDetState struct {
	Demoted    bool   `json:"demoted"`
	Virtual    bool   `json:"virtual,omitempty"`
	DemotedAt  int64  `json:"demotedAt,omitempty"`  // unix sn
	RestoredAt int64  `json:"restoredAt,omitempty"` // unix sn (14 gün freni)
	Reason     string `json:"reason,omitempty"`
	At         int64  `json:"at,omitempty"` // son değişiklik (unix sn)
}

// MLPrecisionRecord — günlük değerlendirmenin bir kaydı (son 30 gün).
type MLPrecisionRecord struct {
	Day      string  `json:"day"` // YYYY-MM-DD (operatör saat dilimi)
	At       int64   `json:"at"`  // unix sn
	Detector string  `json:"detector"`
	Outcome  string  `json:"outcome"` // would_demote | demote | would_restore | restore | blocked:<neden>
	Reason   string  `json:"reason"`
	N        int64   `json:"n"`
	Correct  int64   `json:"correct"`
	Wrong    int64   `json:"wrong"`
	Lower    float64 `json:"lower"`
	Upper    float64 `json:"upper"`
	Window   int     `json:"window"` // gün
	Mode     string  `json:"mode"`
	// Pencerede bu dedektörün olaylarının bağlandığı Problem ve bunlardan
	// onaylı, gürültü olmayan vakası olanlar (on öncesi "kaç gerçek Problem
	// kesilirdi").
	ProblemsFed  int64 `json:"problemsFed"`
	ProblemsReal int64 `json:"problemsReal"`
}

// MLPrecisionState — `ml_precision_state` blobu.
type MLPrecisionState struct {
	LastEvalDay string                         `json:"lastEvalDay,omitempty"`
	LastEvalAt  int64                          `json:"lastEvalAt,omitempty"`
	Mode        string                         `json:"mode,omitempty"` // son değerlendirmenin kipi
	Detectors   map[string]MLPrecisionDetState `json:"detectors,omitempty"`
	Records     []MLPrecisionRecord            `json:"records,omitempty"`
	LastError   string                         `json:"lastError,omitempty"`
}

// PruneMLPrecisionRecords — SAF: 30 günden eski kayıtları atar (en yeni önde).
func PruneMLPrecisionRecords(recs []MLPrecisionRecord, now time.Time) []MLPrecisionRecord {
	cut := now.Add(-MLPrecisionRecordRetention).Unix()
	out := make([]MLPrecisionRecord, 0, len(recs))
	for _, r := range recs {
		if r.At >= cut {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > 1000 {
		out = out[:1000]
	}
	return out
}

// MLPrecisionDemotedSet — SAF: `on` kipinde gölgede olan dedektörler
// (gerçek kural kaydı + operatörün shadow pini; on pini her şeyi ezer).
// Kip on DEĞİLSE boş: shadow / off kipinde hiçbir davranış değişmez.
func MLPrecisionDemotedSet(cfg MLPrecisionShadow, st MLPrecisionState) map[string]bool {
	if cfg.Mode != MLPrecisionModeOn {
		return nil
	}
	out := map[string]bool{}
	for det, d := range st.Detectors {
		if d.Demoted && !d.Virtual {
			if ok, _ := MLPrecisionRuleScope(det); ok {
				out[det] = true
			}
		}
	}
	for det, v := range cfg.Pins {
		switch v {
		case MLPrecisionPinShadow:
			if ok, _ := MLPrecisionRuleScope(det); ok { // muaf dedektör pinle de gölgeye girmez
				out[det] = true
			}
		case MLPrecisionPinOn:
			delete(out, det)
		}
	}
	return out
}

// GetMLPrecisionState — durum blobu (yoksa ok=false).
func (s *Store) GetMLPrecisionState(ctx context.Context) (MLPrecisionState, bool, error) {
	var st MLPrecisionState
	raw, err := s.GetSetting(ctx, mlPrecisionStateKey)
	if err != nil || len(raw) == 0 {
		return st, false, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return MLPrecisionState{}, false, err
	}
	return st, true, nil
}

// SaveMLPrecisionState — tek yazıcı ML lideri.
func (s *Store) SaveMLPrecisionState(ctx context.Context, st MLPrecisionState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, mlPrecisionStateKey, raw)
}

// ── CH okumaları ────────────────────────────────────────────────────────

// MLEventProblemLink — olayın bağlı olduğu Problem (dedektör × Problem).
type MLEventProblemLink struct {
	Detector  string
	ProblemID string
}

// mlRecentProblemLinksSQL — SAF: son pencerede görülen olayların Problem bağı
// (açık P1 freni). Küçük state tablosu, zaman sınırlı, LIMIT, süre tavanı.
func mlRecentProblemLinksSQL() string {
	return `
		SELECT toString(detector) AS detector, problem_ref
		FROM (SELECT detector, ` + mlEventProblemIDExpr + ` AS problem_ref
		      FROM ml_events FINAL
		      WHERE last_seen >= ?)
		WHERE problem_ref != ''
		GROUP BY detector, problem_ref
		LIMIT 5000
		SETTINGS max_execution_time = 10`
}

// MLRecentProblemLinks — son `since`'den beri görülen olayların Problem bağları.
func (s *Store) MLRecentProblemLinks(ctx context.Context, since time.Time) ([]MLEventProblemLink, error) {
	rows, err := s.conn.Query(ctx, mlRecentProblemLinksSQL(), chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLEventProblemLink
	for rows.Next() {
		var l MLEventProblemLink
		if err := rows.Scan(&l.Detector, &l.ProblemID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// OpenP1ProblemIDs — açık (open / acknowledged) Problem'lerden önceliği şu
// an P1 olanlar (öncelik okuma anında hesaplanır; saklanmaz).
func (s *Store) OpenP1ProblemIDs(ctx context.Context, now time.Time) (map[string]bool, error) {
	snap, err := s.OpenProblemsSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	cfg := CurrentProblemPriority()
	out := map[string]bool{}
	for _, p := range snap.all {
		if prio, _ := computePriority(*p, now.UnixNano(), cfg); prio == "P1" {
			out[p.ID] = true
		}
	}
	return out, nil
}

// MLDetectorProblemsFed — dedektörün penceredeki olaylarının bağlandığı
// Problem sayısı ve bunlardan onaylı, gürültü olmayan vakası olanlar.
type MLDetectorProblemsFed struct {
	Fed  int64 `json:"fed"`
	Real int64 `json:"real"`
}

// mlProblemsFedSQL — SAF: tek tarama (ml_events FINAL, zaman sınırlı) +
// vaka ifadesi (Döküm / karne ile AYNI problemCaseLabelSQL).
func mlProblemsFedSQL() string {
	return `
		SELECT toString(m.detector) AS detector,
		       toUInt64(uniqExact(m.problem_ref)) AS fed,
		       toUInt64(uniqExactIf(m.problem_ref, c.source_id != '' AND c.case_cat != 'noise')) AS real_n
		FROM (SELECT detector, ` + mlEventProblemIDExpr + ` AS problem_ref
		      FROM ml_events FINAL
		      WHERE started_at >= ?) AS m
		LEFT JOIN
		  (` + problemCaseLabelSQL + `) AS c ON c.source_id = m.problem_ref
		WHERE m.problem_ref != ''
		GROUP BY m.detector
		LIMIT 500
		SETTINGS max_execution_time = 20, join_use_nulls = 0`
}

// MLProblemsFed — dedektör → beslenen / gerçek Problem sayısı (pencere `since`).
func (s *Store) MLProblemsFed(ctx context.Context, since time.Time) (map[string]MLDetectorProblemsFed, error) {
	rows, err := s.conn.Query(ctx, mlProblemsFedSQL(), chDateTime64Arg(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MLDetectorProblemsFed{}
	for rows.Next() {
		var det string
		var fed, real uint64
		if err := rows.Scan(&det, &fed, &real); err != nil {
			return nil, err
		}
		out[strings.TrimSpace(det)] = MLDetectorProblemsFed{Fed: int64(fed), Real: int64(real)}
	}
	return out, rows.Err()
}
