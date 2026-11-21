package sre

// replay_report.go — CoSRE doğruluk ölçümü Faz A, dilim A2:
// "Kök neden" etiketli Problem'lerde SRE araştırmasını yeniden koşturup
// etikete göre notlayan rapor (incident.io emsali: Bullseye 100 / On target
// 65 / Miss 35 / Nowhere near 0 + erişim).
//
// YENİ MEKANİZMA YOK — mevcut değerlendirme tekrarı (eval.go, Kind=replay)
// aynen kullanılır:
//   - Tekrar işi kendi kuyruk anahtarında: "replay-<runKey>-<problemId>"
//     (runKey = "<raporId>n<koşu>"); canlı "sre-<problemId>" kaydı ASLA
//     yazılmaz. Drain işi canlı işlerin HEPSİNDEN sonra alır (OrderQueued),
//     MaxConcurrent + ItemBudgetSec aynen geçerli, kuyruk payı MaxQueue/2
//     (evalQueueRoom — değerlendirme koşularıyla ORTAK pay).
//   - Orkestratörün tekrar dalı: plan anı problemin kendi penceresi (AsOf =
//     başlangıç + replayAsOfDelay, okumalar o pencereye bağlı), öneri yok,
//     anlatım yok, Argo okuması yok. sre paketi notify'ı import ETMEZ; tekrar
//     hiçbir bildirim / gönderi üretmez (replay_report_test.go pinler).
//   - Problem başına N koşu (varsayılan 2) → koşudan koşuya varyans.
//
// Notlama (ProcessReplayReports) sre pod'unda lider kilitli tik (dakikada
// bir): biten tekrar satırını okur, sonucu + deterministik erişimi rapora
// işler, hakemi çağırır. Hakem = arka plan AI kapıları (Active, otomatik AI
// açık, kota geri çekilmesi yok) + saatlik sre bütçesinin (MaxLLMCallsPerHour)
// EN ÇOK YARISI + yalnız yerel (self-hosted) çözülen profil — profil yerel
// değilse çağrı yapılmaz, koşu notsuz kalır. Tik başına ≤3 hakem çağrısı.
//
// Rapor satırı: saved_views page='sre-replay-report' (chstore/sre_replay_report.go).
// Haftalık zamanlama: system_settings["sre_agent.replay"] (ayrı anahtar —
// sre_agent formu tüm blobu yazar; contextpack emsali), VARSAYILAN KAPALI.
//
// Varyant: koşu başına özellik anahtarı — yalnız orkestratörün ZATEN okuduğu
// bayraklar: contextPack (Investigation.Pack), plain (Investigation.Plain —
// CoSRE Faz B kapalı). Başka anahtar yok.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// Rapor durumları (satırın name kolonu).
const (
	ReplayRunning = "running"
	ReplayDone    = "done"
)

const (
	// ReplayMaxProblems / ReplayDefaultProblems — rapor başına Problem.
	ReplayMaxProblems     = 30
	ReplayDefaultProblems = 20
	// ReplayMaxRepeats / ReplayDefaultRepeats — Problem başına koşu.
	ReplayMaxRepeats     = 3
	ReplayDefaultRepeats = 2
	// ReplayDefaultDays — etiket penceresi varsayılanı.
	ReplayDefaultDays = 30
	// ReplayJudgeSurface — hakem çağrısının ai_calls yüzeyi (arka plan grubu).
	ReplayJudgeSurface = "sre-replay-judge"

	replayJudgePerTick   = 3
	replayJudgeTimeout   = 60 * time.Second
	replayJudgeAttempts  = 2
	replayTick           = time.Minute
	replayLockKey        = "coremetry:lock:sre-replay-grader"
	replayLockLease      = 5 * time.Minute
	replayTickBudget     = 4 * time.Minute
	replayWeekly         = 7 * 24 * time.Hour
	replayCauseRunes     = 300
	replayReasonNoise    = "gürültü — gerçek kök neden yok, notlanmaz"
	replayReasonNoRoom   = "kuyruk payı dolu (tekrar MaxQueue'nun yarısını aşamaz)"
	replayReasonNoAnchor = "servis / başlangıç yok"
)

