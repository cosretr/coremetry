// tracePodCols.ts — v0.10.976 — Trace › Metrics pod tablosunun kolon tanımları (SAF).
//
// v0.10.976 — TracePodTable'dan ayrıldı: kolon tabanı çivisi
// (tracePodTable.cols.test.ts) bileşen dosyasını değil bu saf modülü okur
// (react-refresh "yalnız bileşen dışa aç" uyarısı olmadan). Kural, TracePodTable
// başlığındaki gerekçeyle aynı: hiçbir kolon sıralanmaz (sortValue yok).
//
// cpu / mem: minWidth = width (116). Operatör, prod v0.10.968: 400 px yan panel
// tabloyu sıkıştırınca sığdırma (fitColumnWidths) bu kolonları DEFAULT_MIN'e
// (48 px) çekiyor, `.tpm-mval` değeri "%…"ya kırpıyordu. Yan panel gitti; taban
// kap ne kadar dar olursa olsun (1280–1440 px pencerede, ayrıntı açıkken de)
// değer hücresini korur.
import type { ColumnDef } from '@/components/ui/DataTable';
import type { TraceMetricsRow } from './traceMetrics';

export const rowErrors = (r: TraceMetricsRow): number | null => {
  switch (r.kind) {
    case 'pod': return r.pod.errors;
    case 'group': return r.pods.reduce((a, p) => a + p.errors, 0);
    case 'nopod': return r.summary.errors;
    case 'nopod-svc': return r.svc.errors;
    default: return null;
  }
};

export const TRACE_POD_COLS: ColumnDef<TraceMetricsRow>[] = [
  { id: 'chev', label: '', width: 24, minWidth: 24 },
  { id: 'pod', label: 'Pod', flex: true, minWidth: 200, mono: true, truncate: 'middle' },
  { id: 'spans', label: 'Span', width: 56, numeric: true },
  { id: 'errors', label: 'Hata', width: 52, numeric: true, tone: r => { const e = rowErrors(r); return e == null ? undefined : e > 0 ? 'err' : 'faint'; } },
  { id: 'self', label: 'Öz süre', width: 72, numeric: true },
  { id: 'crit', label: 'Kritik yol', width: 92, numeric: true },
  { id: 'cpu', label: 'CPU', width: 116, minWidth: 116, align: 'right' },
  { id: 'mem', label: 'Bellek', width: 116, minWidth: 116, align: 'right' },
  { id: 'actions', label: 'Eylemler', width: 36, kind: 'actions' },
];
