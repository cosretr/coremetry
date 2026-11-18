// protocol.ts — v0.10.1598 (CoSRE v2 Faz 1, R8): CoSRE v2 sohbet akışının
// (POST /api/cosre/v2/chat) tel sözleşmesi — Go tarafı internal/cosre/uistream.
//
// Vercel AI SDK v5 "UI Message Stream" biçimi: `data: <json>` çerçeve başına bir
// parça, son satır `data: [DONE]`, yanıt başlığı
// `x-vercel-ai-ui-message-stream: v1`. v0.10.1618 (Faz 2a): `ai` 5.0.276 +
// `@ai-sdk/react` 2.0.279 tam pinli (aiSdkPin.test.ts); paylaşılan fixture'lar
// gerçek istemciden geçirilerek doğrulanır (useCosreChat.test.tsx).
//
// Kapalı kümeler: Go çeviricisi yeni bir parça türü ya da data adı üretirse
// uiStreamFixture.test.ts (paylaşılan testdata/cosre/*.sse) kırmızıya döner;
// fixture yenilendiği commit'te bu dosya da güncellenir.

import type { CosreActionCard, CosreActionFields, CosreActionKind, CosreActionResult, CosreApproval, CosreConvState, CosreEndpointPick, CosreEndpointPickItem, CosreLinkCard, CosreQuestion, CosreQuestionKind, CosreScope, CosreScopeReason, CosreStepContract, CosreStepSignal, CosreStepState, CosreStop, CosreStopReason, CosreToolResult, CosreToolResultKind } from '@/lib/types';

export const PROTOCOL_VERSION = 'v1';
export const PROTOCOL_HEADER = 'x-vercel-ai-ui-message-stream';
export const DONE_MARKER = '[DONE]';

/** v0.10.1603 — `start` parçasının sunucu üretimi messageId'si: ULID
 *  (26 karakter Crockford base32; Go: internal/cosre/uistream/ulid.go). */
export const MESSAGE_ID_RE = /^[0-9A-HJKMNP-TV-Z]{26}$/;

/** Çeviricinin ürettiği data-* dışı parça türleri. */
export const UI_STREAM_PART_TYPES = [
  'start', 'start-step', 'finish-step', 'finish',
  'text-start', 'text-delta', 'text-end',
  'tool-input-start', 'tool-input-available', 'tool-output-available',
  'source-url', 'source-document',
  'message-metadata', 'error',
] as const;

/** internal/ai/agent/blocks Type* sabitleri (13). */
export const BLOCK_TYPES = [
  'text', 'table', 'chart', 'trace_list', 'link', 'action', 'evidence', 'operation',
  'service_report', 'topology', 'charts_signals', 'log_spike', 'metric_panel',
  // v0.10.2360 — S3 kanıt blokları (çizici S5'te; o gelene dek bilinen ama çizilmeyen tip).
  'exception', 'trace', 'logs', 'anomaly',
] as const;

/** `data-<ad>` parçalarının adları (önek olmadan). */
export const DATA_PART_NAMES = [
  'step', 'budget', 'reset', 'links', 'followups', 'evidence-summary',
  'code-lock', 'error', 'unknown',
  // agent-core S4 — araç onayı: toolCallId'li parça input-available'da kalır,
  // eşli data-approval{toolCallId, tool, token, argsDigest, expiresAt}; finish 'tool-calls'.
  'approval',
  // araç seti dilim 1 (docs/cosre/tools-audit.md §10 G1/G2) — yapısal araç
  // sonucu (tek parça, kind içeride) + döngünün her çıkışında "neden durdum".
  'tool-result', 'stop',
  // araç seti dilim 2 (tools-audit §3, Q1/Q2) — alışverişin kullanıcı kapsamı.
  'scope',
  // v0.10.1737 — v2 seçici kartı (kind=endpoint): "Endpoint seç".
  'pick',
  // v0.10.2100 (experience-audit D1) — kapalı netleştirme sorusu; ayrıştırıcı + kart D2.
  'question',
  // v0.10.2325 (conversation-audit §6.1 A) — konuşma durumu (kapsam; defter yok).
  'state',
  // v0.10.2333 (conversation-audit §6.1 F1) — araç turunun ara durum cümlesi;
  // transient (yalnız onData, mesaja/arşive girmez).
  'status',
  // v0.10.2353 (interaction-audit §6 I5) — onaylı aksiyon kartı (yalnız sunucu
  // kuralı önerir) + onay ucunun sonucu (istemci mesaja ekler).
  'action', 'action-result',
  ...BLOCK_TYPES.map(t => `block-${t}` as const),
] as const;

