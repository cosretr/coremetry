import { chartScopeLabel, formatAiParam, parseAiParam, type AISubject } from '@/lib/aiSubject';
import type { ChatExplainAsk, ChatExplainKind, ChatExplainMeta, ChatExplainRenderer, ChatSendWindow, ChatTurn, MetricPanelAsk } from '@/lib/types';
import type { ExplainEvidence } from './ExplainBody';
import { buildExplainContext, endpointAskQuestion } from './drawerChat';
import { isOracleGroup } from '@/features/anomalies/oracleGroup';

// explainTurn — v0.10.1400 (Tek CoSRE; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor.").
//
// Trace / exception ✨ açıklaması artık CoSRE sohbetinin SIRADAN bir turu:
// görünür kullanıcı turu ("Bu trace'i açıkla · 5b8efff7…") + yapısal niyet
// (context.explainAsk {kind, id, src}). Sunucu cevabı ✨ düğmesinin çekirdeği
// ve önbellek anahtarıyla üretir (internal/api/chat_explain_turn.go); answer
// çerçevesinin `explain` künyesi balonun ExplainBody ile çizilmesini, kanıtın
// sayfaya duyurulmasını ve takip sorularının bağlamını sağlar.
//
// v0.10.1405 (Tek CoSRE adım 2) — span, problem, incident, anomaly, servis
// sağlığı, runbook ve log satırı da sohbet turu (sunucu: chat_explain_registry.go,
// düğmenin kurucusu + aynı önbellek satırı). Kimliği tek alana sığmayan türler
// (span / service-health / log) niyete `subject` kodeğini (formatAiParam) koyar.
// endpoint: ayrı açıklama yüzeyi yok — görünür soru (endpointAskQuestion) +
// düğmeye basılan pencere (ChatSendWindow), operasyon ön-getirmesi çözer.
//
// v0.10.1407 (Tek CoSRE adım 4) — charts (Service → Details grafik ✨'ları) ve
// log-spike (Logs histogramı ✨) da sohbet turu: AI_KINDS'in HER türü artık bir
// açıklama turuna (ya da endpoint sorusuna) iner; çekmecenin özne kipi silindi.
// Bu iki türün cevabı ölçülmüş kanıtın tipli bloğunu taşır, balon kart çizer
// (explainRenderer). log-spike'ta boş kimlik = filo geneli (meşru).
//
// v0.10.1435 — metric_panel (panelden "Bu grafiği açıkla"): niyet kodekle
// birlikte panelin sorgusunu (`panel`) taşır; görünen metin "Bu grafiği açıkla
// · <başlık> · 14:05–14:20 seçili · seri: pod-a".
//
// Saf modül (vitest node) — React yok.

/** Sohbet turu olarak açıklanan özne türleri — v0.10.1407: AI_KINDS'in TAMAMI (explainTurn.test pinler). */
export const EXPLAIN_TURN_KINDS = [
  'trace', 'exception', 'span', 'problem', 'incident', 'anomaly', 'service-health', 'runbook', 'log', 'endpoint',
  'charts', 'log-spike', // v0.10.1407
  'metric_panel', // v0.10.1435
] as const;
export type ExplainTurnKind = typeof EXPLAIN_TURN_KINDS[number];

const TURN_KIND_SET = new Set<string>(EXPLAIN_TURN_KINDS);
/** Tek kimlikli türler: niyet `subject` taşımaz, özne {kind, id}'den kurulur. */
const SIMPLE_KINDS = new Set<string>(['trace', 'exception', 'problem', 'incident', 'anomaly', 'runbook']);

export function isExplainTurnKind(kind: string | null | undefined): kind is ExplainTurnKind {
  return !!kind && TURN_KIND_SET.has(kind);
}

/** shortExplainId — kullanıcı turunda görünen kısa kimlik (`ora:` öneki korunur). */
export function shortExplainId(id: string): string {
  const pre = id.startsWith('ora:') ? 'ora:' : '';
  const body = pre ? id.slice(4) : id;
  return body.length > 8 ? `${pre}${body.slice(0, 8)}…` : id;
}

/** explainUserText — trace / exception açıklama turunun GÖRÜNEN kullanıcı metni (v0.10.1400). */
export function explainUserText(kind: 'trace' | 'exception', id: string, label?: string): string {
  if (kind === 'trace') return `Bu trace'i açıkla · ${shortExplainId(id)}`;
  const l = (label ?? '').trim();
  return `${l || 'Exception'} grubunu açıkla · ${shortExplainId(id)}`;
}

