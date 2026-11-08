// httpMetrics.ts — v0.10.1244 (operatör: "OpenTelemetry HTTP Metrics"
// Grafana panosunun aynısı servis sayfasında, VictoriaMetrics'ten). SAF:
// PromQL üreticileri, etiket ayrıştırma, lejant ve tablo birleştirme.
//
// Sorgular panodakiyle aynı (http_server_* inbound, http_client_* downstream;
// service_name / http_route / http_request_method / http_response_status_code /
// server_address / error_type etiketleri). Grafana değişkenlerinin karşılığı:
//   $app            → servis adı
//   $route/$method  → seçiliyse tam eşitlik, değilse süzgeç yok
//   $__rate_interval→ rateWindow(step)
//   $__range        → pencere saniyesi (range sorgusunun SON noktası okunur)
import type { SpanMetricSeries } from './types';
import { HTTP_FAMILY, statusClassOf, statusClassToken, statusCodeLabel, type RedFamilySpec } from './redFamily';

export interface HttpScope {
  app: string; route?: string; method?: string;
  /**
   * v0.10.1429 — RED ailesi (lib/redFamily.ts). Yoksa HTTP_FAMILY: her üretici
   * bugünküyle BAYT BAYT aynı (altın dosyalar). gRPC ailesinde yalnız
   * __name__ (rpc_server_duration_milliseconds_* / rpc_server_call_duration_
   * seconds_*), etiketler (rpc_method / rpc_service / rpc_grpc_status_code),
   * hata kümesi ve ms ölçeği (/1000) değişir; ifade gövdeleri aynı.
   */
  family?: RedFamilySpec;
  /** v0.10.1352 — route REGEX (`=~`, VM tam-çapalı); şekil kipi (`/users/:id`). route'u ezer. */
  routeRe?: string;
  /**
   * v0.10.1377 — ortam eşleyicisi (ortam kapsamının `matcher`'ı — v0.10.1429'dan
   * beri /red-family aile envScope'u; eski http-env-scope ucu v0.10.1437'de gitti;
   * ör. `deployment_environment_name="prod-eu-a"`); seçicide service_name'den
   * HEMEN sonra (Go altını httpMetrics.envScope.golden.json ile aynı yer).
   * Yoksa / boşsa her sorgu bugünküyle bayt bayt aynı.
   */
  envSel?: string;
  /**
   * v0.10.1976 — operatörün "her metrik" route dışlama desenleri (ANKORSUZ RE2; /api/metrics/route-exclusions).
   * Yalnız HTTP ailesinde ve açık route süzgeci yokken `route!~".*(?:p).*"` olarak eklenir — sunucu metrik
   * yolunun (vmMetricSource.excluded) uyguladığı kümenin aynısı. Boş/yoksa sorgu bayt bayt eski.
   */
  routeExclude?: readonly string[];
}

// v0.10.1350 — VM etiket adları TEK yerde. Backend aynı OTel anahtarlarını
// promLabel ile buraya sanitize eder (internal/api/http_labels.go
// httpStatusKeyCandidates[0] → status); Go pin testi
// (TestHTTPLabelsMatchFrontend) iki dilin ayrışmasını kırar.
export const HTTP_LABELS = {
  service: 'service_name',
  route: 'http_route',
  method: 'http_request_method',
  status: 'http_response_status_code',
} as const;
const L = HTTP_LABELS;
const ST4 = `${L.status}=~"4.."`;
const ST5C = `${L.status}=~"5..|"`; // downstream: bağlantı hatası (boş kod) da hata

// v0.10.1429 — aile yardımcıları. famOf(scope) → HTTP varsayılanı; st5/st4
// ailenin hata kümeleri; scaleS ms ailesinde gecikme ifadesini saniyeye
// böler (karolar/eşikler/tablolar hep saniye okur, ölçek ailenin BEYAN
// ETTİĞİ birimden).
export const famOf = (s: Pick<HttpScope, 'family'> | undefined): RedFamilySpec => s?.family ?? HTTP_FAMILY;
const st5 = (F: RedFamilySpec) => `${F.labels.status}=~"${F.serverErr}"`;
const st4 = (F: RedFamilySpec) => `${F.labels.status}=~"${F.clientErr}"`;
const scaleS = (F: RedFamilySpec, expr: string) => (F.unit === 'ms' ? `(${expr}) / 1000` : expr);
/** Durum sınıfı ifadesi: HTTP `${1}xx` (bayt bayt eski); gRPC ok | client | server (kod kümeleri). */
function statusClassExpr(F: RedFamilySpec, inner: string, by = ''): string {
  const st = F.labels.status;
  if (F.kind === 'http') return `sum by (${by}cls) (label_replace(${inner}, "cls", "\${1}xx", "${st}", "([0-9]).*"))`;
  return `sum by (${by}cls) (label_replace(label_replace(label_replace(${inner}, "cls", "ok", "${st}", "^0$"), "cls", "client", "${st}", "^(${F.clientErr})$"), "cls", "server", "${st}", "^(${F.serverErr})$"))`;
}