// ReplayDayRungs — etiket penceresi basamakları (gün).
var ReplayDayRungs = []int{7, 30, 90, 180}

var (
	// ErrReplayBusy — bitmemiş taze rapor var.
	ErrReplayBusy = errors.New("replay raporu sürüyor — önceki rapor bitmeden yenisi başlamaz")
	// ErrReplayNotFound — rapor yok.
	ErrReplayNotFound = errors.New("replay raporu bulunamadı")
)

// ReplayVariant — koşu başına özellik anahtarları (yalnız orkestratörün okuduğu bayraklar).
type ReplayVariant struct {
	// ContextPack — tekrar Context Pack okuyucularıyla (Investigation.Pack).
	ContextPack bool `json:"contextPack,omitempty"`
	// Plain — CoSRE Faz B: oyun kitabı + hipotez durumu + kanıt kapısı
	// KAPALI (B öncesi davranış, Investigation.Plain). Aynı etiket
	// penceresinde plain ve varsayılan iki rapor = B'nin ölçümü.
	Plain bool `json:"plain,omitempty"`
}

// ReplayRun — bir Problem'in bir tekrar koşusu + notu.
type ReplayRun struct {
	Rep        int       `json:"rep"`
	State      string    `json:"state"` // queued | running | done | partial | error | skipped
	Reason     string    `json:"reason,omitempty"`
	Conclusion string    `json:"conclusion,omitempty"` // ≤400 rune
	Top        []EvalHyp `json:"top,omitempty"`
	Grade      string    `json:"grade,omitempty"`
	Score      *int      `json:"score,omitempty"`
	Why        string    `json:"why,omitempty"`
	Reach      *bool     `json:"reach,omitempty"`
	// Inconclusive — CoSRE Faz B3: kanıt kapısı "yetersiz" dedi; sonuç
	// kök neden iddia etmedi (plain varyantta hep yok).
	Inconclusive bool   `json:"inconclusive,omitempty"`
	ReachBy      string `json:"reachBy,omitempty"` // evidence | judge
	GradeErr     string `json:"gradeErr,omitempty"`
	Attempts     int    `json:"gradeAttempts,omitempty"`
	FinishedAt   int64  `json:"finishedAt,omitempty"`
}

// ReplayItem — etiketli bir Problem.
type ReplayItem struct {
	ProblemID string      `json:"problemId"`
	CaseID    string      `json:"caseId"`
	Service   string      `json:"service"`
	Cause     string      `json:"cause"` // etiket ≤300 rune
	Category  string      `json:"category"`
	Culprit   string      `json:"culprit,omitempty"`
	StartedAt int64       `json:"startedAt"`       // unix ns — çıpa
	State     string      `json:"state,omitempty"` // "" etkin | skipped
	Reason    string      `json:"reason,omitempty"`
	Runs      []ReplayRun `json:"runs"`
}

// ReplayReport — rapor satırının gövdesi.
type ReplayReport struct {
	ReportID   string        `json:"reportId"`
	State      string        `json:"state"`
	Trigger    string        `json:"trigger"` // manual | schedule
	By         string        `json:"by,omitempty"`
	Build      string        `json:"build,omitempty"`
	Days       int           `json:"days"`
	Repeats    int           `json:"repeats"`
	Limit      int           `json:"limit"`
	From       int64         `json:"from"` // unix ns — etiket (vaka açılışı) penceresi
	To         int64         `json:"to"`
	Variant    ReplayVariant `json:"variant"`
	CreatedAt  int64         `json:"createdAt"`
	FinishedAt int64         `json:"finishedAt,omitempty"`
	Found      int           `json:"found"`
	Judge      string        `json:"judge,omitempty"` // hakem modeli (son not)
	Items      []ReplayItem  `json:"items"`
}

