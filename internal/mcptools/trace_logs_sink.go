package mcptools

// trace_logs_sink.go — v0.10.1034 (operatör: "Kod inceleme çalışma mantığı ile
// direkt Ask CoSRE farklı."). "Kodu da incele" artık Ask CoSRE incelemesinin
// (api/trace_investigate.go) üstüne kod ekliyor; kod çekicinin girdisi
// (stacktrace + onu basan servis) incelemenin ZATEN yaptığı get_logs_for_trace
// okumasından gelir — ikinci bir log okuması yok.
//
// Neden dikiş: aracın modele giden çıktısı öznitelik değerini 200, gövdeyi 500
// runede keser (logAttrs / logRows). Kesik bir stack'ten dosya+satır taşıyan
// uygulama karesi çıkmaz (OTel/ECS exception.stacktrace çoğunlukla başlık + ~1
// kare kalır). Bu kanca AYNI okumanın HAM kayıtlarını çağırana verir.
//
// Sözleşme: araç çıktısı DEĞİŞMEZ (MCP ve sohbet bayt bayt aynı); kanca yalnız
// ctx'e konduğunda, okuma başarılıyken, handler dönmeden ve aynı goroutine'de
// çağrılır. Kayıtlar aracın kendi dilimidir: kanca dilimi DEĞİŞTİRMEZ
// (sıralayacaksa kopyalar) — araç onu kancadan sonra satıra çevirir.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/logstore"
)

type traceLogsSinkKey struct{}

// TraceLogsSink — get_logs_for_trace'in okuduğu kesilmemiş kayıtlar (gövde ve
// öznitelikler aynen). Dilim salt-okunur.
type TraceLogsSink func(logs []*logstore.LogRecord)

// WithTraceLogsSink — v0.10.1034: get_logs_for_trace bu ctx'le çağrılınca ham
// kayıtları sink'e verir. nil sink → ctx aynen.
func WithTraceLogsSink(ctx context.Context, sink TraceLogsSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, traceLogsSinkKey{}, sink)
}

// traceLogsSinkOf — ctx'teki kanca; yoksa nil.
func traceLogsSinkOf(ctx context.Context) TraceLogsSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(traceLogsSinkKey{}).(TraceLogsSink)
	return sink
}
