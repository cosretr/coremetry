// exchangeView.ts — v0.10.1153 (operatör, prod: "/ai her CoSRE etkileşimini
// göstermiyor"). /ai "CoSRE etkileşimleri" panelinin SAF görünüm modeli:
// kademe etiketi, LLM yok işareti, model/token/geri bildirim metinleri.

import type { AIExchange } from '@/lib/types';

/** Sunucunun kademe adları (Go: aisurface.Tier*) → ekran etiketi. */
const TIER_LABELS: Record<string, string> = {
  scope: 'Kapsam / komut',
  wiki: 'Wiki',
  guided: 'Guided',
  drawer: 'Çekmece',
  rag: 'Doküman (RAG)',
  intent: 'Niyet sınıflandırıcı',
  loop: 'Serbest döngü',
  other: 'Diğer',
};

/** "LLM yok" — model çağrısı olmadan (deterministik) cevaplanan tur. */
export const NO_LLM_LABEL = 'LLM yok';

export function exchangeTierLabel(ex: Pick<AIExchange, 'tier' | 'route'>): string {
  const base = TIER_LABELS[ex.tier] ?? ex.tier ?? '—';
  return ex.route ? `${base} · ${ex.route}` : base;
}

export function exchangeUsedLLM(ex: Pick<AIExchange, 'llmCalls'>): boolean {
  return ex.llmCalls > 0;
}

/** Model sütunu: LLM'siz turda işaret; aksi hâlde turun modelleri (+ profil). */
export function exchangeModelLabel(ex: Pick<AIExchange, 'llmCalls' | 'models' | 'profileId'>): string {
  if (!exchangeUsedLLM(ex)) return NO_LLM_LABEL;
  const models = (ex.models ?? []).filter(Boolean).join(', ') || '—';
  return ex.profileId ? `${models} (${ex.profileId})` : models;
}

/** Token sütunu: LLM'siz turda "—" (0/0 değil — ölçülecek çağrı yok). */
export function exchangeTokensLabel(ex: Pick<AIExchange, 'llmCalls' | 'inputTokens' | 'outputTokens'>): string {
  if (!exchangeUsedLLM(ex)) return '—';
  return `${ex.inputTokens} / ${ex.outputTokens}`;
}

export function exchangeFeedbackLabel(ex: Pick<AIExchange, 'feedback'>): string {
  if (ex.feedback === 1) return '👍';
  if (ex.feedback === -1) return '👎';
  return '—';
}

/** Model çağrısı sayısı metni (alt çağrı tablosunun başlığı). */
export function exchangeCallsSummary(ex: Pick<AIExchange, 'llmCalls' | 'calls'>): string {
  const markers = (ex.calls ?? []).filter(c => !c.llm).length;
  if (!exchangeUsedLLM(ex)) return markers ? `${NO_LLM_LABEL} · ${markers} işaret satırı` : NO_LLM_LABEL;
  return markers ? `${ex.llmCalls} model çağrısı · ${markers} işaret satırı` : `${ex.llmCalls} model çağrısı`;
}
