// traceMetrics.ts — v0.10.913 (operatör 2026-09-25: "trace'te geçen pod
// metriklerine yeni bir sekmede erişilsin"). Trace sayfası "Metrics"
// sekmesinin SAF yardımcıları (tablo testli).
//
// v0.10.968 — yeniden tasarım (operatör onayı 2026-09-27, "3 onay"): çip
// duvarı yerine TEK gruplu tablo, sıra TRACE İLGİSİNE göre ve metrikler
// sırayı DEĞİŞTİRMEZ (satırlar metrik gelince zıplamaz). Buradaki her şey
// saf: model (buildTraceMetricsModel), satırlar (buildRows), toplu uç dilimleri
// (tracePodChunks / chunksToEnable), dilim sonucu → pod durumu
// (podMetricState), seçim kuralı (selectPod). v0.10.962'nin dışa aktarımları
// (shortPod, jvmPodSeries, traceMetricsPodHref, togglePod, podAtCap,
// tracePods) aynı imzayla duruyor.
import type { SpanMetricSeries, SpanRow, TraceAnalysis, TracePodMetricsResponse, TracePodSeriesRow } from '@/lib/types';
import type { CriticalPath } from '@/lib/criticalPath';
import { spanHasError } from '@/lib/otel/links';
import { apiErrorDetail } from '@/lib/api';
import { windowRangeParam } from '@/lib/urlState';
import { podDetailPath } from '@/pages/service/podDetailPath';
import type { CellTone } from '@/components/ui/DataTable';
import { fmtBytesTr, fmtCores, fmtCoresShort, fmtPct } from './traceMetricsFmt';
import { trPossessive } from './trSuffix';
import {
  CRIT_FLAG_SHARE, LEVEL_ERR, LEVEL_WARN, TOP_SELF_N, TRACE_METRICS_MAX_COMPARE,
  TRACE_POD_AUTO_CHUNKS, TRACE_POD_CHUNK_MAX, TRACE_POD_CHUNK_REGEX_MAX, TRACE_POD_MAX_INFLIGHT,
  type MetricLevel, type NoPodService, type NoPodSummary, type PodMetricSeries, type PodMetricState,
  type PodNameParts, type TraceErrorMark, type TraceMetricsFilter, type TraceMetricsGrouping,
  type TraceMetricsModel, type TracePodGroup, type TracePodInfo,
} from './traceMetricsModel';

// v0.10.968 — pencere sabitleri modelde; eski içe aktarımlar buradan da bulur.
export { TRACE_METRICS_WINDOWS, TRACE_METRICS_DEFAULT_WINDOW, type TraceMetricsWindow } from './traceMetricsModel';

const CLUSTER_KEYS = ['cluster', 'k8s.cluster.name', 'openshift.cluster.name'] as const;

export interface TracePod {
  pod: string;
  namespace: string;
  clusterValue: string;
  service: string;
  spans: number;
  errors: number;
}

function attr(sp: SpanRow, key: string): string {
  return sp.resourceAttributes?.[key] || sp.attributes?.[key] || '';
}

/** v0.10.968 — span bitişi (ns): endTime geçerliyse o, yoksa start + süre. */
function spanEndNs(sp: SpanRow): number {
  return sp.endTime > 0 && sp.endTime >= sp.startTime ? sp.endTime : sp.startTime + (sp.durationMs || 0) * 1e6;
}

const cmpStr = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
/** v0.10.968 — erken başlayan önce; eşitlikte spanId (girdi sırasından bağımsız). */
const earlier = (a: SpanRow, b: SpanRow) => a.startTime < b.startTime || (a.startTime === b.startTime && a.spanId < b.spanId);

/** Trace span'larından pod'lar (k8s.pod.name; resource önce, yoksa span attr).
 *  v0.10.968 — hata sayımı sayfanın kuralıyla (spanHasError: hata durumu YA DA
 *  exception olayı); sekme etiketi yalnız uzunluğu okur. */
export function tracePods(spans: SpanRow[]): TracePod[] {
  const by = new Map<string, TracePod>();
  for (const sp of spans) {
    const pod = attr(sp, 'k8s.pod.name');
    if (!pod) continue;
    let p = by.get(pod);
    if (!p) {
      p = {
        pod,
        namespace: attr(sp, 'k8s.namespace.name'),
        clusterValue: CLUSTER_KEYS.map(k => attr(sp, k)).find(Boolean) ?? '',
        service: sp.serviceName,
        spans: 0, errors: 0,
      };
      by.set(pod, p);
    }
    p.spans++;
    if (spanHasError(sp)) p.errors++;
    if (!p.namespace) p.namespace = attr(sp, 'k8s.namespace.name');
  }
  return [...by.values()];
}

// ── v0.10.968 — hata durumu metni (spec §1 D7) ───────────────────────────
const GRPC_CODES = ['OK', 'CANCELLED', 'UNKNOWN', 'INVALID_ARGUMENT', 'DEADLINE_EXCEEDED', 'NOT_FOUND',
  'ALREADY_EXISTS', 'PERMISSION_DENIED', 'RESOURCE_EXHAUSTED', 'FAILED_PRECONDITION', 'ABORTED',
  'OUT_OF_RANGE', 'UNIMPLEMENTED', 'INTERNAL', 'UNAVAILABLE', 'DATA_LOSS', 'UNAUTHENTICATED'];

/** v0.10.968 — hatalı span'ın kısa durum metni; İLK eşleşen kazanır:
 *  HTTP ≥400 → gRPC adı → ilk exception tipi → statusMessage (≤60) → "hata".
 *  "yeniden denendi" türetilemez, yazılmaz. */
export function spanErrorStatus(sp: SpanRow): string {
  const a = sp.attributes ?? {};
  const http = sp.httpStatus || Number(a['http.response.status_code'] || a['http.status_code'] || 0);
  if (Number.isFinite(http) && http >= 400) return String(http);
  const g = a['rpc.grpc.status_code'];
  if (g !== undefined && g !== '') {
    const n = Number(g);
    if (Number.isInteger(n) && n >= 1 && n <= 16) return GRPC_CODES[n];
  }
  const ex = (sp.events ?? []).find(e => e.name === 'exception');
  const t = ex?.attributes?.['exception.type'];
  if (t) return t;
  const msg = (sp.statusMessage ?? '').trim();
  if (msg) return msg.length > 60 ? msg.slice(0, 59) + '…' : msg;
  return 'hata';
}

// ── v0.10.968 — pod adı parçaları (spec §1 D13) ──────────────────────────
const POD_RE = /^(.+)-([a-z0-9]{6,10})-([a-z0-9]{5})$/;

