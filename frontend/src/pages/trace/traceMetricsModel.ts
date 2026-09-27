// traceMetricsModel.ts — v0.10.968 — Trace › Metrics yeniden tasarımının ortak model tipleri.
//
// v0.10.968 — Yalnız tipler ve sabitler (apiContract E, birebir). L-table
// (tablo + kabuk) ve L-pod (seçili pod paneli + odak görünümü) buradan okur;
// Go tarafı (internal/thanos/trace_pods.go) TRACE_POD_CHUNK_MAX ve
// TRACE_POD_CHUNK_REGEX_MAX değerlerini bu dosyadan pinler (route_pins emsali).
import type { SpanRow } from '@/lib/types';

export const TRACE_METRICS_MAX_COMPARE = 4;
export const TRACE_METRICS_WINDOWS = [5, 15, 60] as const;
export type TraceMetricsWindow = (typeof TRACE_METRICS_WINDOWS)[number];
export const TRACE_METRICS_DEFAULT_WINDOW: TraceMetricsWindow = 15;
export const TRACE_METRICS_MDP = 120;
export const CRIT_FLAG_SHARE = 0.03;
export const TOP_SELF_N = 5;
export const LEVEL_WARN = 0.7;
export const LEVEL_ERR = 0.9;
export const SIBLING_RATIO_WARN = 1.5;
export const MAX_SIBLING_LINES = 12;
export const TRACE_POD_CHUNK_MAX = 64;
export const TRACE_POD_CHUNK_REGEX_MAX = 4000;
export const TRACE_POD_AUTO_CHUNKS = 4;
export const TRACE_POD_MAX_INFLIGHT = 4;
export const TRACE_METRICS_URL_PARAMS = ['mpod', 'mwin', 'mf', 'mq', 'mgrp', 'msib', 'mview'] as const;

export type MetricLevel = 'none' | 'warn' | 'err';
export type TraceMetricsFilter = 'all' | 'err' | 'crit' | 'slow';
export type TraceMetricsGrouping = 'service' | 'flat';

export interface TracePodError { spanId: string; name: string; status: string; timeNs: number }
export interface PodNameParts { head: string; rs: string; tail: string } // rs = muted hash segment ('' = no split)

export interface TracePodInfo {
  pod: string;
  namespace: string;          // '' = not on spans
  clusterValue: string;       // '' = no cluster attribute
  service: string;
  spans: number;
  errors: number;             // spanHasError rule
  maxSelfNs: number;          // 0 when !selfKnown
  maxSelfSpanId: string;
  critNs: number;
  critShare: number;          // 0..1 of rootWallNs
  topSelf: boolean;
  rootCause: boolean;
  entry: boolean;
  firstError: TracePodError | null;
  activeFromNs: number;
  activeToNs: number;
  flagged: boolean;
  runtime: string;            // telemetry.sdk.language, '' unknown
  replicaSet: string;         // k8s.replicaset.name or parsed, '' unknown
  nameParts: PodNameParts;
}
export interface TracePodGroup {
  service: string;
  pods: TracePodInfo[];       // pod order
  spans: number;
  errors: number;
  maxSelfNs: number;
  critShare: number;          // sum
  flagged: number;
  lead: boolean;              // error-source service
}
export interface NoPodService { service: string; spans: number; errors: number; maxSelfNs: number; critShare: number }
export interface NoPodSummary { spans: number; errors: number; maxSelfNs: number; critShare: number; services: NoPodService[] }
export interface TraceErrorMark { spanId: string; pod: string; service: string; name: string; status: string; timeNs: number }

export interface TraceMetricsModel {
  pods: TracePodInfo[];                   // flat relevance order
  groups: TracePodGroup[];                // relevance order
  byPod: ReadonlyMap<string, TracePodInfo>;
  podSpans: ReadonlyMap<string, SpanRow[]>;
  criticalIds: ReadonlySet<string>;
  errorMarks: TraceErrorMark[];           // every error span of the trace, start order
  noPod: NoPodSummary | null;
  clusterValues: string[];
  primaryCluster: string;                 // value with most pods; its prefix is hidden
  traceStartNs: number;
  traceEndNs: number;
  rootWallNs: number;
  selfKnown: boolean;
  truncated: boolean;
  errorSpans: number;
  rootCausePod: string;
  entryPod: string;
  counts: Record<TraceMetricsFilter, number>;
}

export interface TraceMetricsWindowInfo {
  fromNs: number;
  toNs: number;
  startNs: number;
  endNs: number;
  win: TraceMetricsWindow;
  stepSec: number | null;     // null until the first mapped response
  openWindow: boolean;        // toNs later than now − 10 min (computed in useMemo)
}

export interface PodMetricSeries {
  startSec: number;
  stepSec: number;
  cpu: (number | null)[];
  mem: (number | null)[];
  cpuAt: number | null;
  memAt: number | null;
  cpuLimit?: number; memLimit?: number; cpuRequest?: number; memRequest?: number;
  cpuLevel: MetricLevel;
  memLevel: MetricLevel;
  inventory: 'present' | 'absent' | 'unknown';
  // v0.10.968 — kube-state-metrics sorgularının bir kısmı başarısız (instant
  // "partial"): eksik limit/istek "tanımsız" DEĞİL "bilinmiyor" yazılır.
  instantPartial?: boolean;
  now: { phase?: string; restarts?: number; lastTermReason?: string; lastTermAtSec?: number };
  cluster: { id: string; name: string };
  namespace: string;          // resolved (may come from Thanos)
  fetchedAtMs: number;
}
export type PodMetricState =
  | { kind: 'loading' }
  | { kind: 'idle' }
  | { kind: 'off' }
  | { kind: 'unmapped'; clusterValue: string; reason: 'no_cluster_value' | 'no_remote_cluster' }
  | { kind: 'error'; message: string; timeout: boolean }
  | { kind: 'no_samples'; cluster: { id: string; name: string } }
  | { kind: 'ambiguous'; cluster: { id: string; name: string } }
  | { kind: 'ok'; data: PodMetricSeries };

export interface TracePodPanelProps {
  model: TraceMetricsModel;
  selected: TracePodInfo;                      // === byPod.get(compare[0])
  compare: string[];                           // pod names, ≤4, same service, first = selected
  metrics: (pod: string) => PodMetricState;
  window: TraceMetricsWindowInfo;
  siblingLines: boolean;
  focus: boolean;                              // mview=pod
  onCompareChange: (next: string[]) => void;
  onSelect: (pod: string) => void;             // applies selectPod rule
  onClose: () => void;                         // mpod=-
  onToggleFocus: () => void;
  onToggleSiblingLines: () => void;
  onShowSpans: (pod: string) => void;
  onOpenSpan: (spanId: string) => void;
  onRetry: (pod: string) => void;              // refetch or enable the chunk holding this pod
  announce: (msg: string) => void;             // polite live region owned by the shell
}
