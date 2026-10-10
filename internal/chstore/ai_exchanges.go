package chstore

// ai_exchanges.go — v0.10.1153 (operatör, prod): /ai "CoSRE etkileşimleri".
//
// /ai bugüne dek yalnız MODEL ÇAĞRILARINI listeliyordu; bir CoSRE turu (soru →
// kademe → cevap) hiçbir yerde tek satır değildi ve LLM'siz cevaplar hiç
// görünmüyordu. Artık her tur ai_calls'a bir etkileşim satırı yazıyor
// (api/chat_exchange.go; yüzey "cosre-turn:<kademe>[/<rota>][+deep]",
// exchange_id "<kök>:turn") ve turun model çağrıları aynı köke bağlı
// (cevap çağrısı kökü aynen, yardımcı/işaret satırları "<kök>:<yüzey>").
//
// Okuma JOIN'siz, üç küçük sorgu + Go'da birleşim (aiFeedbackBySurface
// emsali; küme kipinde GLOBAL JOIN derdi yok):
//
//	1. pencere içindeki etkileşim satırları (LIMIT);
//	2. bu köklerin model çağrıları (zaman sınırlı, LIMIT, kaynak koşulu);
//	3. bu köklerin 👍/👎'ları (ai_feedback FINAL).
//
// Yeni tablo / kolon YOK; saklama ai_calls'ın 90 g TTL'i, örnekler 4 KB.

