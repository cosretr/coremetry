package copilot

// trace.go — v0.10.1463 (CoSRE program D1): sağlayıcı çağrısının
// self-telemetri bağı.
//
//  1. tracedProviderTransport — sağlayıcı HTTP istemcisi otelhttp ile
//     sarılır: her model çağrısı çağıranın span'ı altında bir HTTP istemci
//     span'ı (ai.chat.turn / sre.narrate → "HTTP POST") açar ve W3C
//     `traceparent` başlığını sağlayıcıya (self-hosted vLLM) taşır — vLLM'in
//     kendi OTel izleri aynı trace'e düşer. Yayıcı AÇIKÇA TraceContext
//     (/otel-conventions §1: tek yayıcı W3C): süreç geneli
//     otel.GetTextMapPropagator() hiç kurulmadığı için boş — ona güvenseydik
//     başlık hiç gitmezdi. Self-obs kapalıyken tracer noop → span yok,
//     geçerli span bağlamı yok → başlık da yok (davranış bayt bayt eski).
//     Dış sağlayıcıya (Anthropic / OpenAI) giden başlık yalnız rastgele bir
//     trace/span kimliğidir; gövde, kimlik bilgisi taşımaz.
//  2. callTraceID — ai_calls.trace_id: çağrı anındaki span ÖRNEKLENMİŞSE
//     trace kimliği; örneklenmemiş kimlik sorgulanamaz → yazılmaz.

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// tracedTransport — otelhttp sarmalı + altındaki *http.Transport (TLS /
// zaman aşımı ayarlarının doğrulanabilmesi için; baseTransport).
type tracedTransport struct {
	base *http.Transport
	rt   http.RoundTripper
}

func (t *tracedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return gatedRoundTrip(t.rt, r) // v0.10.1564 — eşzamanlılık kapısı (provider_gate.go)
}

// tracedProviderTransport — sağlayıcı istemcisinin taşıyıcısı (tek kurulum
// yeri buildCopilotHTTPClient; profiller de ondan geçer). Span adı otelhttp
// varsayılanı "HTTP POST" (semconv istemci span'ı).
func tracedProviderTransport(base *http.Transport) http.RoundTripper {
	return &tracedTransport{base: base, rt: otelhttp.NewTransport(base,
		otelhttp.WithPropagators(propagation.TraceContext{}))}
}

// baseTransport — istemcinin altındaki *http.Transport (yoksa nil).
func baseTransport(c *http.Client) *http.Transport {
	switch t := c.Transport.(type) {
	case *tracedTransport:
		return t.base
	case *http.Transport:
		return t
	}
	return nil
}

// callTraceID — ctx'teki örneklenmiş trace kimliği ("" = yok).
func callTraceID(ctx context.Context) string {
	return selfobs.SampledTraceID(trace.SpanContextFromContext(ctx))
}
