package chstore

// ai_eval_runs.go — v0.10.940 (Settings › AI › Değerlendirme paneli; operatör
// onayı 2026-09-26): sunucuda koşan evalset koşularının kaydı.
//
// Tablo `tables` diliminde (store.go), migrations/*.sql'de DEĞİL: uygulamanın
// sahip olduğu düşük hacimli state tablosu, boot'ta IF NOT EXISTS ile doğar.
// Yazım deseni: koşu başı (running, cases boş) → her vaka sonrası ilerleme
// (sayaçlar + summary, cases BOŞ) → son yazım (son durum + cases JSON).
// ReplacingMergeTree(version) tam-satır replace: HER yazım bütün alanları
// taşır (CLAUDE.md invariant #4) — ilerleme yazımının cases'i boş bırakması
// bilinçli, son yazım onu doldurur.
//
// Şekil sözleşmesi api katmanında: summary (yüzey özeti) ve cases (vaka
// sonuçları) buraya OPAK JSON olarak gelir. chstore evalset'in iç şeklini
// bilmez — ai_calls'ın prompt_sample'ı gibi taşıyıcıdır.
//
// Saklama: liste en yeni 20'yi gösterir (LIMIT, api tarafı), fiziksel silme
// 180g TTL. ALTER DELETE mutasyonu YOK — sayıya göre budama repoda emsalsiz
// ve Replicated kümede ağır; 180 günlük koşu birikimi (günde birkaç satır)
// önemsiz.
//
// Tablo yokluğu (küme kipinde ertelenen DDL, clickhouse-schema §9) okuma
// tarafında HATA değil: liste boş, nokta okuma "yok" döner.

import (
	"context"
	"fmt"
	"time"
)

// EvalRun — bir evalset koşusunun satırı. Summary/Cases opak JSON; Cases
// ListEvalRuns'ta hiç okunmaz (liste yükü küçük kalsın).
type EvalRun struct {
	ID            string
	StartedAt     time.Time
	UpdatedAt     time.Time
	FinishedAt    time.Time // sıfır = sürüyor (sütunda epoch sentinel'i)
	Status        string    // running | done | cancelled | failed
	StartedBy     string
	AppVersion    string
	PromptVersion string
	Model         string
	ProfileID     string
	Surfaces      []string // boş = tümü
	Total         uint32
	Done          uint32
	Pass          uint32
	Fail          uint32
	Skipped       uint32
	RubricMean    float64
	Error         string
	Summary       string // JSON — api'nin yüzey özeti
	Cases         string // JSON — api'nin vaka sonuçları; ilerleme yazımında ""
}

// evalRunsListLimitMax — liste tavanı; panel 20 ister, fazlası crafted.
const evalRunsListLimitMax = 50

// evalRunsSelectCols — liste ve nokta okumanın ortak kolonları (cases HARİÇ;
// nokta okuma sona ekler).
const evalRunsSelectCols = `id, started_at, updated_at, finished_at, status, started_by,
		app_version, prompt_version, model, profile_id, surfaces,
		total, done, pass, fail, skipped, rubric_mean, error, summary`

const evalRunsInsertSQL = `INSERT INTO ai_eval_runs (id, started_at, updated_at, finished_at,
		status, started_by, app_version, prompt_version, model, profile_id, surfaces,
		total, done, pass, fail, skipped, rubric_mean, error, summary, cases, version)`

// evalRunTimeArg — SAF: sıfır zaman → epoch (finished_at sentinel'i; Go'nun
// sıfır zamanı 1. yıl, DateTime64 aralığı dışında). Nullable yok (C4).
func evalRunTimeArg(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return t.UTC()
}

// evalRunTimeFromCol — SAF: sentinel'i geri sıfır zamana çevirir.
func evalRunTimeFromCol(t time.Time) time.Time {
	if t.Unix() <= 0 {
		return time.Time{}
	}
	return t.UTC()
}

// evalRunVersion — SAF: version = updated_at (ns). Koşucu updated_at'i koşu
// içinde KESİN artan tutar; böylece son yazım FINAL'de kazanır ve iki yazım
// asla özdeş blok olmaz (Replicated insert dedup'u — settings.go v0.10.129).
func evalRunVersion(r EvalRun) uint64 {
	if r.UpdatedAt.IsZero() {
		return uint64(time.Now().UnixNano())
	}
	return uint64(r.UpdatedAt.UnixNano())
}

