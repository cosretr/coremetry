import type { MouseEvent as ReactMouseEvent } from 'react';
import type { TimeRange } from './types';
import { activeLang } from './format/lang';
import { compact, fixed } from './format/number';
import { bytes } from './format/bytes';
import { durationNs, durationSec } from './format/duration';
import { agoTime, dateTime, relTime } from './format/time';

// Quick-range presets in seconds, ordered for the dropdown panel.
export const PRESET_SECONDS: Record<string, number> = {
  '5m':  300,    '15m': 900,    '30m': 1800,
  '1h':  3600,   '3h':  10800,  '6h':  21600,   '12h': 43200,
  '24h': 86400,  '2d':  172800, '7d':  604800,  '30d': 2592000,
};

// Preset display labels live in lib/i18n.ts ('range.5m' … 'range.30d',
// EN + TR) since the Grafana-parity picker; keep the keys in lockstep
// with PRESET_SECONDS above.

// Converts a TimeRange to absolute nanosecond bounds for API queries.
export function timeRangeToNs(range: TimeRange): { from: number; to: number } {
  if (range.preset === 'custom' && range.fromMs && range.toMs) {
    return { from: range.fromMs * 1_000_000, to: range.toMs * 1_000_000 };
  }
  const secs = PRESET_SECONDS[range.preset] ?? 86400;
  const now = Date.now();
  return {
    from: Math.floor((now - secs * 1000) * 1_000_000),
    to: now * 1_000_000,
  };
}

// rangeToSince — TimeRange → a duration string the Go backend can
// actually parse, plus the effective window so the caller can LABEL it.
//
// v0.9.257. Closes two silent-wrong-data traps:
//
//  1. Go's time.ParseDuration has no day unit. `internal/api.parseDuration`
//     falls back to the endpoint default when parsing fails, so sending
//     '7d' — a real PRESET_SECONDS key — silently yields 1h of data with
//     nothing in the UI admitting it. pages/service/Overview.tsx shipped
//     exactly that table ('2d': '2d', '7d': '7d'); Service.tsx and
//     ServiceBacktrace.tsx kept correct hand-written copies ('7d': '168h').
//     Emitting hours for anything ≥ 1d removes the trap for good.
//  2. Sampled / expensive endpoints need a ceiling. `capSeconds` clamps
//     AND reports `capped`, so the caller renders "24h (capped)" instead
//     of narrowing the window behind the operator's back — the same
//     silent lie as (1), one layer up.
//
// Fallback is 1h, NOT timeRangeToNs's 24h: every call site this replaced
// spelled `?? '1h'`, and these endpoints sample raw spans, so the cheap
// window is the safe default for an unrecognised preset.
// A duration string Go's time.ParseDuration actually accepts. There is NO day
// unit in Go — `'7d'` is a parse error, and internal/api.parseDuration answers
// a parse error with the endpoint's default, so a day-suffixed window silently
// collapses to something far smaller with nothing in the UI admitting it.
//
// v0.9.261 — encoded in the type system after the same defect turned up three
// separate times: the neighbours panel (v0.9.257), the Runbook audit trail
// (asked 30d, got 24h) and the Service instances strip (asked 7d, got 15m).
// Every api.ts method that forwards a window to parseDuration takes this type,
// so `api.auditLog('30d')` is now a compile error rather than a silent
// truncation discovered in prod.
export type GoDuration = `${number}h` | `${number}m` | `${number}s`;

export function rangeToSince(
  range: TimeRange,
  capSeconds?: number,
): { since: GoDuration; seconds: number; capped: boolean } {
  let secs: number;
  // `!= null`, not truthiness: timeRangeToNs above spells this `&&`, which
  // drops a fromMs of 0 into the preset branch. Harmless there today (0 ms
  // is 1970) but it is the wrong test, and it silently returns the WRONG
  // window rather than failing — exactly the class of bug this helper exists
  // to kill. Caught by the '91s' case in utils.test.ts.
  if (range.preset === 'custom' && range.fromMs != null && range.toMs != null) {
    secs = Math.max(1, Math.round((range.toMs - range.fromMs) / 1000));
  } else {
    secs = PRESET_SECONDS[range.preset] ?? 3600;
  }
  const capped = capSeconds !== undefined && secs > capSeconds;
  if (capped) secs = capSeconds as number;
  return { since: goDuration(secs), seconds: secs, capped };
}

