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

import type { CosreApproval, CosreScope, CosreScopeReason, CosreStop, CosreStopReason, CosreToolResult, CosreToolResultKind } from '@/lib/types';

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