/** data-tool-result `kind` kapalı kümesi (Go uistream.ToolResultKinds). */
export const TOOL_RESULT_KINDS = ['text', 'evidence', 'error', 'skipped'] as const satisfies readonly CosreToolResultKind[];

/** data-stop `reason` kapalı kümesi (Go uistream.StopReasons). */
export const STOP_REASONS = [
  'stop', 'round_cap', 'call_cap', 'cancelled', 'deadline', 'approval_pending', 'error', 'token_budget',
] as const satisfies readonly CosreStopReason[];

/** data-scope `reason` kapalı kümesi (Go uistream.ScopeReasons). */
export const SCOPE_REASONS = [
  'team', 'no_identity', 'no_team', 'no_services', 'disabled', 'escape', 'error',
] as const satisfies readonly CosreScopeReason[];

/** data-tool-result tavanı (Go uistream.ToolResultMaxBytes). */
export const TOOL_RESULT_MAX_BYTES = 2048;

const TOOL_RESULT_ID_RE = /^s[1-9]\d{0,4}$/;

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

const isKind = (v: unknown): v is CosreToolResultKind =>
  typeof v === 'string' && (TOOL_RESULT_KINDS as readonly string[]).includes(v);

const isStopReason = (v: unknown): v is CosreStopReason =>
  typeof v === 'string' && (STOP_REASONS as readonly string[]).includes(v);

const finiteNum = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);

/** data-tool-result → CosreToolResult; bilinmeyen kind, biçimsiz id ya da
 *  nesne olmayan data null (yok sayılır — ileri-uyum, akış kırılmaz). page_url
 *  yalnız kök-göreli kalır. */
export function parseDataToolResult(p: { type: string; id?: unknown; data?: unknown }): CosreToolResult | null {
  if (p.type !== 'data-tool-result' || typeof p.id !== 'string' || !TOOL_RESULT_ID_RE.test(p.id)) return null;
  const d = p.data;
  if (!isRecord(d) || !isKind(d.kind)) return null;
  const out: CosreToolResult = { ...(d as Partial<CosreToolResult>), id: p.id, kind: d.kind, ok: d.ok === true };
  const url = d.page_url;
  if (typeof url !== 'string' || !url.startsWith('/') || url.startsWith('//') || url.startsWith('/\\')) delete out.page_url;
  return out;
}

/** agent-core S5 — data-approval → CosreApproval; biçimsiz id/toolCallId,
 *  boş jeton ya da sayı olmayan expiresAt null (kart çizilmez). */
export function parseDataApproval(p: { type: string; id?: unknown; data?: unknown }): CosreApproval | null {
  if (p.type !== 'data-approval' || !isRecord(p.data)) return null;
  const { toolCallId, tool, token, argsDigest, expiresAt, exchangeId } = p.data;
  const exp = finiteNum(expiresAt);
  if (typeof toolCallId !== 'string' || !TOOL_RESULT_ID_RE.test(toolCallId) || typeof tool !== 'string' || !tool
    || typeof token !== 'string' || !token || typeof argsDigest !== 'string' || exp === undefined) return null;
  return { toolCallId, tool, token, argsDigest, expiresAt: exp, ...(typeof exchangeId === 'string' && exchangeId ? { exchangeId } : {}) };
}