/** explainTurnText — v0.10.1405: her türün GÖRÜNEN kullanıcı metni. */
export function explainTurnText(s: AISubject, label?: string): string {
  switch (s.kind) {
    case 'trace':
    case 'exception':      return explainUserText(s.kind, s.id, label);
    case 'span':           return `Bu span'i açıkla · ${shortExplainId(s.spanId)}`;
    case 'problem':        return `Bu problemi açıkla · ${shortExplainId(s.id)}`;
    case 'incident':       return `Bu olayı açıkla · ${shortExplainId(s.id)}`;
    case 'anomaly':        return `Bu anomaliyi açıkla · ${shortExplainId(s.id)}`;
    case 'service-health': return `${s.id} sağlığını açıkla`;
    case 'runbook':        return `Bu problem için runbook çıkar · ${shortExplainId(s.id)}`;
    case 'log':            return `Bu log satırını açıkla · ${s.id}${s.pod ? ` · ${s.pod}` : ''}`;
    case 'endpoint':       return endpointAskQuestion(s);
    case 'charts':         return `${s.id} grafiklerini açıkla · ${chartScopeLabel(s.scope)}`; // v0.10.1407
    case 'log-spike':      return `${s.id || 'Tüm servislerin'} log artışını açıkla`; // v0.10.1407
    case 'metric_panel':   return metricPanelTurnText(s, label); // v0.10.1435
    default:               return '';
  }
}

/**
 * metricPanelTurnText — v0.10.1435: panel açıklamasının GÖRÜNEN metni:
 * `Bu grafiği açıkla · <başlık> · 14:05–14:20 seçili · seri: pod-a` (+ kıyas).
 */
export function metricPanelTurnText(s: Extract<AISubject, { kind: 'metric_panel' }>, title?: string): string {
  const parts = ['Bu grafiği açıkla'];
  const tl = (title ?? '').trim();
  parts.push(tl ? (tl.length > 60 ? `${tl.slice(0, 59)}…` : tl) : metricPanelLocLabel(s.id));
  if (s.brushFromNs > 0 && s.brushToNs > s.brushFromNs) {
    parts.push(`${clockHM(s.brushFromNs / 1e6)}–${clockHM(s.brushToNs / 1e6)} seçili`);
  }
  if (s.series.length) parts.push(`seri: ${s.series.join(', ')}`);
  if (s.cmp) parts.push('önceki pencereyle');
  return parts.join(' · ');
}

/** v0.10.1435 — yerel saat SS:DD (görünen metin kısa kalsın; saniye yok). */
function clockHM(ms: number): string {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** v0.10.1435 — konumun okunur adı (başlık yoksa). */
export function metricPanelLocLabel(loc: string): string {
  if (loc === 'm') return 'Metrics editörü';
  if (loc.startsWith('x.')) return `Explore · ${loc.slice(2)}`;
  return 'dashboard paneli';
}

/**
 * explainCodeCapable — v0.10.1406: "Kodu da incele"nin anlamlı olduğu özne —
 * eski ✨ kartındaki codeCapable'ın aynası (sunucu: chat_explain_turn.go
 * explainCodeCapable): trace ve span exception grubu (Oracle `ora:` stack taşımaz).
 */
export function explainCodeCapable(kind: string, id: string): boolean {
  return kind === 'trace' || (kind === 'exception' && !isOracleGroup(id));
}

/** explainCodeTurnText — v0.10.1406: kod turunun GÖRÜNEN kullanıcı metni (?aicode=1 derin linki). */
export function explainCodeTurnText(kind: 'trace' | 'exception', id: string, label?: string): string {
  if (kind === 'trace') return `Bu trace'i kodla birlikte açıkla · ${shortExplainId(id)}`;
  const l = (label ?? '').trim();
  return `${l || 'Exception'} grubunu kodla birlikte açıkla · ${shortExplainId(id)}`;
}

/**
 * withoutFresh — v0.10.1406: niyetin SAKLANAN hâli. `fresh` yalnız o isteğe
 * aittir ("↻ Yeniden üret"); kullanıcı turunda kalırsa sonraki "yeniden dene"
 * de önbelleği atlardı.
 */
export function withoutFresh(a: ChatExplainAsk): ChatExplainAsk {
  if (!a.fresh) return a;
  const { fresh: _fresh, ...rest } = a;
  return rest;
}

/** endpointWindow — v0.10.1405: endpoint sorusunun sabit penceresi (v0.10.1367 çekmece epWin'i). */
export function endpointWindow(s: Extract<AISubject, { kind: 'endpoint' }>): ChatSendWindow {
  return { rangeS: Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9)), toMs: Math.floor(s.toNs / 1e6) };
}

/** Bir açıklama turunun gönderimi: görünen metin + (varsa) yapısal niyet + (endpoint) sabit pencere. */
export interface ExplainTurnIntent {
  userText: string;
  explainAsk?: ChatExplainAsk;
  window?: ChatSendWindow;
}

