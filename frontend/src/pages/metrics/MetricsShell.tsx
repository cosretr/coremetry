// pages/metrics/MetricsShell — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3;
// audit §1 öneri 1, mockup görünüm 1).
//
// /metrics = Metrics Explorer: bağlam çubuğu (başlık → Cluster → yenile →
// zaman seçici en sağda; ikinci cluster seçici YOK) + iki sekme: Sorgu
// (varsayılan, S2) ve Keşif. Paylaşılabilir görünümün tamamı URL'de:
// `?mx=` (model), `?tab=discover`, `?range=`, `?cluster=`.
//
// Eski URL biçimleri (lib/metricsExplorer/legacy.ts) açılışta BİR KEZ içe
// okunur ve URL'den silinir (replace) — `?mq=`, `?tab=promql&promql=`
// (CoSRE kanıt çipi), `?metric=` (Overview kapısı; S3: artık burada açılır),
// `?editor=0` (Keşif).
import { useEffect, useMemo, type ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { useAuthRole } from '@/components/AuthProvider';
import { useContextParams } from '@/hooks/useContextParams';
import { api } from '@/lib/api';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { parseMetricSource } from '@/lib/metricSource';
import type { ContextDim } from '@/lib/contextParams';
import { decodeMx, defaultMxModel, encodeMx, MX_PARAM, type MxModel } from '@/lib/metricsExplorer/model';
import { applyMxImport, importLegacyMetricsUrl } from '@/lib/metricsExplorer/legacy';
import { QueryTab } from './QueryTab';

const CTX_DIMS: ContextDim[] = ['range', 'cluster'];
const CTX_HIDDEN: ContextDim[] = ['service', 'namespace', 'compare'];

/** Rol yazma yetkisi: admin / editor (auth kapalıysa rol yok → sunucu karar verir). */
function canEditRole(role: string | undefined): boolean {
  return role === undefined || role === 'admin' || role === 'editor';
}

export function MetricsShell({ discover }: { discover: ReactNode }) {
  const [sp, setSearchParams] = useSearchParams();
  const ctx = useContextParams({ defaultPreset: DEFAULT_RANGE_PRESET, applies: CTX_DIMS });
  const canEdit = canEditRole(useAuthRole());

  // Eski biçim → tek model (bir kez; ardından URL temiz).
  useEffect(() => {
    const imp = importLegacyMetricsUrl(sp);
    if (imp) setSearchParams(prev => applyMxImport(prev, imp), { replace: true });
  }, [sp, setSearchParams]);

  const raw = sp.get(MX_PARAM);
  const model = useMemo<MxModel>(() => decodeMx(raw) ?? defaultMxModel(), [raw]);
  const setModel = (next: MxModel) => {
    setSearchParams(prev => {
      const p = new URLSearchParams(prev);
      const enc = encodeMx(next);
      if (enc === '{}') p.delete(MX_PARAM); else p.set(MX_PARAM, enc);
      return p;
    }, { replace: true });
  };
  const tab = sp.get('tab') === 'discover' ? 'discover' : 'query';
  const setTab = (t: string) => setSearchParams(prev => {
    const p = new URLSearchParams(prev);
    if (t === 'discover') p.set('tab', 'discover'); else p.delete('tab');
    return p;
  }, { replace: true });

  const trial = parseMetricSource(sp.toString());
  const capsQ = useQuery({
    queryKey: ['metrics-capabilities', trial ?? ''],
    queryFn: () => api.metricsCapabilities(),
    staleTime: 30_000,
  });

  return (
    <>
      <Topbar title="Metrics" context={{ ctx, hidden: CTX_HIDDEN }} />
      <PageShell>
        <Tabs value={tab} onValueChange={setTab} className="gap-3">
          <TabsList variant="line" aria-label="Metrics bölümleri">
            <TabsTrigger value="query">Sorgu</TabsTrigger>
            <TabsTrigger value="discover">Keşif</TabsTrigger>
          </TabsList>
          <TabsContent value="query">
            <QueryTab model={model} onModel={setModel} from={ctx.windowNs.from} to={ctx.windowNs.to}
              cluster={ctx.params.cluster} caps={capsQ.data} canEdit={canEdit} />
          </TabsContent>
          <TabsContent value="discover">{discover}</TabsContent>
        </Tabs>
      </PageShell>
    </>
  );
}