/** data-stop → CosreStop; bilinmeyen neden ya da sayı olmayan alan null. */
export function parseDataStop(p: { type: string; data?: unknown }): CosreStop | null {
  if (p.type !== 'data-stop' || !isRecord(p.data)) return null;
  const { reason, calls, rounds, elapsed_ms } = p.data;
  const c = finiteNum(calls), r = finiteNum(rounds), e = finiteNum(elapsed_ms);
  if (!isStopReason(reason) || c === undefined || r === undefined || e === undefined) return null;
  return { reason, calls: c, rounds: r, elapsed_ms: e };
}

const isScopeReason = (v: unknown): v is CosreScopeReason =>
  typeof v === 'string' && (SCOPE_REASONS as readonly string[]).includes(v);

/** data-scope → CosreScope; bilinmeyen neden, biçimsiz alan ya da çelişki
 *  (kapsamlı 'team' ama unscoped:true, ya da tersi) null — rozet yanlış
 *  kapsam iddia etmesin. services yalnız dize elemanlarını tutar. */
export function parseDataScope(p: { type: string; data?: unknown }): CosreScope | null {
  if (p.type !== 'data-scope' || !isRecord(p.data)) return null;
  const { unscoped, reason, count, team, services, trimmed } = p.data;
  const n = finiteNum(count);
  if (typeof unscoped !== 'boolean' || !isScopeReason(reason) || n === undefined) return null;
  if (unscoped === (reason === 'team')) return null;
  const out: CosreScope = { unscoped, reason, count: n };
  if (typeof team === 'string' && team !== '') out.team = team;
  if (Array.isArray(services)) {
    const names = services.filter((s): s is string => typeof s === 'string' && s !== '');
    if (names.length > 0) out.services = names;
  }
  const tr = finiteNum(trimmed);
  if (tr !== undefined && tr > 0) out.trimmed = tr;
  if (p.data.teamPick === true && reason === 'no_team') out.teamPick = true; // v0.10.1725
  return out;
}

export type UIStreamPartType = (typeof UI_STREAM_PART_TYPES)[number];
export type DataPartName = (typeof DATA_PART_NAMES)[number];

/** Tek parça — `type` ayırt edicidir; alanlar türüne göre değişir. */
export interface UIStreamPart {
  type: string;
  id?: string;
  [k: string]: unknown;
}

export function isDataPart(p: UIStreamPart): boolean {
  return p.type.startsWith('data-');
}

export function dataPartName(p: UIStreamPart): string {
  return p.type.slice('data-'.length);
}

/** v0.10.1606 — geri alma parçası, TAM OLARAK
 *  `{"type":"data-reset","data":{"textId":"<text-start id>"},"transient":true}`:
 *  kendi `id`'si yok (uzlaştırılmaz, her reset tekil olay), providerMetadata
 *  yok. İstemci `textId`'li metin parçasını boşaltır. v0.10.1607: `transient`
 *  ZORUNLU — AI SDK parçayı yalnız `onData`'ya iletir, `message.parts`'a
 *  eklemez (kalıcı mesaj içeriği değil, akış olayı). */
export interface DataResetPart {
  type: 'data-reset';
  data: { textId: string };
  transient: true;
}

/** Parça geçerli bir data-reset ise onu döner; şekil dışıysa (eski `target`,
 *  boş textId, fazladan alan, transient eksik/false) null — sözleşme kayması
 *  sessiz geçmesin. */
export function parseDataReset(p: UIStreamPart): DataResetPart | null {
  if (p.type !== 'data-reset') return null;
  if (Object.keys(p).some(k => k !== 'type' && k !== 'data' && k !== 'transient')) return null;
  if (p.transient !== true) return null;
  const d = p.data;
  if (typeof d !== 'object' || d === null) return null;
  const keys = Object.keys(d);
  const textId = (d as { textId?: unknown }).textId;
  if (keys.length !== 1 || typeof textId !== 'string' || textId === '') return null;
  return { type: 'data-reset', data: { textId }, transient: true };
}

