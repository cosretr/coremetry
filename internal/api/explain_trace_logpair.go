package api

// explain_trace_logpair.go — v0.10.2461 (operatör-bildirimli, 2026-11-22):
// span exception mesajı ↔ aynı hatanın log metni.
//
// ── SEMPTOM ─────────────────────────────────────────────────────────────
//
// Trace açıklamasının "Hata Kaynağı" satırı `XBusinessException: "CODE -
// We cannot help you …"` yazdı; trace'in Logs sekmesindeki satır Türkçe
// ("… yardımcı olamıyoruz …"). Operatör Türkçe cevap ve özgün metni istedi.
//
// ── KÖK NEDEN ───────────────────────────────────────────────────────────
//
// İngilizce metin kanıtta VARDI — model çevirmedi: uygulama span'in
// exception event'ine (exception.message) İngilizce, log gövdesine Türkçe
// yazıyor. Kanıt sırası: HATA ZİNCİRİ (en başta; halkanın kendi span
// event'i log exception'ını EZER — tracechain.Chain.Block) → KRİTİK YOL →
// span JSON → SPAN EVENT'LERİ (exMessage) → LOGLAR (en sonda). İstem de
// "exception event'i varsa tipini ve mesajını AYNEN yaz" diyordu; model ilk
// gördüğü, istemin işaret ettiği span mesajını aldı, log metni hiç öne
// çıkmadı.
//
// ── ÇÖZÜM ───────────────────────────────────────────────────────────────
//
// Ek okuma yok: zaten okunan trace logları içinde, exception event'li span
// ile AYNI hatayı anlatan log satırı aranır (aynı span kimliği + WARN/ERROR,
// ya da aynı servis + aynı exception sınıfı + span zamanı). Bulunursa ve
// metni span mesajından farklıysa: zincir halkası ve "Zincirin ardından
// kaydedilen exception" satırı log metnini ÖNCE verir ("log:" / "span
// event:" etiketli), SPAN EVENT'LERİ girdisi exMessage'tan önce logMessage
// taşır. Eşleşme yoksa kanıt bayt bayt eskisi.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/stackparse"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// Zaman toleransı: log span bittikten biraz sonra yazılabilir.
const (
	traceLogPairBeforeNs = int64(1e9) // span başından 1 sn önce
	traceLogPairAfterNs  = int64(5e9) // span sonundan 5 sn sonra
)

// exceptionShortName — "com.x.y.FooException" → "fooexception" (küçük harf).
func exceptionShortName(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.LastIndexAny(t, ".$"); i >= 0 {
		t = t[i+1:]
	}
	return strings.ToLower(t)
}

// traceLogUserText — SAF: log satırının kullanıcıya görünen metni: gövdenin
// stack öncesi ilk satırı ("Tip: " öneki atılır); gövde boşsa
// exception.message. Tek satır.
func traceLogUserText(l *logstore.LogRecord, exType string) string {
	head := strings.TrimSpace(stackparse.MessageHead(l.Body))
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = strings.TrimSpace(head[:i])
	}
	if exType != "" {
		for _, pre := range []string{exType, exType[strings.LastIndexAny(exType, ".$")+1:]} {
			if pre != "" && strings.HasPrefix(head, pre+":") {
				head = strings.TrimSpace(head[len(pre)+1:])
				break
			}
		}
	}
	if head == "" {
		head = strings.TrimSpace(l.Attributes["exception.message"])
	}
	return logOneLine(head)
}

// traceExceptionLogText — SAF: span'in exception'ıyla (exType, exMsg) AYNI
// hatayı anlatan log satırının metni; yoksa ya da metin span mesajıyla aynıysa "".
// Öncelik: (1) aynı span kimliği, WARN/ERROR; (2) aynı servis, aynı exception
// sınıfı (exception.type ya da gövdede sınıf adı), span zamanında. Sınıf
// içinde ERROR önce, sonra en erken.
func traceExceptionLogText(sp chstore.SpanRow, exType, exMsg string, logs []*logstore.LogRecord) string {
	short := exceptionShortName(exType)
	var best *logstore.LogRecord
	bestRank := 0
	for _, l := range logs {
		if l == nil || traceLogSevClass(l) == 0 {
			continue
		}
		if sp.ServiceName != "" && l.ServiceName != "" && l.ServiceName != sp.ServiceName {
			continue
		}
		rank := 0
		switch {
		case sp.SpanID != "" && l.SpanID == sp.SpanID:
			rank = 4
		case short != "" && len(short) >= 4 && inSpanTime(sp, l.Timestamp) &&
			(exceptionShortName(l.Attributes["exception.type"]) == short || strings.Contains(strings.ToLower(l.Body), short)):
			rank = 2
		default:
			continue
		}
		if traceLogSevClass(l) == 2 {
			rank++
		}
		if best == nil || rank > bestRank || (rank == bestRank && l.Timestamp < best.Timestamp) {
			best, bestRank = l, rank
		}
	}
	if best == nil {
		return ""
	}
	txt := traceLogUserText(best, exType)
	if txt == "" || quoteNorm(txt) == quoteNorm(exMsg) {
		return ""
	}
	return txt
}

func inSpanTime(sp chstore.SpanRow, ts int64) bool {
	if ts <= 0 || sp.StartTime <= 0 {
		return true
	}
	end := sp.EndTime
	if end < sp.StartTime {
		end = sp.StartTime
	}
	return ts >= sp.StartTime-traceLogPairBeforeNs && ts <= end+traceLogPairAfterNs
}

// pairTraceExceptionLogs — SAF: zincir halkalarına ve SPAN EVENT'LERİ
// girdilerine eşleşen log metnini yazar (yerinde). spans: event bloğunun
// kurulduğu span'ler (kimlikle aranır).
func pairTraceExceptionLogs(chain *tracechain.Chain, evd *traceEventDigest, spans []chstore.SpanRow, logs []*logstore.LogRecord) {
	if len(logs) == 0 {
		return
	}
	if chain != nil {
		for i := range chain.Hops {
			h := &chain.Hops[i]
			if h.Fail.ExType == "" {
				continue
			}
			h.Fail.LogMsg = truncRunesN(traceExceptionLogText(h.Span, h.Fail.ExType, h.Fail.ExMsg, logs), traceExMsgMaxRunes)
		}
	}
	if evd == nil {
		return
	}
	byID := make(map[string]int, len(spans))
	for i := range spans {
		byID[spans[i].SpanID] = i
	}
	for si := range evd.Spans {
		e := &evd.Spans[si]
		idx, ok := byID[e.SpanID]
		if !ok {
			continue
		}
		for ei := range e.Events {
			ev := &e.Events[ei]
			if ev.exKey == "" || (ev.ExType == "" && ev.ExMessage == "") {
				continue
			}
			ev.LogMessage = truncRunesN(traceExceptionLogText(spans[idx], ev.ExType, ev.ExMessage, logs), traceExMsgMaxRunes)
		}
	}
}
