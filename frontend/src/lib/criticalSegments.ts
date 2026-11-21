// criticalSegments.ts — Dalga 3 (2026-11-21, Grafana 12.4 / Jaeger kritik
// yol gölgesi). lib/criticalPath'in TEK zinciri (kök→yaprak, Go ikizi
// chstore/tracetree.go ile parite — başlık ölçüsü onu okur) her seviyede
// YALNIZ bir çocuk seçer: sıralı A→B→C çağrılarından yalnız biri "kritik"
// görünür. Bu modül Jaeger/Grafana'nın kesit (segment) tanımıdır: span'in
// bitişinden geriye yürünür, imleçten ÖNCE biten EN GEÇ biten çocuk kritiktir;
// çocuğun bitişi ile imleç arasındaki boşluk ebeveynin kendi kritik kesitidir;
// imleç çocuğun başlangıcına iner ve aynı kural tekrarlanır. Sonuç: span
// başına [başlangıç, bitiş] ns kesitleri — çubuğun YALNIZ o kısmı gölgelenir.
//
// Async / saat kayması kuralları (sınırlı, açıklanabilir):
//   - Ebeveyn bitişinden SONRA başlayan çocuk (follows-from / fire-and-forget)
//     zincire girmez.
//   - Ebeveyn bitişini tolerans (ebeveyn süresinin %5'i, ≥1 µs) içinde aşan
//     çocuk SAAT KAYMASI sayılır ve ebeveyn sınırına kırpılır; toleransı aşan
//     çocuk async'tir (ebeveyn onu beklemedi) — criticalPath.ts'in "çocuk
//     ebeveynden geç biterse bloklamaz" kuralıyla aynı ruh.
//   - Ebeveynden önce başlayan çocuk başlangıcı ebeveyn başlangıcına kırpılır.
// Özyineleme YOK (10k derinlikte yığın taşmasın): her çocuk görevi yalnız
// kendi imlecini taşır, ebeveynin sonucu çocuğun sonucuna bağlı değil →
// düz iş kuyruğu. O(N log N) (çocuk sıralaması).

export interface SegSpan {
  spanId: string;
  parentId: string;
  startTime: number; // ns
  endTime: number;   // ns
}

/** spanId → kritik kesitler [başlangıç, bitiş] ns (artan sırada). */
export type CriticalSegments = Map<string, Array<[number, number]>>;

const SKEW_FRAC = 0.05;
const SKEW_MIN_NS = 1_000;

export function computeCriticalSegments(spans: readonly SegSpan[]): CriticalSegments {
  const out: CriticalSegments = new Map();
  if (spans.length === 0) return out;
  const byId = new Map<string, SegSpan>();
  for (const s of spans) byId.set(s.spanId, s);
  const kidsOf = new Map<string, SegSpan[]>();
  const roots: SegSpan[] = [];
  for (const s of spans) {
    if (s.parentId && s.parentId !== s.spanId && byId.has(s.parentId)) {
      const l = kidsOf.get(s.parentId);
      if (l) l.push(s); else kidsOf.set(s.parentId, [s]);
    } else roots.push(s);
  }
  if (roots.length === 0) return out;
  // Kök seçimi criticalPath.ts ile aynı: en uzun, eşitlikte en erken.
  roots.sort((a, b) => (b.endTime - b.startTime) - (a.endTime - a.startTime) || a.startTime - b.startTime);

  const push = (id: string, a: number, b: number) => {
    if (b <= a) return;
    const l = out.get(id);
    if (l) l.push([a, b]); else out.set(id, [[a, b]]);
  };
  const seen = new Set<string>();
  // İş: (span, kırpılmış başlangıç, imleç = kırpılmış bitiş).
  const work: Array<[SegSpan, number, number]> = [[roots[0], roots[0].startTime, roots[0].endTime]];
  while (work.length > 0) {
    const [s, sStart, sEnd] = work.pop()!;
    if (seen.has(s.spanId)) continue; // döngülü / bozuk veri koruması
    seen.add(s.spanId);
    const tol = Math.max(SKEW_MIN_NS, (sEnd - sStart) * SKEW_FRAC);
    const kids: Array<[SegSpan, number, number]> = [];
    for (const k of kidsOf.get(s.spanId) ?? []) {
      if (k.startTime >= sEnd) continue;           // ebeveyn bittikten sonra: async
      if (k.endTime > sEnd + tol) continue;        // ebeveyni beklemeden aştı: async
      const ks = Math.max(k.startTime, sStart);
      const ke = Math.min(k.endTime, sEnd);
      if (ke <= ks) continue;
      kids.push([k, ks, ke]);
    }
    kids.sort((a, b) => b[2] - a[2] || a[1] - b[1]);
    let cursor = sEnd;
    for (const kid of kids) {
      if (cursor <= sStart) break;
      const [, ks, ke] = kid;
      if (ke > cursor) continue; // seçilmiş daha geç bir kardeşle örtüşüyor
      push(s.spanId, ke, cursor);
      work.push(kid);
      cursor = ks;
    }
    push(s.spanId, sStart, cursor);
  }
  for (const l of out.values()) l.sort((a, b) => a[0] - b[0]);
  return out;
}

/** Wire biçimi (worker ↔ ana iş parçacığı): düz diziler, Map yapılandırılmış klonla da geçer ama düz dizi ucuz. */
export interface SegWire { ids: string[]; seg: Float64Array; off: Uint32Array }

export function segmentsToWire(m: CriticalSegments): SegWire {
  const ids: string[] = [];
  let n = 0;
  for (const l of m.values()) n += l.length;
  const seg = new Float64Array(n * 2);
  const off = new Uint32Array(m.size + 1);
  let i = 0, j = 0;
  for (const [id, l] of m) {
    ids.push(id); off[j++] = i / 2;
    for (const [a, b] of l) { seg[i++] = a; seg[i++] = b; }
  }
  off[j] = i / 2;
  return { ids, seg, off };
}

export function segmentsFromWire(w: SegWire): CriticalSegments {
  const m: CriticalSegments = new Map();
  for (let j = 0; j < w.ids.length; j++) {
    const l: Array<[number, number]> = [];
    for (let k = w.off[j]; k < w.off[j + 1]; k++) l.push([w.seg[2 * k], w.seg[2 * k + 1]]);
    m.set(w.ids[j], l);
  }
  return m;
}
