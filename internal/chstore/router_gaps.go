package chstore

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// Router boşluk raporu — v0.9.549.
//
// CoSRE'nin guided router'ı 16 intent tanıyor; tanımayan soru serbest
// tool döngüsüne düşüyor. O döngü çalışır ama pahalıdır (N tur LLM
// çağrısı) ve küçük modelde kalitesi düşüktür — asıl mesele şu:
// HANGİ soruların düştüğünü kimse görmüyordu, dolayısıyla "sıradaki
// intent ne olmalı" sorusu sezgiyle cevaplanıyordu.
//
// Yeni kayıt GEREKMEDİ: serbest döngü zaten ai_calls'a surface='chat'
// ile yazıyor ve prompt_sample tam olarak KULLANICININ SORUSU
// (copilot_chat.go: RecordUsage(..., lastUserText(req.Messages), ...)).
// Guided yol surface='chat-guided' yazıyor, yani ayrım hazır duruyordu.
//
// Rapor bu yüzden saf bir OKUMA — ekstra maliyet yok, geçmiş veri de
// dahil.

type RouterGap struct {
	// Question — operatörün yazdığı soru (prompt_sample, kırpılmış).
	Question string `json:"question"`
	Count    uint64 `json:"count"`
	LastAt   int64  `json:"lastAt"` // unix ns
	// Users — kaç FARKLI kullanıcı sordu. Tek kişinin ısrarla denediği
	// bir soru ile ekibin tamamının sorduğu soru aynı öncelikte
	// değildir; sayı tek başına bunu ayırt edemez.
	Users uint64 `json:"users"`
	// Kind — v0.10.2134 (router boşlukları D1): "loop" = serbest döngü
	// (surface chat), "general" = sınıflandırıcı none → genel cevap
	// (chat-intent-none). Aynı soru iki türde ayrı satırdır.
	Kind string `json:"kind"`
}

// Router boşluk türleri (RouterGap.Kind).
const (
	RouterGapKindLoop    = "loop"
	RouterGapKindGeneral = "general"
)

// RouterGapTotals — v0.10.2134: penceredeki GERÇEK toplamlar (ilk 50 grubun
// toplamı değil; v0.9.549'dan beri totalFallbacks yalnız listelenen grupları
// topluyordu).
type RouterGapTotals struct {
	Loop    uint64 `json:"loop"`
	General uint64 `json:"general"`
}

// routerGapKindExpr — SAF: yüzeyden tür; none işareti "general", gerisi
// (RouterGapLabels'ın kalan üyesi: chat) "loop". Tek bind: ChatIntentNone.
const routerGapKindExpr = `if(surface = ?, 'general', 'loop')`

// RouterGaps — guided router'ın yakalayamadığı sorular, sıklığa göre.
//
// surface='chat' = serbest tool döngüsü (guided 'chat-guided' yazar).
// Boş prompt_sample elenir: kayıt var ama soru yok demek, sıralamada
// gürültü.
// v0.10.172 — 'chat-intent-none': niyet sınıflandırıcısı none dediği sorular
// (on_no_loop kipinde serbest döngü koşmaz, 'chat' satırı yazılmaz — rapor
// kör kalmasın; copilot_intent.go).
// v0.10.2134 — satırlar türüyle (loop / general) ve gerçek toplamlarla döner;
// ön-getirmeyle tohumlanan döngü 'chat-prefetch' yazar, burada yok.
func (s *Store) RouterGaps(ctx context.Context, since time.Duration, limit int) ([]RouterGap, RouterGapTotals, error) {
	var totals RouterGapTotals
	if since <= 0 {
		since = 7 * 24 * time.Hour
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// v0.10.1153 — liste yüzey kaydından (chat, chat-intent-none): elle
	// yazılmış IN listesi yeni yüzeyleri görmüyordu.
	labels, sinceArg := aisurface.RouterGapLabels(), chDateTime64Arg(time.Now().Add(-since))
	// liste: soru × tür grupları (bind: none, labels, since, limit).
	rows, err := s.conn.Query(ctx, `
		SELECT prompt_sample                        AS q,
		       `+routerGapKindExpr+`             AS kind,
		       count()                              AS n,
		       toUnixTimestamp64Nano(max(created_at)) AS last_at,
		       uniqExact(user_id)                   AS users
		FROM ai_calls
		WHERE surface IN (?)
		  AND created_at >= ?
		  AND prompt_sample != ''
		GROUP BY q, kind
		ORDER BY n DESC, last_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, aisurface.ChatIntentNone, labels, sinceArg, limit)
	if err != nil {
		return nil, totals, err
	}
	defer rows.Close()
	out := make([]RouterGap, 0, limit)
	for rows.Next() {
		var g RouterGap
		if err := rows.Scan(&g.Question, &g.Kind, &g.Count, &g.LastAt, &g.Users); err != nil {
			continue
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, totals, err
	}
	trows, err := s.conn.Query(ctx, `
		SELECT `+routerGapKindExpr+` AS kind, count() AS n
		FROM ai_calls
		WHERE surface IN (?)
		  AND created_at >= ?
		  AND prompt_sample != ''
		GROUP BY kind
		LIMIT 10
		SETTINGS max_execution_time = 10`, aisurface.ChatIntentNone, labels, sinceArg)
	if err != nil {
		return nil, totals, err
	}
	defer trows.Close()
	for trows.Next() {
		var kind string
		var n uint64
		if err := trows.Scan(&kind, &n); err != nil {
			continue
		}
		addRouterGapTotal(&totals, kind, n)
	}
	return out, totals, trows.Err()
}

// addRouterGapTotal — SAF: tür satırını toplamlara yazar.
func addRouterGapTotal(t *RouterGapTotals, kind string, n uint64) {
	switch kind {
	case RouterGapKindGeneral:
		t.General += n
	default:
		t.Loop += n
	}
}