/** v0.10.968 — replicaset adı biliniyorsa ondan; yoksa `<baş>-<hash>-<ek>`
 *  kalıbından; o da tutmazsa bölünmez. Soluk parça hash'tir (`rs`). */
export function podNameParts(pod: string, replicaSet: string): PodNameParts {
  if (replicaSet && pod.startsWith(replicaSet + '-') && pod.length > replicaSet.length + 1) {
    const i = replicaSet.lastIndexOf('-');
    if (i > 0) return { head: replicaSet.slice(0, i), rs: replicaSet.slice(i + 1), tail: pod.slice(replicaSet.length + 1) };
  }
  const m = POD_RE.exec(pod);
  if (m) return { head: m[1], rs: m[2], tail: m[3] };
  return { head: pod, rs: '', tail: '' };
}

/** v0.10.968 — derinlik yedeği: TEK geçişte, belleklenmiş O(N) harita
 *  (eski `depthOf` her hatalı span için köke kadar yürüyordu). Döngüye
 *  karşı korumalı: döngüdeki en üst halka kök sayılır. */
export function spanDepthMap(spans: SpanRow[]): Map<string, number> {
  const byId = new Map(spans.map(s => [s.spanId, s]));
  const depth = new Map<string, number>();
  for (const s of spans) {
    if (depth.has(s.spanId)) continue;
    const path: string[] = [];
    const onPath = new Set<string>();
    let cur: SpanRow | undefined = s;
    let base = -1;
    while (cur && !depth.has(cur.spanId)) {
      if (onPath.has(cur.spanId)) break;
      onPath.add(cur.spanId);
      path.push(cur.spanId);
      const pid: string = cur.parentSpanId;
      cur = pid && pid !== cur.spanId ? byId.get(pid) : undefined;
    }
    if (cur && depth.has(cur.spanId)) base = depth.get(cur.spanId)!;
    for (let i = path.length - 1, d = base + 1; i >= 0; i--, d++) depth.set(path[i], d);
  }
  return depth;
}

interface PodAcc {
  info: TracePodInfo;
  list: SpanRow[];
  svcCount: Map<string, number>;
  firstErr: SpanRow | null;
}

const podCmp = (a: TracePodInfo, b: TracePodInfo) =>
  (Number(b.rootCause) - Number(a.rootCause))
  || (Number(b.errors > 0) - Number(a.errors > 0))
  || (b.critShare - a.critShare)
  || (b.maxSelfNs - a.maxSelfNs)
  || (b.spans - a.spans)
  || cmpStr(a.pod, b.pod);

/** v0.10.968 — Trace › Metrics modeli (spec §4.1). SAF, O(N); panel bellekler.
 *  Öz süre sunucu analizinden (analysis.nodes[].selfNs; yoksa bilinmez, istemci
 *  yedeği YOK), derinlik analysis'ten (yoksa O(N) yedek harita), kritik yol
 *  payı sayfanın hesapladığı criticalPath'ten (yeniden hesaplanmaz). Metrikler
 *  modele GİRMEZ: sıra yalnız trace'ten bilinir. */
