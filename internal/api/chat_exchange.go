package api

// chat_exchange.go — v0.10.1153 (operatör, prod): "/ai (AI insights) her
// CoSRE etkileşimini göstermiyor".
//
// Kök neden: /ai yalnız ai_calls satırlarını okuyor ve o tabloya yalnız
// MODEL ÇAĞRILARI yazılıyordu. Bir sohbet turunun kendisi hiçbir yerde
// kaydedilmiyordu:
//   - LLM'siz cevaplar (/help, bilinmeyen komut, kapsam "bunu mu demek
//     istedin", /wiki kapı mesajları, "Wikide bulunamadı", guided şablonları,
//     netleştirme, önbellekten gelen trace anlatımı …) HİÇ satır üretmiyordu;
//   - yardımcı çağrılar (niyet sınıflandırıcısı, wiki sayfa seçimi) bilerek
//     exchange'siz yazılıyordu (geri bildirim JOIN'i ikiye katlanmasın diye) —
//     /ai'da turla ilişkisiz satırlar olarak görünüyordu;
//   - kademe, rota, Derin bayrağı ve sonucu hiçbir satır taşımıyordu.
//
// Çözüm (yeni tablo/kolon YOK, mevcut kaydedici): her tur, kademesi ne olursa
// olsun, sonunda ai_calls'a TEK bir etkileşim satırı yazar
// (copilot.RecordExchange — RecordUsage'ın ikizi; aynı Recorder, aynı 4 KB
// tavan, 90 g TTL). Yüzey aisurface.TurnLabel (kademe/rota/derin), exchange
// kimliği aisurface.TurnExchangeID(kök). Turun model çağrıları kökle
// gruplanır: cevap çağrısı kökü AYNEN, yardımcı/işaret satırları "kök:yüzey"
// taşır. /api/ai/exchanges (ai_exchanges.go) bunları birleştirir.
//
// Kayıt merkezî: copilotChat'in tek defer'i — yeni bir kademe eklemek kaydı
// unutamaz (her erken dönüş de yazar). Soru ve cevap metni emit akışından
// okunur (tap); kademe chatSpan'dan.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatExchange — bir CoSRE turunun etkileşim durumu. tap birden çok
// kademeden (ve kurtarma sarmalayıcısından) çağrılabilir → kilitli.
type chatExchange struct {
	mu       sync.Mutex
	root     string
	question string
	deep     bool
	started  time.Time
	route    string
	answer   string
	errMsg   string
}

type chatExchangeKey struct{}

func newChatExchange(root, question string, deep bool, started time.Time) *chatExchange {
	return &chatExchange{root: root, question: strings.TrimSpace(question), deep: deep, started: started}
}

func withChatExchange(ctx context.Context, x *chatExchange) context.Context {
	return context.WithValue(ctx, chatExchangeKey{}, x)
}

// chatExchangeFrom — ctx'teki tur; sohbet dışında nil (nil-güvenli yöntemler).
func chatExchangeFrom(ctx context.Context) *chatExchange {
	if ctx == nil {
		return nil
	}
	x, _ := ctx.Value(chatExchangeKey{}).(*chatExchange)
	return x
}

// noteRoute — guided rotası (runGuidedRoute girişi). İlk rota kalır: kurtarma
// / takip yolları ikinci bir rota çalıştırsa da turun yönlendirmesi ilkidir.
func (x *chatExchange) noteRoute(route string) {
	if x == nil || route == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.route == "" {
		x.route = route
	}
}

// tap — emit sarmalayıcısı: "answer" metnini (son cevap kazanır) ve "error"
// olayını yakalar, olayı DEĞİŞTİRMEDEN iletir.
func (x *chatExchange) tap(emit func(string, any)) func(string, any) {
	return func(kind string, v any) {
		switch kind {
		case "answer":
			x.mu.Lock()
			x.answer = eventField(v, "text")
			x.mu.Unlock()
		case "error":
			x.mu.Lock()
			x.errMsg = eventField(v, "error")
			x.mu.Unlock()
		}
		emit(kind, v)
	}
}

// eventField — SAF: SSE yükünden dize alan (yükler map[string]any ya da
// map[string]string).
func eventField(v any, key string) string {
	switch m := v.(type) {
	case map[string]any:
		s, _ := m[key].(string)
		return s
	case map[string]string:
		return m[key]
	}
	return ""
}

// chatExchangeOutcome — SAF: turun sonucu. Kademe başarısızsa (chatSpan.err)
// ya da akışa hata olayı düştüyse "error"; cevap yokken istek iptal olduysa
// (istemci gitti / alışveriş tavanı) yine "error" — sessiz kayıp sayılmaz.
func chatExchangeOutcome(tierErr error, emittedErr string, answered bool, ctxErr error) (status, msg string) {
	switch {
	case emittedErr != "":
		return "error", emittedErr
	case tierErr != nil:
		return "error", tierErr.Error()
	case !answered && ctxErr != nil:
		return "error", ctxErr.Error()
	}
	return "ok", ""
}

// recordChatExchange — turun etkileşim satırı (copilotChat'in defer'i).
// ctx alışverişin ctx'i (kullanıcı meta'sı, açık/derin profil); iptal edilmiş
// olabilir — kayıt kendi sınırlı ctx'iyle yazılır (copilot.emitRecord).
func (s *Server) recordChatExchange(ctx context.Context, x *chatExchange, cs *chatSpan) {
	if s == nil || s.copilot == nil || x == nil {
		return
	}
	tier, tierErr := aisurface.TierLoop, error(nil)
	if cs != nil {
		tier, tierErr = cs.tierV, cs.err
	}
	x.mu.Lock()
	answer, emittedErr, route := x.answer, x.errMsg, x.route
	x.mu.Unlock()
	status, msg := chatExchangeOutcome(tierErr, emittedErr, answer != "", ctx.Err())
	m := copilot.MetaFromContext(ctx)
	m.Surface = aisurface.TurnLabel(tier, route, x.deep)
	m.ExchangeID = aisurface.TurnExchangeID(x.root)
	m.Observe, m.Shield, m.PromptLogOverride = nil, nil, ""
	s.copilot.RecordExchange(copilot.WithMeta(ctx, m), x.started, status, msg, x.question, answer)
}