import (
	"context"
	"slices"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// AIExchangeCall — bir etkileşimin altındaki ai_calls satırı (örneksiz;
// tam satır GET /api/ai/calls/{id}).
type AIExchangeCall struct {
	ID           string `json:"id"`
	CreatedAt    int64  `json:"createdAt"`
	Surface      string `json:"surface"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	DurationMs   uint32 `json:"durationMs"`
	InputTokens  uint32 `json:"inputTokens"`
	OutputTokens uint32 `json:"outputTokens"`
	Status       string `json:"status"`
	// LLM — false: model koşmadan yazılan işaret satırı (chat-offtopic …).
	LLM bool `json:"llm"`
}

// AIExchange — bir CoSRE kullanıcı turu.
type AIExchange struct {
	ExchangeID string `json:"exchangeId"` // kök — 👍/👎 anahtarı
	CreatedAt  int64  `json:"createdAt"`  // unix ns, tur başlangıcı
	Tier       string `json:"tier"`
	Route      string `json:"route,omitempty"`
	Deep       bool   `json:"deep,omitempty"`
	Question   string `json:"question"`
	Answer     string `json:"answer,omitempty"`
	Status     string `json:"status"` // ok | error
	ErrorMsg   string `json:"errorMsg,omitempty"`
	DurationMs uint32 `json:"durationMs"` // turun tamamı
	UserID     string `json:"userId,omitempty"`
	UserEmail  string `json:"userEmail,omitempty"`
	ProfileID  string `json:"profileId,omitempty"`
	// LLMCalls — turun model çağrısı sayısı; 0 = LLM yok (deterministik).
	LLMCalls     int              `json:"llmCalls"`
	InputTokens  uint64           `json:"inputTokens"`
	OutputTokens uint64           `json:"outputTokens"`
	Models       []string         `json:"models"`
	Calls        []AIExchangeCall `json:"calls"`
	Feedback     int8             `json:"feedback,omitempty"` // 1 | -1; 0 = oylanmadı
	FeedbackNote string           `json:"feedbackComment,omitempty"`
}

// ListAIExchangesParams — pencere + tavan (sıfır değerler: son 24 sa, 100).
type ListAIExchangesParams struct {
	From  time.Time
	To    time.Time
	Limit int
}

const (
	aiExchangesDefaultLimit = 100
	aiExchangesMaxLimit     = 500
	// aiExchangeCallSlack — turun çağrıları tur başlangıcından sonra yazılır;
	// alışveriş tavanı (chatDeadlineMax 15 dk) + pay.
	aiExchangeCallSlack = 20 * time.Minute
	// aiExchangeCallsPerTurn — çağrı sorgusunun tavanı (tur başına pay).
	aiExchangeCallsPerTurn = 20
)

// aiCallsTurnCond — etkileşim satırlarını seçen koşul (aiCallsNotTurnCond'un tersi).
const aiCallsTurnCond = "startsWith(surface, '" + aisurface.TurnPrefix + "')"

// aiExchangeRootExpr — exchange_id'nin kökü (aisurface.ExchangeRoot'un SQL ikizi).
const aiExchangeRootExpr = "splitByChar('" + aisurface.ExchangeSep + "', exchange_id)[1]"

// aiExchangeTurnsSQL — SAF: pencere içindeki etkileşim satırları. profile_id
// yalnız genişletilmiş kolonlar varken (iki-boot sözleşmesi). Bind: from, to, limit.
func aiExchangeTurnsSQL(ext bool) string {
	cols := `exchange_id, toUnixTimestamp64Nano(created_at), surface, status, error_msg,
		       duration_ms, user_id, user_email, prompt_sample, response_sample`
	if ext {
		cols += `, profile_id`
	}
	return `SELECT ` + cols + `
		FROM ai_calls
		WHERE created_at >= toDateTime64(?, 9, 'UTC')
		  AND created_at <  toDateTime64(?, 9, 'UTC')
		  AND ` + aiCallsTurnCond + `
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// aiExchangeCallsSQL — SAF: verilen köklerin model çağrıları. Üretim kaynak
// koşulu (evalset ve etkileşim satırları dışarıda). Bind: from, to, roots, limit.
func aiExchangeCallsSQL() string {
	return `SELECT ` + aiExchangeRootExpr + `, id, toUnixTimestamp64Nano(created_at), surface,
		       provider, model, duration_ms, input_tokens, output_tokens, status
		FROM ai_calls
		WHERE created_at >= toDateTime64(?, 9, 'UTC')
		  AND created_at <  toDateTime64(?, 9, 'UTC')
		  AND ` + aiCallsSourceCond(AICallSourceProduction) + `
		  AND exchange_id != ''
		  AND ` + aiExchangeRootExpr + ` IN (?)
		ORDER BY created_at
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// aiExchangeFeedbackSQL — köklerin son 👍/👎'ı (küçük state tablosu, FINAL).
const aiExchangeFeedbackSQL = `SELECT exchange_id, verdict, comment
		FROM ai_feedback FINAL
		WHERE exchange_id IN (?)
		LIMIT ?
		SETTINGS max_execution_time = 5`

// aiTurnRow / aiExchangeChild / aiExchangeVerdict — birleşimin girdileri.
type aiTurnRow struct {
	ExchangeID, Surface, Status, ErrorMsg string
	CreatedAt                             int64
	DurationMs                            uint32
	UserID, UserEmail                     string
	Prompt, Response, ProfileID           string
}

type aiExchangeChild struct {
	Root string
	Call AIExchangeCall
}

type aiExchangeVerdict struct {
	Verdict int8
	Comment string
}

// assembleAIExchanges — SAF: etkileşim satırları + çağrılar + oylar →
// etkileşimler (satır sırası korunur; aynı köke ikinci etkileşim satırı
// yok sayılır). Token ve model yalnız gerçek model çağrılarından.
func assembleAIExchanges(turns []aiTurnRow, calls []aiExchangeChild, fb map[string]aiExchangeVerdict) []AIExchange {
	byRoot := map[string][]AIExchangeCall{}
	for _, c := range calls {
		byRoot[c.Root] = append(byRoot[c.Root], c.Call)
	}
	out := make([]AIExchange, 0, len(turns))
	seen := map[string]bool{}
	for _, t := range turns {
		root := aisurface.ExchangeRoot(t.ExchangeID)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		tier, route, deep, _ := aisurface.ParseTurnLabel(t.Surface)
		ex := AIExchange{
			ExchangeID: root, CreatedAt: t.CreatedAt, Tier: tier, Route: route, Deep: deep,
			Question: t.Prompt, Answer: t.Response, Status: t.Status, ErrorMsg: t.ErrorMsg,
			DurationMs: t.DurationMs, UserID: t.UserID, UserEmail: t.UserEmail, ProfileID: t.ProfileID,
			Models: []string{}, Calls: []AIExchangeCall{},
		}
		for _, c := range byRoot[root] {
			c.LLM = aisurface.IsLLMCall(c.Surface)
			ex.Calls = append(ex.Calls, c)
			if !c.LLM {
				continue
			}
			ex.LLMCalls++
			ex.InputTokens += uint64(c.InputTokens)
			ex.OutputTokens += uint64(c.OutputTokens)
			if c.Model != "" && !slices.Contains(ex.Models, c.Model) {
				ex.Models = append(ex.Models, c.Model)
			}
		}
		if v, ok := fb[root]; ok {
			ex.Feedback, ex.FeedbackNote = v.Verdict, v.Comment
		}
		out = append(out, ex)
	}
	return out
}

// aiExchangeCallWindow — SAF: çağrı sorgusunun penceresi — en eski tur
// başlangıcından en yeni turun başlangıcı + pay'a.
func aiExchangeCallWindow(turns []aiTurnRow) (from, to time.Time) {
	for i, t := range turns {
		ts := time.Unix(0, t.CreatedAt).UTC()
		if i == 0 || ts.Before(from) {
			from = ts
		}
		if i == 0 || ts.After(to) {
			to = ts
		}
	}
	return from, to.Add(aiExchangeCallSlack)
}

// ListAIExchanges — /api/ai/exchanges. En yeni tur önce.
func (s *Store) ListAIExchanges(ctx context.Context, p ListAIExchangesParams) ([]AIExchange, error) {
	if p.Limit <= 0 || p.Limit > aiExchangesMaxLimit {
		p.Limit = aiExchangesDefaultLimit
	}
	if p.To.IsZero() {
		p.To = time.Now().UTC()
	}
	if p.From.IsZero() {
		p.From = p.To.Add(-24 * time.Hour)
	}
	ext := aiCallsExtended.Load()
	rows, err := s.conn.Query(ctx, aiExchangeTurnsSQL(ext), chDateTime64Arg(p.From), chDateTime64Arg(p.To), p.Limit)
	if err != nil {
		return nil, err
	}
	var turns []aiTurnRow
	for rows.Next() {
		var t aiTurnRow
		dest := []any{&t.ExchangeID, &t.CreatedAt, &t.Surface, &t.Status, &t.ErrorMsg,
			&t.DurationMs, &t.UserID, &t.UserEmail, &t.Prompt, &t.Response}
		if ext {
			dest = append(dest, &t.ProfileID)
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, err
		}
		turns = append(turns, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(turns) == 0 {
		return []AIExchange{}, nil
	}
	roots := make([]string, 0, len(turns))
	for _, t := range turns {
		if r := aisurface.ExchangeRoot(t.ExchangeID); r != "" {
			roots = append(roots, r)
		}
	}
	cFrom, cTo := aiExchangeCallWindow(turns)
	var calls []aiExchangeChild
	cRows, err := s.conn.Query(ctx, aiExchangeCallsSQL(), chDateTime64Arg(cFrom), chDateTime64Arg(cTo),
		roots, len(roots)*aiExchangeCallsPerTurn)
	if err != nil {
		return nil, err
	}
	for cRows.Next() {
		var c aiExchangeChild
		if err := cRows.Scan(&c.Root, &c.Call.ID, &c.Call.CreatedAt, &c.Call.Surface, &c.Call.Provider,
			&c.Call.Model, &c.Call.DurationMs, &c.Call.InputTokens, &c.Call.OutputTokens, &c.Call.Status); err != nil {
			cRows.Close()
			return nil, err
		}
		calls = append(calls, c)
	}
	cRows.Close()
	if err := cRows.Err(); err != nil {
		return nil, err
	}
	fb := map[string]aiExchangeVerdict{}
	fRows, err := s.conn.Query(ctx, aiExchangeFeedbackSQL, roots, len(roots))
	if err != nil {
		return nil, err
	}
	for fRows.Next() {
		var id string
		var v aiExchangeVerdict
		if err := fRows.Scan(&id, &v.Verdict, &v.Comment); err != nil {
			fRows.Close()
			return nil, err
		}
		fb[id] = v
	}
	fRows.Close()
	if err := fRows.Err(); err != nil {
		return nil, err
	}
	return assembleAIExchanges(turns, calls, fb), nil
}