export function buildTraceMetricsModel(
  spans: SpanRow[],
  analysis: TraceAnalysis | undefined,
  criticalPath: CriticalPath | null,
  capped: boolean,
): TraceMetricsModel {
  const selfKnown = !!analysis;
  const nodeById = new Map((analysis?.nodes ?? []).map(n => [n.spanId, n]));
  let fallbackDepth: Map<string, number> | null = null;
  const depthOf = (sp: SpanRow): number => {
    const n = nodeById.get(sp.spanId);
    if (n) return n.depth;
    fallbackDepth ??= spanDepthMap(spans);
    return fallbackDepth.get(sp.spanId) ?? 0;
  };
  const selfOf = (sp: SpanRow) => (selfKnown ? nodeById.get(sp.spanId)?.selfNs ?? 0 : 0);
  const critOf = (sp: SpanRow) => criticalPath?.onPathSelfNs.get(sp.spanId) ?? 0;

  let traceStart = Infinity;
  let traceEnd = -Infinity;
  const accs = new Map<string, PodAcc>();
  const noPodBy = new Map<string, NoPodService & { critNs: number }>();
  const errorMarks: TraceErrorMark[] = [];
  const selfCands: { sp: SpanRow; pod: string; self: number }[] = [];
  let rcSpan: SpanRow | null = null;
  let rcDepth = -1;

  for (const sp of spans) {
    const end = spanEndNs(sp);
    if (sp.startTime < traceStart) traceStart = sp.startTime;
    if (end > traceEnd) traceEnd = end;
    const pod = attr(sp, 'k8s.pod.name');
    const err = spanHasError(sp);
    const self = selfOf(sp);
    const crit = critOf(sp);
    if (err) {
      errorMarks.push({ spanId: sp.spanId, pod, service: sp.serviceName, name: sp.name, status: spanErrorStatus(sp), timeNs: sp.startTime });
    }
    if (!pod) {
      const svc = sp.serviceName || '';
      let n = noPodBy.get(svc);
      if (!n) { n = { service: svc, spans: 0, errors: 0, maxSelfNs: 0, critShare: 0, critNs: 0 }; noPodBy.set(svc, n); }
      n.spans++;
      if (err) n.errors++;
      if (self > n.maxSelfNs) n.maxSelfNs = self;
      n.critNs += crit;
      continue;
    }
    let acc = accs.get(pod);
    if (!acc) {
      acc = {
        info: {
          pod, namespace: '', clusterValue: '', service: '', spans: 0, errors: 0,
          maxSelfNs: 0, maxSelfSpanId: '', critNs: 0, critShare: 0, topSelf: false,
          rootCause: false, entry: false, firstError: null, activeFromNs: sp.startTime, activeToNs: end,
          flagged: false, runtime: '', replicaSet: '', nameParts: { head: pod, rs: '', tail: '' },
        },
        list: [], svcCount: new Map(), firstErr: null,
      };
      accs.set(pod, acc);
    }
    const p = acc.info;
    acc.list.push(sp);
    acc.svcCount.set(sp.serviceName, (acc.svcCount.get(sp.serviceName) ?? 0) + 1);
    p.spans++;
    if (err) {
      p.errors++;
      if (!acc.firstErr || earlier(sp, acc.firstErr)) acc.firstErr = sp;
      const d = depthOf(sp);
      // v0.10.892/962 kuralı: pod'u bilinen, hata veren EN DERİN span; eşitlikte erken başlayan.
      if (!rcSpan || d > rcDepth || (d === rcDepth && earlier(sp, rcSpan))) { rcSpan = sp; rcDepth = d; }
    }
    if (self > p.maxSelfNs || (self > 0 && self === p.maxSelfNs && sp.spanId < p.maxSelfSpanId)) {
      p.maxSelfNs = self;
      p.maxSelfSpanId = sp.spanId;
    }
    p.critNs += crit;
    if (sp.startTime < p.activeFromNs) p.activeFromNs = sp.startTime;
    if (end > p.activeToNs) p.activeToNs = end;
    if (!p.namespace) p.namespace = attr(sp, 'k8s.namespace.name');
    if (!p.clusterValue) p.clusterValue = CLUSTER_KEYS.map(k => attr(sp, k)).find(Boolean) ?? '';
    if (!p.runtime) p.runtime = sp.resourceAttributes?.['telemetry.sdk.language'] ?? '';
    if (!p.replicaSet) p.replicaSet = attr(sp, 'k8s.replicaset.name');
    if (selfKnown && self > 0) selfCands.push({ sp, pod, self });
  }
  if (!Number.isFinite(traceStart)) { traceStart = 0; traceEnd = 0; }

  const rootWall = criticalPath?.rootWallNs ?? 0;
  const share = (ns: number) => (rootWall > 0 ? ns / rootWall : 0);

  // En büyük öz süreli 5 span (pod'u bilinenler arasında); eşitlik kararlı.
  selfCands.sort((a, b) => (b.self - a.self) || (a.sp.startTime - b.sp.startTime) || cmpStr(a.sp.spanId, b.sp.spanId));
  const topSelfPods = new Set(selfCands.slice(0, TOP_SELF_N).map(c => c.pod));

  const rootCausePod = rcSpan ? attr(rcSpan, 'k8s.pod.name') : '';
  // Giriş pod'u: kök span'in pod'u (analysis.rootSpanId, yoksa ebeveyni bilinmeyen en erken span).
  const ids = new Set(spans.map(s => s.spanId));
  let root: SpanRow | undefined = analysis?.rootSpanId ? spans.find(s => s.spanId === analysis.rootSpanId) : undefined;
  if (!root) {
    for (const s of spans) {
      if (s.parentSpanId && ids.has(s.parentSpanId)) continue;
      if (!root || earlier(s, root)) root = s;
    }
  }
  const rootPod = root ? attr(root, 'k8s.pod.name') : '';
  const entryPod = rootPod && accs.has(rootPod) ? rootPod : '';

  const pods: TracePodInfo[] = [];
  const podSpans = new Map<string, SpanRow[]>();
  for (const acc of accs.values()) {
    const p = acc.info;
    let svc = ''; let best = -1;
    for (const [s, n] of acc.svcCount) if (n > best || (n === best && s < svc)) { svc = s; best = n; }
    p.service = svc;
    p.critShare = share(p.critNs);
    p.topSelf = topSelfPods.has(p.pod);
    p.rootCause = p.pod === rootCausePod;
    p.entry = p.pod === entryPod;
    p.firstError = acc.firstErr
      ? { spanId: acc.firstErr.spanId, name: acc.firstErr.name, status: spanErrorStatus(acc.firstErr), timeNs: acc.firstErr.startTime }
      : null;
    p.flagged = p.errors > 0 || p.critShare >= CRIT_FLAG_SHARE || p.topSelf;
    p.nameParts = podNameParts(p.pod, p.replicaSet);
    if (!p.replicaSet && p.nameParts.rs) p.replicaSet = `${p.nameParts.head}-${p.nameParts.rs}`;
    pods.push(p);
    podSpans.set(p.pod, [...acc.list].sort((a, b) => (earlier(a, b) ? -1 : earlier(b, a) ? 1 : 0)));
  }
  pods.sort(podCmp);

  const leadSvc = rootCausePod ? accs.get(rootCausePod)?.info.service ?? '' : '';
  const gmap = new Map<string, TracePodInfo[]>();
  for (const p of pods) {
    const g = gmap.get(p.service);
    if (g) g.push(p); else gmap.set(p.service, [p]);
  }
  const groups: TracePodGroup[] = [...gmap.entries()].map(([service, ps]) => ({
    service,
    pods: ps,
    spans: ps.reduce((a, p) => a + p.spans, 0),
    errors: ps.reduce((a, p) => a + p.errors, 0),
    maxSelfNs: ps.reduce((a, p) => Math.max(a, p.maxSelfNs), 0),
    critShare: share(ps.reduce((a, p) => a + p.critNs, 0)),
    flagged: ps.filter(p => p.flagged).length,
    lead: !!leadSvc && service === leadSvc,
  }));
  groups.sort((a, b) =>
    (Number(b.lead) - Number(a.lead))
    || (Number(b.errors > 0) - Number(a.errors > 0))
    || (b.critShare - a.critShare)
    || (b.maxSelfNs - a.maxSelfNs)
    || (b.spans - a.spans)
    || cmpStr(a.service, b.service));

  let noPod: NoPodSummary | null = null;
  if (noPodBy.size > 0) {
    const services: NoPodService[] = [...noPodBy.values()]
      .map(n => ({ service: n.service, spans: n.spans, errors: n.errors, maxSelfNs: n.maxSelfNs, critShare: share(n.critNs) }))
      .sort((a, b) => (Number(b.errors > 0) - Number(a.errors > 0)) || (b.critShare - a.critShare)
        || (b.maxSelfNs - a.maxSelfNs) || (b.spans - a.spans) || cmpStr(a.service, b.service));
    const critNs = [...noPodBy.values()].reduce((a, n) => a + n.critNs, 0);
    noPod = {
      spans: services.reduce((a, s) => a + s.spans, 0),
      errors: services.reduce((a, s) => a + s.errors, 0),
      maxSelfNs: services.reduce((a, s) => Math.max(a, s.maxSelfNs), 0),
      critShare: share(critNs),
      services,
    };
  }

  const cvCount = new Map<string, number>();
  for (const p of pods) cvCount.set(p.clusterValue, (cvCount.get(p.clusterValue) ?? 0) + 1);
  const clusterValues = [...cvCount.entries()].sort((a, b) => (b[1] - a[1]) || cmpStr(a[0], b[0])).map(e => e[0]);

  errorMarks.sort((a, b) => (a.timeNs - b.timeNs) || cmpStr(a.spanId, b.spanId));
  const counts: Record<TraceMetricsFilter, number> = {
    all: pods.length,
    err: pods.filter(p => p.errors > 0).length,
    crit: pods.filter(p => p.critShare >= CRIT_FLAG_SHARE).length,
    slow: pods.filter(p => p.topSelf).length,
  };

  return {
    pods, groups,
    byPod: new Map(pods.map(p => [p.pod, p])),
    podSpans,
    criticalIds: criticalPath?.ids ?? new Set<string>(),
    errorMarks,
    noPod,
    clusterValues,
    primaryCluster: clusterValues[0] ?? '',
    traceStartNs: traceStart,
    traceEndNs: traceEnd,
    rootWallNs: rootWall || Math.max(0, traceEnd - traceStart),
    selfKnown,
    truncated: capped,
    errorSpans: errorMarks.length,
    rootCausePod,
    entryPod,
    counts,
  };
}

