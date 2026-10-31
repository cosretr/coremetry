// stepView — CoSRE v2 araç seti dilim 5 (docs/cosre/tools-audit.md G4/Q6/Q9;
// mockup onayı 2026-10-31). Asistan mesajının parçalarından adım kartlarının
// SAF görünüm modeli:
//   • dynamic-tool (s<i>) → kart sırası, argümanlar, ham çıktı (önizleme satırları);
//   • data-tool-result (aynı s<i>) → kind (text | evidence | error | skipped),
//     kaynak / satır / seri / env / kısaltma etiketleri, evidence.query, page_url;
//   • data-stop → listenin altındaki tek satır.
// v1 ChatBubble / toChatTurn'e dokunmaz; parçalar protocol.ts ayrıştırıcılarından.
// Önizleme satırları yalnız bilinen v2 araçlarının JSON gövdesinden (≤3);
// page_url yoksa satırın href'i YOK (tıklanmaz görünür).
import type { CosreStop, CosreToolResult, CosreToolResultKind } from '@/lib/types';
import { parseDataStop, parseDataToolResult } from '@/lib/cosre/protocol';
import { stepIndex } from '@/lib/cosre/toChatTurn';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { serviceHref } from '@/lib/serviceHref';
import { EVIDENCE_STRINGS as T, PAGE_LABEL_DEFAULT, PAGE_LABELS, SKIP_REASON_LABELS, STOP_LABELS, TOOL_TITLES } from './strings';

type Part = CosreUIMessage['parts'][number];
type Rec = Record<string, unknown>;
const rec = (v: unknown): Rec => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Rec) : {});
const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined);
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

export type StepKind = CosreToolResultKind | 'running';

export interface StepTag { label: string; warn?: boolean }
export interface PreviewColumn { label: string; numeric?: boolean }
export interface PreviewCell { text: string; bad?: boolean; mono?: boolean }
export interface PreviewRow { key: string; cells: PreviewCell[]; href?: string }
export interface StepPreview { columns: PreviewColumn[]; rows: PreviewRow[]; more: number; moreUnknown?: boolean }

export interface StepView {
  id: string;
  i: number;
  tool: string;
  kind: StepKind;
  title: string;
  subtitle?: string;
  durationMs?: number;
  tags: StepTag[];
  query?: string;
  queryKind?: string;
  source?: string;
  reason?: string;
  errorText?: string;
  pageUrl?: string;
  pageLabel?: string;
  preview?: StepPreview;
}

export interface StepsView {
  steps: StepView[];
  stop?: CosreStop;
  totalMs: number;
}

export const PREVIEW_MAX_ROWS = 3;

// ── biçim (tr-TR) ──
const nf = (d: number) => new Intl.NumberFormat('tr-TR', { maximumFractionDigits: d, minimumFractionDigits: 0 });
export function fmtNumTR(v: number, digits = 1): string { return nf(digits).format(v); }
export function fmtPctTR(v: number): string { return `%${fmtNumTR(v, 1)}`; }
export function fmtMsTR(ms: number): string {
  if (ms < 1000) return `${fmtNumTR(Math.round(ms), 0)} ms`;
  return `${fmtNumTR(ms / 1000, 1)} sn`;
}

/** data-stop → listenin altındaki tek satır (neden · çağrı · tur · süre). */
export function stopLine(s: CosreStop): string {
  return `${STOP_LABELS[s.reason]} · ${T.calls(s.calls)} · ${T.rounds(s.rounds)} · ${fmtMsTR(s.elapsed_ms)}`;
}

/** Kaynak etiketi: tool-result source (kanıt zarfı) olduğu gibi; "vm" kısaltması açılır. */
function sourceLabel(s: string | undefined): string | undefined {
  if (!s) return undefined;
  return s === 'vm' ? 'VictoriaMetrics' : s;
}

/** query_kind → sorgu açılırının etiketi. */
export function queryKindLabel(k: string | undefined): string {
  switch (k) {
    case 'promql': return 'PromQL';
    case 'sql': return 'SQL';
    case 'lucene': case 'es': return 'ES';
    case 'summary': return 'özet';
    default: return 'sorgu';
  }
}

/** Hata metni: mcp.ToolErrorJSON {error, hint, detail} ya da düz metin. */
export function errorMessage(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  try {
    const e = rec(JSON.parse(raw));
    const msg = str(e.detail) ?? str(e.hint) ?? str(e.error);
    if (msg) return msg;
  } catch { /* düz metin */ }
  return raw;
}