/** PromQL dize kaçışı (" ve \). */
export function promStr(v: string): string {
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

/** Inbound seçici: service_name + isteğe bağlı ortam (v0.10.1377) + route/method. */
export function serverSel(s: HttpScope, extra = ''): string {
  const FL = famOf(s).labels; // v0.10.1429 — aile etiketleri (HTTP: HTTP_LABELS)
  const parts = [`${FL.service}=${promStr(s.app)}`];
  if (s.envSel) parts.push(s.envSel);
  if (s.routeRe !== undefined) parts.push(`${FL.route}=~${promStr(s.routeRe)}`);
  else if (s.route) parts.push(`${FL.route}=${promStr(s.route)}`);
  if (s.method) parts.push(`${FL.method}=${promStr(s.method)}`);
  if (s.routeExclude?.length && famOf(s).kind === 'http' && s.routeRe === undefined && !s.route) {
    for (const p of s.routeExclude) parts.push(`${FL.route}!~${promStr(`.*(?:${p}).*`)}`);
  }
  if (extra) parts.push(extra);
  return `{${parts.join(', ')}}`;
}

/** Downstream seçici (panoda route/method süzgeci yok; ortam v0.10.1377 — aynı kaynak etiketi). */
export function clientSel(s: HttpScope, extra = ''): string {
  return `{${L.service}=${promStr(s.app)}${s.envSel ? ', ' + s.envSel : ''}${extra ? ', ' + extra : ''}}`;
}

/** Grafik nokta bütçesi; adım = ceil(pencere / bu). */
export const HTTP_METRICS_POINTS = 300;

export function stepSeconds(windowSec: number): number {
  return Math.max(1, Math.ceil(windowSec / HTTP_METRICS_POINTS));
}

/** $__rate_interval karşılığı: en az 1 dk, en az 4 adım. */
export function rateWindow(windowSec: number): string {
  return `${Math.max(60, 4 * stepSeconds(windowSec))}s`;
}

const CC = 'http_client_request_duration_seconds_count';
const CB = 'http_client_request_duration_seconds_bucket';
const CS = 'http_client_request_duration_seconds_sum';

export interface HttpQuery { id: string; expr: string }

/** Pencere uzunluğu (`$__range`): en az 60 s. */
export function windowRange(windowSec: number): string {
  return `${Math.max(60, Math.round(windowSec))}s`;
}

/** `increase(m{sel}[W])`; offsetSec > 0 → bir önceki pencere (`offset Ns`). */
function winInc(s: HttpScope, windowSec: number, m: string, extra = '', offsetSec = 0): string {
  const off = offsetSec > 0 ? ` offset ${Math.round(offsetSec)}s` : '';
  return `increase(${m}${serverSel(s, extra)}[${windowRange(windowSec)}]${off})`;
}

/**
 * v0.10.1352 — route bazında (ya da bütün) yüzdelik zaman serisi:
 * `histogram_quantile(p, sum by (le[, by]) (rate(bucket[R])))`. Sekmenin
 * "Latency Yüzdelikleri" (by yok) ve "P95 gecikme (route)" (by = method,
 * route) panelleri ile endpoint sayfasının gecikme panelleri AYNI üretici.
 */
export function quantileQuery(s: HttpScope, windowSec: number, p: number, by = ''): string {
  const R = rateWindow(windowSec);
  const F = famOf(s);
  return scaleS(F, `histogram_quantile(${p}, sum by (le${by ? ', ' + by : ''}) (rate(${F.names.bucket}${serverSel(s)}[${R}])))`);
}

export interface HttpWindowQueries {
  total: string; err5Count: string; avg: string;
  p50: string; p95: string; p99: string; err5: string; err4: string;
}

/**
 * v0.10.1352 — GERÇEK PENCERE değerleri (anlık sorgu, `increase[W]`):
 * sekmenin "Endpoint Detayları" tablosu (by = method, route) ve endpoint
 * sayfasının RED karoları (by yok → tek seri) aynı üreticiden. Pencere
 * yüzdeliği = penceredeki kova artışlarının yüzdeliği (adım ortalaması
 * DEĞİL). offsetSec > 0 → aynı sorgular bir önceki pencere için (compare).
 */
export function windowQueries(s: HttpScope, windowSec: number, by = '', offsetSec = 0): HttpWindowQueries {
  const F = famOf(s);
  const { count: SC, bucket: SB, sum: SS } = F.names;
  const inc = (m: string, x = '') => winInc(s, windowSec, m, x, offsetSec);
  const agg = (x: string) => (by ? `sum by (${by}) (${x})` : `sum(${x})`);
  const qt = (p: number) => scaleS(F, `histogram_quantile(${p}, sum by (le${by ? ', ' + by : ''}) (${inc(SB)}))`);
  const ratio = (st: string) => `(${agg(inc(SC, st))} or (0 * ${agg(inc(SC))})) / ${agg(inc(SC))}`;
  return {
    total: agg(inc(SC)),
    err5Count: `${agg(inc(SC, st5(F)))} or (0 * ${agg(inc(SC))})`,
    avg: scaleS(F, `${agg(inc(SS))} / ${agg(inc(SC))}`),
    p50: qt(0.5), p95: qt(0.95), p99: qt(0.99),
    err5: ratio(st5(F)), err4: ratio(st4(F)),
  };
}

/** Panodaki sorguların tamamı (id → PromQL). W = pencere, R = rate penceresi. */
export function httpQueries(s: HttpScope, windowSec: number): Record<string, string> {
  const R = rateWindow(windowSec);
  const W = windowRange(windowSec);
  const F = famOf(s); // v0.10.1429 — aile (HTTP: bayt bayt eski)
  const { count: SC, bucket: SB } = F.names;
  const FL = F.labels;
  const ST5 = st5(F);
  const ST4 = st4(F);
  const sel = (x = '') => serverSel(s, x);
  const csel = (x = '') => clientSel(s, x);
  const rate = (m: string, x = '') => `rate(${m}${sel(x)}[${R}])`;
  const crate = (m: string, x = '') => `rate(${m}${csel(x)}[${R}])`;
  const cinc = (m: string, x = '') => `increase(${m}${csel(x)}[${W}])`;
  const q = (p: number) => quantileQuery(s, windowSec, p);
  const cq = (p: number, by = 'le') => `histogram_quantile(${p}, sum by (${by}) (${crate(CB)}))`;
  const byMR = `${FL.method}, ${FL.route}`;
  const byDM = `server_address, ${L.method}`;
  const tw = windowQueries(s, windowSec, byMR);
  return {
    // Genel bakış
    total: `sum(${winInc(s, windowSec, SC)})`,
    rps: `sum(${rate(SC)})`,
    err5: `(sum(${rate(SC, ST5)}) or vector(0)) / sum(${rate(SC)})`,
    err4: `(sum(${rate(SC, ST4)}) or vector(0)) / sum(${rate(SC)})`,
    p95: q(0.95),
    sli: `sum(${rate(SB, `le="${F.sliLe}"`)}) / sum(${rate(SC)})`,
    // Trafik
    rateByRoute: `sum by (${byMR}) (${rate(SC)})`,
    topRoutes: `topk(10, sum by (${FL.route}) (${winInc(s, windowSec, SC)}))`,
    statusClass: statusClassExpr(F, rate(SC)),
    statusCodes: `sum by (${FL.status}) (${winInc(s, windowSec, SC)})`,
    // Gecikme
    p50: q(0.5), p90: q(0.9), p99: q(0.99),
    p95ByRoute: quantileQuery(s, windowSec, 0.95, byMR),
    // Hatalar
    err5ByRoute: `sum by (${FL.route}, ${FL.method}, ${FL.status}) (${rate(SC, ST5)}) > 0`,
    err4ByRoute: `sum by (${FL.route}, ${FL.method}, ${FL.status}) (${rate(SC, ST4)}) > 0`,
    // Endpoint özeti
    tRps: `sum by (${byMR}) (rate(${SC}${sel()}[5m]))`, // panodaki gibi [5m]
    tTotal: tw.total, tAvg: tw.avg, tP95: tw.p95, tP99: tw.p99, tErr5: tw.err5, tErr4: tw.err4,
    // Downstream
    dRps: `sum(${crate(CC)})`,
    dErr: `(sum(${crate(CC, ST5C)}) or vector(0)) / sum(${crate(CC)})`,
    dP95: cq(0.95),
    dConnErr: `sum(${cinc(CC, `${L.status}=""`)}) or vector(0)`,
    dRateByAddr: `sum by (server_address) (${crate(CC)})`,
    dP95ByAddr: cq(0.95, 'le, server_address'),
    dErrByAddr: `sum by (server_address) (${crate(CC, ST5C)}) / sum by (server_address) (${crate(CC)})`,
    dErrTypes: `sum by (server_address, error_type) (${crate(CC, 'error_type!=""')}) > 0`,
    dtTotal: `sum by (${byDM}) (${cinc(CC)})`,
    dtRps: `sum by (${byDM}) (rate(${CC}${csel()}[5m]))`,
    dBars: `sort_desc(sum by (server_address) (rate(${CC}${csel()}[5m])))`, // bargauge (instant)
    dtAvg: `sum by (${byDM}) (${cinc(CS)}) / sum by (${byDM}) (${cinc(CC)})`,
    dtP95: `histogram_quantile(0.95, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtP99: `histogram_quantile(0.99, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtErr5: `(sum by (${byDM}) (${cinc(CC, ST5C)}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
    dtErr4: `(sum by (${byDM}) (${cinc(CC, ST4)}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
  };
}

/** groupKey (`k="v"` dizileri) → etiket haritası. */
export function seriesLabels(s: Pick<SpanMetricSeries, 'groupKey'>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const kv of s.groupKey ?? []) {
    const i = kv.indexOf('=');
    if (i <= 0) continue;
    let v = kv.slice(i + 1);
    if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
      v = v.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    }
    out[kv.slice(0, i)] = v;
  }
  return out;
}

/** Lejant: şablondaki {{label}} yerine değer (Grafana legendFormat). */
export function legendOf(tpl: string, labels: Record<string, string>): string {
  return tpl.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_, k: string) => labels[k] ?? '').trim();
}

