// useExplainContext — v0.10.1672 (CoSRE v2 Faz 2b B7). Açıklama turunun TAKİP
// bağlamı CopilotChat.tsx'ten (v0.10.1400) SATIR SATIR çıkarıldı; v1 çekmecesi
// ve v2 Sheet'i AYNI kuralı kullanır:
//   • konuşmadaki son açıklama turu (explainTurn.activeExplainFrom) takip
//     sorularına context.explain (künye + metin + kanıt) ve context.subject
//     (`?ai=` kodeği; sunucu ham kanıtı yeniden kurar) ekler — trace türünde
//     ayrıca trace kimliği + sayfa bağlamı + trace penceresi.
//   • bağlam şeridinde "✨ trace …" öğesi; ✕ o açıklama turunu çıkarır (yeni
//     açıklama turu yeniden bağlar). Çıkarmalar konuşmaya bağlı: başka
//     konuşmaya geçince sıfır (v0.10.1274).
//   • canlı trace bağlamı (sayfa yayınlıyorsa) → TraceContextStrip.
import { useEffect, useMemo, useRef, useState } from 'react';
import type { ChatTurn } from '@/lib/types';
import { traceChatWindow, traceContextToPage, useTraceAiContext, type TraceAiContext } from '@/lib/traceAiContext';
import { applyContextExclusions, shouldResetExclusions, type ChatRequestContext, type ContextBarItem, type ContextKey } from './chatContextScope';
import { activeExplainFrom, explainContextLabel, explainFollowupContext } from './explainTurn';

export interface ExplainContextInput {
  turns: readonly ChatTurn[];
  ctxExcluded: ReadonlySet<ContextKey>;
  setCtxExcluded: (fn: (prev: ReadonlySet<ContextKey>) => ReadonlySet<ContextKey>) => void;
  baseCtxItems: ContextBarItem[];
  conversationId: string | null;
}

export function useExplainContext({ turns, ctxExcluded, setCtxExcluded, baseCtxItems, conversationId }: ExplainContextInput) {
  const [explainOff, setExplainOff] = useState('');
  const traceCtxRef = useRef<TraceAiContext | null>(null);
  // Gönderim ANINDA turlardan çözülür (bayat closure yok: güncel değerler ref'te).
  const live = useRef({ explainOff, ctxExcluded });
  live.current = { explainOff, ctxExcluded };
  const resolve = (ts: readonly ChatTurn[]): Partial<ChatRequestContext> & { explain?: string; subject?: string } => {
    const a = activeExplainFrom(ts);
    if (!a || a.key === live.current.explainOff) return {};
    const fc = explainFollowupContext(a);
    if (a.meta.kind !== 'trace') return fc;
    const tctx = traceCtxRef.current?.traceId === a.meta.id ? traceCtxRef.current : null;
    const win = tctx ? traceChatWindow(tctx, Date.now()) : null;
    const scoped = applyContextExclusions({
      trace: a.meta.id,
      page: tctx ? traceContextToPage(tctx) : { page: 'trace', path: '/trace', traceId: a.meta.id },
      ...(tctx?.env ? { env: tctx.env } : {}),
      ...(win ? { rangeS: win.rangeS, toMs: win.toMs } : {}),
    }, live.current.ctxExcluded);
    const out: Partial<ChatRequestContext> & { explain: string; subject: string } = { ...fc, trace: scoped.trace, page: scoped.page };
    if (tctx?.env && scoped.env) out.env = scoped.env;
    if (win && scoped.rangeS) { out.rangeS = scoped.rangeS; out.toMs = scoped.toMs; }
    return out;
  };
  const activeAll = useMemo(() => activeExplainFrom(turns), [turns]);
  const activeExplain = activeAll && activeAll.key !== explainOff ? activeAll : null;
  const liveTraceCtx = useTraceAiContext(activeExplain?.meta.kind === 'trace' ? activeExplain.meta.id : null);
  traceCtxRef.current = liveTraceCtx;
  const ctxItems = useMemo(() => (activeExplain
    ? [{ key: 'explain' as const, label: explainContextLabel(activeExplain.meta),
        title: 'Takip soruları bu açıklamaya bağlanır (aynı kanıt) — ✕ ile bu konuşmadan çıkar' }, ...baseCtxItems]
    : baseCtxItems), [activeExplain, baseCtxItems]);
  const removeCtx = (k: ContextKey) => (k === 'explain'
    ? setExplainOff(activeExplain?.key ?? '')
    : setCtxExcluded(prev => new Set(prev).add(k)));
  const prevConvRef = useRef(conversationId);
  useEffect(() => {
    if (shouldResetExclusions(prevConvRef.current, conversationId)) { setCtxExcluded(() => new Set()); setExplainOff(''); }
    prevConvRef.current = conversationId;
  }, [conversationId, setCtxExcluded]);
  return { resolve, activeExplain, liveTraceCtx, ctxItems, removeCtx };
}
