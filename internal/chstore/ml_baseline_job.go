package chstore

// ml_baseline_job.go — v0.10.1814, öğrenme döngüsü Faz 1 dilim 1.3 (audit
// docs/anomaly/learning-loop-audit.md §4.2): günlük baseline işinin CH
// tarafı — ml_series'in bir yerel gününü servis karmasına göre dilimlenmiş
// AKIŞLA okuma + iş durumu / kontrol blobları (system_settings).
//
// Durum (`ml_baseline_status`) ve kontrol (`ml_baseline_control`) AYRI
// anahtarlar: durumun tek yazıcısı ML lideri; kontrolü admin uçları yazar.
// ml_tick_status'a gömülmedi — o blob her tikte baştan yazılır, ikinci
// yazıcının alanını ezerdi. ML sekmesi üçünü de aynı /api/ml/scorecard
// yanıtında okur.

import (
	"context"
	"encoding/json"
	"time"
)

const (
	mlBaselineStatusKey  = "ml_baseline_status"
	mlBaselineControlKey = "ml_baseline_control"
	// mlBaselineSliceLimit — tek dilimin satır tavanı. Audit §4.3: günlük
	// okuma servis düzeyinde ~14,4 M satır / 16 dilim ≈ 0,9 M; 4× pay.
	mlBaselineSliceLimit = 4000000
)

// mlSeriesDaySliceSQL — SAF (test): zaman sınırlı (bucket aralığı), servis
// karması dilimi, FINAL (RMT yeniden yazımlarının son sürümü), anahtar
// sırası (akış hesaplayıcısı seriyi bitince boşaltır; okuma sırası sort
// anahtarıyla aynı → read-in-order), LIMIT + max_execution_time 25. Audit
// §4.2 60 sn diyordu; havuzun ReadTimeout'u 30 sn ve tüm akış için tek
// süre (query_budget_test): 60 asla tetiklenmez, soket önce ölür. 25 sn
// tetiklenebilen sınır; dilim 16'ya bölündüğü için ~0,9 M satır/dilim.
func mlSeriesDaySliceSQL() string {
	return `SELECT signal, service, part, bucket, bucket_sec, count
		FROM ml_series FINAL
		WHERE bucket >= ? AND bucket < ?
		  AND cityHash64(service) % ? = ?
		ORDER BY signal, service, part, bucket, bucket_sec
		LIMIT ?
		SETTINGS max_execution_time = 25`
}

// MLSeriesDaySlice — [from, to) aralığındaki ml_series satırlarının
// `slice`/`slices` dilimi, satır satır fn'e (bellekte gün tutulmaz).
// truncated = satır tavanına ulaşıldı.
func (s *Store) MLSeriesDaySlice(ctx context.Context, from, to time.Time, slice, slices int, fn func(MLSeriesRow) error) (int, bool, error) {
	rows, err := s.conn.Query(ctx, mlSeriesDaySliceSQL(), from.UTC(), to.UTC(), uint64(slices), uint64(slice), mlBaselineSliceLimit) // DateTime kolon: DateTime64 bind YOK
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var r MLSeriesRow
		if err := rows.Scan(&r.Signal, &r.Service, &r.Part, &r.Bucket, &r.BucketSec, &r.Count); err != nil {
			return n, false, err
		}
		n++
		if err := fn(r); err != nil {
			return n, false, err
		}
	}
	return n, n >= mlBaselineSliceLimit, rows.Err()
}

