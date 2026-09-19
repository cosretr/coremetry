// v0.10.807 (dış skill denetimi L2) — önek önbelleği yüzdesi; SAF.
// cached = giriş token'ının önbellekten okunan alt kümesi. input 0 → "0%".
export function cachedPct(cached: number, input: number): number {
  if (!input || input <= 0 || !cached || cached <= 0) return 0;
  return Math.min(100, Math.round((cached / input) * 100));
}

export function cachedPctLabel(cached: number, input: number): string {
  return `${cachedPct(cached, input)}%`;
}