export function skipReasonText(r: string | undefined): string | undefined {
  if (!r) return undefined;
  return SKIP_REASON_LABELS[r] ?? r;
}

function parseJSON(s: string | undefined): Rec | undefined {
  if (!s) return undefined;
  try {
    const v = JSON.parse(s);
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Rec) : undefined;
  } catch {
    return undefined;
  }
}

/** Argümanlardan kısa alt başlık (bilinen araçlar; gerisi servis). */
export function subtitleFor(tool: string, args: Rec, body: Rec | undefined): string | undefined {
  const svc = str(args.service) ?? str(body?.service);
  const nSvcs = arr(args.services).length;
  const scope = svc ?? (nSvcs > 0 ? `${nSvcs} servis` : undefined);
  const parts: (string | undefined)[] = [];
  switch (tool) {
    case 'endpoint_metrics':
      parts.push(svc, str(args.route) ?? str(body?.route) ?? 'en yoğun 10 rota');
      break;
    case 'find_traces':
      parts.push(scope, str(args.operation), args.status === 'error' ? 'hatalı' : undefined, args.sort_by === 'slowest' ? 'en yavaş' : undefined);
      break;
    case 'list_my_services':
      parts.push(str(args.team) ?? str(body?.team), num(body?.matched_services) !== undefined ? `${num(body?.matched_services)} servis` : undefined);
      break;
    case 'search_logs':
      parts.push(scope, str(args.words) ? `“${str(args.words)}”` : undefined, str(args.severity));
      break;
    default:
      parts.push(scope);
  }
  const out = parts.filter(Boolean).join(', ');
  return out || undefined;
}

/** endpoint_metrics satırının sayfa bağlantısı: page_url'nin `search`'ü o rota. */
function withSearch(pageUrl: string, route: string): string {
  const [path, qs = ''] = pageUrl.split('?');
  const q = new URLSearchParams(qs);
  q.set('search', route);
  return `${path}?${q.toString()}`;
}

/** Bilinen v2 aracının gövdesinden ≤3 önizleme satırı (SAF). page_url yoksa href yok. */
export function previewFor(tool: string, body: Rec | undefined, pageUrl: string | undefined): StepPreview | undefined {
  if (!body) return undefined;
  const hasMore = body.has_more === true || body.truncated === true;
  const make = (columns: PreviewColumn[], list: unknown[], row: (r: Rec, i: number) => PreviewRow | null): StepPreview | undefined => {
    const all = list.map(rec).map(row).filter((r): r is PreviewRow => r !== null);
    if (all.length === 0) return undefined;
    const rows = all.slice(0, PREVIEW_MAX_ROWS);
    return { columns, rows, more: all.length - rows.length, ...(hasMore ? { moreUnknown: true } : {}) };
  };
  switch (tool) {
    case 'endpoint_metrics': {
      const top = body.mode === 'top';
      const cols: PreviewColumn[] = [{ label: 'Rota' }, { label: 'istek/sn', numeric: true }, { label: '5xx', numeric: true }];
      if (!top) cols.push({ label: 'p95', numeric: true });
      return make(cols, arr(body.endpoints), r => {
        const route = str(r.route);
        if (!route) return null;
        const e = num(r.err5xx_pct), p95 = num(r.p95_ms);
        const cells: PreviewCell[] = [
          { text: str(r.method) ? `${str(r.method)} ${route}` : route, mono: true },
          { text: num(r.rps) !== undefined ? fmtNumTR(num(r.rps)!, 1) : '—' },
          { text: e !== undefined ? fmtPctTR(e) : '—', bad: e !== undefined && e >= 1 },
        ];
        if (!top) cells.push({ text: p95 !== undefined ? fmtMsTR(p95) : '—' });
        return { key: route, cells, ...(pageUrl ? { href: withSearch(pageUrl, route) } : {}) };
      });
    }
    case 'find_traces':
      return make([{ label: 'Kök operasyon' }, { label: 'süre', numeric: true }, { label: 'durum' }], arr(body.traces), r => {
        const id = str(r.id);
        if (!id) return null;
        const bad = r.error === true;
        return {
          key: id,
          cells: [
            { text: str(r.root_op) ?? id, mono: true },
            { text: num(r.ms) !== undefined ? fmtMsTR(num(r.ms)!) : '—' },
            { text: bad ? 'hata' : 'ok', bad },
          ],
          ...(pageUrl ? { href: `/trace/${encodeURIComponent(id)}` } : {}),
        };
      });
    case 'list_my_services':
      return make([{ label: 'Servis' }, { label: 'hata', numeric: true }, { label: 'p99', numeric: true }], arr(body.services), r => {
        const name = str(r.service);
        if (!name) return null;
        const e = num(r.error_rate_pct), p99 = num(r.p99_ms);
        return {
          key: name,
          cells: [
            { text: name, mono: true },
            { text: e !== undefined ? fmtPctTR(e) : '—', bad: e !== undefined && e >= 1 },
            { text: p99 !== undefined ? fmtMsTR(p99) : '—' },
          ],
          ...(pageUrl ? { href: serviceHref(name) } : {}),
        };
      });
    default:
      return undefined;
  }
}