// ReplayRequest — rapor başlatma isteği.
type ReplayReportRequest struct {
	Days    int
	Repeats int
	Limit   int
	Variant ReplayVariant
	Trigger string
	By      string
	Build   string
	Now     time.Time
}

// ReplayStore — raporun okuma / yazma yüzeyi (*chstore.Store karşılar).
// Problem / vaka yalnız OKUNUR; tek yazımlar tekrar işleri + rapor satırı.
type ReplayStore interface {
	QueueStore
	ListIncidentCases(ctx context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error)
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	ListProblemRootCauseExtras(ctx context.Context, problemIDs []string) (map[string]chstore.ProblemRootCauseExtra, error)
	ListSREReplayRows(ctx context.Context, runID string, limit int) ([]chstore.SavedView, error)
	ListSREReplayReports(ctx context.Context, limit int) ([]chstore.SavedView, error)
	GetSREReplayReport(ctx context.Context, id string) (*chstore.SavedView, error)
	PutSREReplayReport(ctx context.Context, id, state, body string, createdAt int64) error
}

// ReplayRunKey — SAF: raporun r'inci koşusunun tekrar koşu anahtarı (eval
// alfabesi: a-z0-9, '-' yok → "replay-<runKey>-" öneki tek koşuya ait).
func ReplayRunKey(reportID string, rep int) string { return reportID + "n" + strconv.Itoa(rep) }

func newReplayReportID(now time.Time) string { return "r" + strconv.FormatInt(now.UnixNano(), 36) }

// NormalizeReplayRequest — SAF: gün basamağa (en yakın üst; >180 → 180),
// koşu 1..3 (0 → 2), Problem 1..30 (0 → 20), tetik manual | schedule.
func NormalizeReplayRequest(req ReplayReportRequest) ReplayReportRequest {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	days := ReplayDayRungs[len(ReplayDayRungs)-1]
	if req.Days <= 0 {
		days = ReplayDefaultDays
	} else {
		for _, d := range ReplayDayRungs {
			if req.Days <= d {
				days = d
				break
			}
		}
	}
	req.Days = days
	switch {
	case req.Repeats <= 0:
		req.Repeats = ReplayDefaultRepeats
	case req.Repeats > ReplayMaxRepeats:
		req.Repeats = ReplayMaxRepeats
	}
	switch {
	case req.Limit <= 0:
		req.Limit = ReplayDefaultProblems
	case req.Limit > ReplayMaxProblems:
		req.Limit = ReplayMaxProblems
	}
	if req.Trigger != "schedule" {
		req.Trigger = "manual"
	}
	return req
}

// ── kayıt kodlama ───────────────────────────────────────────────────────────

func encodeReplayReport(rep ReplayReport) (string, error) {
	if rep.Items == nil {
		rep.Items = []ReplayItem{}
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		return "", err
	}
	if len(raw) <= chstore.SREReplayReportMaxBytes {
		return string(raw), nil
	}
	// Sığmazsa sonuç metinleri kısalır (not / erişim kalır).
	cp := rep
	cp.Items = append([]ReplayItem(nil), rep.Items...)
	for i := range cp.Items {
		runs := append([]ReplayRun(nil), cp.Items[i].Runs...)
		for j := range runs {
			runs[j].Conclusion = truncRunes(runs[j].Conclusion, 120)
			runs[j].Top = nil
		}
		cp.Items[i].Runs = runs
	}
	raw, err = json.Marshal(cp)
	if err != nil {
		return "", err
	}
	if len(raw) > chstore.SREReplayReportMaxBytes {
		return "", fmt.Errorf("sre replay: rapor %d B, tavan %d B", len(raw), chstore.SREReplayReportMaxBytes)
	}
	return string(raw), nil
}