/** Lejant: şablon dizesi ya da etiket → ad fonksiyonu (methodRouteLegend). */
export type LegendTpl = string | ((labels: Record<string, string>) => string);

/** Serileri lejant şablonuyla yeniden adlandırır (grafik bileşeni groupKey'i basar). */
export function relabel(series: SpanMetricSeries[] | null | undefined, tpl: LegendTpl, scale = 1): SpanMetricSeries[] {
  // v0.10.1245 — Go nil dilimi `points: null` gelebilir; boş say.
  return (series ?? []).map(s => {
    const pts = s.points ?? [];
    const labels = seriesLabels(s);
    return {
      groupKey: [(typeof tpl === 'function' ? tpl(labels) : legendOf(tpl, labels)) || '—'],
      points: scale === 1 ? pts : pts.map(p => ({ time: p.time, value: p.value * scale })),
    };
  });
}

/** Serinin son geçerli değeri (null = veri yok). */
export function lastValue(s: SpanMetricSeries | undefined): number | null {
  const pts = s?.points ?? [];
  for (let i = pts.length - 1; i >= 0; i--) {
    const v = pts[i].value;
    if (Number.isFinite(v)) return v;
  }
  return null;
}

/** Tek değerli sorgu (stat): ilk serinin son değeri. */
export function scalarOf(series: SpanMetricSeries[] | null | undefined): number | null {
  return lastValue(series?.[0]);
}

export interface BarRow { label: string; value: number }

/** Çok serili sorgunun son değerleri → azalan çubuk satırları (pasta yerine). */
export function barsOf(series: SpanMetricSeries[] | null | undefined, labelKey: string, max = 10): BarRow[] {
  const rows: BarRow[] = [];
  for (const s of series ?? []) {
    const v = lastValue(s);
    if (v === null || v <= 0) continue;
    rows.push({ label: seriesLabels(s)[labelKey] || '(boş)', value: v });
  }
  return rows.sort((a, b) => b.value - a.value).slice(0, max);
}

/** Tablo birleştirme: anahtar etiketlere göre her sorgunun son değeri bir sütun. */
export function mergeTable(
  cols: Record<string, SpanMetricSeries[] | null | undefined>, keys: readonly string[],
): Array<Record<string, string | number | null>> {
  const rows = new Map<string, Record<string, string | number | null>>();
  for (const [col, series] of Object.entries(cols)) {
    for (const s of series ?? []) {
      const l = seriesLabels(s);
      const id = keys.map(k => l[k] ?? '').join('\u0000');
      let row = rows.get(id);
      if (!row) {
        row = {};
        for (const k of keys) row[k] = l[k] ?? '';
        rows.set(id, row);
      }
      row[col] = lastValue(s);
    }
  }
  return [...rows.values()];
}

