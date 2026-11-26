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
	// v0.10.2465 (operator-reported): "clarify" = netleştirme kademesi turu
	// (kaynak sorusu doc_or_entity_ask / source_ask / netleştirme cevabı —
	// LLM'siz, etkileşim satırı cosre-turn:clarify…).
	Kind string `json:"kind"`
	// v0.10.2448 (operatör, 2026-11-22: "kimler yazmış göremiyorum") —
	// soran kullanıcıların e-postaları (en çok 10, sıralı; e-postasız eski
	// satırlar listede yok). Uç yalnız admin'e açık (ai_routes.go).
	Emails []string `json:"emails"`
	// ViaGeneral — v0.10.2448: bu gruptaki kaç tur önce sınıflandırıcı
	// «none» (chat-intent-none işareti) deyip SONRA serbest döngüye düştü:
	// yedek zinciri genel → döngü. Tur yine TEK satırdır (son rota: loop).
	ViaGeneral uint64 `json:"viaGeneral"`
}

// Router boşluk türleri (RouterGap.Kind).
const (
	RouterGapKindLoop    = "loop"
	RouterGapKindGeneral = "general"
	RouterGapKindClarify = "clarify" // v0.10.2465
)

// routerGapClarifyPrefix — v0.10.2465: netleştirme kademesinin etkileşim
// satırı yüzeyi (aisurface.TurnLabel(TierClarify, rota, derin) → "cosre-turn:
// clarify", "…/doc_or_entity_ask", "…+deep"). Her tur TEK etkileşim satırı
// yazar; exchange kimliği "kök:turn" → tur anahtarı kök (döngü satırlarıyla
// aynı tur).
var routerGapClarifyPrefix = aisurface.TurnPrefix + ":" + aisurface.TierClarify

// RouterGapTotals — v0.10.2134: penceredeki GERÇEK toplamlar (ilk 50 grubun
// toplamı değil; v0.9.549'dan beri totalFallbacks yalnız listelenen grupları
// topluyordu).
type RouterGapTotals struct {
	Loop    uint64 `json:"loop"`
	General uint64 `json:"general"`
	Clarify uint64 `json:"clarify"` // v0.10.2465
}

// routerGapKindExpr — SAF: TURUN son rotası. v0.10.2448 (operatör, 2026-11-22:
// aynı soru ~10 sn arayla önce "Genel soru" sonra "Serbest döngü" iki satır):
// sınıflandırıcı none deyince copilot_intent.go chat-intent-none İŞARETİNİ
// yazar; ardından (on kipi ya da v2 durumunda özne varsa) serbest döngü koşar
// ve aynı tura surface='chat' satır(lar)ı yazar. Eskiden satır başına
// sayıldığından tek tur iki gruba (general + loop) bölünüyordu. Artık önce
// TUR (exchange kökü) başına toplanır: turda döngü satırı varsa son rota
// "loop", yalnız işaret varsa "general".
// v0.10.2465 — netleştirme turu (has_clarify) ayrı tür; döngü satırı olan tur
// yine loop (son rota).
const routerGapKindExpr = `if(has_loop, 'loop', if(has_clarify, 'clarify', 'general'))`

// routerGapTurnsSQL — SAF: tur başına tek satır (bind: routerGapTurnsArgs).
// v0.10.2472 — CoSRE "Yeniden üret" aynı kullanıcı turunu yeni bir exchange
// ile yeniden yollar (cosre_interactions.attempt = 'regenerate'); o exchange
// aynı sorunun İKİNCİ turu sayılmaz (tur başına tek satır). Yeniden üretme
// sinyali failed_turns'te önceki turun "reask" gerekçesi olarak kalır.
// Tur anahtarı exchange kökü (ayırıcıdan önceki parça: işaret "kök:rol",
// döngü satırı kökün kendisi); kimliksiz eski satır kendi id'siyle tek tur.
// Döngünün her LLM çağrısı da ayrı 'chat' satırı yazdığından "Kez" eskiden
// çağrı sayıyordu; artık tur sayar.
func routerGapTurnsSQL() string {
	return `
		SELECT if(exchange_id = '', id, splitByChar('` + aisurface.ExchangeSep + `', exchange_id)[1]) AS turn_key,
		       argMin(prompt_sample, created_at) AS q,
		       max(surface = ?)                  AS has_none,
		       max(surface != ? AND NOT startsWith(surface, ?)) AS has_loop,
		       max(startsWith(surface, ?))       AS has_clarify,
		       max(created_at)                   AS at,
		       any(user_id)                      AS uid,
		       anyIf(user_email, user_email != '') AS email
		FROM ai_calls
		WHERE (surface IN (?) OR startsWith(surface, ?))
		  AND created_at >= ?
		  AND prompt_sample != ''
		GROUP BY turn_key
		HAVING turn_key GLOBAL NOT IN (
			SELECT exchange_id FROM cosre_interactions WHERE attempt = 'regenerate' AND created_at >= ?)`
}

// routerGapTurnsArgs — SAF: routerGapTurnsSQL'in bind sırası.
func routerGapTurnsArgs(labels []string, since any) []any {
	none := aisurface.ChatIntentNone
	return []any{none, none, routerGapClarifyPrefix, routerGapClarifyPrefix, labels, routerGapClarifyPrefix, since, since}
}

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
	// liste: soru × son-rota grupları, TUR sayısıyla (bind: tur argümanları, limit).
	turnArgs := routerGapTurnsArgs(labels, sinceArg)
	rows, err := s.conn.Query(ctx, `
		SELECT q,
		       `+routerGapKindExpr+`                                AS kind,
		       count()                                              AS n,
		       toUnixTimestamp64Nano(max(at))                       AS last_at,
		       uniqExact(uid)                                       AS users,
		       arraySort(groupUniqArrayIf(10)(email, email != '')) AS emails,
		       countIf(has_none AND has_loop)                       AS via_general
		FROM (`+routerGapTurnsSQL()+`)
		GROUP BY q, kind
		ORDER BY n DESC, last_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, append(append([]any(nil), turnArgs...), limit)...)
	if err != nil {
		return nil, totals, err
	}
	defer rows.Close()
	out := make([]RouterGap, 0, limit)
	for rows.Next() {
		var g RouterGap
		if err := rows.Scan(&g.Question, &g.Kind, &g.Count, &g.LastAt, &g.Users, &g.Emails, &g.ViaGeneral); err != nil {
			continue
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, totals, err
	}
	trows, err := s.conn.Query(ctx, `
		SELECT `+routerGapKindExpr+` AS kind, count() AS n
		FROM (`+routerGapTurnsSQL()+`)
		GROUP BY kind
		LIMIT 10
		SETTINGS max_execution_time = 10`, turnArgs...)
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
	case RouterGapKindClarify:
		t.Clarify += n
	default:
		t.Loop += n
	}
}