func saveReplayReport(ctx context.Context, st ReplayStore, rep ReplayReport) error {
	body, err := encodeReplayReport(rep)
	if err != nil {
		return err
	}
	return st.PutSREReplayReport(ctx, rep.ReportID, rep.State, body, rep.CreatedAt)
}

// DecodeReplayReport — SAF: satır → rapor (kimlik gövdeyle eşleşmeli).
func DecodeReplayReport(v chstore.SavedView) (ReplayReport, bool) {
	if v.Page != chstore.SREReplayReportPage || v.OwnerID != "" || v.Name == "" {
		return ReplayReport{}, false
	}
	var rep ReplayReport
	if err := json.Unmarshal([]byte(v.QueryString), &rep); err != nil || rep.ReportID == "" || v.ID != chstore.SREReplayReportID(rep.ReportID) {
		return ReplayReport{}, false
	}
	rep.State = v.Name
	return rep, true
}

// ── başlatma ────────────────────────────────────────────────────────────────

func replayRunTerminal(r ReplayRun) bool {
	switch r.State {
	case StateDone, StatePartial, StateError, CaseSkipped:
		return true
	}
	return false
}

// replayNeedsGrade — SAF: biten (done / partial) ve notsuz, not hatası almamış koşu.
func replayNeedsGrade(r ReplayRun) bool {
	return (r.State == StateDone || r.State == StatePartial) && r.Grade == "" && r.GradeErr == ""
}

// finalizeReplay — SAF: her koşu bitti ve notlanacak koşu kalmadıysa "done".
func finalizeReplay(rep ReplayReport, now time.Time) ReplayReport {
	for _, it := range rep.Items {
		for _, r := range it.Runs {
			if !replayRunTerminal(r) || replayNeedsGrade(r) {
				rep.State = ReplayRunning
				return rep
			}
		}
	}
	if rep.State != ReplayDone || rep.FinishedAt == 0 {
		rep.State, rep.FinishedAt = ReplayDone, now.UnixNano()
	}
	return rep
}

// replayBusy — taze ve bitmemiş rapor var mı.
func replayBusy(ctx context.Context, st ReplayStore, now time.Time) (bool, error) {
	rows, err := st.ListSREReplayReports(ctx, 10)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		rep, ok := DecodeReplayReport(r)
		if ok && rep.State != ReplayDone && now.Sub(time.Unix(0, rep.CreatedAt)) < evalStaleAfter {
			return true, nil
		}
	}
	return false, nil
}

