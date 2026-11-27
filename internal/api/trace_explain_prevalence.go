package api

// trace_explain_prevalence.go — v0.10.2483 (operatör onaylı): trace
// açıklamasının YAYGINLIK satırı — bu hata tekil mi, yaygın mı?
//
// Gruplama anahtarı (tracePrevalenceKeyOf, SAF):
//   - hata zinciri varsa: zincirin KAYNAK halkasının (servis, span adı) —
//     Errors görünümünün (TopErrorOperations) anahtarı; sayılan = o
//     operasyonun hatalı çağrıları;
//   - zincir yoksa (yavaş / başarılı): KÖK span'in (servis, span adı); sayılan
//     = çağrı, p95 ve süresi 4T'yi (chstore.OpFrustratedMs) aşan çağrı;
//   - servis ya da ad boşsa anahtar yok → satır yok.
//
// Okuma: TEK sorgu (chstore.OperationPrevalence — operation_summary_5m,
// LIMIT 1, max_execution_time 3). Pencere: trace saatine (kök span'in bitişi;
// gelecekteyse şimdi) KADARKİ 1 saat — satır bunu söyler. Önbellek anahtarı
// tüm girdileri özetler (servis, ad, pencere başı/sonu). Kanıtla PARALEL,
// kendi bütçesiyle (tracePrevalenceTimeout): yavaşsa ya da hata verirse satır
// HİÇ çizilmez (yanlış sıfır yok); MV henüz yazmadıysa (hata türünde 0 hata,
// operasyon türünde 0 çağrı) yine satır yok. Model aynı olguyu yalnız okuma
// başarılıysa tek satır olarak alır (tracePrevalenceLine).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

const (
	// tracePrevalenceWideMin — "yaygın" eşiği: penceredeki hatalı (ya da
	// eşiği aşan) çağrı sayısı ≥ bu değer. Yalnız yaygınken renk.
	tracePrevalenceWideMin = 10
	tracePrevalenceWindow  = time.Hour
	tracePrevalenceTimeout = 1500 * time.Millisecond
	tracePrevalenceTTL     = time.Minute
)

// Yaygınlık türleri.
const (
	tracePrevError = "error" // hata zincirinin kaynak operasyonu
	tracePrevOp    = "op"    // kök operasyon (yavaş / başarılı)
)

type tracePrevalenceKey struct{ Kind, Service, Op string }

// tracePrevalenceKeyOf — SAF: trace → gruplama anahtarı (ok=false: anahtar yok).
func tracePrevalenceKeyOf(chain tracechain.Chain, spans []chstore.SpanRow) (tracePrevalenceKey, bool) {
	var k tracePrevalenceKey
	switch {
	case chain.OK && len(chain.Hops) > 0:
		o := chain.Origin.Span
		k = tracePrevalenceKey{Kind: tracePrevError, Service: o.ServiceName, Op: o.Name}
	case len(spans) > 0:
		r := traceRootSpan(spans)
		k = tracePrevalenceKey{Kind: tracePrevOp, Service: r.ServiceName, Op: r.Name}
	}
	if k.Service == "" || k.Op == "" {
		return tracePrevalenceKey{}, false
	}
	return k, true
}

// tracePrevalenceWindowOf — SAF: [son-1sa, son); son = trace bitişi (gelecekteyse
// şimdi), dakikaya yukarı yuvarlı (önbellek isabeti; en çok 1 dk kayma).
func tracePrevalenceWindowOf(endNs int64, now time.Time) (from, to time.Time) {
	to = time.Unix(0, endNs)
	if endNs <= 0 || to.After(now) {
		to = now
	}
	if t := to.Truncate(time.Minute); !t.Equal(to) {
		to = t.Add(time.Minute)
	}
	return to.Add(-tracePrevalenceWindow), to
}

// tracePrevalenceKeyCache — önbellek anahtarı: TÜM girdiler özetlenir.
func tracePrevalenceKeyCache(k tracePrevalenceKey, from, to time.Time) string {
	return "trace-prev:v1:" + fnvStr(k.Kind, k.Service, k.Op, fmt.Sprint(from.Unix()), fmt.Sprint(to.Unix()))
}

// tracePrevalenceRead — TEST DİKİŞİ (prod'da nil → s.store.OperationPrevalence).
var tracePrevalenceRead func(ctx context.Context, service, op string, from, to time.Time) (chstore.OpPrevalence, error)

