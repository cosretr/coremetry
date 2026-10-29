// traceWaterfall.tree.ts — v0.8.537.
//
// Pure tree helpers behind TraceWaterfall's Alt+click subtree toggle.
// They live outside the .tsx on purpose: exporting non-components from a
// component file trips react-refresh/only-export-components, and keeping
// them here lets vitest import them without pulling React in.
import type { SpanRow } from '@/lib/types';

// Every REAL span id at or under rootId, walking the parent→children map.
// Iterative by design: the render DFS recurses, and a deep trace
// (thousands of nested spans) would blow the stack here for nothing.
// An id the map has never seen yields just itself.
export function collectSubtreeIds(children: Map<string, SpanRow[]>, rootId: string): string[] {
  const out: string[] = [];
  const stack = [rootId];
  while (stack.length) {
    const id = stack.pop()!;
    out.push(id);
    for (const k of children.get(id) ?? []) stack.push(k.spanId);
  }
  return out;
}

// clusterBadge — should this row show a cluster chip, and with what text?
// (v0.8.549)
//
// The chip marks SERVICE ENTRY, not every span: it answers "which cluster
// is this service running in?" at the row where the trace first enters that
// service. Operator's words: "sadece alt service çağrımlarının ilk
// girişinde cluster badge olsun".
//
//   • root (no parent in the trace) → show; it establishes the baseline;
//   • a span whose service differs from its parent's → show; that row IS
//     the handoff into a new service;
//   • same service as the parent (the internal spans of a call) → hide.
//     A 200-span trace repeating one chip on every row is noise that
//     buries the handoffs it was meant to mark.
//
// Empty/absent cluster always hides — resources without the attribute (old
// data, non-OpenShift installs) must never render "unknown" or a blank
// chip. An ORPHAN (parent id present but the parent span isn't in the
// trace, so parentService is undefined) shows: it is an entry point as far
// as this trace can tell, and claiming otherwise would be a guess.
export function clusterBadge(
  spanCluster: string | undefined,
  spanService: string,
  parentService: string | undefined,
  hasParent: boolean,
): string | undefined {
  if (!spanCluster) return undefined;
  if (!hasParent) return spanCluster;
  return spanService === parentService ? undefined : spanCluster;
}

// "group:<parentSpanId>:<i>:<key>" → "<parentSpanId>"; null for a real
// span id. Synthetic group rows encode their real parent, which is what
// lets Alt+expand clear the group rows inside a subtree without a second
// index. The cut is the FIRST separator after the prefix: the trailing
// key carries a display name that routinely contains ':'. Safe because
// OTel span ids are 16 hex chars and never contain ':'.
export function groupParentOf(id: string): string | null {
  if (!id.startsWith('group:')) return null;
  const rest = id.slice('group:'.length);
  const cut = rest.indexOf(':');
  return cut < 0 ? null : rest.slice(0, cut);
}

// ── v0.10.1565 — kapalı ağaçta hedefe git ──────────────────────────────────
// Operatör: "Spanler kapalı olduğunda seçtiğim servis focuslanmıyor." Servis
// odağı / ?span= derin bağlantısı hedefi yalnız GÖRÜNEN satırlarda arıyordu;
// ataları kapalı span satırda yok → sessizce kaydırma yok.

/** Hedefin ataları, yakından uzağa (döngü korumalı). */
export function ancestorChain(byId: Map<string, SpanRow>, id: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>([id]);
  let p = byId.get(id)?.parentSpanId;
  while (p && byId.has(p) && !seen.has(p)) {
    out.push(p);
    seen.add(p);
    p = byId.get(p)?.parentSpanId;
  }
  return out;
}

/**
 * Hedefe giden yolu açan yeni küme: zincirdeki kapalı span'ler ve ebeveyni
 * zincirde olan kapalı ×N grup satırları açılır. Değişiklik yoksa null.
 */
export function expandForReveal(collapsed: ReadonlySet<string>, chain: readonly string[]): Set<string> | null {
  const anc = new Set(chain);
  let changed = false;
  const next = new Set(collapsed);
  for (const c of collapsed) {
    const gp = groupParentOf(c);
    if (anc.has(c) || (gp !== null && anc.has(gp))) {
      next.delete(c);
      changed = true;
    }
  }
  return changed ? next : null;
}

/**
 * İlk görünen aday satırın indeksi (hedef önce, sonra atalar yakından uzağa);
 * span kimliği ya da ×N grup üyeliği. Yoksa -1. exact = hedefin kendisi.
 */
export function findRevealRow(
  rows: readonly { span: { spanId: string }; memberIds?: string[] }[],
  candidates: readonly string[],
): { idx: number; exact: boolean } {
  for (let k = 0; k < candidates.length; k++) {
    const id = candidates[k];
    const idx = rows.findIndex(r => r.span.spanId === id || r.memberIds?.includes(id));
    if (idx >= 0) return { idx, exact: k === 0 };
  }
  return { idx: -1, exact: false };
}