/** Etiketler: araç adı, kaynak, satır/seri, env, kısaltma. */
function tagsFor(tool: string, tr: CosreToolResult | undefined): StepTag[] {
  const tags: StepTag[] = [{ label: tool }];
  const src = sourceLabel(tr?.source);
  if (src) tags.push({ label: src });
  if (tr?.series !== undefined) tags.push({ label: T.seriesTag(tr.series) });
  else if (tr?.rows !== undefined) tags.push({ label: T.rowsTag(tr.rows) });
  if (tr?.env) tags.push({ label: T.envTag(tr.env) });
  if (tr?.truncated) tags.push({ label: T.truncated, warn: true });
  return tags;
}

interface ToolPartLike { toolCallId: string; toolName: string; state: string; input?: unknown; output?: unknown; errorText?: string }

/** Parçalar → adım kartları + durdurma satırı (SAF; bilinmeyen parça yok sayılır). */
export function stepsView(parts: readonly Part[]): StepsView {
  const results = new Map<string, CosreToolResult>();
  let stop: CosreStop | undefined;
  const tools: ToolPartLike[] = [];
  for (const p of parts) {
    if (p.type === 'data-tool-result') {
      const tr = parseDataToolResult(p as { type: string; id?: unknown; data?: unknown });
      if (tr) results.set(tr.id, tr);
    } else if (p.type === 'data-stop') {
      stop = parseDataStop(p as { type: string; data?: unknown }) ?? stop;
    } else if (p.type === 'dynamic-tool') {
      tools.push(p as unknown as ToolPartLike);
    }
  }
  const steps: StepView[] = [];
  let totalMs = 0;
  for (const t of tools) {
    const i = stepIndex(t.toolCallId);
    if (i === undefined) continue;
    const tr = results.get(t.toolCallId);
    const out = rec(t.output);
    const input = rec(t.input);
    const args = rec(input.args);
    const body = parseJSON(str(out.preview));
    let kind: StepKind = tr?.kind ?? 'running';
    if (!tr) {
      if (t.state === 'output-error') kind = 'error';
      else if (t.state === 'output-available') kind = out.skipped === true ? 'skipped' : out.ok === false ? 'error' : 'text';
    }
    const durationMs = tr?.duration_ms ?? num(out.durationMs);
    if (durationMs !== undefined) totalMs += durationMs;
    const pageUrl = tr?.page_url;
    const view: StepView = {
      id: t.toolCallId, i, tool: t.toolName, kind,
      title: TOOL_TITLES[t.toolName] ?? t.toolName.replace(/_/g, ' '),
      tags: tagsFor(t.toolName, tr),
      ...(durationMs !== undefined ? { durationMs } : {}),
      ...(pageUrl ? { pageUrl, pageLabel: PAGE_LABELS[t.toolName] ?? PAGE_LABEL_DEFAULT } : {}),
    };
    const sub = kind === 'skipped' ? T.skippedTitle : subtitleFor(t.toolName, args, body);
    if (sub) view.subtitle = sub;
    if (kind === 'skipped') view.reason = skipReasonText(tr?.reason ?? str(out.reason));
    if (kind === 'error') view.errorText = errorMessage(tr?.text ?? t.errorText ?? str(out.preview));
    if (tr?.query) { view.query = tr.query; view.queryKind = tr.query_kind; }
    if (tr?.source) view.source = sourceLabel(tr.source);
    if (kind === 'evidence' || kind === 'text') {
      const pv = previewFor(t.toolName, body, pageUrl);
      if (pv) view.preview = pv;
    }
    steps.push(view);
  }
  return { steps, ...(stop ? { stop } : {}), totalMs };
}