/** v0.10.2333 (conversation-audit §6.1 F1) — ara durum cümlesi, TAM OLARAK
 *  `{"type":"data-status","id":"status","data":{"text":"…"},"transient":true}`.
 *  transient ZORUNLU (data-reset emsali): AI SDK yalnız onData'ya iletir, mesaja
 *  ve arşive girmez. Şekil dışıysa (transient eksik, boş metin, fazladan alan) null. */
export function parseDataStatus(p: UIStreamPart): { text: string } | null {
  if (p.type !== 'data-status' || p.transient !== true || p.id !== 'status') return null;
  if (Object.keys(p).some(k => k !== 'type' && k !== 'id' && k !== 'data' && k !== 'transient')) return null;
  const d = p.data;
  if (!isRecord(d) || Object.keys(d).length !== 1 || typeof d.text !== 'string') return null;
  const text = d.text.trim();
  return text ? { text } : null;
}

/** v0.10.2102 — data-question kind kapalı kümesi (Go uistream.QuestionKinds). */
export const QUESTION_KINDS = ['source', 'doc_or_entity', 'trace_synth', 'entity_pick', 'slot'] as const satisfies readonly CosreQuestionKind[];
/** v0.10.2102 — seçenek tavanı (Go uistream.QuestionMaxOptions). */
export const QUESTION_MAX_OPTIONS = 3;
/** v0.10.2332 — `slot` (eksik tek alan) seçenek tavanı (Go uistream.QuestionSlotMaxOptions). */
export const QUESTION_SLOT_MAX_OPTIONS = 4;

/** v0.10.2102 — data-question → CosreQuestion; bilinmeyen kind, boş metin,
 *  0 ya da >3 seçenek, biçimsiz / yinelenen seçenek null (kart çizilmez; düz
 *  metin soru balonda zaten var). */
export function parseDataQuestion(p: { type: string; data?: unknown }): CosreQuestion | null {
  if (p.type !== 'data-question' || !isRecord(p.data)) return null;
  const { kind, text, options, allowFree } = p.data;
  if (!(QUESTION_KINDS as readonly unknown[]).includes(kind) || typeof text !== 'string' || !text || !Array.isArray(options)) return null;
  if (options.length === 0 || options.length > (kind === 'slot' ? QUESTION_SLOT_MAX_OPTIONS : QUESTION_MAX_OPTIONS)) return null;
  const out: CosreQuestion['options'] = [];
  for (const o of options) {
    if (!isRecord(o) || typeof o.id !== 'string' || !o.id || typeof o.label !== 'string' || !o.label) return null;
    if (out.some(x => x.id === o.id)) return null;
    out.push({ id: o.id, label: o.label });
  }
  const chosen = p.data.chosen; // v0.10.2104 (D5) — arşivden; seçenek dışıysa yok sayılır
  return { kind: kind as CosreQuestionKind, text, options: out, allowFree: allowFree === true,
    ...(typeof chosen === 'string' && out.some(x => x.id === chosen) ? { chosen } : {}) };
}

/** v0.10.2101 — kart linkinin üst sınırı (api summaryCardMaxLinks). */
export const LINK_CARD_MAX_LINKS = 3;

/** v0.10.2101 — data-links → CosreLinkCard; title/summary yoksa (bugünkü
 *  çip yükü), link iç yol değilse ya da biçimsizse null (çipler çizilir). */
export function parseDataLinksCard(p: { type: string; data?: unknown }): CosreLinkCard | null {
  if (p.type !== 'data-links' || !isRecord(p.data)) return null;
  const { title, summary, links } = p.data;
  if (typeof title !== 'string' || !title || typeof summary !== 'string' || !summary || !Array.isArray(links)) return null;
  if (links.length === 0 || links.length > LINK_CARD_MAX_LINKS) return null;
  const out: CosreLinkCard['links'] = [];
  for (const l of links) {
    if (!isRecord(l) || typeof l.label !== 'string' || !l.label || typeof l.href !== 'string') return null;
    if (!l.href.startsWith('/') || l.href.startsWith('//') || l.href.startsWith('/\\')) return null;
    out.push({ label: l.label, href: l.href });
  }
  return { title, summary, links: out };
}