/** Route/method seçenekleri route-bazlı rate serisinden (ek sorgu yok). */
export function filterOptions(series: SpanMetricSeries[] | null | undefined, family: RedFamilySpec = HTTP_FAMILY): { routes: string[]; methods: string[] } {
  const routes = new Set<string>();
  const methods = new Set<string>();
  for (const s of series ?? []) {
    const l = seriesLabels(s);
    const r = l[family.labels.route];
    const m = l[family.labels.method];
    if (r && r !== 'none') routes.add(r);
    if (m) methods.add(m);
  }
  return { routes: [...routes].sort(), methods: [...methods].sort() };
}

// v0.10.1245 — operatör (prod): sekme "Cannot read properties of undefined
// (reading 'toLocaleString')" ile düştü. Tablo satırında bir sütunun serisi
// yoksa değer undefined geliyordu; biçimlendiriciler yalnız null'a bakıyordu.
// Saf ve toleranslı: null / undefined / NaN / Infinity → "—".
type MaybeNum = number | string | null | undefined;
const finite = (v: MaybeNum): v is number => typeof v === 'number' && Number.isFinite(v);

export function fmtCount(v: MaybeNum, digits = 0): string {
  return finite(v) ? v.toLocaleString('tr-TR', { maximumFractionDigits: digits }) : '—';
}
export function fmtRatio(v: MaybeNum): string {
  return finite(v) ? `%${(v * 100).toFixed(2)}` : '—';
}
export function fmtSeconds(v: MaybeNum, fmt: (n: number) => string): string {
  return finite(v) ? fmt(v) : '—';
}

// v0.10.1247 — Grafana paritesi: panodaki eşikler ve sabit seri renkleri.
// Renk yalnız sapan değerde (CLAUDE.md tablo standardı): yeşil = renksiz.
export type HttpTone = 'err' | 'warn' | undefined;
const fin = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);
/** Gecikme (s): panoda P95/P99 eşikleri 0,5 sn sarı, 1 sn kırmızı. */
export function latencyTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 1 ? 'err' : v >= 0.5 ? 'warn' : undefined;
}
/** Hata oranı (0–1): %1 sarı, %5 kırmızı (5xx ve 4xx sütunları). */
export function errorRatioTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 0.05 ? 'err' : v >= 0.01 ? 'warn' : undefined;
}
/** 4xx stat kartı: %5 sarı, %15 kırmızı (panodaki "4xx Oranı" eşiği). */
export function clientErrorTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 0.15 ? 'err' : v >= 0.05 ? 'warn' : undefined;
}
/** SLI < 500ms (0–1): %99 altı sarı, %95 altı kırmızı. */
export function sliTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v < 0.95 ? 'err' : v < 0.99 ? 'warn' : undefined;
}

/** Sabit seri renkleri (CSS token adı): status sınıfları ve yüzdelikler. */
export const HTTP_SERIES_TOKENS: Readonly<Record<string, string>> = {
  '2xx': '--ok', '3xx': '--info', '4xx': '--orange', '5xx': '--err',
  p50: '--ok', p90: '--warn', p95: '--orange', p99: '--err',
};

/** Token → çözülmüş renk haritası (resolve: CSS değişkeni okuyucu). */
export function httpSeriesColors(names: readonly string[], resolve: (token: string) => string): Map<string, string> {
  const out = new Map<string, string>();
  for (const n of names) {
    const tok = HTTP_SERIES_TOKENS[n];
    if (!tok) continue;
    const c = resolve(tok).trim();
    if (c) out.set(n, c);
  }
  return out;
}

// v0.10.1247 — Grafana birebir: donut / stat sparkline yardımcıları (heatmap v0.10.1258'de kaldırıldı).

export interface DonutSlice { label: string; value: number; pct: number }

/** Çubuk satırları → donut dilimleri (yüzde toplamı 100). */
export function donutSlices(rows: BarRow[]): DonutSlice[] {
  const total = rows.reduce((a, r) => a + r.value, 0);
  return rows.map(r => ({ label: r.label, value: r.value, pct: total > 0 ? (r.value / total) * 100 : 0 }));
}

// v0.10.1325 — operatör (prod, Status Code Dağılımı): "%0.01 olarak da
// dağılım gösterebilir, burada 0 yazıyor çoğu." Lejant payı tam sayıya
// yuvarlanıyordu (toFixed(0)); 40,5 Mn × 200 yanında 134,3 B × 400 "%0"
// okunuyordu. Uyarlanır hassasiyet, tr-TR ondalık virgül:
//   ≥ 99 ve < 100 → 2 ondalık, asla "%100" okunmaz (99,999 → %99,99)
//   ≥ 10          → tam sayı (%42)
//   ≥ 1           → 1 ondalık (%2,4)
//   ≥ 0,01        → 2 ondalık (%0,33)
//   (0, 0,01)     → "<%0,01"   · tam 0 → "%0"
const SHARE_FMT = [0, 1, 2].map(d => new Intl.NumberFormat('tr-TR', { maximumFractionDigits: d, useGrouping: false }));
/** Yüzde payı (0–100) → "%0,33" biçimi; null/NaN → "—". */
export function fmtShare(pct: MaybeNum): string {
  if (!finite(pct)) return '—';
  if (pct <= 0) return '%0';
  if (pct >= 100) return '%100';
  if (pct >= 99) return `%${SHARE_FMT[2].format(Math.min(Math.round(pct * 100) / 100, 99.99))}`;
  if (pct >= 10) return `%${SHARE_FMT[0].format(pct)}`;
  if (pct >= 1) return `%${SHARE_FMT[1].format(pct)}`;
  if (pct >= 0.01) return `%${SHARE_FMT[2].format(pct)}`;
  return '<%0,01';
}

/** Stat kartı trend çizgisi: ilk serinin değerleri (yoksa boş). */
export function sparkValues(series: SpanMetricSeries[] | null | undefined): (number | null)[] {
  return (series?.[0]?.points ?? []).map(p => (Number.isFinite(p.value) ? p.value : null));
}

