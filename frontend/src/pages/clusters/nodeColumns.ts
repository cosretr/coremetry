// nodeColumns — Clusters detay "Nodes" tablosunun kolonları ve hücre metinleri
// (Clusters S6; plan docs/frontend/clusters-shadcn-plan.md §0 D3, §4).
// SAF: girdi yalnız ClusterNodeRow (S3 alanları), ek sorgu yok.
//
// D3: varsayılan kolonlar Node · Role · Status · Pods (N / kapasite) · CPU ·
// Memory ("kullanılan / kapasite · %" + çubuk). Ayrılabilir, istek / ayrılabilir
// %, limit toplamı, ağ, kubelet / OS / çekirdek ve Cluster "Tüm kolonlar"da.
// Sıra: dikkat isteyenler önce, sonra bellek yüzdesi (Status kolonunun sıralama
// değeri ikisini birden taşır). Kapasite yoksa hücre yalnız kullanılanı yazar.
import type { ColumnDef } from '@/components/coremetry/DataTable';
import type { ColumnModel } from '@/lib/columnModel';

type CellTone = NonNullable<ReturnType<NonNullable<ColumnDef<unknown>['tone']>>>;
import type { ClusterNodeRow } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { cpuUsageText, memUsageText } from './fleetSummary';
import { safePct } from './thresholds';

export interface NodeStatus {
  /** Tek sözcük (mockup: Ready, NotReady, Bellek / Disk / PID baskısı). */
  word: string;
  tone: CellTone | undefined;
  /** Dikkat istiyor mu (NotReady ya da etkin baskı koşulu). */
  attention: boolean;
  /** Etkin koşulların tamamı (ipucu). */
  title?: string;
}

const PRESSURE_WORD: Record<string, string> = {
  MemoryPressure: 'Bellek baskısı',
  DiskPressure: 'Disk baskısı',
  PIDPressure: 'PID baskısı',
};
// Birden çok baskı varsa sözcük bu sırayla seçilir; hepsi ipucunda.
const PRESSURE_ORDER = ['MemoryPressure', 'DiskPressure', 'PIDPressure'];

/** Ready koşulu + baskı koşullarından tek sözcük. Koşul serisi yoksa "—". */
export function nodeStatus(r: Pick<ClusterNodeRow, 'ready' | 'pressure'>): NodeStatus {
  const pressure = (r.pressure ?? []).filter(p => p in PRESSURE_WORD);
  const title = pressure.length > 0 ? pressure.map(p => PRESSURE_WORD[p]).join(', ') : undefined;
  if (r.ready === 'false') return { word: 'NotReady', tone: 'err', attention: true, title };
  const first = PRESSURE_ORDER.find(p => pressure.includes(p));
  if (first) return { word: PRESSURE_WORD[first], tone: 'warn', attention: true, title };
  if (r.ready === 'unknown') return { word: 'Unknown', tone: 'warn', attention: true };
  if (r.ready === 'true') return { word: 'Ready', tone: undefined, attention: false };
  return { word: '', tone: undefined, attention: false };
}

/** Yüzde: kapasite varsa kullanılan / kapasite, yoksa node_exporter yüzdesi. */
export function nodeCpuPct(r: ClusterNodeRow): number | null {
  return safePct(r.cpuCores, r.cpuCapacityCores) ?? (r.cpuPct ?? null);
}
export function nodeMemPct(r: ClusterNodeRow): number | null {
  return safePct(r.memBytes, r.memCapacityBytes) ?? (r.memPct ?? null);
}

/** "212 / 250"; kapasite yoksa "212"; sayı yoksa ''. */
export function nodePodsText(r: Pick<ClusterNodeRow, 'pods' | 'podCapacity'>): string {
  if (r.pods == null) return '';
  return r.podCapacity ? `${fmtNum(r.pods)} / ${fmtNum(r.podCapacity)}` : fmtNum(r.pods);
}

export const nodeCpuText = (r: ClusterNodeRow): string => cpuUsageText(r.cpuCores, r.cpuCapacityCores) ?? '';
export const nodeMemText = (r: ClusterNodeRow): string => memUsageText(r.memBytes, r.memCapacityBytes) ?? '';

const coreText = (v?: number) => (v ? cpuUsageText(v) ?? '' : '');
const pair = (a: string, b: string) => [a, b].filter(Boolean).join(' · ');