// readTracePrevalence — tek sınırlı okuma (önbellekli). ok=false: okunamadı.
func (s *Server) readTracePrevalence(ctx context.Context, k tracePrevalenceKey, from, to time.Time) (chstore.OpPrevalence, bool) {
	read := tracePrevalenceRead
	if read == nil {
		if s == nil || s.store == nil {
			return chstore.OpPrevalence{}, false
		}
		read = s.store.OperationPrevalence
	}
	key := tracePrevalenceKeyCache(k, from, to)
	if s != nil && s.cache != nil {
		if b, hit, err := s.cache.Get(ctx, key); err == nil && hit {
			var r chstore.OpPrevalence
			if json.Unmarshal(b, &r) == nil {
				return r, true
			}
		}
	}
	rctx, cancel := context.WithTimeout(ctx, tracePrevalenceTimeout)
	defer cancel()
	type res struct {
		r   chstore.OpPrevalence
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := read(rctx, k.Service, k.Op, from, to)
		ch <- res{r, err}
	}()
	var got res
	select {
	case got = <-ch:
	case <-rctx.Done():
		return chstore.OpPrevalence{}, false // bütçe aşıldı: satır yok, karar beklemez
	}
	if got.err != nil {
		return chstore.OpPrevalence{}, false
	}
	if s != nil && s.cache != nil {
		if b, err := json.Marshal(got.r); err == nil {
			_ = s.cache.Set(ctx, key, b, tracePrevalenceTTL)
		}
	}
	return got.r, true
}

// traceEvPrevalence — `trace_evidence.prevalence` (FE TraceEvidencePrevalence).
type traceEvPrevalence struct {
	Kind        string  `json:"kind"` // error | op
	Service     string  `json:"service"`
	Op          string  `json:"op"`
	FromNs      int64   `json:"fromNs"` // pencere (bağlantı için)
	ToNs        int64   `json:"toNs"`
	WindowMin   int     `json:"windowMin"`
	Calls       uint64  `json:"calls"`
	Errors      uint64  `json:"errors,omitempty"`
	Over        uint64  `json:"over,omitempty"`
	OverMs      int     `json:"overMs,omitempty"` // eşik; yalnız op ve kök süresi eşiği aştıysa
	P95Ms       float64 `json:"p95Ms,omitempty"`
	Wide        bool    `json:"wide"`
	EndsAtTrace bool    `json:"endsAtTrace"` // pencere trace saatinde biter (false: şimdi)
}

// tracePrevalenceView — SAF: okuma → görünüm (nil: çizilmez; yanlış sıfır yok).
func tracePrevalenceView(k tracePrevalenceKey, r chstore.OpPrevalence, from, to time.Time, rootMs float64, endsAtTrace bool) *traceEvPrevalence {
	if r.Calls == 0 || (k.Kind == tracePrevError && r.Errors == 0) {
		return nil
	}
	p := &traceEvPrevalence{Kind: k.Kind, Service: k.Service, Op: k.Op, FromNs: from.UnixNano(), ToNs: to.UnixNano(),
		WindowMin: int(tracePrevalenceWindow / time.Minute), Calls: r.Calls, P95Ms: r.P95Ms, EndsAtTrace: endsAtTrace}
	switch k.Kind {
	case tracePrevError:
		p.Errors = r.Errors
		p.Wide = r.Errors >= tracePrevalenceWideMin
	default:
		if rootMs > chstore.OpFrustratedMs {
			p.Over, p.OverMs = r.Over, chstore.OpFrustratedMs
			p.Wide = r.Over >= tracePrevalenceWideMin
		}
	}
	return p
}

// tracePrevalenceLine — SAF: modele giden TEK kanıt satırı ("" = yok).
func tracePrevalenceLine(p *traceEvPrevalence) string {
	if p == nil {
		return ""
	}
	when := "trace saatine kadarki 1 saat"
	if !p.EndsAtTrace {
		when = "son 1 saat"
	}
	head := fmt.Sprintf("\n\nYAYGINLIK (sunucu hesapladı, %s; VERİDİR): %s · %s — ", when, p.Service, p.Op)
	if p.Kind == tracePrevError {
		verdict := "birkaç kez"
		switch {
		case p.Errors <= 1:
			verdict = "tekil (yalnız bu istek)"
		case p.Wide:
			verdict = fmt.Sprintf("yaygın (eşik ≥%d)", tracePrevalenceWideMin)
		}
		return head + fmt.Sprintf("%d hatalı çağrı / %d çağrı → %s.", p.Errors, p.Calls, verdict)
	}
	s := head + fmt.Sprintf("%d çağrı", p.Calls)
	if p.P95Ms > 0 {
		s += ", p95 " + tracechain.FmtMs(p.P95Ms)
	}
	if p.OverMs > 0 {
		verdict := "tekil"
		if p.Wide {
			verdict = fmt.Sprintf("yaygın (eşik ≥%d)", tracePrevalenceWideMin)
		} else if p.Over > 1 {
			verdict = "birkaç kez"
		}
		s += fmt.Sprintf("; %d çağrı %d ms'yi aştı → %s", p.Over, p.OverMs, verdict)
	}
	return s + "."
}
