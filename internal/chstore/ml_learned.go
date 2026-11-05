package chstore

// ml_learned.go — v0.10.1816, öğrenme döngüsü Faz 1 dilim 1.5 (audit
// docs/anomaly/learning-loop-audit.md §6.7): sürümlü öğrenilmiş ayarlar.
// Tablo store.go `ml_learned`: RMT(version), ORDER BY (kind, key, rev),
// partition YOK (P1), TTL YOK (revizyonlar geri alma geçmişi). Bu sürümde
// yalnız kind='profile' (key = servis).
//
// Etkin revizyon: §6.7 global işaretçi `system_settings['ml'].learning.
// activeRev` ADAY revizyon kümeleri (sig_param, Faz 5) için tanımlı —
// etkinleştirme kapısı + admin tıklaması. Profil anahtar başına tek değer ve
// bu sürümde hiçbir dedektör okumuyor → en küçük güvenli seçim: profilde
// etkin revizyon = anahtarın EN BÜYÜK rev'i (işaretçi yok). DECISIONS'ta.
//
// rev = yazım anının unix milisaniyesi (önceki rev'den büyük olacak şekilde):
// iki yazıcı (lider sınıflandırıcı + admin sabitleme) aynı anahtara "max+1"
// ile yazsa aynı rev'de çakışır ve RMT birini siler; zaman damgası çakışmaz.

import (
	"context"
	"time"
)

const (
	MLLearnedKindProfile = "profile"
	mlLearnedReadLimit   = 200000
)

// MLLearnedRow — bir revizyon.
type MLLearnedRow struct {
	Kind      string    `json:"kind"`
	Key       string    `json:"key"`
	Rev       uint64    `json:"rev"`
	Params    string    `json:"params"` // JSON
	Stats     string    `json:"stats"`  // JSON
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"` // "auto" | kullanıcı
}

// MLLearnedNextRev — SAF: yeni revizyon numarası (ms damgası, öncekinden büyük).
func MLLearnedNextRev(prev uint64, now time.Time) uint64 {
	r := uint64(now.UnixMilli())
	if r <= prev {
		return prev + 1
	}
	return r
}

// InsertMLLearned — toplu yazım (açık kolon listesi).
func (s *Store) InsertMLLearned(ctx context.Context, rows []MLLearnedRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_learned (kind, key, rev, params, stats, created_at, created_by)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		at := r.CreatedAt
		if at.IsZero() {
			at = time.Now()
		}
		if err := batch.Append(r.Kind, r.Key, r.Rev, r.Params, r.Stats, at.UTC(), r.CreatedBy); err != nil {
			return err
		}
	}
	return batch.Send()
}

// mlLearnedLatestSQL — SAF: anahtar başına en büyük rev (etkin revizyon),
// isteğe bağlı ad araması (büyük/küçük harf duyarsız) + profil süzgeci,
// sayfalı. FINAL (aynı rev'in yeniden yazımı), LIMIT, max_execution_time.
func mlLearnedLatestSQL(count bool) string {
	inner := `SELECT key, rev, params, stats, created_at, created_by FROM ml_learned FINAL
		WHERE kind = ? AND (? = '' OR positionCaseInsensitiveUTF8(key, ?) > 0)
		ORDER BY key, rev DESC
		LIMIT 1 BY key`
	filter := `WHERE (? = '' OR JSONExtractString(params, 'profile') = ?)`
	if count {
		return `SELECT count() FROM (` + inner + `) ` + filter + ` SETTINGS max_execution_time = 10`
	}
	return `SELECT key, rev, params, stats, created_at, created_by FROM (` + inner + `) ` + filter + `
		ORDER BY key
		LIMIT ? OFFSET ?
		SETTINGS max_execution_time = 10`
}

// MLLearnedQuery — liste isteği.
type MLLearnedQuery struct {
	Kind    string
	Search  string // anahtar alt dizgesi
	Profile string // params.profile eşitliği; "" = hepsi
	Offset  int
	Limit   int // 1..mlLearnedReadLimit
}

