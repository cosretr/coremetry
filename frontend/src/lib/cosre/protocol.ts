// protocol.ts — v0.10.1598 (CoSRE v2 Faz 1, R8): CoSRE v2 sohbet akışının
// (POST /api/cosre/v2/chat) tel sözleşmesi — Go tarafı internal/cosre/uistream.
//
// Vercel AI SDK v5 "UI Message Stream" biçimi: `data: <json>` çerçeve başına bir
// parça, son satır `data: [DONE]`, yanıt başlığı
// `x-vercel-ai-ui-message-stream: v1`. `ai` paketi frontend'de HENÜZ yok
// (bağımlılık eklemek operatör onayı ister); bu dosya v5 dokümanına göre yazıldı.
// Faz 2'de `ai` pinlendiğinde parça adları node_modules/ai tiplerine karşı
// doğrulanır ve sürüm burada sabitlenir.
//
// Kapalı kümeler: Go çeviricisi yeni bir parça türü ya da data adı üretirse
// uiStreamFixture.test.ts (paylaşılan testdata/cosre/*.sse) kırmızıya döner;
// fixture yenilendiği commit'te bu dosya da güncellenir.

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
  ...BLOCK_TYPES.map(t => `block-${t}` as const),
] as const;

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
