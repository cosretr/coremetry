// approval — agent-core S5 (docs/cosre/agent-core-ideas.md §4.1 adım 5): v2
// asistan mesajındaki onay bekleyen araç çağrılarının SAF görünümü + devam
// isteğinin `approvals[]` kalemi. Kart (components/cosre/conversation/
// ApprovalCard) ve çekirdek (useCosreChat answerApproval) bunu kullanır.
//
// Eşleme: `data-approval{toolCallId:"s<i>"}` ↔ aynı toolCallId'li dynamic-tool
// parçası. Parça input-available iken karar bekler; istemci cevabı
// addToolOutput ile `{approved}` yazar (output-available), devam akışı aynı
// s<i>'ye sunucu sonucunu yazar.
import type { CosreApproval, CosreApprovalAnswer } from '@/lib/types';
import { parseDataApproval } from './protocol';
import type { CosreUIMessage } from './resetReducer';

/** Kartın durumu: karar bekliyor | onaylandı | reddedildi. */
export type ApprovalState = 'pending' | 'approved' | 'denied';

export interface ApprovalView {
  approval: CosreApproval;
  /** Çalışacak argüman (tool-input-available `input.args`). */
  args: unknown;
  state: ApprovalState;
  /** Mesajdaki en büyük s<i> (devam kimlik tabanı). */
  stepBase: number;
}

type Part = CosreUIMessage['parts'][number];

const STEP_RE = /^s([1-9]\d{0,4})$/;

function stepNum(id: unknown): number {
  const m = typeof id === 'string' ? STEP_RE.exec(id) : null;
  return m ? Number(m[1]) : 0;
}

/** Mesajdaki en büyük s<i> (araç parçası, data-step, data-tool-result). */
export function maxStepId(parts: readonly Part[]): number {
  let max = 0;
  for (const p of parts) {
    const id = p.type === 'dynamic-tool' ? p.toolCallId : 'id' in p ? (p as { id?: unknown }).id : undefined;
    max = Math.max(max, stepNum(id));
  }
  return max;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** Mesajın onay kartları (data-approval sırasıyla); eşli araç parçası yoksa yok. */
export function approvalViews(m: CosreUIMessage): ApprovalView[] {
  if (m.role !== 'assistant') return [];
  const base = maxStepId(m.parts);
  const out: ApprovalView[] = [];
  for (const p of m.parts) {
    if (p.type !== 'data-approval') continue;
    const a = parseDataApproval(p as { type: string; id?: unknown; data?: unknown });
    if (!a) continue;
    const tool = m.parts.find(q => q.type === 'dynamic-tool' && q.toolCallId === a.toolCallId);
    if (!tool || tool.type !== 'dynamic-tool') continue;
    const input = isRecord(tool.input) ? tool.input : {};
    let state: ApprovalState = 'pending';
    if (tool.state === 'output-available' || tool.state === 'output-error') {
      // İstemcinin yazdığı {approved} ya da devam akışının sunucu sonucu:
      // skipped = yürütülmedi (ret / süre / geçersiz), değilse yürütüldü.
      const o = isRecord(tool.output) ? tool.output : {};
      const approved = typeof o.approved === 'boolean' ? o.approved : o.skipped !== true && tool.state === 'output-available';
      state = approved ? 'approved' : 'denied';
    }
    out.push({ approval: a, args: input.args ?? {}, state, stepBase: base });
  }
  return out;
}

/** Devam isteğinin `approvals[]` kalemi. */
export function approvalAnswer(v: ApprovalView, approved: boolean): CosreApprovalAnswer {
  return {
    stepId: v.approval.toolCallId, tool: v.approval.tool, args: v.args, token: v.approval.token, approved,
    ...(v.approval.exchangeId ? { exchangeId: v.approval.exchangeId } : {}), stepBase: v.stepBase,
  };
}

/** Süresi geçti mi (zaman aşımı = ret; kart düğmeleri kapanır). */
export function approvalExpired(a: CosreApproval, nowMs: number): boolean {
  return nowMs >= a.expiresAt;
}
