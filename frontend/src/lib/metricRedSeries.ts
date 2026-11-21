// metricRedSeries — v0.10.2419: /metric-red cevabında BU servise ait en az
// bir noktalı seri var mı. Metrik kurulumda var (metricExists) ama servise
// eşleşen seri yoksa Overview otomatik kipte span'a döner.
import type { ServiceMetricRED } from './types';

export function redHasServiceSeries(d: Pick<ServiceMetricRED, 'series'> | null | undefined): boolean {
  if (!d?.series) return false;
  return Object.values(d.series).some(arr => !!arr?.some(s => s.points.length > 0));
}
