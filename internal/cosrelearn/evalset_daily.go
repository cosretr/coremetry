package cosrelearn

// evalset_daily.go — v0.10.2109 (CoSRE kalite döngüsü D4,
// docs/cosre/quality-loop-audit.md §3.4; operatör onayı 2026-11-13 soru 2):
// günlük v2 evalset koşusu worker'da. Kapı DEĞİL, trend: cevap kalitesi
// ancak modelin olduğu yerde (prod worker) ölçülür.
//
// Zamanlama: UTC gün kapandıktan evalsetDailySettle (2 sa) sonra, kalite
// günü (dünün gece puanı + hakem) bittiyse; bitmediyse en geç
// evalsetDailyQualityGrace (4 sa) sonunda yine koşar — hakem ile koşu aynı
// yerel GPU'yu üst üste yüklemesin, ama kalite işi takılınca koşu da
// takılmasın. Günde bir: o UTC gün içinde başlamış bir "system:daily"
// satırı (ai_eval_runs) varsa atlanır; süreç içinde de hatırlanır.
//
// Acil fren: system_settings("cosre_evalset_daily") {"enabled":false}.
// Anahtar yok = AÇIK (karar). Bozuk blob = KAPALI: anahtarı yazan biri
// freni çekmek istemiştir. UI/rota YOK (operatör: yeni vida yok); kapatma
// yolu audit §3.4'te (CH INSERT ya da config içe aktarımı). Her tikte okunur
// (15 dk; tek nokta okuma) — pod yeniden başlatmak gerekmez.
//
// Model profili yerel değilse koşu YOK: o kontrol koşucuda (api), çünkü
// profil kümesi orada; burada yalnız "ne zaman".

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	evalsetDailyLockKey = "cosre-evalset-daily"
	// EvalsetDailySettingKey — acil fren anahtarı (system_settings).
	EvalsetDailySettingKey = "cosre_evalset_daily"
	// EvalsetDailyStartedBy — günlük koşunun ai_eval_runs.started_by değeri
	// (panel ve "bugün koştu mu" kontrolü bunu okur).
	EvalsetDailyStartedBy = "system:daily"
	// EvalsetDailyBudget — koşu tavanı (operatör kararı: 30 dk).
	EvalsetDailyBudget = 30 * time.Minute

	evalsetDailySettle       = 2 * time.Hour
	evalsetDailyQualityGrace = 4 * time.Hour
	evalsetDailyInterval     = 15 * time.Minute
	// evalsetDailyRecent — "bugün koştu mu" için okunan son koşu sayısı
	// (ListEvalRuns tavanı 50; günde 50 elle koşu gerçekçi değil).
	evalsetDailyRecent = 50
)

// EvalsetDailyStore — günlük koşu zamanlayıcısının G/Ç yüzeyi (*chstore.Store).
type EvalsetDailyStore interface {
	SettingsReader
	CosreQualityDayDone(ctx context.Context, day time.Time) (bool, error)
	ListEvalRuns(ctx context.Context, limit int) ([]chstore.EvalRun, error)
}

// EvalsetDailyRunner — koşuyu yapan taraf (api). started=false + note:
// bilinçli atlama (AI yok, profil yerel değil, başka koşu sürüyor); err:
// koşucu hatası. Koşu bitene dek BLOKLAR.
type EvalsetDailyRunner func(ctx context.Context) (started bool, note string, err error)

// EvalsetDailyEnabled — SAF: fren blobu → açık mı. nil/boş = açık
// (varsayılan); bozuk = kapalı; {"enabled":false} = kapalı.
func EvalsetDailyEnabled(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	var v struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return v.Enabled == nil || *v.Enabled
}

