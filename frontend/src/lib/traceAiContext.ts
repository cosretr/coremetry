// traceAiContext.ts — v0.10.944 (CoSRE araştırma asistanı, Faz A): trace
// sayfasının CoSRE çekmecesine devrettiği BAĞLAM.
//
// Sorun: "CoSRE'ye sor" çekmecesi yalnız `trace:<id>` öznesini biliyordu.
// Operatörün seçtiği span, span'in servisi, ortamı (env), cluster/namespace
// ve trace'in zaman penceresi ne ekranda görünüyor ne de takip sorularıyla
// sunucuya gidiyordu — "bu servisin logları" sorusu hangi env'e, hangi
// pencereye bakılacağını tahmin etmek zorunda kalıyordu.
//
// Bu modül üç şey taşır, üçü de SAF (React yalnız en alttaki abonelikte):
//   1. traceChatContext — yüklü span'lerden bağlam (EK İSTEK YOK). Kaynak
//      özniteliklerini TEK çözücü okur: lib/otel/semconv resolveResource
//      (öncelik zinciri backend'le aynı); burada ikinci bir anahtar listesi
//      yazmak, CLUSTER_KEYS/resolveResource ayrışmasının (traceMetrics.ts)
//      yeni bir kopyası olurdu.
//   2. traceContextToPage / traceChatWindow — sohbet isteğinin `page`
//      (PageContext) ve rangeS/toMs alanları.
//   3. Küçük bir depo (publish/useTraceAiContext) — Trace.tsx ve
//      TraceKiosk.tsx yayınlar, AppShell'deki tek CopilotChat okur. Prop
//      zinciri YOK: çekmece sayfanın çocuğu değil (AppShell mount'u).
import { useSyncExternalStore } from 'react';
import { resolveResource } from './otel/semconv';
import { displaySpanName } from './utils';
import type { PageContext, SpanRow } from './types';

export interface TraceAiContext {
  traceId: string;
  /** Yalnız operatör bir span SEÇMİŞSE. */
  spanId?: string;
  spanName?: string;
  /** Seçili span'in servisi; seçim yoksa kök span'inki. */
  service: string;
  env?: string;
  cluster?: string;
  namespace?: string;
  pod?: string;
  version?: string;
  /** Trace'in kapsamı (unix ns): ilk span başlangıcı → son span bitişi. 0 = bilinmiyor. */
  fromNs: number;
  toNs: number;
}

export type TraceCtxSpan = Pick<SpanRow, 'spanId' | 'parentSpanId' | 'name' | 'serviceName' | 'startTime' | 'endTime'> & {
  resourceAttributes?: Record<string, string> | null;
  attributes?: Record<string, string> | null;
};

// pickRoot — ebeveyni yok (ya da ebeveyni bu trace'te değil) olanlardan EN
// ERKEN başlayan; hiçbiri yoksa en erken span. Backend'in explain girdisiyle
// aynı kural (explain_trace_input.go: ebeveynsiz span, yoksa en erken).
function pickRoot<S extends TraceCtxSpan>(spans: S[]): S {
  const ids = new Set(spans.map(s => s.spanId));
  let best: S | null = null;
  for (const s of spans) {
    if (s.parentSpanId && ids.has(s.parentSpanId)) continue;
    if (!best || s.startTime < best.startTime) best = s;
  }
  if (best) return best;
  let first = spans[0];
  for (const s of spans) if (s.startTime < first.startTime) first = s;
  return first;
}

/**
 * traceChatContext — yüklü span'lerden sohbet bağlamı. SAF.
 *
 * Servis/env/cluster/namespace/pod/sürüm ODAK span'den: seçili span varsa o,
 * yoksa kök. Kaynak öznitelikleri süreç başına sabit olduğu için odak
 * span'in kaynağı o servisin kimliğidir; başka bir servisin env'ini ödünç
 * almak (ör. trace'in ilk span'i) iki ortamı birleştiren bir trace'te
 * yanlış ortamı söylerdi.
 *
 * Pencere trace'in TAMAMI (min start → max end); spread yerine döngü (50k).
 */
