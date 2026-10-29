// exchangeView.ts — v0.10.1153 (operatör, prod: "/ai her CoSRE etkileşimini
// göstermiyor"). /ai "CoSRE etkileşimleri" panelinin SAF görünüm modeli:
// kademe etiketi, LLM yok işareti, model/token/geri bildirim metinleri.

import type { AIExchange, AIExchangeFeedbackFilter } from '@/lib/types';
import { feedbackReasonLabel } from '@/lib/aiFeedbackReasons';

/** Sunucunun kademe adları (Go: aisurface.Tier*) → ekran etiketi. */
const TIER_LABELS: Record<string, string> = {
  scope: 'Kapsam / komut',
  wiki: 'Wiki',
  guided: 'Guided',
  drawer: 'Çekmece',
  rag: 'Doküman (RAG)',
  intent: 'Niyet sınıflandırıcı',
  'doc-rows': 'Doküman satırı',
  operation: 'Operasyon seçimi',
  memory: 'Hafıza (kurallar)', // v0.10.1289 — "bunu hatırla" (chat_memory.go)
  explain: 'Açıklama turu', // v0.10.1400 — trace/exception ✨ sohbet içinde (chat_explain_turn.go)
  'code-sticky': 'Kod kilidi (yerel profil yok)', // v0.10.1465 — kod okunmuş konuşma, LLM'siz kapalı cevap (chat_code_sticky.go)
  clarify: 'Netleştirme sorusu', // v0.10.1571 — düz metin "Coremetry mi, wiki mi?" (chat_clarify.go)
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

export function exchangeFeedbackLabel(ex: Pick<AIExchange, 'feedback' | 'feedbackReason'>): string {
  if (ex.feedback === 1) return '👍';
  if (ex.feedback === -1) {
    // v0.10.1280 — hazır neden oyun yanında ("👎 eksik").
    const r = feedbackReasonLabel(ex.feedbackReason);
    return r ? `👎 ${r}` : '👎';
  }
  return '—';
}

// ── v0.10.1280 (CoSRE adım 8) — 👍/👎 süzgeci, sayımlar, arşiv bağı ──────

/** Panelin süzgeç seçenekleri (URL `?fb=`; '' = hepsi). */
export const EXCHANGE_FEEDBACK_FILTERS: ReadonlyArray<{ value: AIExchangeFeedbackFilter; label: string; title: string }> = [
  { value: '', label: 'Tümü', title: 'Bütün turlar' },
  { value: 'down', label: '👎', title: 'Yalnız 👎 alan turlar (inceleme kuyruğu)' },
  { value: 'up', label: '👍', title: 'Yalnız 👍 alan turlar' },
  { value: 'rated', label: 'Oylanan', title: 'Oylanmış her tur' },
];

/** URL değeri → süzgeç; kapalı küme dışı '' (sunucu da aynı kuralı uygular). */
export function parseExchangeFeedbackFilter(v: string | null | undefined): AIExchangeFeedbackFilter {
  return EXCHANGE_FEEDBACK_FILTERS.some(f => f.value === v) ? (v as AIExchangeFeedbackFilter) : '';
}

/** /api/ai/exchanges isteğinin TÜM girdileri — sorgu anahtarı da istek de
 *  bundan kurulur (anahtar bir girdiyi unutursa süzgeç değişince eski liste
 *  önbellekten gelir: CLAUDE.md "cache key hashes ALL inputs"). */
export interface AIExchangesQueryParams {
  from: number;
  to: number;
  limit: number;
  feedback: AIExchangeFeedbackFilter;
}

/** Sabit sıra — yeni girdi buraya eklenmeden anahtar onu görmez; test
 *  AIExchangesQueryParams'ın her alanının burada olduğunu pinler. */
export const AI_EXCHANGES_QUERY_FIELDS = ['from', 'to', 'limit', 'feedback'] as const satisfies ReadonlyArray<keyof AIExchangesQueryParams>;

export function aiExchangesQueryKey(p: AIExchangesQueryParams): readonly unknown[] {
  return ['ai-exchanges', ...AI_EXCHANGES_QUERY_FIELDS.map(k => p[k])];
}

/** İsteğin sorgu parametreleri: süzgeç yoksa alan HİÇ gönderilmez (eski gövde). */
export function aiExchangesRequestParams(p: AIExchangesQueryParams): { from: number; to: number; limit: number; feedback?: AIExchangeFeedbackFilter } {
  const { feedback, ...rest } = p;
  return feedback ? { ...rest, feedback } : rest;
}

/** Görünen satırların 👍/👎 sayımı. */
export function exchangeFeedbackCounts(rows: ReadonlyArray<Pick<AIExchange, 'feedback'>>): { up: number; down: number } {
  let up = 0, down = 0;
  for (const r of rows) {
    if (r.feedback === 1) up++;
    else if (r.feedback === -1) down++;
  }
  return { up, down };
}

/** Sonuç rozeti: ok / hata / iptal (istemci cevap gelmeden gitti). */
export function exchangeStatusBadge(ex: Pick<AIExchange, 'status'>): { label: string; cls: string } {
  if (ex.status === 'ok') return { label: 'ok', cls: 'b-gray' };
  if (ex.status === 'aborted') return { label: 'iptal', cls: 'b-warn' };
  return { label: 'hata', cls: 'b-err' };
}

/** Konuşma arşivi bağı (/cosre?chat=<id>); kimlik yoksa null. */
export function exchangeConversationHref(ex: Pick<AIExchange, 'conversationId'>): string | null {
  return ex.conversationId ? `/cosre?chat=${encodeURIComponent(ex.conversationId)}` : null;
}

/** Model çağrısı sayısı metni (alt çağrı tablosunun başlığı). */
export function exchangeCallsSummary(ex: Pick<AIExchange, 'llmCalls' | 'calls'>): string {
  const markers = (ex.calls ?? []).filter(c => !c.llm).length;
  if (!exchangeUsedLLM(ex)) return markers ? `${NO_LLM_LABEL} · ${markers} işaret satırı` : NO_LLM_LABEL;
  return markers ? `${ex.llmCalls} model çağrısı · ${markers} işaret satırı` : `${ex.llmCalls} model çağrısı`;
}