/**
 * v0.10.1248 — Grafana'nın ANLIK (instant) sorguyla okuduğu paneller: stat
 * toplamı, pasta, tablo, bağlantı hataları, downstream bargauge. Bunlar
 * /api/v1/query ile `to` anında değerlendirilir (range sorgusunun son
 * noktası bir adım geride kalıyordu).
 */
export const HTTP_INSTANT_IDS: ReadonlySet<string> = new Set([
  'total', 'topRoutes', 'statusCodes', 'dConnErr', 'dBars',
  'tTotal', 'tRps', 'tAvg', 'tP95', 'tP99', 'tErr5', 'tErr4',
  'dtTotal', 'dtRps', 'dtAvg', 'dtP95', 'dtP99', 'dtErr5', 'dtErr4',
]);

// v0.10.1253 (operatör: Overview'da "Failure rate · metrik" yerine HTTP
// status; yalnız sınıflar değil kodlar) — status KODU başına istek hızı.
// v0.10.1377 — envSel: Overview'ın ortam eşleyicisi (yoksa bayt bayt eski sorgu).
// v0.10.1429 — family: gRPC ailesinde rpc_grpc_status_code (kodlar 0..16).
export function statusCodeRateQuery(app: string, windowSec: number, envSel?: string, family: RedFamilySpec = HTTP_FAMILY): string {
  return `sum by (${family.labels.status}) (rate(${family.names.count}${serverSel({ app, envSel, family })}[${rateWindow(windowSec)}]))`;
}

/** Kod serileri: ad = kod, sıra 2xx→5xx (kod artan); renk tokenı sınıftan. gRPC (v0.10.1429): ad "14 UNAVAILABLE", sıra sayısal, renk ok/client/server sınıfından. */
export function statusCodeSeries(series: SpanMetricSeries[] | null | undefined, family: RedFamilySpec = HTTP_FAMILY): { series: SpanMetricSeries[]; tokens: Map<string, string> } {
  const grpc = family.kind === 'grpc';
  const codes = relabel(series, `{{${family.labels.status}}}`)
    .filter(s => s.groupKey[0] && s.groupKey[0] !== '—')
    .sort((a, b) => (grpc ? Number(a.groupKey[0]) - Number(b.groupKey[0]) : a.groupKey[0].localeCompare(b.groupKey[0])));
  const tokens = new Map<string, string>();
  const out = codes.map(s => {
    const code = s.groupKey[0];
    const name = statusCodeLabel(family, code);
    const tok = HTTP_SERIES_TOKENS[statusClassToken(statusClassOf(family, code))];
    if (tok) tokens.set(name, tok);
    return grpc ? { ...s, groupKey: [name] } : s;
  });
  return { series: out, tokens };
}

// v0.10.1259 — Overview "Toplam istek" karosu: penceredeki toplam (anlık sorgu).
export function totalRequestsQuery(app: string, windowSec: number, envSel?: string, family: RedFamilySpec = HTTP_FAMILY): string {
  return `sum(increase(${family.names.count}${serverSel({ app, envSel, family })}[${Math.max(60, Math.round(windowSec))}s]))`;
}

// ── v0.10.1352 — endpoint sayfası metrik kipi = HTTP Metrics sekmesinin
// AYNI panelleri, route'a süzülü (operatör: "HTTP Metrics tabındaki aynı
// paneli/grafiği ilgili endpoint'e göre filtreleyerek koyabiliriz direkt").
// Eski kip /api/endpoints/metric'ten (throughput-metric seam'i, 180 s adım
// başına yüzdelik + çağrı-ağırlıklı karo ortalaması) okuyordu; sekmeyle
// tutmuyordu.

/** Lejant: "GET /a"; route etiketi yoksa "GET (route yok)" (çıplak "GET" değil). gRPC (v0.10.1429): "pkg.Service/Method". */
export function methodRouteLegend(labels: Record<string, string>, withStatus = false, family: RedFamilySpec = HTTP_FAMILY): string {
  const FL = family.labels;
  if (family.kind === 'grpc') {
    const svc = labels[FL.method] ?? '';
    const m = labels[FL.route] ? labels[FL.route] : '(method yok)';
    const call = svc ? `${svc}/${m}` : m;
    return [withStatus ? statusCodeLabel(family, labels[FL.status] ?? '') : '', call].filter(Boolean).join(' ');
  }
  const route = labels[FL.route] ? labels[FL.route] : '(route yok)';
  return [withStatus ? labels[FL.status] : '', labels[FL.method] ?? '', route]
    .filter(Boolean).join(' ');
}

/** Kod → sınıf rengi (`var(--ok)` …); bilinmeyen sınıf → undefined. gRPC: ok/client/server sınıfından (v0.10.1429). */
export function statusCodeColorVar(code: string, family: RedFamilySpec = HTTP_FAMILY): string | undefined {
  const tok = HTTP_SERIES_TOKENS[statusClassToken(statusClassOf(family, code.split(' ')[0]))];
  return tok ? `var(${tok})` : undefined;
}

/** Grafana "short": 19 Mn, 2 B (tam sayı title'da). */
const COMPACT = new Intl.NumberFormat('tr-TR', { notation: 'compact', maximumFractionDigits: 1 });
export function fmtCompact(v: MaybeNum): string {
  return finite(v) ? COMPACT.format(v) : '—';
}

/**
 * Şekil (`/users/:id`, endpointPathSignature çıktısı) → route regex'i: regex
 * metakarakterleri kaçışlı, her `:id` = bir ya da daha çok segment-içi
 * karakter (`[^/]+`). VM `=~` tam çapalar; ham şablon `/users/:id` da eşleşir.
 */
