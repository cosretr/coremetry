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
  /** v0.10.1646 — sunucu sayfası (satır ofseti). */
  offset: number;
  feedback: AIExchangeFeedbackFilter;
  /** v0.10.1646 — soru metninde arama (sunucu, büyük/küçük harf duyarsız). */
  q: string;
  /** v0.10.1646 — kullanıcı (e-posta ya da kimlik, seçiciden). */
  user: string;
  /** v0.10.1646 — kademe (EXCHANGE_TIER_OPTIONS). */
  tier: string;
}

/** Sabit sıra — yeni girdi buraya eklenmeden anahtar onu görmez; test
 *  AIExchangesQueryParams'ın her alanının burada olduğunu pinler. */
export const AI_EXCHANGES_QUERY_FIELDS = ['from', 'to', 'limit', 'offset', 'feedback', 'q', 'user', 'tier'] as const satisfies ReadonlyArray<keyof AIExchangesQueryParams>;

export function aiExchangesQueryKey(p: AIExchangesQueryParams): readonly unknown[] {
  return ['ai-exchanges', ...AI_EXCHANGES_QUERY_FIELDS.map(k => p[k])];
}

type ExchangeFilterReq = { feedback?: AIExchangeFeedbackFilter; q?: string; user?: string; tier?: string };

function filterReq(p: Pick<AIExchangesQueryParams, 'feedback' | 'q' | 'user' | 'tier'>): ExchangeFilterReq {
  const out: ExchangeFilterReq = {};
  if (p.feedback) out.feedback = p.feedback;
  if (p.q) out.q = p.q;
  if (p.user) out.user = p.user;
  if (p.tier) out.tier = p.tier;
  return out;
}

/** İsteğin sorgu parametreleri: boş süzgeç HİÇ gönderilmez. */
export function aiExchangesRequestParams(p: AIExchangesQueryParams): { from: number; to: number; limit: number; offset: number } & ExchangeFilterReq {
  return { from: p.from, to: p.to, limit: p.limit, offset: p.offset, ...filterReq(p) };
}

/** v0.10.1646 — CSV isteği: AYNI süzgeçler, sayfa yok (sunucu tavanı). */
export function aiExchangesCSVParams(p: AIExchangesQueryParams): { from: number; to: number } & ExchangeFilterReq {
  return { from: p.from, to: p.to, ...filterReq(p) };
}

// ── v0.10.1646 — sayfa, arama, kullanıcı/kademe süzgeci: URL durumu ───────

/** Sunucu sayfa boyu (chstore aiExchangesMaxLimit 500 altında). */
export const EXCHANGES_PAGE_SIZE = 100;
/** CSV'nin sert satır tavanı (Go: api.aiExchangesCSVMaxRows). */
export const EXCHANGES_CSV_MAX_ROWS = 50_000;
/** Sunucu ofset tavanı (Go: chstore.aiExchangesMaxOffset). */
export const EXCHANGES_MAX_OFFSET = 100_000;

/** Panelin URL durumu. Parametreler /ai'ın diğer panelleriyle çakışmasın
 *  diye `x` önekli; `fb` v0.10.1280'den beri. */
export interface ExchangesUrlState {
  feedback: AIExchangeFeedbackFilter;
  q: string;
  user: string;
  tier: string;
  page: number;
}

const URL_KEYS = { feedback: 'fb', q: 'xq', user: 'xuser', tier: 'xtier', page: 'xpage' } as const satisfies Record<keyof ExchangesUrlState, string>;

/** Kademe süzgeci seçenekleri: Tümü + sunucunun kademe kümesi (kapalı). */
export const EXCHANGE_TIER_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: 'Tüm kademeler' },
  ...Object.entries(TIER_LABELS).map(([value, label]) => ({ value, label })),
];

export function parseExchangeTier(v: string | null | undefined): string {
  return v && Object.prototype.hasOwnProperty.call(TIER_LABELS, v) ? v : '';
}

export function readExchangesUrlState(sp: URLSearchParams): ExchangesUrlState {
  const page = Math.floor(Number(sp.get(URL_KEYS.page)));
  return {
    feedback: parseExchangeFeedbackFilter(sp.get(URL_KEYS.feedback)),
    q: (sp.get(URL_KEYS.q) ?? '').trim(),
    user: (sp.get(URL_KEYS.user) ?? '').trim(),
    tier: parseExchangeTier(sp.get(URL_KEYS.tier)),
    page: Number.isFinite(page) && page > 0 ? page : 0,
  };
}

/** `setSearchParams(prev => writeExchangesUrlState(prev, patch), { replace: true })`.
 *  Yabancı parametreler korunur (kopya); süzgeç değişince sayfa başa döner
 *  (bayat sayfa boş liste gösterirdi). Boş değer parametreyi siler. */
export function writeExchangesUrlState(prev: URLSearchParams, patch: Partial<ExchangesUrlState>): URLSearchParams {
  const next = new URLSearchParams(prev);
  const filterChanged = (['feedback', 'q', 'user', 'tier'] as const).some(k => k in patch);
  for (const k of ['feedback', 'q', 'user', 'tier'] as const) {
    if (!(k in patch)) continue;
    const v = (patch[k] ?? '').trim();
    if (v) next.set(URL_KEYS[k], v);
    else next.delete(URL_KEYS[k]);
  }
  if ('page' in patch || filterChanged) {
    const p = filterChanged && !('page' in patch) ? 0 : Math.max(0, Math.floor(patch.page ?? 0));
    if (p > 0) next.set(URL_KEYS.page, String(p));
    else next.delete(URL_KEYS.page);
  }
  return next;
}