export function traceChatContext(
  spans: readonly TraceCtxSpan[] | null | undefined,
  opts: { traceId: string; spanId?: string | null },
): TraceAiContext | null {
  const traceId = (opts.traceId ?? '').trim();
  if (!traceId || !spans || spans.length === 0) return null;
  const list = spans as TraceCtxSpan[];
  let fromNs = Infinity;
  let toNs = -Infinity;
  for (const s of list) {
    if (s.startTime > 0 && s.startTime < fromNs) fromNs = s.startTime;
    if (s.endTime > toNs) toNs = s.endTime;
  }
  if (!Number.isFinite(fromNs) || !Number.isFinite(toNs) || toNs < fromNs) { fromNs = 0; toNs = 0; }

  const sel = opts.spanId ? list.find(s => s.spanId === opts.spanId) ?? null : null;
  const focus = sel ?? pickRoot(list);
  const id = resolveResource(focus.resourceAttributes ?? undefined);
  const ctx: TraceAiContext = {
    traceId,
    service: focus.serviceName || id.serviceName || '',
    fromNs,
    toNs,
  };
  if (sel) {
    ctx.spanId = sel.spanId;
    const nm = displaySpanName({ name: sel.name, serviceName: sel.serviceName, attributes: sel.attributes ?? undefined });
    if (nm) ctx.spanName = nm;
  }
  if (id.deploymentEnvironment) ctx.env = id.deploymentEnvironment;
  if (id.cluster) ctx.cluster = id.cluster;
  if (id.k8s.namespace) ctx.namespace = id.k8s.namespace;
  if (id.k8s.pod) ctx.pod = id.k8s.pod;
  if (id.serviceVersion) ctx.version = id.serviceVersion;
  return ctx;
}

// ── Sohbet isteği + kalıcı anlık görüntü ──

/**
 * traceContextToPage — sohbetin `page` alanı (Go aynası agentctx.PageContext).
 * timeRange trace'in KENDİ penceresi, mutlak (custom) — pad'siz; pad'li
 * pencere rangeS/toMs'te (traceChatWindow). ms'e yuvarlama DIŞA doğru
 * (from aşağı, to yukarı) ve en az 1 ms: 3 ms'lik bir trace sıfır genişlikli
 * pencereye çökmesin (sunucu rangeTR iki ucu da > 0 ister).
 */
export function traceContextToPage(ctx: TraceAiContext): PageContext {
  const p: PageContext = { page: 'trace', path: '/trace', traceId: ctx.traceId };
  if (ctx.spanId) p.spanId = ctx.spanId;
  if (ctx.service) p.service = ctx.service;
  if (ctx.env) p.env = ctx.env;
  if (ctx.cluster) p.cluster = ctx.cluster;
  if (ctx.namespace) p.namespace = ctx.namespace;
  if (ctx.pod) p.pod = ctx.pod;
  if (ctx.fromNs > 0 && ctx.toNs >= ctx.fromNs) {
    const fromMs = Math.floor(ctx.fromNs / 1e6);
    const toMs = Math.max(Math.ceil(ctx.toNs / 1e6), fromMs + 1);
    p.timeRange = { preset: 'custom', fromMs, toMs };
  }
  return p;
}

/** Sohbet penceresinin trace kapsamına eklenen pay (her iki uç). */
export const TRACE_CHAT_PAD_MS = 5 * 60_000;

/**
 * traceChatWindow — sohbetin rangeS/toMs alanları: trace penceresi ±5 dk.
 * Sunucu toMs'i BİTİŞ çapası, rangeS'i süre olarak okur (chatAnchorTime).
 * Bitiş şimdiden ilerideyse şimdiye kırpılır: sunucu gelecekteki çapayı
 * reddedip ŞİMDİYE düşer ve pencere sessizce kayardı. Pencere bilinmiyorsa null.
 */
export function traceChatWindow(ctx: Pick<TraceAiContext, 'fromNs' | 'toNs'>, nowMs: number): { rangeS: number; toMs: number } | null {
  if (!(ctx.fromNs > 0) || !(ctx.toNs >= ctx.fromNs)) return null;
  const fromMs = Math.floor(ctx.fromNs / 1e6) - TRACE_CHAT_PAD_MS;
  let toMs = Math.ceil(ctx.toNs / 1e6) + TRACE_CHAT_PAD_MS;
  if (nowMs > 0 && toMs > nowMs) toMs = Math.max(Math.floor(nowMs), fromMs + 1000);
  return { rangeS: Math.max(1, Math.ceil((toMs - fromMs) / 1000)), toMs };
}

// ── Şeritteki zaman penceresi ──

function localParts(ms: number, tz: string | undefined): { date: string; time: string } {
  const f = new Intl.DateTimeFormat('tr-TR', {
    timeZone: tz || undefined,
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  });
  const parts: Record<string, string> = {};
  for (const p of f.formatToParts(new Date(ms))) parts[p.type] = p.value;
  const frac = String(((Math.floor(ms) % 1000) + 1000) % 1000).padStart(3, '0');
  return {
    date: `${parts.day}.${parts.month}.${parts.year}`,
    time: `${parts.hour}:${parts.minute}:${parts.second}.${frac}`,
  };
}