/** v0.10.1737 — data-pick → CosreEndpointPick; bilinmeyen kind, boş servis,
 *  sayı olmayan alan ya da biçimsiz satır null (kart çizilmez). */
export function parseDataPick(p: { type: string; data?: unknown }): CosreEndpointPick | null {
  if (p.type !== 'data-pick' || !isRecord(p.data)) return null;
  const { kind, service, env, rangeS, total, capped, items } = p.data;
  const rs = finiteNum(rangeS), tot = finiteNum(total);
  if (kind !== 'endpoint' || typeof service !== 'string' || !service || rs === undefined || rs <= 0 || tot === undefined || !Array.isArray(items)) return null;
  const rows: CosreEndpointPickItem[] = [];
  for (const it of items) {
    if (!isRecord(it) || typeof it.route !== 'string' || !it.route) return null;
    const rps = finiteNum(it.rps), p95 = finiteNum(it.p95Ms), err = finiteNum(it.errPct);
    if (rps === undefined || p95 === undefined || err === undefined) return null;
    rows.push({ route: it.route, rps, p95Ms: p95, errPct: err });
  }
  return { kind, service, rangeS: rs, total: tot, items: rows,
    ...(typeof env === 'string' && env ? { env } : {}), ...(capped === true ? { capped: true } : {}) };
}

const optStr = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined);

/** v0.10.2325 — data-state → CosreConvState; updatedAt sayı değilse ya da
 *  pencere biçimsizse null (bağlam satırı çizilmez). Bilinmeyen alan düşer. */
export function parseDataState(p: { type: string; data?: unknown }): CosreConvState | null {
  if (p.type !== 'data-state' || !isRecord(p.data)) return null;
  const d = p.data;
  const at = finiteNum(d.updatedAt);
  if (at === undefined) return null;
  const out: CosreConvState = { updatedAt: at };
  for (const k of ['service', 'env', 'cluster', 'channelCode', 'functionCode', 'problem', 'trace', 'operation', 'clientIp'] as const) { // v0.10.2356 — clientIp
    const v = optStr(d[k]);
    if (v) out[k] = v;
  }
  if (Array.isArray(d.services)) {
    const s = d.services.filter((x): x is string => typeof x === 'string' && x !== '');
    if (s.length) out.services = s;
  }
  if (d.address === 'sen' || d.address === 'siz') out.address = d.address;
  if (d.window !== undefined) {
    if (!isRecord(d.window)) return null;
    const rs = finiteNum(d.window.rangeS);
    if (rs === undefined || rs <= 0) return null;
    const to = finiteNum(d.window.toMs);
    out.window = { rangeS: rs, ...(to ? { toMs: to } : {}), ...(optStr(d.window.label) ? { label: d.window.label as string } : {}),
      ...(d.window.explicit === true ? { explicit: true } : {}) };
  }
  if (isRecord(d.offer) && optStr(d.offer.kind)) {
    const at2 = finiteNum(d.offer.at);
    out.offer = { kind: d.offer.kind as string, ...(optStr(d.offer.label) ? { label: d.offer.label as string } : {}), ...(at2 ? { at: at2 } : {}) };
  }
  return out;
}

/** v0.10.2353 — data-action kind kapalı kümesi (Go uistream.ActionKinds; prod'a dokunan tür yok). */
export const ACTION_KINDS = ['share_link', 'problem_note'] as const satisfies readonly CosreActionKind[];
/** v0.10.2353 — data-action-result status kapalı kümesi (Go uistream.ActionResultStatuses). */
export const ACTION_RESULT_STATUSES = ['done', 'needs_save'] as const satisfies readonly CosreActionResult['status'][];

const isActionKind = (v: unknown): v is CosreActionKind => (ACTION_KINDS as readonly unknown[]).includes(v);
const innerPath = (v: unknown): v is string => typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') && !v.startsWith('/\\');

/** v0.10.2353 — data-action → CosreActionCard; bilinmeyen kind, boş jeton /
 *  alışveriş, sayı olmayan expiresAt ya da başlıksız alanlar null (kart çizilmez). */