/**
 * explainAskFor — özne → {görünen metin, yapısal niyet}. AI_KINDS'in her türü
 * (EXPLAIN_TURN_KINDS). Boş kimlik null — v0.10.1407: log-spike hariç (boş =
 * filo geneli). v0.10.1404 — `label`: görünen metindeki grup adı (CoSRE
 * başlangıcının exception çipi "ORDERS_GET_X · orders-api"); yoksa "Exception".
 * v0.10.1405 — span / service-health / log niyeti `subject` kodeğini taşır;
 * endpoint niyet taşımaz, `window` taşır.
 */
export function explainAskFor(subject: AISubject | null | undefined, src?: string, label?: string, code?: boolean, panel?: MetricPanelAsk): ExplainTurnIntent | null {
  if (!subject || !isExplainTurnKind(subject.kind)) return null;
  const id = (subject.id ?? '').trim();
  if (!id && subject.kind !== 'log-spike') return null;
  const s = { ...subject, id } as AISubject;
  // v0.10.1406 — kod turu (?aicode=1) yalnız kod-yetenekli öznede; aksi kodsuz açıklama.
  const withCode = !!code && explainCodeCapable(s.kind, id);
  // v0.10.1435 — metric_panel: görünen metindeki başlık yükten (etiket yoksa).
  const userText = withCode ? explainCodeTurnText(s.kind as 'trace' | 'exception', id, label)
    : explainTurnText(s, label ?? (s.kind === 'metric_panel' ? panel?.title : undefined));
  if (s.kind === 'endpoint') return { userText, window: endpointWindow(s) };
  const kind = s.kind as ChatExplainKind;
  return {
    userText,
    explainAsk: {
      kind, id, ...(SIMPLE_KINDS.has(kind) ? {} : { subject: formatAiParam(s) }), ...(src ? { src } : {}),
      ...(withCode ? { code: true } : {}),
      ...(panel && s.kind === 'metric_panel' ? { panel } : {}), // v0.10.1435 — yalnız panel türünde
    },
  };
}

/** explainTurnSendOpts — niyet → send'in ikinci argümanı (yalnız dolu alanlar). */
export function explainTurnSendOpts(x: ExplainTurnIntent): { explainAsk?: ChatExplainAsk; window?: ChatSendWindow } {
  return { ...(x.explainAsk ? { explainAsk: x.explainAsk } : {}), ...(x.window ? { window: x.window } : {}) };
}

/**
 * subjectOf — künye / niyet → özne. Kodek (subject) varsa ve türü tutuyorsa
 * ondan, basit türde {kind, id}'den; kurulamıyorsa null (bozuk arşiv satırı).
 */
function subjectOf(kind: string, id: string, subject?: string): AISubject | null {
  if (subject) {
    const s = parseAiParam(subject);
    if (s && s.kind === kind) return s;
  }
  if (!id || !SIMPLE_KINDS.has(kind)) return null;
  return { kind, id } as AISubject;
}

/** explainAskSubject — v0.10.1405: niyet (çip / başlangıç öğesi) → özne. */
export function explainAskSubject(a: ChatExplainAsk): AISubject | null {
  return subjectOf(a.kind, a.id, a.subject);
}

/** explainSubject — künye → özne (takip bağlamı / kodek / yedek çipler için). */
export function explainSubject(meta: Pick<ChatExplainMeta, 'kind' | 'id' | 'subject'>): AISubject | null {
  return subjectOf(meta.kind, meta.id, meta.subject);
}

/** explainEvidence — künyeden ExplainBody'nin "Kanıt:" sayıları. */
export function explainEvidence(meta: ChatExplainMeta | undefined): ExplainEvidence | undefined {
  if (!meta) return undefined;
  return {
    spans: meta.evidenceSpanIds?.length ?? 0,
    traces: meta.evidenceTraceIds?.length ?? 0,
    oracle: meta.oracleRows ?? 0,
    ...(meta.logs ? { logs: meta.logs } : {}), // v0.10.1742
  };
}

/** Etkin açıklama: konuşmadaki SON tamamlanmış açıklama turu (+ sırası). */
export interface ActiveExplain {
  /** turun konuşmadaki sırası — ✕ ile çıkarma bu kimliğe bağlanır. */
  key: string;
  meta: ChatExplainMeta;
  text: string;
  /** v0.10.1405 — künyeden çözülen özne (kodek / basit tür). */
  subject: AISubject;
}