/** URL→durum içe alma imzası: YALNIZ panelin parametreleri (v0.8.253 —
 *  ilgisiz bir yazım, ör. aralık, yerel taslağı silmesin). */
export function exchangesUrlSig(sp: URLSearchParams): string {
  return Object.values(URL_KEYS).map(k => `${k}=${sp.get(k) ?? ''}`).join('&');
}

/** URL durumu + pencere → istek girdileri (sorgu anahtarı da bundan). */
export function exchangesQueryParams(win: { from: number; to: number }, st: ExchangesUrlState): AIExchangesQueryParams {
  return {
    from: win.from, to: win.to, limit: EXCHANGES_PAGE_SIZE, offset: st.page * EXCHANGES_PAGE_SIZE,
    feedback: st.feedback, q: st.q, user: st.user, tier: st.tier,
  };
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

// ── v0.10.1645 — açıklanan öznenin TAM kimliği ─────────────────────────────
// Operator-reported: "Açıklama turu" çekmecesinin SORU kutusu
// `Bu trace'i açıkla · eea6c7b0…` gösteriyordu — sohbet balonunun kısa kimliği
// (explainTurn.ts shortExplainId) sorunun KENDİSİ olarak kaydediliyordu.
// v0.10.1645'ten sonra sunucu tam kimliği kaydeder (chat_explain_question.go);
// eski satırlarda kısa belirteç turun bağlam kimliğinden (traceId /
// exceptionId / problemId) önek eşleşmesiyle geri kurulur. Eşleşme yoksa
// belirteç olduğu gibi kalır (tam kimlik saklanmamış — uydurulmaz).

export type ExchangeSubjectKind = 'trace' | 'exception' | 'span' | 'problem' | 'incident' | 'anomaly' | 'runbook';

export interface ExchangeSubjectRef {
  kind: ExchangeSubjectKind;
  /** Kimlik — `full` false iken kısa belirteç ("eea6c7b0…"). */
  id: string;
  full: boolean;
  /** Tam kimlikle yeniden yazılmış soru metni. */
  question: string;
}

/** explainTurnText'in (components/ai/explainTurn.ts) kimlikli kalıpları. */
const SUBJECT_PATTERNS: ReadonlyArray<{ kind: ExchangeSubjectKind; re: RegExp }> = [
  { kind: 'trace', re: /^Bu trace'i (?:kodla birlikte )?açıkla · (\S+)$/ },
  { kind: 'exception', re: /grubunu (?:kodla birlikte )?açıkla · (\S+)$/ },
  { kind: 'span', re: /^Bu span'i açıkla · (\S+)$/ },
  { kind: 'problem', re: /^Bu problemi açıkla · (\S+)$/ },
  { kind: 'incident', re: /^Bu olayı açıkla · (\S+)$/ },
  { kind: 'anomaly', re: /^Bu anomaliyi açıkla · (\S+)$/ },
  { kind: 'runbook', re: /^Bu problem için runbook çıkar · (\S+)$/ },
];

/** Türün eski satırda geri düşebileceği bağlam kimliği (span / incident / anomaly: yok). */
function fallbackId(kind: ExchangeSubjectKind, ex: Pick<AIExchange, 'traceId' | 'exceptionId' | 'problemId'>): string {
  switch (kind) {
    case 'trace': return ex.traceId ?? '';
    case 'exception': return ex.exceptionId ?? '';
    case 'problem':
    case 'runbook': return ex.problemId ?? '';
    default: return '';
  }
}

/** exchangeSubjectRef — açıklama turunun öznesi (tür + tam kimlik); kimlikli kalıp değilse null. */
export function exchangeSubjectRef(ex: Pick<AIExchange, 'question' | 'traceId' | 'exceptionId' | 'problemId'>): ExchangeSubjectRef | null {
  const q = (ex.question ?? '').trim();
  for (const { kind, re } of SUBJECT_PATTERNS) {
    const m = re.exec(q);
    if (!m) continue;
    const token = m[1];
    if (!token.endsWith('…')) return { kind, id: token, full: true, question: q };
    const prefix = token.slice(0, -1);
    const cand = fallbackId(kind, ex);
    if (prefix && cand.length > prefix.length && cand.startsWith(prefix)) {
      return { kind, id: cand, full: true, question: q.slice(0, q.length - token.length) + cand };
    }
    return { kind, id: token, full: false, question: q };
  }
  return null;
}

/** Çekmecedeki kimlik satırının etiketi. */
export const EXCHANGE_SUBJECT_LABELS: Record<ExchangeSubjectKind, string> = {
  trace: 'Trace',
  exception: 'Exception grubu',
  span: 'Span',
  problem: 'Problem',
  incident: 'Olay',
  anomaly: 'Anomali',
  runbook: 'Problem (runbook)',
};
