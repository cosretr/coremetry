// lagLinks — v0.10.2475 (operatör bildirimi "Hiçbir şey anlaşılmıyor"):
// consumer lag Problem'inin "Ne yapılabilir" bağlantıları, SAF. Hata trace'i /
// hata logu lag'i açıklamaz; servisin Messaging sekmesi (topic süzgeci
// ?ktopic, v0.10.2467) ve lag metriğinin grafiği (Metrics, VictoriaMetrics;
// ML'in yargıladığı seri `max by (svc, topic) (kafka_consumer_records_lag_max)`).
import type { StdLag } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { metricsLinkHref } from '@/lib/metricsLink';
import { windowRangeParam } from '@/lib/urlState';

export const LAG_METRIC = 'kafka_consumer_records_lag_max';

export interface LagLinks {
  messagingHref: string;
  /** null = grafik açılamıyor (bağlantı takılmaz). */
  chartHref: string | null;
}

export function lagProblemLinks(service: string, lag: StdLag, win: { fromNs: number; toNs: number }): LagLinks {
  const topic = (lag.topic ?? '').trim();
  return {
    messagingHref: serviceHref(service, { tab: 'messaging', params: { ktopic: topic }, range: win }),
    chartHref: metricsLinkHref(
      { service, rangeParam: windowRangeParam(win), range: { preset: '1h' }, src: 'vm' },
      { queries: [{ metric: LAG_METRIC, agg: 'max', by: 'topic', filters: topic ? [{ k: 'topic', op: '=', v: topic }] : [] }] },
    ),
  };
}