// StartReplayReport — etiketli Problem'leri okur, Problem başına N tekrar
// işini kuyruğa alır, rapor satırını yazar.
func StartReplayReport(ctx context.Context, st ReplayStore, req ReplayReportRequest, cfg Settings) (ReplayReport, error) {
	if !cfg.IsEnabled() {
		return ReplayReport{}, ErrDisabled
	}
	req = NormalizeReplayRequest(req)
	now := req.Now
	if busy, err := replayBusy(ctx, st, now); err != nil {
		return ReplayReport{}, err
	} else if busy {
		return ReplayReport{}, ErrReplayBusy
	}
	from := now.Add(-time.Duration(req.Days) * 24 * time.Hour)
	cases, err := st.ListIncidentCases(ctx, chstore.IncidentCaseQuery{Status: chstore.CaseConfirmed, Source: "problem",
		From: from, To: now, Limit: req.Limit})
	if err != nil {
		return ReplayReport{}, err
	}
	ids := make([]string, 0, len(cases))
	for _, c := range cases {
		ids = append(ids, c.SourceID)
	}
	extras, err := st.ListProblemRootCauseExtras(ctx, ids)
	if err != nil {
		return ReplayReport{}, err
	}
	queued, err := st.CountSREQueued(ctx)
	if err != nil {
		return ReplayReport{}, err
	}
	room := evalQueueRoom(cfg, queued)
	rep := ReplayReport{ReportID: newReplayReportID(now), State: ReplayRunning, Trigger: req.Trigger, By: req.By, Build: req.Build,
		Days: req.Days, Repeats: req.Repeats, Limit: req.Limit, From: from.UnixNano(), To: now.UnixNano(), Variant: req.Variant,
		CreatedAt: now.UnixNano(), Found: len(cases), Items: make([]ReplayItem, 0, len(cases))}
	for _, c := range cases {
		it := ReplayItem{ProblemID: c.SourceID, CaseID: c.CaseID, Service: c.Service, Category: c.Category,
			Cause: truncRunes(strings.TrimSpace(c.FinalCause), replayCauseRunes), StartedAt: c.OpenedAt.UnixNano(),
			Culprit: extras[c.SourceID].Culprit}
		switch {
		case c.Category == "noise":
			it.State, it.Reason = CaseSkipped, replayReasonNoise
		default:
			if p, err := st.GetProblem(ctx, c.SourceID); err == nil && p != nil && p.ID != "" {
				if p.Service == "" || chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
					it.State, it.Reason = CaseSkipped, evalReasonNotService
				} else {
					it.Service = p.Service
					if p.StartedAt > 0 {
						it.StartedAt = p.StartedAt
					}
				}
			}
			if it.State == "" && (it.Service == "" || it.StartedAt <= 0) {
				it.State, it.Reason = CaseSkipped, replayReasonNoAnchor
			}
			if it.State == "" && room < req.Repeats {
				it.State, it.Reason = CaseSkipped, replayReasonNoRoom
			}
		}
		if it.State == "" {
			for r := 1; r <= req.Repeats; r++ {
				run := ReplayRun{Rep: r, State: StateQueued}
				created, err := EnqueueReplay(ctx, st, ReplayRequest{RunID: ReplayRunKey(rep.ReportID, r), CaseID: c.CaseID,
					ProblemID: it.ProblemID, Service: it.Service, ProblemStartedAt: it.StartedAt, By: req.By, Now: now,
					ContextPack: req.Variant.ContextPack, Plain: req.Variant.Plain}, cfg)
				switch {
				case err != nil:
					run.State, run.Reason = CaseSkipped, truncRunes("kuyruğa alınamadı: "+err.Error(), 200)
				case created:
					room--
				}
				it.Runs = append(it.Runs, run)
			}
		}
		rep.Items = append(rep.Items, it)
	}
	rep = finalizeReplay(rep, now)
	if err := saveReplayReport(ctx, st, rep); err != nil {
		return ReplayReport{}, err
	}
	return rep, nil
}

// ── okuma ───────────────────────────────────────────────────────────────────

// ListReplayReports — en yeni raporlar (≤limit).
func ListReplayReports(ctx context.Context, st ReplayStore, limit int) ([]ReplayReport, error) {
	rows, err := st.ListSREReplayReports(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ReplayReport, 0, len(rows))
	for _, r := range rows {
		if rep, ok := DecodeReplayReport(r); ok {
			out = append(out, rep)
		}
	}
	return out, nil
}

// LoadReplayReport — tek rapor (yoksa ok=false).
func LoadReplayReport(ctx context.Context, st ReplayStore, id string) (ReplayReport, bool, error) {
	v, err := st.GetSREReplayReport(ctx, id)
	if err != nil || v == nil {
		return ReplayReport{}, false, err
	}
	rep, ok := DecodeReplayReport(*v)
	return rep, ok, nil
}

// ── notlama ─────────────────────────────────────────────────────────────────

// ReplayJudge — hakem: gate (çağrı yapılabilir mi + neden) ve call (tek çağrı).
type ReplayJudge struct {
	Gate  func(ctx context.Context) (bool, string)
	Call  func(ctx context.Context, system, user string) (string, error)
	Model func() string
}

