// MLTraceHeatmap — v0.10.1870, öğrenme döngüsü Faz 2 parti B (audit §7.2
// "Trace: ısı haritası + baseline çizgisi"; mockup anomaly-detail.html
// görünüm 3). Endpoint'li trace olayının "Olayın seyri" üst katmanı: istek
// süreleri ısı haritası (mevcut /api/spans/heatmap — ham span, ÖRNEKLENMİŞ;
// sunucuda 30 sn serveCached) + olay öncesi p95 çizgisi (sunucuda
// ml_series'ten, ml.BuildTraceCourse) + anomali penceresi + oynatma imleci.
// Pencere ve süzgeç sunucudan (course.trace) — FE zaman HESAPLAMAZ, sorgu
// anahtarı olay başına sabit. Etiket metni sunucudan; FE cümle kurmaz.
import { useQuery } from '@tanstack/react-query';
import { LatencyHeatmap } from '@/components/LatencyHeatmap';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { heatmapFilters } from '@/pages/service/heatmapFilters';
import type { FilterExpr, TraceCourse } from '@/lib/types';
import { t, useLang } from '@/lib/i18n'; // v0.10.2291 — metin arayüz dilinde

/** SAF (test): giriş span'leri (server+consumer) + varsa http.route. */
export function mlHeatmapFilters(tc: TraceCourse): FilterExpr[] {
  const f = heatmapFilters(tc.service, undefined, undefined, true);
  if (tc.route) f.push({ k: 'http.route', op: '=', v: [tc.route] });
  return f;
}

/** SAF (test): dakikada bir sütun, 30–120 arası (≤ 6 sa pencere). */
export function mlHeatmapBuckets(fromNs: number, toNs: number): number {
  const min = Math.round((toNs - fromNs) / 60e9);
  return Math.min(120, Math.max(30, min));
}

export function MLTraceHeatmap({ tc, cursorSec }: { tc: TraceCourse; cursorSec: number | null }) {
  const q = useQuery({
    queryKey: [...keys.anomalies.ml, 'heatmap', tc.service, tc.route ?? '', tc.fromNs, tc.toNs],
    queryFn: ({ signal }) => api.spanHeatmap({
      from: tc.fromNs, to: tc.toNs, buckets: mlHeatmapBuckets(tc.fromNs, tc.toNs), filters: JSON.stringify(mlHeatmapFilters(tc)),
    }, signal),
    staleTime: 30_000,
    retry: false,
  });
  const empty = !q.data || q.data.times.length === 0 || q.data.maxCount === 0;
  const lang = useLang();
  return (
    <div className="mb-3" data-testid="ml-trace-heatmap">
      <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>{t('mlx.hm.head', lang)}{tc.route ? <> · <span className="font-mono">{tc.route}</span></> : t('mlx.tc.entry', lang)}</span>
        {tc.line && <span>┄ {tc.line.label}</span>}
        <span className="inline-flex items-center gap-1"><i aria-hidden="true" className="inline-block h-2 w-3 rounded-[2px] bg-[color:var(--err-bg)]" />{t('mlx.hm.window', lang)}</span>
      </div>
      {q.isLoading ? <Spinner /> : q.error || empty ? (
        <p className="m-0 text-sm text-muted-foreground" data-testid="ml-trace-heatmap-empty">
          {t(q.error ? 'mlx.hm.err' : 'mlx.hm.empty', lang)}
        </p>
      ) : q.data && (
        <LatencyHeatmap data={q.data} height={200}
          overlay={{ lineMs: tc.line?.ms ?? null, lineLabel: tc.line?.label, windowNs: [tc.onsetNs, tc.endNs], cursorSec }} />
      )}
      <p className="mt-1.5 mb-0 text-xs text-muted-foreground">
        {t('mlx.hm.foot', lang)}
      </p>
    </div>
  );
}
