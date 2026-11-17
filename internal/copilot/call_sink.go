package copilot

// call_sink.go — CoSRE etkileşim günlüğü (cosre_interactions, Faz 1) turun
// HANGİ modelle cevaplandığını ve toplam token'ı bilmeli. Observe yalnız
// anlatım yolunda (Explain/StreamText) ve span kancası onu eziyor; sohbet
// döngüsü (RecordUsage) hiç çağırmıyor. Bu ayrı, zincirlenmeyen bir ctx
// kancası: her iki kayıt yolu da recorder'dan BAĞIMSIZ ve SENKRON çağırır.
//
// v0.10.1280 (CoSRE adım 8) — kanca çağrının ai_calls KİMLİĞİNİ de taşır.
// Kimlik artık kayıt anında (InsertAICall'da rastgele) değil, çağrı
// bittiği an copilot'ta basılır: sohbet handler'ı cevabı yayınlarken hangi
// ai_calls satırının o cevabı ürettiğini bilmeli ki 👍/👎 (ai_feedback.call_id)
// somut çağrıya bağlansın. Kayıt gorutini kimliği DEĞİŞTİRMEZ.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type callSinkKey struct{}

// CallNote — kancaya iletilen tek model çağrısı.
type CallNote struct {
	// CallID — ai_calls.id (NewCallID); kayıt bu kimlikle yazılır.
	CallID string
	// ExchangeID — çağrının meta'sındaki exchange kimliği: kök (cevap
	// çağrısı) ya da "kök:rol" (yardımcı/işaret satırı).
	ExchangeID   string
	Model        string
	InputTokens  uint32
	OutputTokens uint32
	// Rounds — v0.10.2297: bu notun temsil ettiği model gidiş-dönüşü sayısı
	// (serbest döngünün tek satırı N tur). 0 = tek çağrı.
	Rounds uint16
	// Loop / TTFTMs — v0.10.2323 (CoSRE H1): serbest döngü notunda
	// v0.10.2318 döngü telemetrisi (RecordUsage'ın ai_calls satırına yazdığı
	// değerlerin aynısı); anlatım notunda akışın ilk token süresi. Sıfır =
	// bilinmiyor. Yalnız okunur (değerlendirme koşucusu); yazım yolu değişmez.
	Loop   LoopUsage
	TTFTMs uint32
}

// CallSink — bir model çağrısı bittiğinde çağrılır (başarılı ya da değil).
type CallSink func(CallNote)

// WithCallSink — ctx'e kanca bağlar; nil kanca ctx'i değiştirmez.
func WithCallSink(ctx context.Context, f CallSink) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, callSinkKey{}, f)
}

func notifyCallSink(ctx context.Context, n CallNote) {
	if ctx == nil {
		return
	}
	if f, ok := ctx.Value(callSinkKey{}).(CallSink); ok && f != nil {
		f(n)
	}
}

// ── ai_calls kimliği ────────────────────────────────────────────────────

type callIDKey struct{}

// NewCallID — ai_calls.id biçiminde (24 hex, chstore.randHex(12) ile aynı
// uzunluk) taze kimlik. crypto/rand okunamazsa "" — kayıt yolu boş kimliği
// InsertAICall'da yine basar (eski davranış), yalnız cevaba bağlanamaz.
func NewCallID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// WithCallID — sonraki RecordUsage çağrısının kimliğini ÖNCEDEN sabitler.
// Sohbet döngüleri cevabı yayınladıktan SONRA toplu satırı yazar; cevap
// olayındaki callId'nin o satırla eşleşmesi için kimlik cevaptan önce
// basılıp buradan geçirilir. Boş kimlik ctx'i değiştirmez.
//
// v0.10.1312 — Explain/StreamText kaydı (recordNarration) da bu kimliği
// okur: arka plan AI SRE anlatımı kimliği çağrıdan önce basar ve özetle
// saklar. ctx TEK çağrıya verilmeli — aynı ctx'le iki çağrı aynı ai_calls
// kimliğini yazar.
func WithCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, callIDKey{}, id)
}

// CallIDFromContext — v0.10.1312: ctx'te WithCallID ile sabitlenmiş kimlik
// ("" = yok). Narrator test dikişleri (internal/sre) gerçek kaydın hangi
// kimlikle yazılacağını buradan görür.
func CallIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if id, ok := ctx.Value(callIDKey{}).(string); ok {
			return id
		}
	}
	return ""
}

// callIDFor — ctx'te sabitlenmiş kimlik, yoksa taze.
func callIDFor(ctx context.Context) string {
	if id := CallIDFromContext(ctx); id != "" {
		return id
	}
	return NewCallID()
}
