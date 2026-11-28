// flowParts — v0.10.2491 (CoSRE sohbet dilim 1): inline akışın parça sırası
// (SAF). data-narration bir grup açar; ardından gelen araç (dynamic-tool) ve
// sözleşme (data-step) parçaları o gruptadır. Arşiv kaydı (ChatMessage.flow)
// ve geri yükleme aynı kuralla (components/cosre/evidence/flowView.ts çizer).
import { parseDataNarration } from './protocol';
import type { CosreUIMessage } from './resetReducer';
import type { CosreFlowGroup } from '@/lib/types';
import { toolPurpose } from './toolPurpose';

type Part = CosreUIMessage['parts'][number];
type Rec = Record<string, unknown>;
const rec = (v: unknown): Rec => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Rec) : {});

export interface Slot { id: string; text?: string; ids: string[]; purposes: Map<string, string> }

/** Parçalar → sıralı gruplar (adım kimlikleri + amaç satırları). */
export function slotsOf(parts: readonly Part[]): { context?: string; slots: Slot[] } | null {
  let context: string | undefined;
  let seen = false;
  const slots: Slot[] = [{ id: 'g0', ids: [], purposes: new Map() }];
  const placed = new Set<string>();
  for (const p of parts) {
    if (p.type === 'data-narration') {
      const n = parseDataNarration(p as { type: string; data?: unknown });
      if (!n) continue;
      seen = true;
      if (n.context && context === undefined) context = n.context;
      slots.push({ id: (p as { id?: string }).id ?? `n${slots.length}`, text: n.text, ids: [], purposes: new Map() });
      continue;
    }
    const cur = slots[slots.length - 1];
    if (p.type === 'dynamic-tool') {
      const t = p as unknown as { toolCallId: string; toolName: string; input?: unknown };
      if (placed.has(t.toolCallId)) continue;
      placed.add(t.toolCallId);
      cur.ids.push(t.toolCallId);
      const input = rec(t.input);
      const purpose = typeof input.purpose === 'string' && input.purpose ? input.purpose : toolPurpose(t.toolName, input.args);
      cur.purposes.set(t.toolCallId, purpose);
    } else if (p.type === 'data-step') {
      const id = (p as { id?: string }).id;
      if (id && !placed.has(id)) { placed.add(id); cur.ids.push(id); }
    }
  }
  return seen ? { context, slots } : null;
}

/** SAF: arşiv kaydı (ChatMessage.flow) — yalnız anlatım varsa. */
export function flowArchive(parts: readonly Part[]): CosreFlowGroup[] | undefined {
  const s = slotsOf(parts);
  if (!s) return undefined;
  const tools = new Map<string, string>();
  for (const p of parts) {
    if (p.type === 'dynamic-tool') {
      const t = p as unknown as { toolCallId: string; toolName: string };
      tools.set(t.toolCallId, t.toolName);
    }
  }
  const out: CosreFlowGroup[] = [];
  let first = true;
  for (const sl of s.slots) {
    const steps = sl.ids.flatMap(id => {
      const tool = tools.get(id);
      if (!tool) return [];
      const purpose = sl.purposes.get(id);
      return [{ id, tool, ...(purpose ? { purpose } : {}) }];
    });
    if (!sl.text && steps.length === 0) continue;
    const ctx = sl.text && first && s.context ? s.context : undefined;
    if (sl.text) first = false;
    out.push({ ...(sl.text ? { text: sl.text } : {}), ...(ctx ? { context: ctx } : {}), ...(steps.length ? { steps } : {}) });
  }
  return out.length ? out : undefined;
}

/** SAF: arşivden geri — anlatım + araç parçaları aynı sırayla (sonuç toolResults'tan, aynı s<i>). */
export function flowParts(flow: readonly CosreFlowGroup[] | undefined): Part[] {
  const out: Part[] = [];
  let k = 0;
  for (const g of flow ?? []) {
    if (g.text) {
      k++;
      out.push({ type: 'data-narration', id: `n${k}`, data: { text: g.text, ...(g.context ? { context: g.context } : {}) } } as Part);
    }
    for (const st of g.steps ?? []) {
      out.push({
        type: 'dynamic-tool', toolCallId: st.id, toolName: st.tool, state: 'output-available',
        input: st.purpose ? { purpose: st.purpose } : {}, output: {},
      } as unknown as Part);
    }
  }
  return out;
}