// goDuration — whole seconds → the largest EXACT h/m/s unit. Never emits
// 'd' (see rangeToSince trap 1). Inexact values keep seconds rather than
// rounding, so a custom range never silently widens or narrows.
function goDuration(secs: number): GoDuration {
  if (secs % 3600 === 0) return `${secs / 3600}h`;
  if (secs % 60 === 0) return `${secs / 60}m`;
  return `${secs}s`;
}

// v0.10.1417 (operatör, System sayfası "2770.0M" / "68365.1M": "billion yazsın") —
// milyar ve trilyon basamakları; aksi hâlde büyük sayaçlar M ile uzuyordu.
// v0.10.2484 — gövde lib/format/number `compact`'ta; dil etkin arayüz dili
// (TR "1,2 bin" / "3,4 mn", EN "1.2K" / "3.4M"). Veri yoksa "—".
export function fmtNum(n: number): string {
  return compact(n, activeLang());
}

// fmtFixed — null-tolerant `.toFixed` for display sites (v0.8.305,
// quality bar S4). The API types say `number`, but a zero-traffic
// service can serialize a metric field as null; bare `.toFixed()`
// then throws mid-render and (post v0.8.298) blanks the route.
// Non-finite values render "—" — the app's established no-data glyph.
// v0.10.2484 — lib/format/number `fixed`: TR arayüzde ondalık virgül (A7).
export function fmtFixed(n: number | null | undefined, digits: number): string {
  return fixed(n, digits, activeLang());
}