export function parseDataAction(p: { type: string; data?: unknown }): CosreActionCard | null {
  if (p.type !== 'data-action' || !isRecord(p.data)) return null;
  const d = p.data;
  const exp = finiteNum(d.expiresAt);
  if (!isActionKind(d.kind) || !optStr(d.token) || !optStr(d.exchangeId) || exp === undefined || !isRecord(d.fields)) return null;
  const f = d.fields;
  if (!optStr(f.title)) return null;
  const fields: CosreActionFields = {
    title: f.title as string, summary: typeof f.summary === 'string' ? f.summary : '',
    ...(optStr(f.service) ? { service: f.service as string } : {}), ...(optStr(f.window) ? { window: f.window as string } : {}),
    ...(Array.isArray(f.evidence) ? { evidence: f.evidence.filter((x): x is string => typeof x === 'string' && x !== '') } : {}),
  };
  if (d.kind === 'problem_note' && !optStr(d.problemId)) return null;
  return {
    kind: d.kind, token: d.token as string, exchangeId: d.exchangeId as string, expiresAt: exp, fields,
    label: optStr(d.label) ?? '',
    ...(optStr(d.problemId) ? { problemId: d.problemId as string } : {}),
    ...(optStr(d.cluster) ? { cluster: d.cluster as string } : {}),
    ...(optStr(d.namespace) ? { namespace: d.namespace as string } : {}),
  };
}

/** v0.10.2353 — data-action-result → CosreActionResult; dış / biçimsiz link düşer. */
export function parseDataActionResult(p: { type: string; data?: unknown }): CosreActionResult | null {
  if (p.type !== 'data-action-result' || !isRecord(p.data)) return null;
  const d = p.data;
  if (!isActionKind(d.kind) || !(ACTION_RESULT_STATUSES as readonly unknown[]).includes(d.status)) return null;
  return {
    kind: d.kind, status: d.status as CosreActionResult['status'],
    ...(innerPath(d.link) ? { link: d.link } : {}),
    ...(optStr(d.noteId) ? { noteId: d.noteId as string } : {}),
    ...(optStr(d.exchangeId) ? { exchangeId: d.exchangeId as string } : {}),
  };
}

/** v0.10.2363 (I1) — data-step sözleşmesinin kapalı kümeleri (Go uistream.StepStates / StepSignals). */
export const STEP_STATES = ['running', 'done', 'failed', 'skipped'] as const satisfies readonly CosreStepState[];
export const STEP_SIGNALS = ['metric', 'trace', 'log', 'knowledge'] as const satisfies readonly CosreStepSignal[];

/** v0.10.2363 (I1) — sözleşmeli data-step → CosreStepContract; etiket-yalnız data-step ya da bozuk yük → null. */
export function parseDataStepState(p: { type: string; id?: unknown; data?: unknown }): CosreStepContract | null {
  if (p.type !== 'data-step' || !isRecord(p.data)) return null;
  const d = p.data;
  const order = finiteNum(d.order);
  if (!optStr(d.id) || d.id !== p.id || !optStr(d.title) || order === undefined || !(STEP_STATES as readonly unknown[]).includes(d.state)) return null;
  const spent = isRecord(d.spent) ? d.spent : {};
  return {
    id: d.id as string, order, title: d.title as string, state: d.state as CosreStepState,
    spent: { calls: finiteNum(spent.calls) ?? 0, rounds: finiteNum(spent.rounds) ?? 0 },
    ...((STEP_SIGNALS as readonly unknown[]).includes(d.signal) ? { signal: d.signal as CosreStepSignal } : {}),
    ...(optStr(d.result) ? { result: d.result as string } : {}),
    ...(optStr(d.evidenceRef) ? { evidenceRef: d.evidenceRef as string } : {}),
    ...(optStr(d.tool) ? { tool: d.tool as string } : {}),
    ...(optStr(d.token) ? { token: d.token as string } : {}),
  };
}
