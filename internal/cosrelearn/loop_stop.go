package cosrelearn

// loop_stop.go — CoSRE ölçüm: serbest araç döngüsünün tur başına KÜNYESİ.
// Sohbet turu bitince sunucu tek bir loop_stop sistem sinyali yazar
// (cosre_signals; yeni tablo / kolon YOK):
//
//	stop=<neden> rounds=<n> calls=<n> tools=<n> schema_bytes=<n> overflow=<retried|failed>
//
// stop      döngünün durma nedeni (api/chat_stop.go kapalı kümesi; bilinmeyen
//           değer "other"a katlanır — kardinalite sınırlı)
// rounds    harcanan model turu · calls modelin istediği araç çağrısı
// tools     modele gönderilen araç kataloğu boyu · schema_bytes o kataloğun
//           JSON baytı (ad + açıklama + giriş şeması; küçük modelin bağlamını
//           yiyen "schema soup" ölçüsü)
// overflow  bağlam taştı: retried = geçmiş küçültülüp yeniden denendi
//           (KIRPILMIŞ bağlamla cevap), failed = yine sığmadı
// free      v0.10.2480: bütçeden DÜŞMEYEN çağrı (kayıttan / aynı tur önbelleği);
//           calls artık yalnız harcanan hak. 0 ise yazılmaz.
// tier      v0.10.2480 (H): karmaşıklık kademesi simple|medium|complex;
//           cue_fallback=1 → sınıflandırıcı kararı yoktu, deterministik işaret.
//
// Başarısız tur dedektörü bu sinyalden iki gerekçe okur (failed_turns.go):
// round_cap (tur / çağrı / token tavanı) ve context_overflow. Etiket DEĞİL —
// inceleme kuyruğu; ağırlığı yok (Weight'te değil), gece puanına girmez.

import (
	"strconv"
	"strings"
)

// SigLoopStop — tur başına döngü künyesi (sistem sinyali).
const SigLoopStop = "loop_stop"

// Döngü durma nedenleri (api/chat_stop.go + chat_stop_event.go aynası).
var loopStopKnown = map[string]bool{
	"stop": true, "round_cap": true, "call_cap": true, "token_budget": true,
	"cancelled": true, "deadline": true, "error": true, "approval_pending": true,
}

// loopCapStops — round_cap gerekçesini doğuran durma nedenleri.
var loopCapStops = map[string]bool{"round_cap": true, "call_cap": true, "token_budget": true}

// Bağlam taşması durumları.
const (
	OverflowRetried = "retried"
	OverflowFailed  = "failed"
)

// LoopShape — turun döngü künyesi.
type LoopShape struct {
	Stop        string
	Rounds      int
	Calls       int
	Tools       int
	SchemaBytes int
	Overflow    string // "" | retried | failed
	Free        int    // v0.10.2480 — bütçeden düşmeyen çağrı
	Tier        string // v0.10.2480 — simple | medium | complex | ""
	CueFallback bool   // v0.10.2480 — kademe deterministik işaretten
}

// loopTiers — tier değerinin kapalı kümesi (kardinalite sınırlı).
var loopTiers = map[string]bool{"simple": true, "medium": true, "complex": true}

// LoopStopDetail — SAF: künye → sinyal ayrıntısı (sabit anahtar sırası).
func LoopStopDetail(l LoopShape) string {
	stop := l.Stop
	if stop == "" {
		stop = "none"
	} else if !loopStopKnown[stop] {
		stop = "other"
	}
	var b strings.Builder
	b.WriteString("stop=" + stop)
	for _, kv := range []struct {
		k string
		v int
	}{{"rounds", l.Rounds}, {"calls", l.Calls}, {"tools", l.Tools}, {"schema_bytes", l.SchemaBytes}} {
		b.WriteString(" " + kv.k + "=" + strconv.Itoa(max(kv.v, 0)))
	}
	if l.Overflow == OverflowRetried || l.Overflow == OverflowFailed {
		b.WriteString(" overflow=" + l.Overflow)
	}
	if l.Free > 0 {
		b.WriteString(" free=" + strconv.Itoa(l.Free))
	}
	if loopTiers[l.Tier] {
		b.WriteString(" tier=" + l.Tier)
		if l.CueFallback {
			b.WriteString(" cue_fallback=1")
		}
	}
	return b.String()
}

// ParseLoopStopDetail — SAF: ayrıntı → künye (bozuk alan yok sayılır).
func ParseLoopStopDetail(d string) LoopShape {
	var l LoopShape
	for _, f := range strings.Fields(d) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(v)
		switch k {
		case "stop":
			l.Stop = v
		case "rounds":
			l.Rounds = n
		case "calls":
			l.Calls = n
		case "tools":
			l.Tools = n
		case "schema_bytes":
			l.SchemaBytes = n
		case "overflow":
			l.Overflow = v
		case "free":
			l.Free = n
		case "tier":
			l.Tier = v
		case "cue_fallback":
			l.CueFallback = n == 1
		}
	}
	return l
}

// LoopStopReasons — SAF: loop_stop ayrıntısı → başarısız tur gerekçeleri.
func LoopStopReasons(d string) []string {
	l := ParseLoopStopDetail(d)
	var out []string
	if loopCapStops[l.Stop] {
		out = append(out, FailRoundCap)
	}
	if l.Overflow == OverflowRetried || l.Overflow == OverflowFailed {
		out = append(out, FailCtxOverflow)
	}
	return out
}

// ContextOverflowText — SAF: hata metni bağlam taşması mı (ham sağlayıcı
// metni ya da sohbetin TR mesajı chatOverflowMessageTR).
func ContextOverflowText(msg string) bool {
	m := strings.ToLower(msg)
	for _, kw := range []string{"bağlam penceresine sığmadı", "context length", "context_length", "context window",
		"maximum context", "context_length_exceeded", "prompt is too long"} {
		if strings.Contains(m, kw) {
			return true
		}
	}
	return false
}