// escapeHTML — minimal HTML-entity escape for safely
// interpolating untrusted strings into innerHTML template
// literals (chart tooltips, etc.). Covers the five chars
// that change the parse tree (& < > " '). Span attributes
// are operator-controllable but ultimately a malicious
// ingester could ship `service.name = "<script>"` and trip
// XSS at render time; wrapping any interpolation site keeps
// the rendered string inert.
export function escapeHTML(s: string): string {
  return String(s ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// Binary unit formatter — KiB / MiB / GiB / TiB. Hoisted from
// ServiceInfra.tsx where it was duplicated locally; the
// cardinality page also needs it for column-bytes columns.
// Canonical binary-unit byte formatter (v0.8.272 dedup — was
// re-implemented in adminstats/shared and AdminClickhouse with
// drifting precision/fallbacks).
// fixed — v0.10.2194: sabit ondalık (OpenShift Console gibi "248.3 MiB").
// v0.10.2484 — gövde lib/format/bytes (IEC, A7); TR ondalık virgül; veri yoksa "—".
export function fmtBytes(n: number, fixed?: number): string {
  return bytes(n, activeLang(), fixed);
}

// Canonical "ne kadar önce" çekirdeği (v0.8.463 dedup — dört bileşen
// üç FARKLI giriş birimiyle (sec/unixNs/ms) kendi kopyasını taşıyordu;
// CLAUDE.md'nin unit-mixing pitfall sınıfı). Giriş birimi fonksiyon
// adında AÇIK: fmtDurShort saniye alır, fmtAgoNs unix-nanosaniye.
// v0.10.2484 — lib/format/duration: 3 anlamlı hane, tek birim, dile göre
// ("51 s", "1,5 dk"); fmtAgoNs lib/format/time relTime ("5 dk önce" / "5 min ago").
export function fmtDurShort(seconds: number): string {
  return durationSec(Math.max(0, Math.round(seconds)), activeLang());
}

export function fmtAgoNs(unixNs: number): string {
  return agoTime(unixNs, activeLang());
}

// agoTR — fmtAgoNs'in Türkçe kardeşi. v0.9.1317'de components/ai/greeting.ts
// içinden BURAYA taşındı: yukarıdaki v0.8.463 notu bu dosyayı "ne kadar önce"
// yardımcılarının tek evi ilan ediyor, ve agoTR o konsolidasyonun dışında
// kalmış beşinci kopyaydı. greeting.ts onu buradan re-export eder, böylece
// kendi testi ve çağrı yeri aynen çalışır.
//
// v0.9.1329 — taşımayı gerekçelendiren ikinci tüketici (/services "Son
// görülme" kolonu) İngilizceye alındığı için ARTIK YOK; agoTR bugün yine
// tek tüketicili. Buradan geri taşımıyoruz: v0.8.463'ün kuralı "tüketici
// sayısı" değil "bu dosya bu yardımcıların tek evi". Bu şerh, yukarıdaki
// gerekçenin bayatladığını açıkça söylemek için duruyor — sessiz bıraksak
// yorum var olmayan bir tüketiciye atıf yapmaya devam ederdi.
//
// Giriş birimi adında AÇIK (unixNs), fmtAgoNs ile aynı sözleşme. nowMs
// enjekte edilebilir — testler sabit saatle koşar.
// v0.10.2484 — gövde lib/format/time relTime (TR çıktısı birebir aynı).
export function agoTR(unixNs: number, nowMs = Date.now()): string {
  return agoTime(unixNs, 'tr', nowMs);
}

// v0.10.2484 — lib/format/duration durationNs: "1,23 ms" / "850 µs" (3 anlamlı hane, boşluklu birim).
export function fmtNs(ns: number): string {
  return durationNs(ns, activeLang());
}

// v0.9.879 — the ONE clock / date+time formatter ("HH:mm:ss",
// "dd.mm.yyyy HH:mm:ss", 24-hour, hand-rolled, viewer timezone) and the ns
// wrappers tsShort / tsDateTime / tsLong / tsCompact / tsMinute. v0.10.2484 —
// bodies moved to lib/format/time; same names re-exported here.
export {
  clock as fmtClock, dateTime as fmtDateTime, tsShort, tsDateTime, tsLong, tsCompact, tsMinute,
} from './format/time';

// v0.10.1794 — Operator-reported (prod Service › Logs detayı): `timestamp` /
// `event.es_ingested` öznitelikleri ES'in ham UTC dizesi ("…T07:23:41Z")
// olarak basılıyordu; tablo 10:23 derken detay 07:23 "3 saat geri" okundu.
// ISO-8601 + dilim (Z / ±hh:mm / ±hhmm) biçimindeki dizgeyi tarayıcı yerel
// saatinde "dd.mm.yyyy HH:mm:ss.mmm" verir; başka her şey için null (ham
// değer olduğu gibi kalır — tam doğruluk).
const ISO_STAMP_RE = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)$/;
export function isoStampLocal(v: string): string | null {
  if (!ISO_STAMP_RE.test(v)) return null;
  const ms = Date.parse(v.replace(',', '.').replace(/([+-]\d\d)(\d\d)$/, '$1:$2'));
  if (Number.isNaN(ms)) return null;
  return dateTime(ms) + '.' + String(new Date(ms).getMilliseconds()).padStart(3, '0');
}

// tsRel — unix-ns göreli zaman, geçmiş ve gelecek. v0.10.2484 — lib/format/time
// relTime, dile göre ("3 gün önce" / "3 days ago", "12 sa sonra" / "in 12 h").
export function tsRel(ns: number): string {
  return relTime(ns, activeLang());
}

// hashColor (v0.9.398'de SÖKÜLDÜ) — üç bağımsız renk sistemi tekleşti:
// kanonik kaynak lib/chartFmt.ts seriesColor (FNV + 10'luk palet).
// Aynı servis artık topoloji/flame/neighbors/Explore/waterfall dahil
// her yüzeyde AYNI renk.

// Known messaging brokers. A topology node is "messaging" when the
// backend classified it as a messaging.system (kind:"queue") OR it
// landed as a peer.service'd external whose name is a broker (e.g.
// the demo's `ext:kafka` → kind:"external", subkind:"kafka"). Anchored
// so "kafka-broker-1" matches but "payment-sqs-service" (a real HTTP
// service that merely contains a broker substring) does not.
const MESSAGING_BROKER = /^(kafka|rabbitmq|rabbit|amqp|sqs|sns|pubsub|nats|activemq|kinesis|eventhubs?|servicebus|mqtt|pulsar|rocketmq|jms|stomp)\b/i;

// isMessagingDep — true for messaging-broker dependency nodes, however
// the backend labelled them. Topology views exclude these: a broadcast
// topic fans in/out to dozens of unrelated producers + consumers, which
// the layout can't separate ("kafka message devreye girince topoloji
// saçmalıyor"). Messaging keeps its own /messaging surface. Synchronous
// call edges (service→service grpc/http/rest/soap, service→db) stay.
export function isMessagingDep(kind?: string, subkind?: string): boolean {
  if (kind === 'queue') return true;
  return !!subkind && MESSAGING_BROKER.test(subkind.trim());
}

const SEV = ['', 'TRACE', 'TRACE2', 'TRACE3', 'TRACE4', 'DEBUG', 'DEBUG2', 'DEBUG3', 'DEBUG4',
             'INFO', 'INFO2', 'INFO3', 'INFO4', 'WARN', 'WARN2', 'WARN3', 'WARN4',
             'ERROR', 'ERROR2', 'ERROR3', 'ERROR4', 'FATAL', 'FATAL2', 'FATAL3', 'FATAL4'];
export function sevName(n: number): string { return SEV[n] || String(n); }
export function sevClass(n: number): string {
  if (n >= 21) return 's-fatal';
  if (n >= 17) return 's-error';
  if (n >= 13) return 's-warn';
  if (n >= 9)  return 's-info';
  if (n >= 5)  return 's-debug';
  return 's-trace';
}

// substituteVars expands ${name} references against a variables map.
// Lines whose ALL ${name} references resolve to empty strings are
// dropped — that's how an "all services" filter degrades cleanly:
// ${service} = '' makes `service.name = "${service}"` collapse to
// nothing instead of becoming a literal empty-string predicate.
//
// Used by the dashboard renderer to inject Grafana-style variables
// into spanmetric DSLs and other line-based config fields.
export function substituteVars(s: string, vars: Record<string, string>): string {
  if (!s) return s;
  // Multi-line case: drop a line if every variable on it is empty
  // (not "if any are empty" — partially-resolved lines may still be
  // useful, e.g. when the DSL combines a fixed predicate with a
  // variable one). For our presets the rule "all empty → drop" is
  // the right one.
  const lines = s.split('\n');
  const out: string[] = [];
  for (const line of lines) {
    const refs = [...line.matchAll(/\$\{([^}]+)\}/g)].map(m => m[1]);
    if (refs.length > 0 && refs.every(name => !vars[name])) continue;
    out.push(line.replace(/\$\{([^}]+)\}/g, (_, name) => vars[name] ?? ''));
  }
  return out.join('\n');
}

