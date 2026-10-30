// toChatTurn — v0.10.1656 (CoSRE v2 Faz 2b B3, plan §3 / §10); B4 (v0.10.1659):
// exchangeId / turnId + data-evidence-summary → evidence. v2 asistan
// mesajını (AI SDK UIMessage parçaları) v1'in ChatTurn şekline çevirir; v2
// cevabı v1 ChatBubble'ıyla çizilir — operatör aynı balonu görür.
//
// Neden ters çeviri: Go çeviricisi (internal/cosre/uistream/translator.go)
// v1 olaylarını parçaya bire bir taşır — `step` → data-step (etiket) ya da
// dynamic-tool (araç, toolCallId = "s<i>"), `step-result` → tool-output-
// available `output` (= step-result eksi i/tool). Burada geri katlanır:
//   • steps[]        : parça sırasıyla etiket (data-step.label) / toolName
//   • stepDetails[]  : i = toolCallId / data-step id'den; output alanları
//                      v1 step-result alanlarıyla aynı adlarla
//   • budget         : data-budget (tek parça, aynı id yerine geçer)
// Saf: React yok; testler fixture'ı gerçek SDK'dan geçirip bunu çağırır.
import type { ChatQueryEvidence, ChatStepDetail, ChatStepSourceState, ChatTurn, CosreStepBudget } from '@/lib/types';
import type { CosreUIMessage } from './resetReducer';

type Part = CosreUIMessage['parts'][number];

export interface ToChatTurnOpts {
  /** Bu mesaj hâlâ akıyor (son mesaj + submitted/streaming). */
  pending?: boolean;
  /** Akış hatası (yalnız son mesaj, status error). */
  error?: string;
}

type Rec = Record<string, unknown>;
const rec = (v: unknown): Rec => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Rec) : {});
const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined);
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const bool = (v: unknown): boolean | undefined => (typeof v === 'boolean' ? v : undefined);

/** "s12" → 12; biçim dışı → undefined (detay açılmaz, çip düz etiket kalır). */
export function stepIndex(id: unknown): number | undefined {
  const m = typeof id === 'string' ? /^s([1-9]\d*)$/.exec(id) : null;
  return m ? Number(m[1]) : undefined;
}

/** dynamic-tool `output` → v1 step-result alanları. */
function applyOutput(d: ChatStepDetail, out: Rec): ChatStepDetail {
  const skipped = bool(out.skipped) === true;
  return {
    ...d,
    ok: bool(out.ok),
    // v1 çipi «hazır»ı preview varlığından okur; yürütülmeyen çağrının
    // önizlemesi gerekçesi (sunucu `reason`).
    preview: str(out.preview) ?? str(out.reason) ?? '',
    truncated: bool(out.truncated),
    bytes: num(out.bytes),
    href: str(out.href),
    durationMs: num(out.durationMs),
    ...(skipped ? { skipped: true } : {}),
    ...(Array.isArray(out.sources) ? { sources: out.sources as ChatStepSourceState[] } : {}),
    ...(out.evidence ? { evidence: out.evidence as ChatQueryEvidence } : {}),
    ...(Array.isArray(out.evidenceList) ? { evidenceList: out.evidenceList as ChatQueryEvidence[] } : {}),
    ...(out.ctxApplied ? { ctxApplied: out.ctxApplied as ChatStepDetail['ctxApplied'] } : {}),
  };
}

/** data-budget → CosreStepBudget; sayısal alan eksikse null (yalan özet çizilmez). */
export function parseBudget(v: unknown): CosreStepBudget | null {
  const b = rec(v);
  const executed = num(b.executed), maxCalls = num(b.maxCalls), roundsUsed = num(b.roundsUsed), maxRounds = num(b.maxRounds);
  if (executed === undefined || maxCalls === undefined || roundsUsed === undefined || maxRounds === undefined) return null;
  return { executed, maxCalls, roundsUsed, maxRounds, skipped: num(b.skipped) ?? 0, exhausted: bool(b.exhausted) === true };
}

/** Adım çipleri + şeffaflık paneli + bütçe (B3). */
export function stepsOf(parts: readonly Part[]): Pick<ChatTurn, 'steps' | 'stepDetails' | 'budget'> {
  const steps: string[] = [];
  const details: ChatStepDetail[] = [];
  let budget: CosreStepBudget | null = null;
  for (const p of parts) {
    if (p.type === 'data-step') {
      const d = rec(p.data);
      const label = str(d.label) ?? '';
      steps.push(label);
      const i = num(d.i) ?? stepIndex(p.id);
      if (i !== undefined) {
        details.push({ i, tool: '', label, ...(str(d.origin) ? { origin: str(d.origin) } : {}), ...(str(d.phase) ? { phase: str(d.phase) } : {}) });
      }
    } else if (p.type === 'dynamic-tool') {
      steps.push(p.toolName);
      const i = stepIndex(p.toolCallId);
      if (i === undefined) continue;
      const input = rec(p.input);
      let d: ChatStepDetail = { i, tool: p.toolName, ...(str(input.argsText) !== undefined ? { args: str(input.argsText) } : {}) };
      if (p.state === 'output-available') d = applyOutput(d, rec(p.output));
      else if (p.state === 'output-error') d = { ...d, ok: false, preview: p.errorText ?? '' };
      details.push(d);
    } else if (p.type === 'data-budget') {
      budget = parseBudget(p.data) ?? budget;
    }
  }
  return {
    ...(steps.length ? { steps } : {}),
    ...(details.length ? { stepDetails: details } : {}),
    ...(budget ? { budget } : {}),
  };
}

/** v1 kanıt çipinin okuduğu asgari şekil: kaynak + pencere (kısmi kayıt çip çizmez). */
function isQueryEvidence(v: unknown): v is ChatQueryEvidence {
  const e = rec(v);
  const w = rec(e.window);
  return typeof e.tool === 'string' && typeof e.source === 'string' && num(w.fromMs) !== undefined && num(w.toMs) !== undefined;
}

/** v0.10.1659 (B4) — data-evidence-summary → ChatTurn.evidence (v1 answer.evidence). */
export function evidenceOf(parts: readonly Part[]): Pick<ChatTurn, 'evidence'> {
  const p = parts.find(x => x.type === 'data-evidence-summary');
  if (!p || p.type !== 'data-evidence-summary') return {};
  const list = rec(p.data).evidence;
  const ev = Array.isArray(list) ? list.filter(isQueryEvidence) : [];
  return ev.length ? { evidence: ev } : {};
}

/** Görünür parçalar (visibleParts) → v1 asistan turu. */
export function toChatTurn(m: CosreUIMessage, parts: readonly Part[], opts: ToChatTurnOpts = {}): ChatTurn {
  const text = parts.flatMap(p => (p.type === 'text' && p.text !== '' ? [p.text] : [])).join('\n\n');
  const meta = m.metadata;
  return {
    role: 'assistant',
    text,
    ...stepsOf(parts),
    ...(opts.pending ? { pending: true } : {}),
    ...(opts.error ? { error: opts.error } : {}),
    ...(meta?.partial ? { stopped: true } : {}),
    ...(meta?.allowedLinks ? { allowedLinks: meta.allowedLinks } : {}),
    // v0.10.1659 (B4) — sinyal anahtarı + cevabın kanıt çipleri.
    ...(meta?.exchangeId ? { exchangeId: meta.exchangeId } : {}),
    ...(meta?.turnId ? { turnId: meta.turnId } : {}),
    ...evidenceOf(parts),
  };
}
