package api

// chat_long_wait.go — v0.10.2466 (operatör kararı 2026-11-22): CoSRE'de
// "daha uzun bekleyeyim mi?".
//
// Bir turda okuma aracı (trace / endpoint / search_traces …) okuma bütçesini
// aşar ya da kısmi sonuç dönerse (chstore.NoteReadTimeout — GetTraces kısmi
// sonucu + araç katmanında zaman aşımı hatası) CoSRE elindekiyle cevaplar ve
// cevabın sonuna SORAR: "Daha geniş arama için daha uzun bekleyeyim mi?
// (en fazla 60 sn)" + takip çipi (kind=long_wait). Çipin tıkı aynı soruyu
// context.longWait=true ile gönderir; YALNIZ o turun okumaları
// min(60, admin tavanı) sn bütçeyle koşar (chstore.WithLongWait; araç
// bütçesi buna göre uzar). Kullanıcının evet'i olmadan ASLA uzun bütçeyle
// yeniden denenmez. Model ayrımı yok: soru ve çip sunucu metnidir, Qwen da
// gemma da aynı SAF kararı görür; ek model çağrısı yok.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const (
	longWaitChipKind = "long_wait"
	// longWaitToolSlack — araç bütçesi = okuma bütçesi + pay (sunucunun 159'u
	// araç zaman aşımından önce gelsin, açıklayıcı hata modele ulaşsın).
	longWaitToolSlack = 10 * time.Second
)

// longWaitQuestionTR — cevabın sonuna eklenen soru (SAF).
func longWaitQuestionTR(sec int) string {
	return fmt.Sprintf("_Okuma süre sınırına takıldı, sonuç kısmi olabilir. Daha geniş arama için daha uzun bekleyeyim mi? (en fazla %d sn)_", sec)
}

// longWaitStillPartialTR — uzun bütçeyle de bitmedi: soru TEKRAR sorulmaz.
func longWaitStillPartialTR(sec int) string {
	return fmt.Sprintf("_%d sn'lik uzun bekleme de yetmedi; sonuç kısmi. Daha dar bir zaman aralığı ya da ek filtre deneyin._", sec)
}

// longWaitChip — takip çipi (SAF): tık Text'i + context.longWait gönderir.
func longWaitChip(sec int) chatFollowup {
	return chatFollowup{
		Text:     fmt.Sprintf("Evet, %d sn bekle: aynı aramayı daha uzun süreyle tekrarla", sec),
		Label:    fmt.Sprintf("Evet, daha uzun bekle (%d sn)", sec),
		Kind:     longWaitChipKind,
		LongWait: true,
	}
}

// chatLongWaitTurn — bir turun kaydı.
type chatLongWaitTurn struct {
	rec     *chstore.TimeoutRecorder
	usedSec int // >0: kullanıcı evet dedi, bu tur uzun bütçeyle
	offer   int // sorulacak bütçe (min(60, tavan))
}

// beginChatLongWaitTurn — ctx'e kayıtçı (+ evet'te uzun bütçe) ve answer
// sarıcısı. Kısmi/zaman aşımı yoksa answer AYNEN geçer.
func beginChatLongWaitTurn(ctx context.Context, emit func(string, any), longWait bool) (func(string, any), context.Context) {
	t := &chatLongWaitTurn{offer: chstore.EffectiveLongWaitSec(chstore.LongWaitCap())}
	ctx, t.rec = chstore.WithTimeoutRecorder(ctx)
	if longWait {
		t.usedSec = t.offer
		ctx = chstore.WithLongWait(ctx, t.usedSec)
		emit("step", map[string]string{"label": fmt.Sprintf("Daha uzun bekleniyor (en fazla %d sn)", t.usedSec)})
	}
	return func(kind string, payload any) {
		if kind == "answer" && t.rec.Hit() {
			payload = withLongWaitAsk(payload, t.usedSec, t.offer)
		}
		emit(kind, payload)
	}, ctx
}

// withLongWaitAsk — SAF: answer yüküne soru + çip (ya da uzun beklemede
// "yetmedi" notu). Sığ kopya; aynı türden çip varsa eklenmez.
func withLongWaitAsk(payload any, usedSec, offerSec int) any {
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	text, _ := m["text"].(string)
	note := longWaitQuestionTR(offerSec)
	if usedSec > 0 {
		note = longWaitStillPartialTR(usedSec)
	}
	if t := strings.TrimRight(text, " \t\n"); t != "" {
		out["text"] = t + "\n\n" + note
	} else {
		out["text"] = note
	}
	if usedSec > 0 {
		return out
	}
	fu, _ := m["followups"].([]chatFollowup)
	for _, c := range fu {
		if c.Kind == longWaitChipKind {
			return out
		}
	}
	out["followups"] = append(append([]chatFollowup(nil), fu...), longWaitChip(offerSec))
	return out
}

// wrapLongWaitHandlers — araç hatası okuma bütçesi / zaman aşımıysa tur
// kayıtçısı işaretlenir (kısmi sonucu GetTraces kendisi işaretler).
func wrapLongWaitHandlers(byName map[string]mcp.ToolHandler) {
	for n, h := range byName {
		h := h
		byName[n] = func(ctx context.Context, args json.RawMessage) (any, error) {
			out, err := h(ctx, args)
			if err != nil && chstore.IsReadTimeout(err) {
				chstore.NoteReadTimeout(ctx)
			}
			return out, err
		}
	}
}

// longWaitToolBudget — evet'li turda araç bütçesi (0 = varsayılan bütçe).
func longWaitToolBudget(ctx context.Context) time.Duration {
	if sec := chstore.LongWaitFrom(ctx); sec > 0 {
		return time.Duration(sec)*time.Second + longWaitToolSlack
	}
	return 0
}
