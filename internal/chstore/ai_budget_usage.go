package chstore

// ai_budget_usage.go — v0.10.411 (CoSRE denetimi E8): bütçe rozeti için
// SON 24 SAAT kullanımı. /api/ai/stats seçici pencereyi ölçer; günlük
// tavan sabit pencere ister, yoksa 7g/30g penceresi günlük tavanla
// kıyaslanırdı. Dolar HESAPLANMAZ — fiyat tablosu istemcide
// (lib/ai-rates.ts); model başına token döner, istemci fiyatlar.

import (
	"context"
	"time"
)

// AIModelTokens — bir sağlayıcı/model çiftinin pencere içi token'ları.
type AIModelTokens struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  uint64 `json:"inputTokens"`
	OutputTokens uint64 `json:"outputTokens"`
}

// AIBudgetUsage — bütçe kıyası için pencere toplamları. Sayaçlar uint64
// (count/sum UInt64 döner — v0.9.543 sınıfı).
type AIBudgetUsage struct {
	Calls        uint64          `json:"calls"`
	InputTokens  uint64          `json:"inputTokens"`
	OutputTokens uint64          `json:"outputTokens"`
	P95Ms        float64         `json:"p95Ms"`
	ByModel      []AIModelTokens `json:"byModel"`
}

// aiBudgetUsageQueries — v0.10.940 (K2): SAF; bütçe okuması DAİMA
// üretimdir, kaynak parametresi bilinçli olarak YOK. Rozet "üretimin AI
// kullanımı günlük tavanın içinde mi" sorusunu cevaplar; 51 vakalık bir
// değerlendirme koşusu (uzun RCA prompt'ları) token ve p95 rozetini
// üretimle ilgisiz bir sebeple kaydırırdı. Neden kaynak seçeneği değil:
// bütçe yalnız gösterim (sunucuda uygulanmıyor) ve evalset'in token'ı
// /ai?source=evalset ekranında zaten görünür. İki sorgu da tam iki bind
// taşır (from, to).
func aiBudgetUsageQueries() (totals, byModel string) {
	w := aiCallsWindowWhere(AICallSourceProduction)
	totals = `
		SELECT
			toUInt64(count()),
			toUInt64(sum(input_tokens)),
			toUInt64(sum(output_tokens)),
			coalesce(toFloat64(quantile(0.95)(toFloat64(duration_ms))), 0)
		FROM ai_calls
		` + w + `
		SETTINGS max_execution_time = 10`
	byModel = `
		SELECT provider, model, toUInt64(sum(input_tokens)), toUInt64(sum(output_tokens))
		FROM ai_calls
		` + w + `
		GROUP BY provider, model
		ORDER BY sum(input_tokens) + sum(output_tokens) DESC
		LIMIT 200
		SETTINGS max_execution_time = 10`
	return totals, byModel
}

// ComputeAIBudgetUsage — [from, to) penceresi; ai_calls küçük bir state
// tablosu ama yine zaman sınırı + max_execution_time + LIMIT.
// v0.10.940 — yalnız üretim satırları (aiBudgetUsageQueries).
func (s *Store) ComputeAIBudgetUsage(ctx context.Context, from, to time.Time) (AIBudgetUsage, error) {
	var u AIBudgetUsage
	totalsSQL, byModelSQL := aiBudgetUsageQueries()
	err := s.conn.QueryRow(ctx, totalsSQL,
		chDateTime64Arg(from), chDateTime64Arg(to)).Scan(&u.Calls, &u.InputTokens, &u.OutputTokens, &u.P95Ms)
	if err != nil {
		return u, err
	}
	rows, err := s.conn.Query(ctx, byModelSQL,
		chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var m AIModelTokens
		if err := rows.Scan(&m.Provider, &m.Model, &m.InputTokens, &m.OutputTokens); err != nil {
			return u, err
		}
		u.ByModel = append(u.ByModel, m)
	}
	return u, rows.Err()
}