/**
 * formatTraceClock — v0.10.2480: tek bir anın saati (SS:DD:ss.mmm), Window
 * çipiyle AYNI kural: tarayıcı (izleyici) saat diliminde, dilim etiketiyle.
 * Sunucu kanıtı unix ns taşır; UTC metin basılmaz. `tz` test dikişi.
 */
export function formatTraceClock(ns: number, tz?: string): { time: string; tzLabel: string } | null {
  if (!(ns > 0)) return null;
  const ms = ns / 1e6;
  let zone = tz;
  let p: { date: string; time: string };
  try {
    p = localParts(ms, zone);
  } catch {
    zone = 'UTC';
    p = localParts(ms, zone);
  }
  let label = zone || '';
  if (!label) {
    try { label = Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''; } catch { label = ''; }
  }
  return { time: p.time, tzLabel: label || offsetLabel(ms) };
}

function offsetLabel(ms: number): string {
  const off = -new Date(ms).getTimezoneOffset();
  const sign = off >= 0 ? '+' : '−';
  const a = Math.abs(off);
  return `UTC${sign}${String(Math.floor(a / 60)).padStart(2, '0')}:${String(a % 60).padStart(2, '0')}`;
}

/**
 * formatTraceWindow — şeridin pencere metni, tr-TR biçiminde, TARAYICI saat
 * diliminde (sunucu sonuçları UTC ISO; ekran operatörün yerel saati) ve saat
 * dilimi ETİKETİYLE — etiketsiz bir saat hangi dilimde olduğunu söylemez.
 * Aynı gün → bitişte yalnız saat. Milisaniye var: çoğu trace saniyenin
 * altında, saniye çözünürlüğü "14:03:12 → 14:03:12" gibi boş bir aralık
 * gösterirdi. `tz` test dikişi; boşsa tarayıcı dilimi.
 */
export function formatTraceWindow(fromNs: number, toNs: number, tz?: string): { text: string; tzLabel: string } | null {
  if (!(fromNs > 0) || !(toNs >= fromNs)) return null;
  const a = fromNs / 1e6;
  const b = toNs / 1e6;
  let zone = tz;
  let from: { date: string; time: string };
  let to: { date: string; time: string };
  try {
    from = localParts(a, zone);
    to = localParts(b, zone);
  } catch {
    // Tanınmayan dilim adı RangeError atar: UTC'ye düş ve bunu SÖYLE.
    zone = 'UTC';
    from = localParts(a, zone);
    to = localParts(b, zone);
  }
  let label = zone || '';
  if (!label) {
    try { label = Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''; } catch { label = ''; }
  }
  if (!label) label = offsetLabel(a);
  const text = from.date === to.date
    ? `${from.date} ${from.time} → ${to.time}`
    : `${from.date} ${from.time} → ${to.date} ${to.time}`;
  return { text, tzLabel: label };
}

// ── Depo: sayfa yayınlar, çekmece okur ──
//
// TEK yuva, trace kimliğiyle anahtarlı: çekmece yalnız KENDİ öznesinin
// bağlamını görür (useTraceAiContext(traceId) kimlik eşleşmezse null) — başka
// bir trace'in bağlamı yanlış özneye sızmaz. Eşit içerikli yeniden yayın
// dinleyicileri UYANDIRMAZ (span seçimi dışındaki render'lar yeniden yayınlıyor).

let current: TraceAiContext | null = null;
const listeners = new Set<() => void>();

function sameCtx(a: TraceAiContext | null, b: TraceAiContext | null): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.traceId === b.traceId && a.spanId === b.spanId && a.spanName === b.spanName
    && a.service === b.service && a.env === b.env && a.cluster === b.cluster
    && a.namespace === b.namespace && a.pod === b.pod && a.version === b.version
    && a.fromNs === b.fromNs && a.toNs === b.toNs;
}

export function publishTraceAiContext(ctx: TraceAiContext | null): void {
  if (sameCtx(current, ctx)) return;
  current = ctx;
  for (const l of listeners) l();
}

/** Sayfa ayrılırken: yuva HÂLÂ bu trace'inse boşalt (yeni sayfa önce yayınlamış olabilir). */
export function clearTraceAiContext(traceId: string): void {
  if (current && current.traceId === traceId) publishTraceAiContext(null);
}

export function getTraceAiContext(traceId?: string | null): TraceAiContext | null {
  if (!current) return null;
  return !traceId || current.traceId === traceId ? current : null;
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => { listeners.delete(l); };
}

const snapshot = () => current;
const serverSnapshot = () => null;

/** Çekmecenin öznesi `traceId` ise yayınlanan bağlam; değilse null. */
export function useTraceAiContext(traceId: string | null | undefined): TraceAiContext | null {
  const snap = useSyncExternalStore(subscribe, snapshot, serverSnapshot);
  return snap && traceId && snap.traceId === traceId ? snap : null;
}