/** v0.10.968 — `mpod` yokken varsayılan seçim: kök neden pod'u; yoksa giriş
 *  pod'u; yoksa ilk pod. TEK pod (v0.10.962'nin ≤4 hatalı pod'u bilerek
 *  değişti: panel tek pod'u anlatır, karşılaştırma operatörün eylemi). */
export function defaultTracePod(model: TraceMetricsModel): string {
  return model.rootCausePod || model.entryPod || model.pods[0]?.pod || '';
}

/** v0.10.968 — satır süzgeci: Hatalı / Kritik yol / Yavaş + arama (pod,
 *  servis, namespace; büyük/küçük harf duyarsız alt dizge). */
export function podMatches(p: TracePodInfo, filter: TraceMetricsFilter, query: string): boolean {
  if (filter === 'err' && p.errors <= 0) return false;
  if (filter === 'crit' && p.critShare < CRIT_FLAG_SHARE) return false;
  if (filter === 'slow' && !p.topSelf) return false;
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return p.pod.toLowerCase().includes(q) || p.service.toLowerCase().includes(q) || p.namespace.toLowerCase().includes(q);
}

/** v0.10.968 — ilk görünümde açık gruplar: ≤12 pod'da hepsi; aksi hâlde
 *  işaretli pod taşıyan İLK 6 grup (sıra korunur) + seçili pod'un grubu
 *  (derin linkle gelen seçim görünür kalsın — v0.10.962 hata 1). */
export function defaultOpenGroups(model: TraceMetricsModel, selected = ''): Set<string> {
  if (model.pods.length <= 12) return new Set(model.groups.map(g => g.service));
  const out = new Set(model.groups.filter(g => g.flagged > 0).slice(0, 6).map(g => g.service));
  const sel = selected ? model.byPod.get(selected) : undefined;
  if (sel) out.add(sel.service);
  return out;
}

// ── v0.10.968 — tablo satırları (spec §4.2) ─────────────────────────────
interface RowBase { key: string; level: 1 | 2; setsize: number; posinset: number }
export type TraceMetricsRow =
  | (RowBase & { kind: 'group'; group: TracePodGroup; pods: TracePodInfo[]; open: boolean; filtered: boolean })
  | (RowBase & { kind: 'pod'; pod: TracePodInfo; prefix: boolean })
  | (RowBase & { kind: 'more'; service: string; hidden: TracePodInfo[] })
  | (RowBase & { kind: 'nopod'; summary: NoPodSummary; open: boolean })
  | (RowBase & { kind: 'nopod-svc'; svc: NoPodService });

export interface TraceMetricsRowView {
  grouping: TraceMetricsGrouping;
  filter: TraceMetricsFilter;
  query: string;
  openGroups: ReadonlySet<string>;
  expanded: ReadonlySet<string>;   // "N pod daha" açılmış servisler
  noPodOpen: boolean;
  selected: string;                // seçili pod ('' = yok)
}

type Bare<R> = R extends unknown ? Omit<R, 'level' | 'setsize' | 'posinset'> : never;
type BareRow = Bare<TraceMetricsRow>;

/** v0.10.968 — görünür satırlar. Servis kipinde grup → pod'lar (işaretsizleri
 *  "N pod daha" satırında), Düz kipte pod sırası; süzgeç/arama varken
 *  eşleşen gruplar zorla açık, daraltma yok, "Pod bilgisi olmayan" satırı
 *  gizli. aria-level/setsize/posinset burada hesaplanır (treegrid). */
export function buildRows(model: TraceMetricsModel, view: TraceMetricsRowView): TraceMetricsRow[] {
  const filtered = view.filter !== 'all' || view.query.trim() !== '';
  const top: { head: BareRow; kids: BareRow[] }[] = [];

  if (view.grouping === 'flat') {
    for (const p of model.pods) {
      if (filtered && !podMatches(p, view.filter, view.query)) continue;
      top.push({ head: { kind: 'pod', key: `p:${p.pod}`, pod: p, prefix: true }, kids: [] });
    }
  } else {
    for (const g of model.groups) {
      const pods = filtered ? g.pods.filter(p => podMatches(p, view.filter, view.query)) : g.pods;
      if (pods.length === 0) continue;
      const open = filtered || view.openGroups.has(g.service);
      const kids: BareRow[] = [];
      if (open) {
        let show = pods;
        let rest: TracePodInfo[] = [];
        if (!filtered && !view.expanded.has(g.service) && pods.length > 3 && pods.some(p => p.flagged)) {
          const keep = (p: TracePodInfo) => p.flagged || p.pod === view.selected;
          const hide = pods.filter(p => !keep(p));
          if (hide.length >= 2) { show = pods.filter(keep); rest = hide; }
        }
        for (const p of show) kids.push({ kind: 'pod', key: `p:${p.pod}`, pod: p, prefix: false });
        if (rest.length) kids.push({ kind: 'more', key: `m:${g.service}`, service: g.service, hidden: rest });
      }
      top.push({ head: { kind: 'group', key: `g:${g.service}`, group: g, pods, open, filtered }, kids });
    }
  }
  if (!filtered && model.noPod) {
    const kids: BareRow[] = view.noPodOpen
      ? model.noPod.services.map(s => ({ kind: 'nopod-svc' as const, key: `n:${s.service}`, svc: s }))
      : [];
    top.push({ head: { kind: 'nopod', key: 'n:', summary: model.noPod, open: view.noPodOpen }, kids });
  }

  const rows: TraceMetricsRow[] = [];
  top.forEach((t, i) => {
    rows.push({ ...t.head, level: 1, setsize: top.length, posinset: i + 1 });
    t.kids.forEach((k, j) => rows.push({ ...k, level: 2, setsize: t.kids.length, posinset: j + 1 }));
  });
  return rows;
}

/** v0.10.968 — "N pod daha" satırının notları (gizlenen pod'lar üzerinden):
 *  sapan her şey görünür kalsın — daraltma bir kırmızıyı SAKLAMAZ. */