// rowClickHandlers: spread onto a row element to make it behave like a
// real anchor for `href` — left-click navigates SPA-style via `navigate`,
// middle-click or Cmd/Ctrl/Shift+click opens in a new tab. Plain
// router.push doesn't honor middle-click because the table row isn't
// an <a>; this restores the browser convention without restructuring the
// table layout.
//
//   <tr {...rowClickHandlers(`/trace?id=${t.traceId}`,
//                             () => router.push(`/trace?id=${t.traceId}`))}>
//
// v0.10.939 (tablo standardı T7) — satırın İÇİNDEKİ etkileşimli bir öğeden
// (a, button, input, role=button …) gelen olay YOK SAYILIR: o öğe kendi işini
// yapar (link gezinir, düğme eylemini çalıştırır). Eskiden satır da gezinirdi:
// hücredeki bir <a>'ya orta tık tarayıcının kendi yeni sekmesi + satırın
// window.open'ı = İKİ sekme; satırdaki düğmeye tık eylem + gezinme. Aynı
// koruma useDataTable'ın getRowHref yedeğinde (dt.rowProps) — o da bu
// yardımcıdan kurulur, ikinci bir kopya yok.
const ROW_INTERACTIVE = 'a[href], button, input, select, textarea, label, summary, '
  + '[role="button"], [role="link"], [role="menuitem"], [role="checkbox"], [role="switch"], [role="option"]';
/** v0.10.939 (tablo standardı T7) — olay satırın içindeki etkileşimli bir öğeden mi geldi?
 *  Satırın KENDİSİ (ör. role="button" satır) sayılmaz. */
