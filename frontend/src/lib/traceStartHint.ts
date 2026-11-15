// traceStartHint.ts — v0.10.2201 (operator-reported): trace sayfasının
// yüklediği trace'in başlangıcı (unix ms), "Bu trace'i açıkla" turuna
// pencere ipucu olarak gider (ChatExplainAsk.startMs). Sunucu bunu CH
// pencere aramasının ilk probe'u yapar; ipucu yoksa kademeli arama aynen.
// URL'de zaman yok, bu yüzden sayfa yayınlar; yalnız SON trace tutulur.

let last: { id: string; startMs: number } | null = null;

/** Trace sayfası spanları yüklediğinde çağırır (ns → ms). */
export function rememberTraceStart(id: string, spans: ReadonlyArray<{ startTime?: number }> | null | undefined): void {
  if (!id || !spans?.length) return;
  let min = 0;
  for (const s of spans) {
    const t = s.startTime ?? 0;
    if (t > 0 && (min === 0 || t < min)) min = t;
  }
  if (min > 0) last = { id: id.toLowerCase(), startMs: Math.floor(min / 1e6) };
}

/** Kimliğe ait bilinen başlangıç (ms); yoksa undefined. */
export function traceStartFor(id: string): number | undefined {
  return last && last.id === id.toLowerCase() ? last.startMs : undefined;
}

/** Yalnız testler. */
export function resetTraceStartHint(): void { last = null; }