export interface MoreNote { text: string; tone: 'err' | 'warn' | 'faint' }
export function moreRowNotes(hidden: TracePodInfo[], metrics: (pod: string) => PodMetricState): MoreNote[] {
  const notes: MoreNote[] = [];
  let cpuHot = 0, memHot = 0, restarts = 0, bad = 0;
  const unmapped = new Map<string, number>();
  for (const p of hidden) {
    const st = metrics(p.pod);
    if (st.kind === 'ok') {
      if (st.data.cpuLevel === 'err') cpuHot++;
      if (st.data.memLevel === 'err') memHot++;
      restarts += Math.max(0, st.data.now.restarts ?? 0);
    } else if (st.kind === 'unmapped') {
      unmapped.set(st.clusterValue, (unmapped.get(st.clusterValue) ?? 0) + 1);
    } else if (st.kind === 'error') {
      bad++;
    }
  }
  if (cpuHot) notes.push({ text: `CPU ≥%90: ${cpuHot} pod`, tone: 'err' });
  if (memHot) notes.push({ text: `bellek ≥%90: ${memHot} pod`, tone: 'err' });
  if (restarts) notes.push({ text: `${restarts} restart (şu an)`, tone: 'warn' });
  for (const [cv, n] of unmapped) notes.push({ text: `${cv || 'cluster özniteliği yok'} eşlenmemiş: ${n} pod`, tone: 'faint' });
  if (bad) notes.push({ text: `okunamadı: ${bad} pod`, tone: 'err' });
  return notes;
}

// ── v0.10.968 — toplu uç dilimleri (spec §3) ────────────────────────────
export interface TracePodChunk {
  key: string;                      // `${cv}#${index}` — kararlı kimlik
  clusterValue: string;
  index: number;                    // cluster değeri içindeki sıra
  pods: { ns: string; pod: string }[];
  podsParam: string;                // `ns/pod,…` — sorgu anahtarı
}

const QUOTE_META = new Set('\\.+*?()|[]{}^$'.split(''));
/** v0.10.968 — Go regexp.QuoteMeta sonrası uzunluk: meta karakter 2 sayılır. */
export function quoteMetaLen(s: string): number {
  let n = 0;
  for (const c of s) n += QUOTE_META.has(c) ? 2 : 1;
  return n;
}

/** v0.10.968 — pod'lar cluster değerine göre, TABLO sırasında (servis kipi,
 *  ilgi sırası) dilimlenir; dilim ≤64 pod ve Σ(quoteMetaLen+1) ≤4000
 *  (sunucunun seçici tavanı). Eşlenmemiş değerin pod'ları da bir dilimde:
 *  o istek Thanos'a gitmez, ucuzdur ve cevabı "eşlenmemiş"i SÖYLER. */
export function tracePodChunks(model: TraceMetricsModel): TracePodChunk[] {
  const order: string[] = [];
  const by = new Map<string, TracePodInfo[]>();
  for (const g of model.groups) {
    for (const p of g.pods) {
      let l = by.get(p.clusterValue);
      if (!l) { l = []; by.set(p.clusterValue, l); order.push(p.clusterValue); }
      l.push(p);
    }
  }
  const out: TracePodChunk[] = [];
  for (const cv of order) {
    let cur: TracePodInfo[] = [];
    let len = 0;
    let idx = 0;
    const flush = () => {
      if (!cur.length) return;
      const pods = cur.map(p => ({ ns: p.namespace, pod: p.pod }));
      out.push({ key: `${cv}#${idx}`, clusterValue: cv, index: idx, pods, podsParam: pods.map(p => `${p.ns}/${p.pod}`).join(',') });
      idx++;
      cur = [];
      len = 0;
    };
    for (const p of by.get(cv)!) {
      const add = quoteMetaLen(p.pod) + 1;
      if (cur.length && (cur.length + 1 > TRACE_POD_CHUNK_MAX || len + add > TRACE_POD_CHUNK_REGEX_MAX)) flush();
      cur.push(p);
      len += add;
    }
    flush();
  }
  return out;
}

/** v0.10.968 — dilimin önbellek durumu: settled (veri ya da hata var),
 *  pending (istek uçuşta), idle (hiç başlamadı). */
export type ChunkPhase = 'idle' | 'pending' | 'settled';

/** v0.10.968 — hangi dilimler açık: cluster değeri başına ilk 4 dilim kendiliğinden
 *  (256 pod), sonrası yalnız "Kalan … yükle" (loadRest) ya da tek dilim zorlaması
 *  (forced, panelin "Yükle"si); aynı anda en çok 4 istek uçuşta — dilim i, kendinden
 *  ÖNCEKİ açık dilimlerden 4'ten azı beklerken açılır. Başlamış dilim açık kalır. */
export function chunksToEnable(
  chunks: Pick<TracePodChunk, 'key' | 'index'>[],
  phases: ChunkPhase[],
  opts: { loadRest: boolean; forced?: ReadonlySet<string> },
): boolean[] {
  let inflight = 0;
  return chunks.map((c, i) => {
    if (!chunkEligible(c, opts)) return false;
    const ph = phases[i] ?? 'idle';
    if (ph === 'settled') return true;
    if (ph === 'pending') { inflight++; return true; }
    if (inflight < TRACE_POD_MAX_INFLIGHT) { inflight++; return true; }
    return false;
  });
}

/** v0.10.968 — dilim YÜKLENECEK mi: otomatik sınır içinde (cluster değeri
 *  başına ilk 4) ya da açıkça istendi ("Kalan … yükle" / panelin "Yükle"si).
 *  Uygun ama uçuş sınırında bekleyen dilim "yükleniyor"dur, "yüklenmedi" değil. */
export function chunkEligible(c: Pick<TracePodChunk, 'key' | 'index'>, opts: { loadRest: boolean; forced?: ReadonlySet<string> }): boolean {
  return c.index < TRACE_POD_AUTO_CHUNKS || opts.loadRest || !!opts.forced?.has(c.key);
}

/** v0.10.968 — useQueries sonucunun saf özeti (podMetricState girdisi).
 *  `eligible` = yüklenecek (chunkEligible); false → `idle` ("yüklenmedi"). */
export interface ChunkSnapshot {
  eligible: boolean;
  status: 'pending' | 'error' | 'success';
  fetching: boolean;
  error: unknown;
  data: TracePodMetricsResponse | undefined;
  dataUpdatedAt: number;
}

/** v0.10.968 — trace anı değeri: trace'i ±1 adım kesen dolu kovaların EN
 *  BÜYÜĞÜ (b + adım ≥ başlangıç − adım ve b ≤ bitiş + adım); yoksa null. */
export function traceMomentValue(
  values: (number | null)[], startSec: number, stepSec: number, traceStartNs: number, traceEndNs: number,
): number | null {
  if (!(stepSec > 0)) return null;
  const t0 = traceStartNs / 1e9;
  const t1 = traceEndNs / 1e9;
  let best: number | null = null;
  for (let i = 0; i < values.length; i++) {
    const v = values[i];
    if (v == null || !Number.isFinite(v)) continue;
    const b = startSec + i * stepSec;
    if (b + stepSec < t0 - stepSec || b > t1 + stepSec) continue;
    if (best === null || v > best) best = v;
  }
  return best;
}

