package cosrelearn

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// QualityStore — gece işinin G/Ç yüzeyi (*chstore.Store karşılar).
type QualityStore interface {
	ListCosreInteractionsForScoring(ctx context.Context, from, to time.Time) ([]chstore.CosreInteraction, error)
	ListCosreSignals(ctx context.Context, from, to time.Time) ([]chstore.CosreSignal, error)
	AIFeedbackVerdictsSince(ctx context.Context, from time.Time) (map[string]int8, error)
	ProblemResolvedAt(ctx context.Context, ids []string) (map[string]time.Time, error)
	InsertCosreAnswerQuality(ctx context.Context, rows []chstore.CosreAnswerQuality) error
	InsertCosreToolQuality(ctx context.Context, rows []chstore.CosreToolQuality) error
	CosreQualityDayDone(ctx context.Context, day time.Time) (bool, error)
	GroundingStore // Faz 4 — çevrimdışı dayanak denetimi puanlamadan ÖNCE
	SettingsReader // istemci sinyali ağırlıkları (system_settings "cosre")
}

const (
	qualityLockKey = "cosre-quality-nightly"
	// qualitySettle — gün kapandıktan sonra geç sinyaller (kopya, çözülme)
	// gelsin diye beklenen süre.
	qualitySettle = 2 * time.Hour
	// qualityCatchUp — kaçırılan günler (worker kapalıydı) en çok bu kadar geriye.
	qualityCatchUp = 3
	followUpSlack  = 10 * time.Minute
)

// DueDays — SAF: now itibarıyla işlenmeye hazır günler (UTC, eskiden yeniye).
func DueDays(now time.Time) []time.Time {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var out []time.Time
	for k := qualityCatchUp; k >= 1; k-- {
		d := today.AddDate(0, 0, -k)
		if !now.Before(d.Add(24*time.Hour + qualitySettle)) {
			out = append(out, d)
		}
	}
	return out
}

// RunQualityDay — bir günün cevap + tool puanlarını hesaplayıp yazar.
// Yeniden koşmak güvenli (RMT dedup anahtarları).
func RunQualityDay(ctx context.Context, st QualityStore, day time.Time) (answers, tools int, err error) {
	return RunQualityDayWithJudge(ctx, st, nil, "", day)
}

// RunQualityDayWithJudge — önce örneklem hakeme (Faz 4; judge nil → atlanır),
// sonra puan: dayanaksız cevaplar SigUngrounded alır.
func RunQualityDayWithJudge(ctx context.Context, st QualityStore, judge Judge, model string, day time.Time) (answers, tools int, err error) {
	from, to := day, day.Add(24*time.Hour)
	if j, u, gerr := RunGrounding(ctx, st, judge, model, from, to); gerr != nil {
		log.Printf("[cosre-grounding] %s: %v", day.Format("2006-01-02"), gerr)
	} else if j > 0 {
		log.Printf("[cosre-grounding] %s: %d cevap denetlendi, %d dayanaksız", day.Format("2006-01-02"), j, u)
	}
	ungrounded, uerr := st.UngroundedExchanges(ctx, from, to)
	if uerr != nil {
		ungrounded = nil // okunamadı → işaret yok (puan düşürmek için kanıt gerekir)
	}
	all, err := st.ListCosreInteractionsForScoring(ctx, from, to.Add(followUpSlack))
	if err != nil {
		return 0, 0, err
	}
	sigs, err := st.ListCosreSignals(ctx, from, to.Add(24*time.Hour))
	if err != nil {
		return 0, 0, err
	}
	fb, err := st.AIFeedbackVerdictsSince(ctx, from)
	if err != nil {
		return 0, 0, err
	}
	var pids []string
	seen := map[string]bool{}
	for _, r := range all {
		if r.ProblemID != "" && !seen[r.ProblemID] {
			seen[r.ProblemID] = true
			pids = append(pids, r.ProblemID)
		}
	}
	resolved, err := st.ProblemResolvedAt(ctx, pids)
	if err != nil {
		return 0, 0, err
	}
	in := Inputs{Interactions: all, Signals: sigs, Feedback: fb, Resolved: resolved, Ungrounded: ungrounded}
	derived := DeriveSignals(in)
	weights, werr := LoadSignalWeights(ctx, st)
	if werr != nil {
		log.Printf("[cosre-quality] %s: sinyal ağırlıkları okunamadı, varsayılanlar: %v", day.Format("2006-01-02"), werr)
	}
	var dayRows []chstore.CosreInteraction
	for _, r := range all {
		if !r.CreatedAt.Before(from) && r.CreatedAt.Before(to) {
			dayRows = append(dayRows, r)
		}
	}
	aq := ScoreAnswers(dayRows, derived, CardScores(in, weights), weights)
	if err := st.InsertCosreAnswerQuality(ctx, aq); err != nil {
		return 0, 0, err
	}
	tq := ScoreTools(day, dayRows, derived)
	if err := st.InsertCosreToolQuality(ctx, tq); err != nil {
		return len(aq), 0, err
	}
	return len(aq), len(tq) - 1, nil
}

// StartQualityJob — worker'da saatte bir, Redis lider kilidiyle; hazır ve
// işlenmemiş günleri (koşu işareti yok) puanlar.
// judge nil → Faz 4 hakemi atlanır (model yapılandırılmamış).
func StartQualityJob(ctx context.Context, st QualityStore, lock cache.Lock, interval time.Duration, judge Judge, model func() string) {
	if interval <= 0 {
		interval = time.Hour
	}
	leader := cache.NewLeaderHolder(lock, qualityLockKey, cache.LeaderTTL(interval))
	leader.Start(ctx)
	tick := func() {
		if !leader.IsLeader() {
			return
		}
		for _, d := range DueDays(time.Now()) {
			done, err := st.CosreQualityDayDone(ctx, d)
			if err != nil {
				log.Printf("[cosre-quality] %s durum okunamadı: %v", d.Format("2006-01-02"), err)
				return
			}
			if done {
				continue
			}
			rctx, cancel := context.WithTimeout(ctx, 75*time.Minute) // hakem: ≤40 çağrı × ≤90 s
			m := ""
			if model != nil {
				m = model()
			}
			a, t, err := RunQualityDayWithJudge(rctx, st, judge, m, d)
			cancel()
			if err != nil {
				log.Printf("[cosre-quality] %s: %v", d.Format("2006-01-02"), err)
				return
			}
			log.Printf("[cosre-quality] %s: %d cevap, %d tool puanlandı", d.Format("2006-01-02"), a, t)
		}
	}
	tick()
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			tick()
		}
	}
}