export function shapeRouteRegex(shape: string): string {
  return shape.split(':id').map(p => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('[^/]+');
}

export interface EndpointIdentity {
  service: string;
  /** http_route değeri (ya da sig=true'da şekil). */
  route: string;
  method?: string;
  /** Şekil kipi (Endpoints "group by shape"): route regex ile eşlenir. */
  sig?: boolean;
  /** v0.10.1377 — ortam eşleyicisi (HttpScope.envSel). */
  envSel?: string;
  /** v0.10.1429 — RED ailesi (gRPC: route = rpc_method, method = rpc_service). */
  family?: RedFamilySpec;
}

/** Endpoint kimliği → sekme üreticilerinin kapsamı. Route HER ZAMAN süzer (boşsa `http_route=~""` = route'suz seriler; servis geneline genişlemez). */
export function endpointScope(ep: EndpointIdentity): HttpScope {
  const method = ep.method?.trim() || undefined;
  const env = ep.envSel ? { envSel: ep.envSel } : {}; // v0.10.1377
  const fam = ep.family ? { family: ep.family } : {}; // v0.10.1429
  if (ep.sig) return { app: ep.service, ...env, ...fam, routeRe: shapeRouteRegex(ep.route), method };
  if (!ep.route) return { app: ep.service, ...env, ...fam, routeRe: '', method };
  return { app: ep.service, ...env, ...fam, route: ep.route, method };
}

/** Endpoint'in inbound etiket seçicisi (`{service_name="…", http_route="…"[, http_request_method="…"]}`). */
export function endpointFilter(ep: EndpointIdentity): string {
  return serverSel(endpointScope(ep));
}

export type LatQ = 'p50' | 'p95' | 'p99';
const LAT_P: Record<LatQ, number> = { p50: 0.5, p95: 0.95, p99: 0.99 };

export interface EndpointHttpQuery { id: string; expr: string; instant: boolean }

/**
 * Endpoint metrik kipinin TÜM sorguları. Grafikler sekmenin range
 * sorguları (aynı adım: ceil(W/300), aynı rate penceresi); karolar sekmenin
 * "Endpoint Detayları" tablosunun pencere sorguları (by yok → tek seri,
 * anlık); compare → aynı pencere sorguları `offset W` ile.
 */
export function endpointHttpQueries(ep: EndpointIdentity, windowSec: number, opts: { latQ: LatQ; compare: boolean }): EndpointHttpQuery[] {
  const sc = endpointScope(ep);
  const tab = httpQueries(sc, windowSec);
  const w = windowQueries(sc, windowSec);
  const byMR = `${famOf(sc).labels.method}, ${famOf(sc).labels.route}`;
  const out: EndpointHttpQuery[] = [
    { id: 'rateByRoute', expr: tab.rateByRoute, instant: false },
    { id: 'err5ByRoute', expr: tab.err5ByRoute, instant: false },
    { id: 'err4ByRoute', expr: tab.err4ByRoute, instant: false },
    { id: 'p50', expr: tab.p50, instant: false },
    { id: 'p90', expr: tab.p90, instant: false },
    { id: 'p95', expr: tab.p95, instant: false },
    { id: 'p99', expr: tab.p99, instant: false },
    { id: 'latByRoute', expr: quantileQuery(sc, windowSec, LAT_P[opts.latQ], byMR), instant: false },
    { id: 'statusCodes', expr: tab.statusCodes, instant: true },
    { id: 'wTotal', expr: w.total, instant: true },
    { id: 'wErr5', expr: w.err5Count, instant: true },
    { id: 'wAvg', expr: w.avg, instant: true },
    { id: 'wP50', expr: w.p50, instant: true },
    { id: 'wP95', expr: w.p95, instant: true },
    { id: 'wP99', expr: w.p99, instant: true },
  ];
  if (opts.compare) {
    const pw = windowQueries(sc, windowSec, '', Math.max(60, Math.round(windowSec)));
    out.push(
      { id: 'pTotal', expr: pw.total, instant: true },
      { id: 'pErr5', expr: pw.err5Count, instant: true },
      { id: 'pAvg', expr: pw.avg, instant: true },
      { id: 'pP99', expr: pw.p99, instant: true },
    );
  }
  return out;
}

export interface EndpointRed {
  calls: number | null; errors: number | null; errRate: number | null; reqPerMin: number | null;
  avgS: number | null; p50S: number | null; p95S: number | null; p99S: number | null;
}

/** Pencere sorgularının anlık değerleri → karolar (errRate 0–1, reqPerMin = çağrı / pencere dk). */
export function endpointRed(
  d: Partial<Record<'total' | 'err5' | 'avg' | 'p50' | 'p95' | 'p99', SpanMetricSeries[] | null | undefined>>, windowSec: number,
): EndpointRed {
  const calls = scalarOf(d.total);
  const errors = scalarOf(d.err5);
  const nn = (v: number | null) => (v === null || !Number.isFinite(v) ? null : v);
  const W = Math.max(60, Math.round(windowSec));
  return {
    calls, errors,
    errRate: calls !== null && calls > 0 && errors !== null ? errors / calls : null,
    reqPerMin: calls !== null ? calls / (W / 60) : null,
    avgS: nn(scalarOf(d.avg)), p50S: nn(scalarOf(d.p50)), p95S: nn(scalarOf(d.p95)), p99S: nn(scalarOf(d.p99)),
  };
}

// ── v0.10.1355 — HTTP zaman serisi panellerinin çizim politikası ─────────
// Operatör-raporlu (prod, /endpoint metrik kipi + Service → HTTP Metrics):
// (1) "Latency Yüzdelikleri" p50 2.621–2.653 ms aralığında gezerken y ekseni
// 2.62–2.65 ms'e oturuyordu; 30 µs'lik interpolasyon gürültüsü büyük
// oynaklık gibi görünüyordu ("Küçük ms arasında çok oynak görünüyor").
// (2) "5xx Hataları": iki örnekli seyrek seri (0.0042 ve 0.0033 req/s,
// aralarında ~3.5 saat) dev bir üçgen çiziyordu — panelde başka zaman damgası
// olmadığı için iki örnek KOMŞU indeks oldu ve çizgi aradaki boşluğu kapattı.

/** Yüzdelik panelinin dürüstlük notu: hassasiyet kova düzeyinde. */
export const HTTP_QUANTILE_NOTE =
  'Yüzdelikler histogram kovalarından hesaplanır; kova içi değerler yaklaşıktır (OTel varsayılan sınırları 0/5/10/25… ms).';

// Negatif olamayan miktar birimleri — gecikme (s), hız (req/s), oran (%).
const ZERO_BASE_UNITS: ReadonlySet<string> = new Set(['s', 'ms', 'req/s', '%']);

/**
 * y tabanı 0 mı (MultiLineChart/CorePanel `zeroBase` → softMin 0)?
 * Karar: HTTP panellerinin TÜM birimleri negatif olmayan miktar; gecikmede
 * 0-tabanı Datadog/Dynatrace/Grafana pratiği ve gerçek hassasiyet zaten kova
 * düzeyinde (histogram_quantile kova içinde doğrusal interpolasyon) —
 * µs ölçeğinde zoom anlamlı ayrıntı değil, gürültü gösterir. Hız/oran
 * panellerinde aynı aşırı-zoom (120.1–120.5 req/s) aynı yanlış izlenimi
 * verir, bu yüzden tutarlı olarak onlar da 0'dan çizilir. Soft taban:
 * gerçek bir sıçrama (2.6 → 50 ms) aynen görünür; bilinmeyen birim → false
 * (otomatik aralık).
 */
export function httpPanelZeroBase(unit: string): boolean {
  return ZERO_BASE_UNITS.has(unit);
}

/** Boşluk eşiği: ardışık iki zaman damgası arası > 1.5 adım ⇒ en az bir adım eksik. */
const GAP_FACTOR = 1.5;
/** Savunma tavanı: yanlış/küçük adımda dolgu döngüsü sınırsız büyümesin. */
const MAX_FILL = 4 * HTTP_METRICS_POINTS;

/**
 * PromQL range sonucu → çizilecek seriler: eksik adımlar NULL (NaN — dataFrame
 * köprüsü sonlu olmayanı null taşır), atlanmaz. VM/Prometheus değer
 * üretmeyen adımı yanıttan ÇIKARIR; CorePanel serileri zaman damgalarının
 * birleşimine hizalar (framesToAligned, eksik = null) ama birleşimde HİÇ
 * damga olmayan aralık null taşımaz ve çizgi onu köprüler (spanNulls: false
 * yalnız null'u kırar). Bu yüzden dolgu PANEL düzeyinde, birleşimin
 * boşluklarına yapılır: `prev + k·adım` damgaları her seriye NaN olarak
 * eklenir. Birleşimin içi zaten hizalamada null'lanır; dolgu damgaları
 * hiçbir serinin verisi olmayan anlara düştüğü için adım tahmini birkaç
 * saniye kaysa da başka bir serinin çizgisini kıramaz.
 * SIKI doktrin (CorePanel, operatör onayı 2026-08-06): tek eksik adım da
 * boşluktur; 1.5 çarpanı köprüleme değil, adım yuvarlama payıdır.
 */
export function fillStepGaps(series: SpanMetricSeries[], stepSec: number): SpanMetricSeries[] {
  const stepNs = stepSec * 1e9;
  if (!(stepNs > 0) || series.length === 0) return series;
  const times = [...new Set(series.flatMap(s => (s.points ?? []).map(p => p.time)))].sort((a, b) => a - b);
  const fill: number[] = [];
  for (let i = 1; i < times.length && fill.length < MAX_FILL; i++) {
    const a = times[i - 1], b = times[i];
    if (b - a <= GAP_FACTOR * stepNs) continue;
    for (let t = a + stepNs; t < b - stepNs / 2 && fill.length < MAX_FILL; t += stepNs) fill.push(t);
  }
  if (fill.length === 0) return series;
  return series.map(s => {
    const pts = [...(s.points ?? []), ...fill.map(time => ({ time, value: NaN }))];
    pts.sort((x, y) => x.time - y.time);
    return { ...s, points: pts };
  });
}

// ── v0.10.1359 — /endpoints LİSTESİ metrik kipi = HTTP Metrics PromQL'i ──
// Operatör-raporlu: liste "Kaynak: metrik" kipinde /api/endpoints/metric'ten
// okuyordu (çağrı = adım artışlarının toplamı, yüzdelik = adım değerlerinin
// çağrı-ağırlıklı ortalaması) ve sayılar sekmenin "Endpoint Detayları"
// tablosuyla tutmuyordu. Artık liste satırı = sekme satırı = endpoint detay
// karoları YAPI GEREĞİ: ölçüler sekmenin `windowQueries(scope, W, BY)`
// üreticisinin AYNISI, yalnız `and on (BY) topk(N, toplam)` ile havuza
// süzülü (her yanıt ≤ N seri — promapi 1000-seri tavanı güvenli). Satır
// greni (method, route), sekme gibi. Servis ZORUNLU (filo geneli sorgu yok).

type EndpointListRow = import('./types').EndpointRow; // v0.10.1359

/** v0.10.1359 — metrik liste havuz tavanı: N = min(limit, 300). */
export const ENDPOINT_LIST_POOL_MAX = 300;

/** v0.10.1359 — liste satır greni (sekmenin byMR'ı); v0.10.1429 aileden. */
const listBy = (F: RedFamilySpec) => `${F.labels.method}, ${F.labels.route}`;

/** v0.10.1359 — yol araması → büyük/küçük harf duyarsız alt-dize route regex'i (VM `=~` tam çapalar). */
export function routeSubstringRegex(search: string): string {
  return `(?i).*${search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}.*`;
}

/** v0.10.1359 — liste kapsamı: servis (zorunlu) + isteğe bağlı yol araması (route regex); v0.10.1377 ortam eşleyicisi. */
export function endpointListScope(service: string, search = '', envSel?: string, family?: RedFamilySpec): HttpScope {
  const q = search.trim();
  const env = envSel ? { envSel } : {};
  const fam = family ? { family } : {}; // v0.10.1429
  return q ? { app: service, ...env, ...fam, routeRe: routeSubstringRegex(q) } : { app: service, ...env, ...fam };
}

export type EndpointListId =
  | 'total' | 'err5' | 'avg' | 'p50' | 'p95' | 'p99' | 'cls'
  | 'pTotal' | 'pErr5' | 'pAvg' | 'pP99';

export interface EndpointListQuery { id: EndpointListId; expr: string; instant: true }

/**
 * v0.10.1359 — liste sayfasının TÜM sorguları (anlık, `to` anında). total =
 * `topk(N, sekmenin tTotal'ı)`; diğer her ölçü sekmenin üreticisi
 * `(…) and on (BY) topk(N, …)` ile havuza süzülü. compare → total/err5/avg/p99
 * `offset W` ile (önceki pencere), havuz yine BU pencerenin topk'sı.
 */
export function listQueries(scope: HttpScope, windowSec: number, n: number, compare: boolean): EndpointListQuery[] {
  const N = Math.max(1, Math.min(ENDPOINT_LIST_POOL_MAX, Math.floor(n)));
  const F = famOf(scope);
  const LIST_BY = listBy(F);
  const w = windowQueries(scope, windowSec, LIST_BY);
  const pool = `topk(${N}, ${w.total})`;
  const inPool = (m: string) => `(${m}) and on (${LIST_BY}) ${pool}`;
  const cls = statusClassExpr(F, winInc(scope, windowSec, F.names.count), `${LIST_BY}, `);
  const q = (id: EndpointListId, expr: string): EndpointListQuery => ({ id, expr, instant: true });
  const out: EndpointListQuery[] = [
    q('total', pool),
    q('err5', inPool(w.err5Count)),
    q('avg', inPool(w.avg)),
    q('p50', inPool(w.p50)),
    q('p95', inPool(w.p95)),
    q('p99', inPool(w.p99)),
    q('cls', inPool(cls)),
  ];
  if (compare) {
    const pw = windowQueries(scope, windowSec, LIST_BY, Math.max(60, Math.round(windowSec)));
    out.push(
      q('pTotal', inPool(pw.total)),
      q('pErr5', inPool(pw.err5Count)),
      q('pAvg', inPool(pw.avg)),
      q('pP99', inPool(pw.p99)),
    );
  }
  return out;
}

/**
 * v0.10.1359 — SAF indirgeyici: liste sorgularının anlık sonuçları →
 * EndpointRow[]. Satır kümesi = `total` havuzu; diğer ölçüler (method, route)
 * üzerinden birleşir. s → ms, errorRate = err5 / total × 100, reqPerMin =
 * total / (W / 60), http2xx..5xx `cls`'ten; p90 yok. compare sonucu geldiyse
 * önceki pencerede olmayan satırın prior'u 0 (UI "listede yeni" der).
 * Sırasız döner (istemci sıralar).
 */
export function endpointListRows(
  data: Partial<Record<EndpointListId, SpanMetricSeries[] | null | undefined>>, windowSec: number, service: string,
  family: RedFamilySpec = HTTP_FAMILY,
): EndpointListRow[] {
  const FL = family.labels; // v0.10.1429 — gRPC: method = rpc_service, path = rpc_method
  const grpc = family.kind === 'grpc';
  const key = (l: Record<string, string>) => `${l[FL.method] ?? ''}\u0000${l[FL.route] ?? ''}`;
  const val = (s: SpanMetricSeries) => { const v = lastValue(s); return v !== null && Number.isFinite(v) ? v : null; };
  const index = (series: SpanMetricSeries[] | null | undefined) => {
    const m = new Map<string, number>();
    for (const s of series ?? []) { const v = val(s); if (v !== null) m.set(key(seriesLabels(s)), v); }
    return m;
  };
  const err5 = index(data.err5), avg = index(data.avg);
  const p50 = index(data.p50), p95 = index(data.p95), p99 = index(data.p99);
  const cls = new Map<string, Record<string, number>>();
  for (const s of data.cls ?? []) {
    const l = seriesLabels(s); const v = val(s);
    if (v === null || !l.cls) continue;
    const k = key(l);
    cls.set(k, { ...(cls.get(k) ?? {}), [l.cls]: v });
  }
  const hasPrior = data.pTotal !== undefined;
  const pTotal = index(data.pTotal), pErr5 = index(data.pErr5), pAvg = index(data.pAvg), pP99 = index(data.pP99);
  const W = Math.max(60, Math.round(windowSec));
  const ms = (v: number | undefined) => (v === undefined ? undefined : v * 1000);
  const rows: EndpointListRow[] = [];
  for (const s of data.total ?? []) {
    const calls = val(s);
    if (calls === null) continue;
    const l = seriesLabels(s);
    const k = key(l);
    const errors = err5.get(k) ?? 0;
    const c = cls.get(k) ?? {};
    const row: EndpointListRow = {
      service, path: l[FL.route] ?? '', method: l[FL.method] || undefined,
      calls, errors,
      errorRate: calls > 0 ? (errors / calls) * 100 : 0,
      reqPerMin: calls / (W / 60),
      avgMs: ms(avg.get(k)) ?? 0,
      p50Ms: ms(p50.get(k)), p95Ms: ms(p95.get(k)),
      p99Ms: ms(p99.get(k)) ?? 0,
      // gRPC: sınıf sütunları HTTP karşılığına oturur (ok→2xx, istemci→4xx, sunucu→5xx; 3xx yok).
      http2xx: (grpc ? c['ok'] : c['2xx']) ?? 0, http3xx: grpc ? 0 : (c['3xx'] ?? 0),
      http4xx: (grpc ? c['client'] : c['4xx']) ?? 0, http5xx: (grpc ? c['server'] : c['5xx']) ?? 0,
      ...(grpc ? { family: 'grpc' as const } : {}),
    };
    if (hasPrior) {
      row.priorCalls = pTotal.get(k) ?? 0;
      row.priorErrors = pErr5.get(k) ?? 0;
      row.priorAvgMs = ms(pAvg.get(k)) ?? 0;
      row.priorP99Ms = ms(pP99.get(k)) ?? 0;
    }
    rows.push(row);
  }
  return rows;
}