// EvalsetDailyDue — SAF: now itibarıyla bugünün koşusu başlayabilir mi.
// qualityDone = dünün kalite günü yazıldı mı.
func EvalsetDailyDue(now time.Time, qualityDone bool) bool {
	now = now.UTC()
	today := utcDay(now)
	if now.Before(today.Add(evalsetDailySettle)) {
		return false
	}
	return qualityDone || !now.Before(today.Add(evalsetDailyQualityGrace))
}

// EvalsetDailyRanToday — SAF: runs içinde bugün (UTC) başlamış günlük koşu var mı.
func EvalsetDailyRanToday(runs []chstore.EvalRun, now time.Time) bool {
	today := utcDay(now)
	for _, r := range runs {
		if r.StartedBy == EvalsetDailyStartedBy && !r.StartedAt.UTC().Before(today) {
			return true
		}
	}
	return false
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// evalsetDailyState — süreç içi durum. lastDay: "bugün başlatıldı" (CH
// yazımı düşse de aynı gün ikinci koşu yok); skipDay/skipNote: atlama
// gerekçesi gün başına bir kez loglanır (15 dk'da bir değil).
type evalsetDailyState struct {
	lastDay  time.Time
	skipDay  time.Time
	skipNote string
}

// evalsetDailyTick — tek tik; lider olduğu varsayılır.
func evalsetDailyTick(ctx context.Context, st EvalsetDailyStore, run EvalsetDailyRunner, now time.Time, s *evalsetDailyState) {
	today := utcDay(now)
	if s.lastDay.Equal(today) {
		return
	}
	raw, err := st.GetSetting(ctx, EvalsetDailySettingKey)
	if err != nil {
		log.Printf("[cosre-evalset-daily] fren anahtarı okunamadı, koşu atlandı: %v", err)
		return
	}
	if !EvalsetDailyEnabled(raw) {
		s.noteSkip(today, "acil fren: system_settings "+EvalsetDailySettingKey+" kapalı")
		return
	}
	if now.Before(today.Add(evalsetDailySettle)) {
		return // CH'ye gitmeden erken çık
	}
	qdone, err := st.CosreQualityDayDone(ctx, today.AddDate(0, 0, -1))
	if err != nil {
		qdone = false // okunamadı → yalnız grace sonrası koşar
	}
	if !EvalsetDailyDue(now, qdone) {
		return
	}
	runs, err := st.ListEvalRuns(ctx, evalsetDailyRecent)
	if err != nil {
		log.Printf("[cosre-evalset-daily] son koşular okunamadı: %v", err)
		return
	}
	if EvalsetDailyRanToday(runs, now) {
		s.lastDay = today
		return
	}
	started, note, err := run(ctx)
	switch {
	case err != nil:
		log.Printf("[cosre-evalset-daily] koşu hatası: %v", err)
		s.lastDay = today // koşucu hatası (fikstür vb.) aynı gün tekrar denenmez
	case !started:
		s.noteSkip(today, note)
	default:
		s.lastDay = today
	}
}

// noteSkip — atlama gerekçesini gün başına (ya da gerekçe değişince) bir kez logla.
func (s *evalsetDailyState) noteSkip(today time.Time, note string) {
	if s.skipDay.Equal(today) && s.skipNote == note {
		return
	}
	s.skipDay, s.skipNote = today, note
	log.Printf("[cosre-evalset-daily] atlandı: %s", note)
}

// StartEvalsetDaily — worker'da, Redis lider kilidiyle (cosre-evalset-daily).
// run nil → başlamaz.
func StartEvalsetDaily(ctx context.Context, st EvalsetDailyStore, lock cache.Lock, run EvalsetDailyRunner) {
	if st == nil || run == nil {
		return
	}
	leader := cache.NewLeaderHolder(lock, evalsetDailyLockKey, cache.LeaderTTL(evalsetDailyInterval))
	leader.Start(ctx)
	var state evalsetDailyState
	tk := time.NewTicker(evalsetDailyInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if leader.IsLeader() {
				evalsetDailyTick(ctx, st, run, time.Now(), &state)
			}
		}
	}
}