/** v0.10.968 — limite oran: ≥%90 err, ≥%70 warn; limit yok/değer yok → none. */
export function metricLevel(value: number | null | undefined, limit?: number): MetricLevel {
  if (value == null || !Number.isFinite(value) || !limit || !(limit > 0)) return 'none';
  const r = value / limit;
  if (r >= LEVEL_ERR) return 'err';
  if (r >= LEVEL_WARN) return 'warn';
  return 'none';
}

/** v0.10.968 — hata gövdesinin iç mesajı (apiErrorDetail) + zaman aşımı tespiti.
 *  Sunucunun errUpstream öneki ("upstream metrics backend: ") 80 karakterlik
 *  kırpmadan ÖNCE atılır: yoksa asıl sebep hep kesilen kuyrukta kalıyordu. */
export function chunkError(e: unknown): { message: string; timeout: boolean } {
  const message = apiErrorDetail(e).message.trim().replace(/^upstream metrics backend:\s*/, '');
  return { message, timeout: /zaman aşımı/.test(message) };
}

/** v0.10.968 — hata nedeninin kısa metni: zaman aşımı → "10 sn zaman aşımı",
 *  değilse kırpılmış mesaj (≤80). */
export function metricErrorReason(st: { message: string; timeout: boolean }): string {
  if (st.timeout) return '10 sn zaman aşımı';
  const m = st.message.trim();
  return m.length > 80 ? m.slice(0, 79) + '…' : m;
}

/** v0.10.968 — dilim sonucu → pod'un metrik durumu (spec §3 tablo). Hata
 *  HİÇBİR ZAMAN boş sonuç gibi yazılmaz (v0.10.962 hata 3). */
export function podMetricState(
  pod: Pick<TracePodInfo, 'pod' | 'namespace'>,
  snap: ChunkSnapshot | undefined,
  trace: { startNs: number; endNs: number },
): PodMetricState {
  if (!snap) return { kind: 'loading' };
  if (snap.status === 'success' && snap.data) return fromResponse(pod, snap.data, snap.dataUpdatedAt, trace);
  if (snap.status === 'error') {
    if (snap.fetching) return { kind: 'loading' };
    return { kind: 'error', ...chunkError(snap.error) };
  }
  if (!snap.eligible && !snap.fetching) return { kind: 'idle' };
  return { kind: 'loading' };
}

function fromResponse(
  pod: Pick<TracePodInfo, 'pod' | 'namespace'>, data: TracePodMetricsResponse, fetchedAtMs: number,
  trace: { startNs: number; endNs: number },
): PodMetricState {
  if (!data.thanos) return { kind: 'off' };
  if (!data.mapped) return { kind: 'unmapped', clusterValue: data.clusterValue, reason: data.unmappedReason ?? 'no_remote_cluster' };
  const cluster = data.cluster ?? { id: '', name: data.clusterValue };
  const row: TracePodSeriesRow | undefined = data.pods.find(r => r.pod === pod.pod);
  if (!row || row.state === 'no_samples') return { kind: 'no_samples', cluster };
  if (row.state === 'ambiguous') return { kind: 'ambiguous', cluster };
  const startSec = data.start ?? 0;
  const stepSec = data.step ?? 0;
  const cpu = row.cpu ?? [];
  const mem = row.mem ?? [];
  const cpuAt = traceMomentValue(cpu, startSec, stepSec, trace.startNs, trace.endNs);
  const memAt = traceMomentValue(mem, startSec, stepSec, trace.startNs, trace.endNs);
  const series: PodMetricSeries = {
    startSec, stepSec, cpu, mem, cpuAt, memAt,
    cpuLimit: row.cpuLimit, memLimit: row.memLimit, cpuRequest: row.cpuRequest, memRequest: row.memRequest,
    cpuLevel: metricLevel(cpuAt, row.cpuLimit),
    memLevel: metricLevel(memAt, row.memLimit),
    inventory: row.inventory,
    instantPartial: data.instant === 'partial',
    now: { phase: row.phase, restarts: row.restarts, lastTermReason: row.lastTermReason, lastTermAtSec: row.lastTermAt },
    cluster,
    namespace: row.ns || pod.namespace,
    fetchedAtMs,
  };
  return { kind: 'ok', data: series };
}

// ── v0.10.968 — CPU / Bellek hücresinin kopyası (spec §4.3; saf) ──────
export type MetricKind = 'cpu' | 'mem';

/** v0.10.968 — hücrenin ton sınıfı (`<td>`) ve vurgusu (600). */
export function metricCellTone(st: PodMetricState | null, which: MetricKind): { tone?: CellTone; strong: boolean } {
  if (!st) return { strong: false };
  switch (st.kind) {
    case 'ok': {
      if ((which === 'cpu' ? st.data.cpuAt : st.data.memAt) == null) return { tone: 'faint', strong: false };
      const lv = which === 'cpu' ? st.data.cpuLevel : st.data.memLevel;
      if (lv === 'err') return { tone: 'err', strong: true };
      if (lv === 'warn') return { tone: 'warn', strong: false };
      return { strong: false };
    }
    case 'error': return { tone: 'err', strong: true };
    case 'loading': case 'off': return { strong: false };
    default: return { tone: 'faint', strong: false };
  }
}

const val = (which: MetricKind, v: number) => (which === 'cpu' ? fmtCores(v) : fmtBytesTr(v));
const short = (which: MetricKind, v: number) => (which === 'cpu' ? fmtCoresShort(v) : fmtBytesTr(v));

/** v0.10.968 — `ok` hücresinin metni + ipucu (saf; test ve panel aynı kopyayı okur). */
export function okCellText(d: PodMetricSeries, which: MetricKind): { text: string; tip: string } {
  const at = which === 'cpu' ? d.cpuAt : d.memAt;
  const lim = which === 'cpu' ? d.cpuLimit : d.memLimit;
  const req = which === 'cpu' ? d.cpuRequest : d.memRequest;
  if (at == null) return { text: '—', tip: 'Trace anında örnek yok (±1 adım)' };
  if (lim && lim > 0) {
    const pct = Math.round((at / lim) * 100);
    return {
      text: fmtPct(at / lim),
      tip: `${val(which, at)} · limitin %${pct}${trPossessive(pct)} (limit şu an ${val(which, lim)})`,
    };
  }
  if (d.inventory === 'absent') return { text: short(which, at), tip: 'Pod şu anki envanterde yok — limit bilinmiyor' };
  if (d.inventory === 'unknown') return { text: short(which, at), tip: 'limit okunamadı (kube-state-metrics sorgusu başarısız)' };
  if (d.instantPartial) {
    // v0.10.968 — kube-state-metrics'in bir kısmı düştü: limit belki hiç
    // okunmadı. "tanımsız" bir OKUMA iddiasıdır, burada dürüst olan "bilinmiyor".
    const why = 'limit bilinmiyor (kube-state-metrics sorgusu kısmen başarısız)';
    if (req && req > 0) {
      const pct = Math.round((at / req) * 100);
      return { text: short(which, at), tip: `${why} · isteğin %${pct}${trPossessive(pct)} (şu an)` };
    }
    return { text: short(which, at), tip: why };
  }
  if (req && req > 0) {
    const pct = Math.round((at / req) * 100);
    return { text: short(which, at), tip: `limit tanımsız · isteğin %${pct}${trPossessive(pct)} (şu an)` };
  }
  return { text: short(which, at), tip: 'limit ve istek tanımsız' };
}