// MLLearnedLatest — etkin revizyonlar (sayfalı) + toplam.
func (s *Store) MLLearnedLatest(ctx context.Context, q MLLearnedQuery) ([]MLLearnedRow, int, error) {
	if q.Limit <= 0 || q.Limit > mlLearnedReadLimit {
		q.Limit = mlLearnedReadLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	var total uint64
	if err := s.conn.QueryRow(ctx, mlLearnedLatestSQL(true), q.Kind, q.Search, q.Search, q.Profile, q.Profile).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.conn.Query(ctx, mlLearnedLatestSQL(false), q.Kind, q.Search, q.Search, q.Profile, q.Profile, q.Limit, q.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []MLLearnedRow
	for rows.Next() {
		r := MLLearnedRow{Kind: q.Kind}
		if err := rows.Scan(&r.Key, &r.Rev, &r.Params, &r.Stats, &r.CreatedAt, &r.CreatedBy); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, int(total), rows.Err()
}

// MLLearnedOne — tek anahtarın etkin revizyonu (yoksa ok=false).
func (s *Store) MLLearnedOne(ctx context.Context, kind, key string) (MLLearnedRow, bool, error) {
	rows, err := s.conn.Query(ctx, `SELECT rev, params, stats, created_at, created_by FROM ml_learned FINAL
		WHERE kind = ? AND key = ?
		ORDER BY rev DESC
		LIMIT 1
		SETTINGS max_execution_time = 5`, kind, key)
	if err != nil {
		return MLLearnedRow{}, false, err
	}
	defer rows.Close()
	r := MLLearnedRow{Kind: kind, Key: key}
	if !rows.Next() {
		return r, false, rows.Err()
	}
	if err := rows.Scan(&r.Rev, &r.Params, &r.Stats, &r.CreatedAt, &r.CreatedBy); err != nil {
		return r, false, err
	}
	return r, true, rows.Err()
}

// mlProfileCallsSQL — SAF: profil sınıflandırıcısının girdisi — trace
// "_calls" gözlemleri, hafta aralığı (partition budaması) + servis karması
// dilimi, FINAL, LIMIT, max_execution_time.
func mlProfileCallsSQL() string {
	return `SELECT service, slot, week_start, class, calls, buckets
		FROM ml_baseline_obs FINAL
		WHERE signal = 'trace' AND part = '_calls' AND source = 'ml_series'
		  AND week_start >= ? AND week_start < ?
		  AND cityHash64(service) % ? = ?
		ORDER BY service, week_start, slot
		LIMIT ?
		SETTINGS max_execution_time = 25`
}

// MLProfileCalls — bir dilimin "_calls" gözlemleri, servis başına fn'e
// (servis sırası; bellekte tek servis).
func (s *Store) MLProfileCalls(ctx context.Context, from, to time.Time, slice, slices int, fn func(service string, obs []MLBaselineObs) error) error {
	rows, err := s.conn.Query(ctx, mlProfileCallsSQL(), from.Format("2006-01-02"), to.Format("2006-01-02"), uint64(slices), uint64(slice), mlBaselineReadLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	var cur string
	var buf []MLBaselineObs
	flush := func() error {
		if cur == "" {
			return nil
		}
		err := fn(cur, buf)
		buf = buf[:0]
		return err
	}
	for rows.Next() {
		o := MLBaselineObs{Signal: MLSignalTrace, Part: "_calls", Source: MLBaselineSourceSeries}
		if err := rows.Scan(&o.Service, &o.Slot, &o.WeekStart, &o.Class, &o.Calls, &o.Buckets); err != nil {
			return err
		}
		if o.Service != cur {
			if err := flush(); err != nil {
				return err
			}
			cur = o.Service
		}
		buf = append(buf, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}