/** "30 çekirdek · 120 GiB" (ayrılabilir / limit toplamı). */
export function nodeAllocText(r: ClusterNodeRow): string {
  return pair(coreText(r.cpuAllocatableCores), r.memAllocatableBytes ? fmtBytes(r.memAllocatableBytes) : '');
}
export function nodeLimitsText(r: ClusterNodeRow): string {
  return pair(coreText(r.cpuLimitCores), r.memLimitBytes ? fmtBytes(r.memLimitBytes) : '');
}

/** İstek / ayrılabilir: "CPU %52 · Mem %108" (%100 üstü aşırı taahhüt, kırpılmaz). */
export function nodeRequestPct(r: ClusterNodeRow): { cpu: number | null; mem: number | null } {
  const ratio = (req?: number, alloc?: number) => (req != null && alloc && alloc > 0 ? (req / alloc) * 100 : null);
  return { cpu: ratio(r.cpuRequestCores, r.cpuAllocatableCores), mem: ratio(r.memRequestBytes, r.memAllocatableBytes) };
}
export function nodeRequestText(r: ClusterNodeRow): string {
  const { cpu, mem } = nodeRequestPct(r);
  return pair(cpu == null ? '' : `CPU %${Math.round(cpu)}`, mem == null ? '' : `Mem %${Math.round(mem)}`);
}

export function nodeVersionText(r: ClusterNodeRow): string {
  return r.kubeletVersion ?? '';
}
export function nodeVersionTitle(r: ClusterNodeRow): string | undefined {
  const parts = [
    r.kubeletVersion && `kubelet ${r.kubeletVersion}`,
    r.osImage, r.kernelVersion && `kernel ${r.kernelVersion}`, r.containerRuntime,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(' · ') : undefined;
}

// Kullanım yüzdesi tonu — çubuk rengiyle (pctColor) aynı eşikler (>85 err, >65 warn).
const usageCellTone = (p: number | null): CellTone | undefined =>
  p == null ? undefined : p > 85 ? 'err' : p > 65 ? 'warn' : undefined;

export const NODE_COLS: ColumnDef<ClusterNodeRow>[] = [
  { id: 'node',     label: 'Node',    sortValue: r => r.node, naturalDir: 'asc', flex: true, minWidth: 200, mono: true },
  { id: 'role',     label: 'Role',    sortValue: r => r.role ?? '', naturalDir: 'asc', width: 90 },
  // Varsayılan sıralama: dikkat isteyenler önce (+1000), sonra bellek %.
  { id: 'status',   label: 'Status',  sortValue: r => (nodeStatus(r).attention ? 1000 : 0) + (nodeMemPct(r) ?? 0), width: 130, tone: r => nodeStatus(r).tone },
  // Bilinmeyen pod sayısı sıralamada en alta: -1.
  { id: 'pods',     label: 'Pods',    sortValue: r => r.pods ?? -1, numeric: true, width: 100 },
  { id: 'cpu',      label: 'CPU',     sortValue: r => nodeCpuPct(r) ?? -1, width: 190, tone: r => usageCellTone(nodeCpuPct(r)) },
  { id: 'mem',      label: 'Memory',  sortValue: r => nodeMemPct(r) ?? -1, width: 200, tone: r => usageCellTone(nodeMemPct(r)) },
  { id: 'cluster',  label: 'Cluster', sortValue: r => r.cluster, naturalDir: 'asc', width: 130, tone: () => 'muted' },
  { id: 'alloc',    label: 'Allocatable', sortValue: r => r.memAllocatableBytes ?? -1, width: 170 },
  { id: 'reqPct',   label: 'Requests / allocatable', sortValue: r => nodeRequestPct(r).mem ?? -1, width: 170 },
  { id: 'limits',   label: 'Limits',  sortValue: r => r.memLimitBytes ?? -1, width: 170 },
  { id: 'netIn',    label: 'Net in',  sortValue: r => r.netInBps ?? 0, numeric: true, width: 90 },
  { id: 'netOut',   label: 'Net out', sortValue: r => r.netOutBps ?? 0, numeric: true, width: 90 },
  { id: 'versions', label: 'Kubelet', sortValue: r => r.kubeletVersion ?? '', naturalDir: 'asc', width: 130, tone: () => 'muted' },
];

/** D3 varsayılanı: yalnız ilk altı kolon; gerisi "Tüm kolonlar". */
export const NODE_DEFAULT_HIDDEN = ['cluster', 'alloc', 'reqPct', 'limits', 'netIn', 'netOut', 'versions'];
export const NODE_SORT_IDS: readonly string[] = NODE_COLS.map(c => c.id);
export const NODE_COMPACT: ColumnModel = { v: 1, order: NODE_COLS.map(c => c.id), hidden: NODE_DEFAULT_HIDDEN, sig: 'clusternodes-compact' };