/** v0.10.968 — `ok` dışı durumların metni + ipucu. */
export function stateCellText(st: Exclude<PodMetricState, { kind: 'ok' }>): { text: string; tip: string } {
  switch (st.kind) {
    case 'loading': return { text: '', tip: 'Yükleniyor' };
    case 'idle': return { text: '—', tip: "Metrik yüklenmedi — otomatik yükleme ilk 256 pod'da durur" };
    case 'off': return { text: '', tip: '' };
    case 'unmapped': return st.reason === 'no_cluster_value'
      ? { text: '—', tip: "Span'lerde cluster özniteliği yok" }
      : { text: '—', tip: `${st.clusterValue} bir Remote Cluster kaydına eşlenmemiş` };
    case 'no_samples': return { text: 'örnek yok', tip: 'Bu pencerede Thanos örneği yok' };
    case 'ambiguous': return { text: 'belirsiz', tip: "Pod adı birden çok namespace'te — span'de k8s.namespace.name yok" };
    case 'error': return { text: 'okunamadı', tip: `Veri okunamadı — bu bir hata, boş sonuç değil. ${metricErrorReason(st)}` };
  }
}

/** v0.10.968 — grup satırının CPU/Bellek hücresi: en SICAK pod (limite en
 *  yüksek oran; limit yoksa en yüksek mutlak değer). Hiç `ok` yoksa önce
 *  bekleyen, sonra okunamayan, sonra ilk pod'un durumu. */
export function hottestPodState(pods: string[], metrics: (pod: string) => PodMetricState, which: 'cpu' | 'mem'): PodMetricState | null {
  let best: { st: PodMetricState; ratio: number; abs: number } | null = null;
  let loading: PodMetricState | null = null;
  let error: PodMetricState | null = null;
  for (const p of pods) {
    const st = metrics(p);
    if (st.kind === 'ok') {
      const at = which === 'cpu' ? st.data.cpuAt : st.data.memAt;
      if (at == null) continue;
      const lim = which === 'cpu' ? st.data.cpuLimit : st.data.memLimit;
      const ratio = lim && lim > 0 ? at / lim : -1;
      if (!best || ratio > best.ratio || (ratio === best.ratio && at > best.abs)) best = { st, ratio, abs: at };
    } else if (st.kind === 'loading') loading ??= st;
    else if (st.kind === 'error') error ??= st;
  }
  if (best) return best.st;
  if (loading) return loading;
  if (error) return error;
  return pods.length ? metrics(pods[0]) : null;
}

// ── v0.10.968 — metrik kapsamı (özet şeridi + kapsam diyaloğu; saf) ───
export type CoverageStatus = 'mapped' | 'unmapped' | 'no_cluster' | 'off' | 'pending' | 'error';
export interface CoverageLine { clusterValue: string; name: string; pods: number; status: CoverageStatus }
export interface CoverageSummary {
  lines: CoverageLine[];
  /** Cluster'ı eşlenmiş pod sayısı. v0.10.968 — okunamayan (502) cluster da
   *  eşlenmiş sayılır: sunucu 502'yi yalnız eşlemeden SONRA döner, yani okuma
   *  hatası bir kapsam açığı değildir (şeritte "N servis okunamadı" ayrı). */
  mapped: number;
  total: number;
  /** Sonucu henüz gelmemiş cluster değeri var mı. */
  pending: boolean;
  withData: number;
  noSamples: string[];
  failed: string[];
  /** v0.10.968 — örneği olmayan / okunamayan pod'ların SERVİSLERİ (ilk görülme
   *  sırasıyla, tekrarsız): kapsam satırı hash eki değil servis adı söyler. */
  noSamplesSvcs: string[];
  failedSvcs: string[];
  timeout: boolean;
  ambiguous: string[];
  idle: string[];
}

/** v0.10.968 — cluster değeri başına eşleme durumu + pod sonuçlarının dökümü. */
export function coverageSummary(model: TraceMetricsModel, metrics: (pod: string) => PodMetricState): CoverageSummary {
  const byCv = new Map<string, TracePodInfo[]>();
  for (const p of model.pods) {
    const l = byCv.get(p.clusterValue);
    if (l) l.push(p); else byCv.set(p.clusterValue, [p]);
  }
  const out: CoverageSummary = {
    lines: [], mapped: 0, total: model.pods.length, pending: false,
    withData: 0, noSamples: [], failed: [], noSamplesSvcs: [], failedSvcs: [], timeout: false, ambiguous: [], idle: [],
  };
  for (const cv of model.clusterValues) {
    const pods = byCv.get(cv) ?? [];
    const states = pods.map(p => metrics(p.pod));
    let status: CoverageStatus = 'error';
    let name = cv;
    const known = states.find(s => s.kind === 'ok' || s.kind === 'no_samples' || s.kind === 'ambiguous');
    const unm = states.find(s => s.kind === 'unmapped');
    if (states.some(s => s.kind === 'off')) status = 'off';
    else if (unm && unm.kind === 'unmapped') status = unm.reason === 'no_cluster_value' ? 'no_cluster' : 'unmapped';
    else if (known) {
      status = 'mapped';
      name = known.kind === 'ok' ? known.data.cluster.name : known.kind === 'no_samples' || known.kind === 'ambiguous' ? known.cluster.name : cv;
    } else if (states.some(s => s.kind === 'error')) status = 'error';
    // v0.10.968 — hata beklemeden ÖNCE: otomatik dilimleri düşmüş, kalanı
    // yüklenmemiş bir değer sonsuza dek "pending" kalıp öteki değerlerin
    // uyarısını gizlemesin.
    else if (states.some(s => s.kind === 'loading' || s.kind === 'idle')) status = 'pending';
    // v0.10.968 — 502 yalnız eşlemeden sonra döner: okunamayan cluster kapsam açığı değildir.
    if (status === 'mapped' || status === 'error') out.mapped += pods.length;
    if (status === 'pending') out.pending = true;
    out.lines.push({ clusterValue: cv, name: name || cv, pods: pods.length, status });
    pods.forEach((p, i) => {
      const st = states[i];
      if (st.kind === 'ok') out.withData++;
      else if (st.kind === 'no_samples') {
        out.noSamples.push(p.pod);
        if (!out.noSamplesSvcs.includes(p.service)) out.noSamplesSvcs.push(p.service);
      }
      else if (st.kind === 'ambiguous') out.ambiguous.push(p.pod);
      else if (st.kind === 'idle') out.idle.push(p.pod);
      else if (st.kind === 'error') {
        out.failed.push(p.pod);
        if (!out.failedSvcs.includes(p.service)) out.failedSvcs.push(p.service);
        if (st.timeout) out.timeout = true;
      }
    });
  }
  return out;
}

