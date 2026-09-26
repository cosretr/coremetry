// Critical-path analysis on a trace's span DAG. The "critical
// path" is the chain of dependent (nested) spans that spans the
// trace's wall-clock latency — the spans on this chain ARE the
// operations the operator should optimise to make the request
// faster. Nested durations are never summed (v0.10.944): the
// chain's extent is the root's wall time.
//
// Definition (synchronous critical path):
//   For each span S, the candidate's contribution =
//     S.duration + max(critical(child) for each blocking
//                      child whose start ≥ S.start)
//   The trace's critical path is the chain returned by
//   walking from the root via the child that maximises this
//   score at each level. The score only RANKS candidate
//   chains; it is not a duration and is never displayed
//   (v0.10.944 — see CriticalPath below).
//
// Performance: pure O(N) DFS with one Map of N parent→children
// entries. At 10k spans (a heavy distributed trace) this runs
// in < 5ms in JS — no need for Web Workers. The result is a
// Set<spanId> the caller marks on the waterfall rows.
//
// Async-vs-sync nuance: a parent that fires N async children
// returns when its OWN code finishes; the children continue.
// We approximate "synchronous critical path" by treating any
// child whose end-time ≤ parent's end-time as blocking.
// Children that outlast their parent (fire-and-forget) are
// excluded from the chain. This matches what Datadog/
// Honeycomb call "trace's critical path".

export interface CriticalPathSpan {
  spanId: string;
  parentId: string;
  // Both as nanoseconds (matches the existing trace API).
  startTime: number;
  duration: number;
  // For UI display — not used by the algorithm.
  name?: string;
  serviceName?: string;
}

// v0.10.944 — `totalNs` (zincirdeki sürelerin TOPLAMI) KALDIRILDI. Zincir
// iç içe span'lerden oluşur: çocuk, ebeveyninin süresinin İÇİNDEDİR; ikisini
// toplamak aynı duvar saatini iki kez saymaktır (3 katlı bir zincir 1 s'lik
// bir trace için "2.4 s kritik yol" gösteriyordu). Yerine iki dürüst ölçü:
//   - rootWallNs: zincirin duvar-saati kapsamı = kök span'in süresi;
//   - onPathSelfNs: span başına YOL-ÜSTÜ öz süre = kendi süresi − zincirdeki
//     çocuğunun ona düşen kısmı. İç içe zincirde toplamı rootWallNs'e eşittir
//     (teleskop), yani hiçbir an iki kez sayılmaz.
// Seçim kuralı DEĞİŞMEDİ (backend ikizi chstore/tracetree.go ile aynı kural):
// `score` yalnız hangi çocuğun zincire gireceğini seçen iç sıralama puanı —
// bir süre DEĞİL, hiçbir yerde gösterilmez.
export interface CriticalPath {
  ids: Set<string>;        // span IDs on the critical path
  order: string[];         // zincir sırası: kök → yaprak
  spanCount: number;       // zincirdeki span sayısı
  rootWallNs: number;      // kök span'in süresi (zincirin duvar-saati kapsamı)
  onPathSelfNs: Map<string, number>; // span başına yol-üstü öz süre (ns)
  rootId: string | null;   // first id in the chain
  leafId: string | null;   // last id in the chain
}

const EMPTY = (): CriticalPath => ({
  ids: new Set(), order: [], spanCount: 0, rootWallNs: 0, onPathSelfNs: new Map(), rootId: null, leafId: null,
});

export function computeCriticalPath(spans: CriticalPathSpan[]): CriticalPath {
  if (spans.length === 0) return EMPTY();

  // Build child index. Each span's start ≥ its parent's start
  // by definition; we don't validate that here.
  const byId = new Map<string, CriticalPathSpan>();
  const childrenOf = new Map<string, CriticalPathSpan[]>();
  for (const s of spans) {
    byId.set(s.spanId, s);
    if (s.parentId) {
      const list = childrenOf.get(s.parentId);
      if (list) list.push(s);
      else childrenOf.set(s.parentId, [s]);
    }
  }

  // Find the root span(s) — entries whose parentId isn't in
  // byId (or is empty). Most well-behaved traces have exactly
  // one; multi-root happens with broken or ingest-merged
  // traces. Pick the longest-running root as the canonical.
  const roots: CriticalPathSpan[] = [];
  for (const s of spans) {
    if (!s.parentId || !byId.has(s.parentId)) roots.push(s);
  }
  if (roots.length === 0) {
    // Defensive: cyclic data or all-orphan spans. Return empty.
    return EMPTY();
  }
  roots.sort((a, b) => b.duration - a.duration);
  const root = roots[0];

  // DFS with memoisation. For each node return the best chain
  // from this node through any blocking child. `score` is the
  // selection key only (same rule as the Go twin) — never a
  // duration shown to the operator (v0.10.944).
  type Chain = { ids: string[]; score: number };
  const memo = new Map<string, Chain>();

  function chainFrom(s: CriticalPathSpan): Chain {
    const cached = memo.get(s.spanId);
    if (cached) return cached;
    const kids = childrenOf.get(s.spanId);
    let bestChild: Chain | null = null;
    if (kids) {
      const parentEnd = s.startTime + s.duration;
      for (const k of kids) {
        const kEnd = k.startTime + k.duration;
        // Blocking iff the child finishes before the parent.
        // Fire-and-forget children outlast the parent — they
        // never appear on the parent's critical path.
        if (kEnd > parentEnd) continue;
        const chain = chainFrom(k);
        if (!bestChild || chain.score > bestChild.score) {
          bestChild = chain;
        }
      }
    }
    const chain: Chain = bestChild
      ? { ids: [s.spanId, ...bestChild.ids], score: s.duration + bestChild.score }
      : { ids: [s.spanId],                   score: s.duration };
    memo.set(s.spanId, chain);
    return chain;
  }

  const top = chainFrom(root);
  // Yol-üstü öz süre: her halkanın süresinden, zincirdeki çocuğunun o
  // halkayla ÖRTÜŞEN kısmı düşer (saat kayması çocuğu ebeveynin biraz
  // dışına taşırsa yalnız örtüşen pay düşülür, asla negatif olmaz).
  const onPathSelfNs = new Map<string, number>();
  for (let i = 0; i < top.ids.length; i++) {
    const s = byId.get(top.ids[i]);
    if (!s) continue;
    const next = i + 1 < top.ids.length ? byId.get(top.ids[i + 1]) : undefined;
    let overlap = 0;
    if (next) {
      const a = Math.max(s.startTime, next.startTime);
      const b = Math.min(s.startTime + s.duration, next.startTime + next.duration);
      overlap = Math.max(0, b - a);
    }
    onPathSelfNs.set(s.spanId, Math.max(0, s.duration - overlap));
  }
  return {
    ids: new Set(top.ids),
    order: top.ids,
    spanCount: top.ids.length,
    rootWallNs: root.duration,
    onPathSelfNs,
    rootId: top.ids[0] ?? null,
    leafId: top.ids[top.ids.length - 1] ?? null,
  };
}