// evalRunInsertArgs — SAF: evalRunsInsertSQL kolon sırasıyla argümanlar.
func evalRunInsertArgs(r EvalRun) []any {
	surfaces := r.Surfaces
	if surfaces == nil {
		surfaces = []string{}
	}
	return []any{
		r.ID, evalRunTimeArg(r.StartedAt), evalRunTimeArg(r.UpdatedAt), evalRunTimeArg(r.FinishedAt),
		r.Status, r.StartedBy, r.AppVersion, r.PromptVersion, r.Model, r.ProfileID, surfaces,
		r.Total, r.Done, r.Pass, r.Fail, r.Skipped, r.RubricMean, r.Error, r.Summary, r.Cases,
		evalRunVersion(r),
	}
}

// clampEvalRunsLimit — SAF: 1..evalRunsListLimitMax, varsayılan 20.
func clampEvalRunsLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > evalRunsListLimitMax {
		return evalRunsListLimitMax
	}
	return limit
}

// SaveEvalRun — satırın YENİ sürümünü yazar (tam satır; bkz. dosya başı).
func (s *Store) SaveEvalRun(ctx context.Context, r EvalRun) error {
	if r.ID == "" {
		return fmt.Errorf("eval run: id boş")
	}
	batch, err := s.conn.PrepareBatch(ctx, evalRunsInsertSQL)
	if err != nil {
		return err
	}
	if err := batch.Append(evalRunInsertArgs(r)...); err != nil {
		return err
	}
	return batch.Send()
}

// ListEvalRuns — en yeni koşular (started_at DESC), cases OKUNMAZ. FINAL:
// koşu başına onlarca ilerleme sürümü var, yalnız sonuncusu sayılır. Tablo
// yoksa boş liste.
func (s *Store) ListEvalRuns(ctx context.Context, limit int) ([]EvalRun, error) {
	rows, err := s.conn.Query(ctx, `SELECT `+evalRunsSelectCols+`
		FROM ai_eval_runs FINAL
		ORDER BY started_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, clampEvalRunsLimit(limit))
	if err != nil {
		if isUnknownTableOrColumn(err) {
			return []EvalRun{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := []EvalRun{}
	for rows.Next() {
		var r EvalRun
		if err := rows.Scan(evalRunScanDest(&r, false)...); err != nil {
			return nil, err
		}
		normalizeEvalRun(&r)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetEvalRun — tek koşu, cases DAHİL. Yoksa (ya da tablo yoksa) nil, nil.
func (s *Store) GetEvalRun(ctx context.Context, id string) (*EvalRun, error) {
	if id == "" {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, `SELECT `+evalRunsSelectCols+`, cases
		FROM ai_eval_runs FINAL
		WHERE id = ?
		LIMIT 1
		SETTINGS max_execution_time = 5`, id)
	if err != nil {
		if isUnknownTableOrColumn(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var r EvalRun
	if err := rows.Scan(evalRunScanDest(&r, true)...); err != nil {
		return nil, err
	}
	normalizeEvalRun(&r)
	return &r, nil
}

// evalRunScanDest — evalRunsSelectCols sırası (+ cases, withCases ise).
func evalRunScanDest(r *EvalRun, withCases bool) []any {
	dest := []any{
		&r.ID, &r.StartedAt, &r.UpdatedAt, &r.FinishedAt, &r.Status, &r.StartedBy,
		&r.AppVersion, &r.PromptVersion, &r.Model, &r.ProfileID, &r.Surfaces,
		&r.Total, &r.Done, &r.Pass, &r.Fail, &r.Skipped, &r.RubricMean, &r.Error, &r.Summary,
	}
	if withCases {
		dest = append(dest, &r.Cases)
	}
	return dest
}

// normalizeEvalRun — SAF: sentinel → sıfır zaman, UTC, nil dilim → boş.
func normalizeEvalRun(r *EvalRun) {
	r.StartedAt = evalRunTimeFromCol(r.StartedAt)
	r.UpdatedAt = evalRunTimeFromCol(r.UpdatedAt)
	r.FinishedAt = evalRunTimeFromCol(r.FinishedAt)
	if r.Surfaces == nil {
		r.Surfaces = []string{}
	}
}
