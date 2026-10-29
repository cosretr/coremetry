// traceServiceFocus — v0.10.1556 (operatör: "Bir trace açtığımda birden fazla
// servis yazıyor. Üzerine tıkladığım servisin alt span'lerine götürsün,
// göstersin."). Servis lejantından seçilen servisin span'leri şelalede odak
// olur (diğerleri soluk; TraceWaterfall focusIds) ve en erken başlayan span'ine
// kaydırılır (revealSpanId). SAF.
import type { SpanRow } from './types';

export interface ServiceFocus {
  ids: Set<string>;
  /** Servisin başlangıcı en erken span'i (eşitlikte ilk görülen); yoksa null. */
  first: string | null;
}

export function serviceFocus(spans: readonly SpanRow[] | null | undefined, service: string | null | undefined): ServiceFocus {
  const ids = new Set<string>();
  let first: SpanRow | null = null;
  if (!service) return { ids, first: null };
  for (const s of spans ?? []) {
    if (s.serviceName !== service) continue;
    ids.add(s.spanId);
    if (!first || s.startTime < first.startTime) first = s;
  }
  return { ids, first: first?.spanId ?? null };
}