// applyReplayRow — SAF: biten tekrar kaydını koşuya işler (sonuç + deterministik
// erişim). Bitmemiş tekrar yalnız durumu taşır. Değişti mi döner.
func applyReplayRow(it ReplayItem, r *ReplayRun, inv Investigation) bool {
	switch inv.State {
	case StateDone, StatePartial, StateError:
		r.State, r.FinishedAt = inv.State, inv.FinishedAt
		r.Top = caseTop(inv)
		r.Conclusion = truncRunes(replayConclusion(inv), replayStoredRunes)
		r.Inconclusive = replayInconclusive(inv)
		if inv.State == StateError {
			r.Reason = truncRunes(firstNonEmpty(inv.Error, "tekrar tamamlanamadı"), 200)
		}
		if it.Culprit != "" {
			reached := culpritReached(it.Culprit, evidenceEntities(inv))
			r.Reach, r.ReachBy = &reached, "evidence"
		}
		return true
	case StateQueued, StateRunning:
		if r.State != inv.State {
			r.State = inv.State
			return true
		}
	}
	return false
}

// applyReplayGrade — SAF: hakem çıktısı → koşu. Hata deneme sayar; tavanda
// GradeErr yazılır (koşu notsuz biter, puana girmez).
func applyReplayGrade(r *ReplayRun, out string, callErr error) {
	grade, why, reach := "", "", (*bool)(nil)
	err := callErr
	if err == nil {
		grade, why, reach, err = ParseReplayGrade(out)
	}
	if err != nil {
		r.Attempts++
		if r.Attempts >= replayJudgeAttempts {
			r.GradeErr = truncRunes("hakem notu alınamadı: "+err.Error(), 200)
		}
		return
	}
	sc, _ := GradeScore(grade)
	r.Grade, r.Score, r.Why = grade, &sc, why
	if r.ReachBy != "evidence" && reach != nil {
		r.Reach, r.ReachBy = reach, "judge"
	}
}

// ProcessReplayReport — bir raporun bir tiki: tekrar satırlarını işler, en
// çok budget hakem çağrısı yapar, bayatlamış koşuları kapatır. Yazmaz;
// (rapor, değişti mi, kalan bütçe) döner.
func ProcessReplayReport(ctx context.Context, st ReplayStore, rep ReplayReport, judge ReplayJudge, budget int, now time.Time) (ReplayReport, bool, int, error) {
	if rep.State == ReplayDone {
		return rep, false, budget, nil
	}
	changed := false
	rep.Items = append([]ReplayItem(nil), rep.Items...)
	rows := map[int]map[string]Investigation{}
	for r := 1; r <= rep.Repeats; r++ {
		key := ReplayRunKey(rep.ReportID, r)
		vs, err := st.ListSREReplayRows(ctx, key, chstore.SREReplayRowsMax)
		if err != nil {
			return rep, false, budget, err
		}
		m := map[string]Investigation{}
		for _, v := range vs {
			if inv, ok := Decode(v); ok && inv.Kind == KindReplay && inv.EvalRun == key {
				m[inv.ProblemID] = inv
			}
		}
		rows[r] = m
	}
	stale := now.Sub(time.Unix(0, rep.CreatedAt)) >= evalStaleAfter
	gateWhy := ""
	for i := range rep.Items {
		it := &rep.Items[i]
		if it.State == CaseSkipped {
			continue
		}
		it.Runs = append([]ReplayRun(nil), it.Runs...)
		for j := range it.Runs {
			r := &it.Runs[j]
			if !replayRunTerminal(*r) {
				if inv, ok := rows[r.Rep][it.ProblemID]; ok && applyReplayRow(*it, r, inv) {
					changed = true
				}
			}
			if !replayRunTerminal(*r) && stale {
				r.State, r.Reason = StateError, "tekrar işi kuyrukta yok (süpürüldü ya da yazılamadı)"
				changed = true
			}
			if !replayNeedsGrade(*r) {
				continue
			}
			if budget > 0 && gateWhy == "" && judge.Call != nil {
				if judge.Gate != nil {
					if ok, why := judge.Gate(ctx); !ok {
						gateWhy = firstNonEmpty(why, "hakem kapalı")
					}
				}
				if gateWhy == "" {
					inv := rows[r.Rep][it.ProblemID]
					user := BuildReplayJudgeUser(*it, replayConclusion(inv), evidenceEntities(inv))
					jctx, cancel := context.WithTimeout(ctx, replayJudgeTimeout)
					out, err := judge.Call(jctx, copilot.SystemPromptSREReplayJudge(), user)
					cancel()
					budget--
					applyReplayGrade(r, out, err)
					if judge.Model != nil && r.Grade != "" {
						rep.Judge = judge.Model()
					}
					changed = true
					continue
				}
			}
			if stale && (gateWhy != "" || judge.Call == nil) {
				r.GradeErr = "hakem çalışmadı: " + firstNonEmpty(gateWhy, judgeMissing(judge))
				changed = true
			}
		}
	}
	before := rep.State
	rep = finalizeReplay(rep, now)
	return rep, changed || rep.State != before, budget, nil
}

