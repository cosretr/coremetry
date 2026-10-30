package chstore

// cosre_quality.go — CoSRE öğrenme Faz 2: örtük sinyaller (cosre_signals),
// gece kalite puanları (cosre_answer_quality, cosre_tool_quality) ve
// admin görünümünün okumaları. Puanlama mantığı internal/cosrelearn'de
// (saf); burada yalnız G/Ç. Tablolar küçük (tur başına bir satır), yine
// de her okuma zaman sınırlı + LIMIT + max_execution_time.

import (
	"context"
	"time"
)

// CosreSignal — istemciden gelen bir örtük sinyal.
type CosreSignal struct {
	ExchangeID string    `json:"exchangeId"`
	CreatedAt  time.Time `json:"createdAt"`
	UserEmail  string    `json:"userEmail,omitempty"`
	Kind       string    `json:"kind"`
	Detail     string    `json:"detail,omitempty"`
}

func (s *Store) InsertCosreSignal(ctx context.Context, sig CosreSignal) error {
	if sig.CreatedAt.IsZero() {
		sig.CreatedAt = time.Now()
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO cosre_signals (exchange_id, created_at, user_email, kind, detail)`)
	if err != nil {
		return err
	}
	if err := batch.Append(sig.ExchangeID, sig.CreatedAt.UTC(), sig.UserEmail, sig.Kind, sig.Detail); err != nil {
		return err
	}
	return batch.Send()
}

// ListCosreInteractionsForScoring — puanlama girdisi: pencere içindeki
// turlar, metin kolonları HARİÇ (soru dahil: yeniden-sorma benzerliği).
func (s *Store) ListCosreInteractionsForScoring(ctx context.Context, from, to time.Time) ([]CosreInteraction, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, conversation_id, turn, created_at, user_email, tier,
		       problem_id, question, tool_name, tool_ok, tool_ms, tool_error,
		       total_ms, status, error_msg, attempt, empty_answer, no_evidence
		FROM cosre_interactions
		WHERE created_at >= ? AND created_at < ?
		ORDER BY created_at
		LIMIT 200000
		SETTINGS max_execution_time = 25`, chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CosreInteraction
	for rows.Next() {
		var r CosreInteraction
		var names, errs []string
		var oks []uint8
		var ms []uint32
		var empty, noEv uint8
		if err := rows.Scan(&r.ExchangeID, &r.ConversationID, &r.Turn, &r.CreatedAt, &r.UserEmail, &r.Tier,
			&r.ProblemID, &r.Question, &names, &oks, &ms, &errs,
			&r.TotalMs, &r.Status, &r.ErrorMsg, &r.Attempt, &empty, &noEv); err != nil {
			return nil, err
		}
		r.EmptyAnswer, r.NoEvidence = empty == 1, noEv == 1
		r.Tools = make([]CosreTool, len(names))
		for i := range names {
			t := CosreTool{Name: names[i]}
			if i < len(oks) {
				t.OK = oks[i] == 1
			}
			if i < len(ms) {
				t.Ms = ms[i]
			}
			if i < len(errs) {
				t.Error = errs[i]
			}
			r.Tools[i] = t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListCosreSignals(ctx context.Context, from, to time.Time) ([]CosreSignal, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, created_at, user_email, kind, detail
		FROM cosre_signals
		WHERE created_at >= ? AND created_at < ?
		LIMIT 500000
		SETTINGS max_execution_time = 25`, chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CosreSignal
	for rows.Next() {
		var g CosreSignal
		if err := rows.Scan(&g.ExchangeID, &g.CreatedAt, &g.UserEmail, &g.Kind, &g.Detail); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AIFeedbackVerdictsSince — exchange_id → 👍(1)/👎(-1), son karar (FINAL).
func (s *Store) AIFeedbackVerdictsSince(ctx context.Context, from time.Time) (map[string]int8, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, verdict FROM ai_feedback FINAL
		WHERE created_at >= ?
		LIMIT 200000
		SETTINGS max_execution_time = 15`, chDateTime64Arg(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int8{}
	for rows.Next() {
		var id string
		var v int8
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// ProblemResolvedAt — çözülmüş problemlerin resolved_at'i (id → zaman).
func (s *Store) ProblemResolvedAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT id, assumeNotNull(resolved_at) FROM problems FINAL
		WHERE id IN (?) AND resolved_at IS NOT NULL
		LIMIT 100000
		SETTINGS max_execution_time = 15`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// CosreAnswerQuality — bir cevabın gece puanı.
type CosreAnswerQuality struct {
	ExchangeID string    `json:"exchangeId"`
	CreatedAt  time.Time `json:"createdAt"`
	Tier       string    `json:"tier"`
	Score      float32   `json:"score"`
	Positives  uint8     `json:"positives"`
	Negatives  uint8     `json:"negatives"`
	Reasons    []string  `json:"reasons"`
}

// CosreToolQuality — gün × tool gece puanı. Tool "" = koşu işareti
// (o gün işlendi; tool çağrısı olmayan günler de yeniden koşmasın).
type CosreToolQuality struct {
	Day       time.Time `json:"day"`
	Tool      string    `json:"tool"`
	Calls     uint32    `json:"calls"`
	OKCalls   uint32    `json:"okCalls"`
	SumMs     uint64    `json:"sumMs"`
	P95Ms     uint32    `json:"p95Ms"`
	Rephrased uint32    `json:"rephrased"`
	Score     float32   `json:"score"`
}

func (s *Store) InsertCosreAnswerQuality(ctx context.Context, rows []CosreAnswerQuality) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO cosre_answer_quality (exchange_id, created_at, tier, score, positives, negatives, reasons)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		reasons := r.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		if err := batch.Append(r.ExchangeID, r.CreatedAt.UTC(), r.Tier, r.Score, r.Positives, r.Negatives, reasons); err != nil {
			return err
		}
	}
	return batch.Send()
}

func (s *Store) InsertCosreToolQuality(ctx context.Context, rows []CosreToolQuality) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO cosre_tool_quality (day, tool, calls, ok_calls, sum_ms, p95_ms, rephrased, score)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(r.Day.UTC(), r.Tool, r.Calls, r.OKCalls, r.SumMs, r.P95Ms, r.Rephrased, r.Score); err != nil {
			return err
		}
	}
	return batch.Send()
}

// CosreQualityDayDone — gün işlendi mi (koşu işareti satırı).
func (s *Store) CosreQualityDayDone(ctx context.Context, day time.Time) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT count() FROM cosre_tool_quality
		WHERE day = toDate(?) AND tool = ''
		SETTINGS max_execution_time = 5`, day.UTC().Format("2006-01-02")).Scan(&n)
	return n > 0, err
}

// CosreWorstAnswer — admin görünümü satırı: puan + turun özeti.
type CosreWorstAnswer struct {
	CosreAnswerQuality
	Question  string   `json:"question"`
	Answer    string   `json:"answer"`
	UserEmail string   `json:"userEmail,omitempty"`
	Page      string   `json:"page,omitempty"`
	Service   string   `json:"service,omitempty"`
	ProblemID string   `json:"problemId,omitempty"`
	Model     string   `json:"model,omitempty"`
	Tools     []string `json:"tools"`
}

// WorstCosreAnswers — penceredeki en düşük puanlı cevaplar (iki adım:
// puan tablosundan id'ler, sonra turlar exchange_id bloom'u ile).
func (s *Store) WorstCosreAnswers(ctx context.Context, from time.Time, limit int) ([]CosreWorstAnswer, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, created_at, tier, score, positives, negatives, reasons
		FROM cosre_answer_quality FINAL
		WHERE created_at >= ?
		ORDER BY score ASC, negatives DESC, created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, chDateTime64Arg(from), limit)
	if err != nil {
		return nil, err
	}
	var out []CosreWorstAnswer
	idx := map[string]int{}
	var ids []string
	for rows.Next() {
		var q CosreAnswerQuality
		if err := rows.Scan(&q.ExchangeID, &q.CreatedAt, &q.Tier, &q.Score, &q.Positives, &q.Negatives, &q.Reasons); err != nil {
			rows.Close()
			return nil, err
		}
		idx[q.ExchangeID] = len(out)
		ids = append(ids, q.ExchangeID)
		out = append(out, CosreWorstAnswer{CosreAnswerQuality: q, Tools: []string{}})
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return out, err
	}
	det, err := s.conn.Query(ctx, `
		SELECT exchange_id, question, substring(answer, 1, 600), user_email, page, service, problem_id, model, tool_name
		FROM cosre_interactions
		WHERE created_at >= ? AND exchange_id IN (?)
		LIMIT ?
		SETTINGS max_execution_time = 10`, chDateTime64Arg(from.Add(-time.Hour)), ids, len(ids)*2)
	if err != nil {
		return out, err
	}
	defer det.Close()
	for det.Next() {
		var id, q, a, u, pg, svc, pid, model string
		var tools []string
		if err := det.Scan(&id, &q, &a, &u, &pg, &svc, &pid, &model, &tools); err != nil {
			return out, err
		}
		if i, ok := idx[id]; ok {
			w := &out[i]
			w.Question, w.Answer, w.UserEmail, w.Page, w.Service, w.ProblemID, w.Model = q, a, u, pg, svc, pid, model
			if tools != nil {
				w.Tools = tools
			}
		}
	}
	return out, det.Err()
}

// CosreWorstTool — haftalık tool özeti (günlük satırların toplamı).
type CosreWorstTool struct {
	Tool         string  `json:"tool"`
	Calls        uint64  `json:"calls"`
	SuccessRate  float64 `json:"successRate"`
	AvgMs        float64 `json:"avgMs"`
	P95Ms        uint32  `json:"p95Ms"`
	RephraseRate float64 `json:"rephraseRate"`
	Score        float64 `json:"score"`
}

// WorstCosreTools — günlük puanlar pencere boyunca toplanır; skor =
// başarı × (1 − yeniden-sorma oranı), cosrelearn.ToolScore ile aynı formül.
func (s *Store) WorstCosreTools(ctx context.Context, fromDay time.Time, minCalls, limit int) ([]CosreWorstTool, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT tool, sum(calls) AS c, sum(ok_calls) / c AS sr, sum(sum_ms) / c AS avg_ms,
		       max(p95_ms), sum(rephrased) / c AS rr, sr * (1 - rr) AS score
		FROM cosre_tool_quality FINAL
		WHERE day >= toDate(?) AND tool != ''
		GROUP BY tool
		HAVING c >= ?
		ORDER BY score ASC, c DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, fromDay.UTC().Format("2006-01-02"), minCalls, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CosreWorstTool
	for rows.Next() {
		var t CosreWorstTool
		if err := rows.Scan(&t.Tool, &t.Calls, &t.SuccessRate, &t.AvgMs, &t.P95Ms, &t.RephraseRate, &t.Score); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CosrePrefixStats — v0.10.1637: trace açıklamasının "dış bağımlılık bekleme"
// satırı penceresinde kaç kez uygulandı (payda) ve sunucu modelin cevabını kaç
// kez değiştirdi (eklendi / düzeltildi). Kaynak cosre_signals sistem satırları
// (cosrelearn.SigPrefixApplied / SigPrefixCorrected).
type CosrePrefixStats struct {
	Applied       uint64  `json:"applied"`
	Added         uint64  `json:"added"`
	Corrected     uint64  `json:"corrected"`
	AddedRate     float64 `json:"addedRate"`
	CorrectedRate float64 `json:"correctedRate"`
}

// WithRates — SAF: oranlar [0,1] (payda 0 → 0; pencere kenarında pay paydayı
// aşarsa 1'de kırpılır).
func (p CosrePrefixStats) WithRates() CosrePrefixStats {
	rate := func(n uint64) float64 {
		if p.Applied == 0 {
			return 0
		}
		return min(1, float64(n)/float64(p.Applied))
	}
	p.AddedRate, p.CorrectedRate = rate(p.Added), rate(p.Corrected)
	return p
}

// cosrePrefixStatsSQL — ORDER BY (created_at, kind) önekiyle zaman sınırlı;
// tek satırlık toplam.
const cosrePrefixStatsSQL = `
		SELECT countIf(kind = 'prefix_applied'),
		       countIf(kind = 'prefix_corrected' AND startsWith(detail, 'action=added')),
		       countIf(kind = 'prefix_corrected' AND startsWith(detail, 'action=corrected'))
		FROM cosre_signals
		WHERE created_at >= ? AND kind IN ('prefix_applied', 'prefix_corrected')
		LIMIT 1
		SETTINGS max_execution_time = 10`

// CosrePrefixStatsSince — pencere [from, şimdi) toplamı.
func (s *Store) CosrePrefixStatsSince(ctx context.Context, from time.Time) (CosrePrefixStats, error) {
	var p CosrePrefixStats
	err := s.conn.QueryRow(ctx, cosrePrefixStatsSQL, chDateTime64Arg(from)).Scan(&p.Applied, &p.Added, &p.Corrected)
	return p.WithRates(), err
}