export function fromInteractive(e: ReactMouseEvent): boolean {
  const t = e.target;
  if (!(t instanceof Element)) return false;
  const hit = t.closest(ROW_INTERACTIVE);
  return !!hit && hit !== e.currentTarget && e.currentTarget.contains(hit);
}
const openNewTab = (href: string) => { window.open(href, '_blank', 'noopener,noreferrer'); };

export function rowClickHandlers(href: string, navigate: () => void) {
  return {
    // v0.10.933 (tablo standardı T2) — el imleci + hover yalnız bu işareti
    // (ya da role="button" / .row-link) taşıyan satıra verilir (globals.css).
    'data-row-action': true as const,
    onClick: (e: ReactMouseEvent) => {
      if (fromInteractive(e)) return;
      // Left-click with a modifier → new tab. Match what an <a href> does.
      if (e.metaKey || e.ctrlKey || e.shiftKey) {
        openNewTab(href);
        return;
      }
      navigate();
    },
    // Middle button. preventDefault on mousedown stops Chrome's
    // auto-scroll widget; auxclick fires after and is what we react to.
    onAuxClick: (e: ReactMouseEvent) => {
      if (e.button !== 1 || fromInteractive(e)) return;
      e.preventDefault();
      openNewTab(href);
    },
    onMouseDown: (e: ReactMouseEvent) => {
      if (e.button === 1 && !fromInteractive(e)) e.preventDefault();
    },
  };
}

// displaySpanName builds the string shown in the waterfall + tooltips.
// Most OTel SDKs name spans well (e.g. "GET /api/orders", "SELECT users")
// but gRPC instrumentations often emit useless generic names — "grpc",
// "grpc command", or just the rpc.method on its own — which is unhelpful
// when 5 services are calling each other. When the raw name looks
// generic, build a richer label from the rpc.* / peer.service attributes.
//
// Rule of thumb:
//   - Names containing rpc.service + rpc.method already → leave alone.
//   - Otherwise, if rpc.service is set: "<rpc.service>/<rpc.method>".
//   - If only peer.service is set:        "<service.name> → <peer.service>".
//   - Else fall back to the raw name.
export function displaySpanName(s: {
  name: string;
  serviceName?: string;
  attributes?: Record<string, string>;
}): string {
  const a = s.attributes ?? {};
  const raw = (s.name ?? '').trim();
  const lc  = raw.toLowerCase();

  const rpcService = a['rpc.service'];
  const rpcMethod  = a['rpc.method'];
  const peer       = a['peer.service'];

  // OTel HTTP semconv: server.address (new) or http.host (old) carries
  // the target hostname; url.path / http.target / http.route carries
  // the path. Both new + old conventions are honoured because in-the-
  // wild SDKs are mid-migration.
  const serverAddr = a['server.address'] || a['http.host'] || a['net.peer.name'];
  const urlPath    = a['url.path']       || a['http.target'] || a['http.route'];

  // Bare HTTP verb? Some SDKs (especially the older Node + Python
  // instrumentations) name client spans literally "GET" / "POST" /
  // etc. Useless on its own — enrich with server.address + path
  // when available.
  const httpVerbs = new Set(['get', 'post', 'put', 'delete', 'patch', 'head', 'options']);
  if (httpVerbs.has(lc)) {
    if (serverAddr && urlPath) return `${raw} ${serverAddr}${urlPath}`;
    if (serverAddr)            return `${raw} ${serverAddr}`;
    if (urlPath)               return `${raw} ${urlPath}`;
    return raw;
  }

  // Bail when the existing name is already informative — anything that
  // contains a `/`, `.`, or whitespace and isn't the bare keywords below
  // is treated as descriptive (matches HTTP, DB, message-broker patterns).
  const generic = lc === 'grpc' || lc === 'grpc command' || lc === 'rpc' ||
                  lc === 'call' || (rpcMethod ? lc === rpcMethod.toLowerCase() : false);

  if (!generic) return raw;

  if (rpcService && rpcMethod) {
    if (peer && peer !== rpcService) {
      return `${peer}/${rpcService}/${rpcMethod}`;
    }
    return `${rpcService}/${rpcMethod}`;
  }
  if (rpcMethod && peer) {
    return `${peer}.${rpcMethod}`;
  }
  if (peer && s.serviceName) {
    return `${s.serviceName} → ${peer}`;
  }
  return raw;
}