func judgeMissing(j ReplayJudge) string {
	if j.Call == nil {
		return "hakem bağlı değil"
	}
	return ""
}

// ── zamanlama ayarı ─────────────────────────────────────────────────────────

// ReplayScheduleKey — system_settings anahtarı (ayrı: sre_agent formu tüm blobu yazar).
const ReplayScheduleKey = "sre_agent.replay"

// ReplaySchedule — haftalık rapor (VARSAYILAN KAPALI).
type ReplaySchedule struct {
	Weekly    bool   `json:"weekly"`
	Days      int    `json:"days"`
	Repeats   int    `json:"repeats"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// NormalizeReplaySchedule — SAF: gün / koşu istek kurallarıyla.
func NormalizeReplaySchedule(c ReplaySchedule) ReplaySchedule {
	n := NormalizeReplayRequest(ReplayReportRequest{Days: c.Days, Repeats: c.Repeats, Now: time.Unix(0, 1)})
	c.Days, c.Repeats = n.Days, n.Repeats
	return c
}

// GetReplaySchedule — saklanan zamanlama (yoksa / bozuksa kapalı varsayılan).
func GetReplaySchedule(ctx context.Context, st SettingsStore) (ReplaySchedule, error) {
	raw, err := st.GetSetting(ctx, ReplayScheduleKey)
	if err != nil {
		return ReplaySchedule{}, err
	}
	var c ReplaySchedule
	if len(raw) > 0 && json.Unmarshal(raw, &c) != nil {
		c = ReplaySchedule{}
	}
	return NormalizeReplaySchedule(c), nil
}

// SaveReplaySchedule — admin PUT.
func SaveReplaySchedule(ctx context.Context, st SettingsStore, c ReplaySchedule) (ReplaySchedule, error) {
	c = NormalizeReplaySchedule(c)
	raw, err := json.Marshal(c)
	if err != nil {
		return ReplaySchedule{}, err
	}
	return c, st.PutSetting(ctx, ReplayScheduleKey, raw)
}

// scheduleDue — SAF: haftalık açık ve son rapor (tetik fark etmez) ≥7 gün önce ya da hiç yok.
func scheduleDue(c ReplaySchedule, reports []ReplayReport, now time.Time) bool {
	if !c.Weekly {
		return false
	}
	for _, r := range reports {
		if now.Sub(time.Unix(0, r.CreatedAt)) < replayWeekly {
			return false
		}
	}
	return true
}

// ── döngü (sre pod'u) ───────────────────────────────────────────────────────

// ReplayGraderDeps — notlayıcı döngünün bağımlılıkları.
type ReplayGraderDeps struct {
	Store    ReplayStore
	Settings SettingsStore
	Cache    cache.Cache
	Lock     cache.Lock
	Copilot  Narrator
	Build    string
	Now      func() time.Time
}

// localProfiler — *copilot.Service karşılar: çağrının çözülen profili yerel mi.
type localProfiler interface {
	ProfileLocalOnly(ctx context.Context) bool
}

func judgeCtx(ctx context.Context) context.Context {
	return copilot.WithMeta(ctx, copilot.CallMeta{Surface: ReplayJudgeSurface, UserID: "system"})
}

// replayJudgeGate — SAF: arka plan kapıları + bütçenin yarısı + yerel profil.
func replayJudgeGate(n Narrator, cfg Settings, usedThisHour int, local bool) (bool, string) {
	half := cfg
	half.MaxLLMCallsPerHour = max(1, cfg.MaxLLMCallsPerHour/2)
	if ok, why := narrationGate(n, half, usedThisHour); !ok {
		return false, why
	}
	if !local {
		return false, "hakem profili yerel (self-hosted) değil"
	}
	return true, ""
}

// newReplayJudge — üretim hakemi: Copilot (arka plan grubu yüzeyi), saatlik
// sre bütçesi (aynı Redis sayacı; her çağrı sayılır).
func newReplayJudge(d ReplayGraderDeps) ReplayJudge {
	if d.Copilot == nil {
		return ReplayJudge{}
	}
	budget := newLLMBudget(d.Cache, d.Now)
	return ReplayJudge{
		Gate: func(ctx context.Context) (bool, string) {
			lp, ok := d.Copilot.(localProfiler)
			local := ok && lp.ProfileLocalOnly(judgeCtx(ctx))
			return replayJudgeGate(d.Copilot, CurrentSettings(), budget.used(ctx), local)
		},
		Call: func(ctx context.Context, system, user string) (string, error) {
			budget.inc(ctx)
			return d.Copilot.Explain(judgeCtx(ctx), system, user)
		},
		Model: d.Copilot.ActiveModel,
	}
}

// RunReplayTick — tek tik: zamanlama + bitmemiş raporların notlanması.
func RunReplayTick(ctx context.Context, d ReplayGraderDeps, judge ReplayJudge) error {
	now := d.Now()
	reports, err := ListReplayReports(ctx, d.Store, 10)
	if err != nil {
		return err
	}
	if d.Settings != nil {
		if sc, err := GetReplaySchedule(ctx, d.Settings); err == nil && scheduleDue(sc, reports, now) && CurrentSettings().IsEnabled() {
			rep, err := StartReplayReport(ctx, d.Store, ReplayReportRequest{Days: sc.Days, Repeats: sc.Repeats, Trigger: "schedule",
				By: "schedule", Build: d.Build, Now: now}, CurrentSettings())
			if err != nil {
				log.Printf("[sre-replay] haftalık rapor başlatılamadı: %v", err)
			} else {
				log.Printf("[sre-replay] haftalık rapor %s başladı (%d Problem)", rep.ReportID, rep.Found)
			}
		}
	}
	budget := replayJudgePerTick
	for _, rep := range reports {
		if rep.State == ReplayDone {
			continue
		}
		next, changed, left, err := ProcessReplayReport(ctx, d.Store, rep, judge, budget, d.Now())
		if err != nil {
			return err
		}
		budget = left
		if changed {
			if err := saveReplayReport(ctx, d.Store, next); err != nil {
				return err
			}
		}
	}
	return nil
}

// StartReplayGrader — sre pod'unda (drain'in yanında) lider kilitli dakikalık döngü.
func StartReplayGrader(ctx context.Context, d ReplayGraderDeps) {
	if d.Store == nil || d.Lock == nil {
		return
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	judge := newReplayJudge(d)
	t := time.NewTicker(replayTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ok, err := d.Lock.TryAcquire(ctx, replayLockKey, replayLockLease)
		if err != nil || !ok {
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, replayTickBudget)
		if err := RunReplayTick(tctx, d, judge); err != nil {
			log.Printf("[sre-replay] tik: %v", err)
		}
		cancel()
		_ = d.Lock.Release(context.WithoutCancel(ctx), replayLockKey)
	}
}