/** v0.10.968 — seçim kuralı (spec §4.4): aynı servisten ve iki pod'un da
 *  metriği `ok` ise yeni pod BAŞA gelir, diğerleri kalır (≤4, sondaki düşer);
 *  aksi hâlde karşılaştırma [pod]'a iner. Servis değiştiyse çağıran duyurur. */
export function selectPod(
  compare: string[], pod: string, model: TraceMetricsModel, ok: (p: string) => boolean,
): { next: string[]; serviceChanged: boolean } {
  const cur = compare[0];
  const curSvc = cur ? model.byPod.get(cur)?.service : undefined;
  const svc = model.byPod.get(pod)?.service;
  if (cur && curSvc !== undefined && curSvc === svc && ok(pod) && ok(cur)) {
    return { next: [pod, ...compare.filter(p => p !== pod)].slice(0, TRACE_METRICS_MAX_COMPARE), serviceChanged: false };
  }
  return { next: [pod], serviceChanged: !!cur && curSvc !== undefined && curSvc !== svc };
}

/** v0.10.962 — tavan: seçim 4'te ve pod AYNI servisten, seçili değil →
 *  tıklama bir şey yapamaz. Panel bu çipi devre dışı + gerekçeli gösterir
 *  (eskiden tıklama sessizce yutuluyordu); togglePod aynı kuralı kullanır. */
export function podAtCap(selected: string[], pod: string, pods: { pod: string; service: string }[]): boolean {
  if (selected.includes(pod) || selected.length < TRACE_METRICS_MAX_COMPARE) return false;
  const svcOf = (p: string) => pods.find(x => x.pod === p)?.service;
  return svcOf(selected[0]) === svcOf(pod);
}

/** Seçim kuralı: yalnız AYNI servisin pod'ları üst üste (≤4). Başka servisten
 *  pod seçmek seçimi o pod'la değiştirir; seçili pod'a tıklamak çıkarır (son
 *  pod çıkarılamaz); tavandayken yeni pod eklenmez (podAtCap). */
export function togglePod(selected: string[], pod: string, pods: { pod: string; service: string }[]): string[] {
  const svcOf = (p: string) => pods.find(x => x.pod === p)?.service;
  if (selected.includes(pod)) {
    return selected.length > 1 ? selected.filter(p => p !== pod) : selected;
  }
  const svc = svcOf(pod);
  if (selected.length === 0 || svcOf(selected[0]) !== svc) return [pod];
  if (podAtCap(selected, pod, pods)) return selected;
  return [...selected, pod];
}

/** Trace span'larının zaman aralığı ± pencere (ns).
 *  v0.10.968 — span'sız girdide 0 (eskiden Date.now(); saf yardımcı saat okumaz). */
export function traceMetricsWindow(spans: SpanRow[], padMin: number): { from: number; to: number; startNs: number; endNs: number } {
  let start = Infinity, end = -Infinity;
  for (const s of spans) {
    start = Math.min(start, s.startTime);
    end = Math.max(end, spanEndNs(s));
  }
  if (!Number.isFinite(start)) start = end = 0;
  return traceWindowNs(start, end, padMin);
}

/** v0.10.968 — trace başlangıç/bitişinden ± pencere (ns). */
export function traceWindowNs(startNs: number, endNs: number, padMin: number): { from: number; to: number; startNs: number; endNs: number } {
  const pad = padMin * 60 * 1e9;
  return { from: startNs - pad, to: endNs + pad, startNs, endNs };
}

/** v0.10.962 — "Pod sayfasında aç" linki panelin GÖSTERDİĞİ pencereyi taşır
 *  (eskiden range'siz → /pod son 1 saate düşüyordu; eski bir trace'te
 *  bambaşka bir zaman). range = windowRangeParam (custom:<fromMs>-<toMs>;
 *  from aşağı, to yukarı yuvarlanır — pencere daralmaz), at = trace
 *  başlangıcı (ms): /pod o an geçerli entity kaydını çözer (onaylı mockup'ın
 *  podHref'i). Saf. */
export function traceMetricsPodHref(
  t: { pod: string; cluster: string; namespace: string; service: string },
  w: { from: number; to: number; startNs: number },
): string {
  return podDetailPath({
    pod: t.pod, cluster: t.cluster, namespace: t.namespace || undefined, service: t.service || undefined,
    range: windowRangeParam({ fromNs: w.from, toNs: w.to }) || null,
    at: Math.floor(w.startNs / 1e6) || undefined,
  });
}

/** Kısa pod etiketi (grafikte): son iki "-" parçası (replicaset-hash + ek). */
export function shortPod(pod: string): string {
  const parts = pod.split('-');
  return parts.length > 2 ? '…' + parts.slice(-2).join('-') : pod;
}

// jvmPodSeries — v0.10.923 (operatör: "sonra JVM GC metriğine bakarız"):
// pod kırılımlı metrik serilerini Trace Metrics grafiğine hazırlar — boş
// seriler düşer, etiket kısa pod adı (tam ad fullKey'de, tooltip/lejant
// title'ında), değer ölçeklenir (GC süresi saniye → ms). SAF.
//
// v0.10.968 — `labelOf` (varsayılan shortPod): seçili pod paneli JVM
// çizgilerini çip / Bellek / CPU ile AYNI etiketle adlandırır (suffixLabels);
// renk addan türediği için aynı pod dört grafikte de aynı renkte kalır.
export function jvmPodSeries(
  series: SpanMetricSeries[] | null | undefined, scale = 1, labelOf: (pod: string) => string = shortPod,
): SpanMetricSeries[] {
  return (series ?? [])
    .filter(s => s.points.length > 0)
    .map(s => {
      const pod = s.groupKey[0] ?? '';
      return {
        ...s,
        groupKey: [labelOf(pod)],
        fullKey: [pod],
        points: scale === 1 ? s.points : s.points.map(p => ({ time: p.time, value: p.value * scale })),
      };
    });
}