export function activeExplainFrom(turns: readonly ChatTurn[]): ActiveExplain | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i];
    if (t.role !== 'assistant' || !t.explain || t.pending || t.error) continue;
    const text = (t.text ?? '').trim();
    if (!text) continue;
    const subject = explainSubject(t.explain);
    if (!subject) continue;
    return { key: `${i}:${t.explain.kind}:${t.explain.id}`, meta: t.explain, text, subject };
  }
  return null;
}

/** Takip bağlamı: çekmece özne kipindeki alanlar (+ servis / pencere). */
export interface ExplainFollowupContext {
  explain: string;
  subject: string;
  service?: string;
  rangeS?: number;
  toMs?: number;
}

/**
 * explainFollowupContext — takip sorularının bağlamı (çekmece özne kipindeki
 * AIDrawerChat ile AYNI alanlar): `explain` = künye + açıklama metni + kanıt
 * kimlikleri; `subject` = `?ai=` kodeği (sunucu ham kanıtı yeniden kurar).
 * v0.10.1405 — service-health / log ayrıca servisi (guided router varsayılanı),
 * service-health açıklanan pencereyi taşır (çekmecenin service kuralı).
 */
export function explainFollowupContext(a: ActiveExplain): ExplainFollowupContext {
  const s = a.subject;
  const out: ExplainFollowupContext = {
    explain: buildExplainContext({ subject: s, text: a.text, spanIds: a.meta.evidenceSpanIds, traceIds: a.meta.evidenceTraceIds }),
    subject: formatAiParam(s),
  };
  if (s.kind === 'service-health') {
    out.service = s.id;
    out.rangeS = Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9));
    out.toMs = Math.floor(s.toNs / 1e6);
  } else if (s.kind === 'log') {
    out.service = s.id;
  } else if (s.kind === 'metric_panel') {
    // v0.10.1435 — panel: açıklanan pencere (servis panelin sorgusunda, kodekte değil).
    out.rangeS = Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9));
    out.toMs = Math.floor(s.toNs / 1e6);
  } else if (s.kind === 'charts' || (s.kind === 'log-spike' && s.id)) {
    // v0.10.1407 — grafik / log artışı: servis + açıklanan pencere (service-health kuralı).
    out.service = s.id;
    out.rangeS = Math.max(60, Math.round((s.toNs - s.fromNs) / 1e9));
    out.toMs = Math.floor(s.toNs / 1e6);
  }
  return out;
}

/**
 * explainRenderer — v0.10.1407: balonun çizim kipi. Künyenin `renderer`ı
 * (sunucu) yoksa türden: charts / log-spike kartı (canlı turun geçici künyesi
 * de kartı ilk metinle çizer), diğerleri body. Kart yine BLOK ister —
 * ChatBubble blok yoksa ExplainBody'ye düşer.
 */
export function explainRenderer(meta: Pick<ChatExplainMeta, 'kind' | 'renderer'>): ChatExplainRenderer {
  if (meta.renderer) return meta.renderer;
  if (meta.kind === 'charts') return 'charts';
  if (meta.kind === 'log-spike') return 'log-spike';
  if (meta.kind === 'metric_panel') return 'metric-panel'; // v0.10.1435
  return 'body';
}

const clipLabel = (l: string): string => (l.length > 40 ? `${l.slice(0, 39)}…` : l);

/** explainContextLabel — bağlam şeridindeki öğe ("✨ trace 5b8efff7…"). */
export function explainContextLabel(meta: Pick<ChatExplainMeta, 'kind' | 'id' | 'label'>): string {
  const l = clipLabel((meta.label ?? '').trim());
  switch (meta.kind) {
    case 'trace':          return `✨ trace ${shortExplainId(meta.id)}`;
    case 'exception':      return `✨ ${meta.label || 'exception'} ${shortExplainId(meta.id)}`;
    case 'span':           return `✨ span ${l || shortExplainId(meta.id)}`;
    case 'problem':        return `✨ problem ${l || shortExplainId(meta.id)}`;
    case 'incident':       return `✨ olay ${l || shortExplainId(meta.id)}`;
    case 'anomaly':        return `✨ anomali ${l || shortExplainId(meta.id)}`;
    case 'service-health': return `✨ ${meta.id} sağlığı`;
    case 'runbook':        return `✨ runbook ${shortExplainId(meta.id)}`;
    case 'log':            return `✨ log ${meta.id}`;
    case 'charts':         return `✨ ${meta.id} grafikleri`; // v0.10.1407
    case 'log-spike':      return `✨ log artışı ${meta.id || 'tüm servisler'}`; // v0.10.1407
    case 'metric_panel':   return `✨ grafik ${l || metricPanelLocLabel(meta.id)}`; // v0.10.1435
    default:               return `✨ ${shortExplainId(meta.id)}`;
  }
}