// MLBaselineRun — bir günün koşusu.
type MLBaselineRun struct {
	Day        string `json:"day"`
	Trigger    string `json:"trigger"` // auto | manual
	Rows       int    `json:"rows"`
	Series     int    `json:"series"`
	Slices     int    `json:"slices"`
	Truncated  int    `json:"truncated,omitempty"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
	At         int64  `json:"at"`
}

// MLBaselineStatus — günlük işin durumu (tek yazıcı: ML lideri).
type MLBaselineStatus struct {
	LastDay    string         `json:"lastDay,omitempty"`    // son başarıyla işlenen otomatik gün (yerel)
	Running    string         `json:"running,omitempty"`    // şu an işlenen gün
	Last       *MLBaselineRun `json:"last,omitempty"`       // son otomatik koşu
	Manual     *MLBaselineRun `json:"manual,omitempty"`     // son elle koşu
	ManualDone string         `json:"manualDone,omitempty"` // işlenmiş son elle istek kimliği
	UpdatedAt  int64          `json:"updatedAt,omitempty"`
}

// MLBaselineRunRequest — admin elle koşu isteği.
type MLBaselineRunRequest struct {
	ID  string `json:"id"`
	Day string `json:"day"`
	By  string `json:"by,omitempty"`
	At  int64  `json:"at"`
}

// MLBaselineControl — admin istekleri (lider yoklar).
type MLBaselineControl struct {
	Run      *MLBaselineRunRequest      `json:"run,omitempty"`
	Backfill *MLBaselineBackfillRequest `json:"backfill,omitempty"` // v0.10.1815
}

func (s *Store) getJSONSetting(ctx context.Context, key string, v any) (bool, error) {
	raw, err := s.GetSetting(ctx, key)
	if err != nil || len(raw) == 0 {
		return false, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, nil // bozuk blob = yok
	}
	return true, nil
}

func (s *Store) putJSONSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, key, raw)
}

func (s *Store) GetMLBaselineStatus(ctx context.Context) (MLBaselineStatus, bool, error) {
	var st MLBaselineStatus
	ok, err := s.getJSONSetting(ctx, mlBaselineStatusKey, &st)
	return st, ok, err
}

func (s *Store) SaveMLBaselineStatus(ctx context.Context, st MLBaselineStatus) error {
	return s.putJSONSetting(ctx, mlBaselineStatusKey, st)
}

func (s *Store) GetMLBaselineControl(ctx context.Context) (MLBaselineControl, bool, error) {
	var c MLBaselineControl
	ok, err := s.getJSONSetting(ctx, mlBaselineControlKey, &c)
	return c, ok, err
}

func (s *Store) SaveMLBaselineControl(ctx context.Context, c MLBaselineControl) error {
	return s.putJSONSetting(ctx, mlBaselineControlKey, c)
}

// ── v0.10.1815: baseline geçmiş doldurma (dilim 1.4) ─────────

const mlBaselineBackfillKey = "ml_baseline_backfill"

// MLBaselineBackfillRequest — admin isteği (kontrol blobu). Cancel = DELETE.
type MLBaselineBackfillRequest struct {
	ID      string   `json:"id"`
	Days    int      `json:"days"`
	Signals []string `json:"signals"`
	By      string   `json:"by,omitempty"`
	At      int64    `json:"at"`
	Cancel  bool     `json:"cancel,omitempty"`
}

// MLBaselineBackfillSignal — sinyal başına ilerleme; Done = biten günler
// (yeniden başlayınca atlanır).
type MLBaselineBackfillSignal struct {
	DaysTotal int      `json:"daysTotal"`
	DaysDone  int      `json:"daysDone"`
	Rows      int      `json:"rows"`
	Errors    int      `json:"errors"`
	LastError string   `json:"lastError,omitempty"`
	Skipped   string   `json:"skipped,omitempty"`
	Done      []string `json:"done,omitempty"`
}

// MLBaselineBackfillStatus — koşu durumu (tek yazıcı: ML lideri).
type MLBaselineBackfillStatus struct {
	ID        string                               `json:"id"`
	Days      int                                  `json:"days"`
	Signals   []string                             `json:"signals"`
	PlanDays  []string                             `json:"planDays"`
	By        string                               `json:"by,omitempty"`
	StartedAt int64                                `json:"startedAt"`
	UpdatedAt int64                                `json:"updatedAt"`
	Running   bool                                 `json:"running"`
	Complete  bool                                 `json:"complete"`
	Cancelled bool                                 `json:"cancelled"`
	Stopped   string                               `json:"stopped,omitempty"`
	PerSignal map[string]*MLBaselineBackfillSignal `json:"perSignal"`
}

func (s *Store) GetMLBaselineBackfill(ctx context.Context) (MLBaselineBackfillStatus, bool, error) {
	var st MLBaselineBackfillStatus
	ok, err := s.getJSONSetting(ctx, mlBaselineBackfillKey, &st)
	return st, ok, err
}

func (s *Store) SaveMLBaselineBackfill(ctx context.Context, st MLBaselineBackfillStatus) error {
	return s.putJSONSetting(ctx, mlBaselineBackfillKey, st)
}
