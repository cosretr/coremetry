// TraceMetricsPanel — v0.10.913: Trace sayfası "Metrics" sekmesi (revize
// mockup Onay 2026-09-25). Trace'in geçtiği pod'lar servise göre gruplu;
// AYNI servisin en çok 4 pod'u CPU / bellek grafiğinde üst üste (hangi
// replika farklı?). Farklı servisler üst üste çizilmez (birim/limit farkı
// yanıltır). Veri Pod sayfasıyla aynı uç (clusterPodDetail, Thanos), yalnız
// sekme açıkken ve yalnız seçili pod'lar için. Trace anı grafikte işaret.
// Seçim + pencere URL'de (?mpod=a,b&mwin=15, replace).
//
// v0.10.962 — yeniden tasarım (mockup onayı 2026-09-27) ÖNCESİ beş hata:
// seçili çip görünmüyordu (tüm çipler `tone="accent"` = `.active` ile aynı
// tint), "Pod sayfasında aç" pencereyi düşürüyordu, tek pod sorgusunun
// hatası tüm grafikleri gizliyordu, tavandaki tıklama sessizce yutuluyordu,
// servis grupları alfabetikti. Ayrıntı: TraceMetricsPanel.render.test.tsx.
import { useMemo } from 'react';
import { useSearchParams, Link } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { api, apiErrorDetail } from '@/lib/api';
import type { SpanMetricSeries, SpanRow } from '@/lib/types';
import { useEntityEnabled } from '@/lib/queries';
import { MultiLineChart, type DeployMarker } from '@/components/MultiLineChart';
import { Spinner, Empty } from '@/components/Spinner';
import { Chip, SegmentedControl } from '@/components/ui';
import { TraceJvmPanel } from './TraceJvmPanel';
import {
  tracePods, podsByService, defaultPodSelection, togglePod, traceMetricsWindow, resolveCluster, shortPod,
  errorSourceService, podAtCap, traceMetricsPodHref,
  TRACE_METRICS_WINDOWS, TRACE_METRICS_DEFAULT_WINDOW, TRACE_METRICS_MAX_PODS, type TraceMetricsWindow,
} from './traceMetrics';

export function TraceMetricsPanel({ spans }: { spans: SpanRow[] }) {
  const [sp, setSp] = useSearchParams();
  const pods = useMemo(() => tracePods(spans), [spans]);
  const groups = useMemo(() => podsByService(pods, errorSourceService(spans, pods)), [spans, pods]);
  const fallback = useMemo(() => defaultPodSelection(spans, pods), [spans, pods]);
  const urlSel = (sp.get('mpod') ?? '').split(',').filter(p => pods.some(x => x.pod === p));
  const selected = urlSel.length ? urlSel : fallback;
  const winRaw = Number(sp.get('mwin'));
  const win: TraceMetricsWindow = (TRACE_METRICS_WINDOWS as readonly number[]).includes(winRaw) ? winRaw as TraceMetricsWindow : TRACE_METRICS_DEFAULT_WINDOW;
  const { from, to, startNs } = useMemo(() => traceMetricsWindow(spans, win), [spans, win]);
  const { clusters, enabled: entitiesOn, loading } = useEntityEnabled(pods.length > 0);

  const set = (k: string, v: string | null) => setSp(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set(k, v); else next.delete(k);
    return next;
  }, { replace: true });

  const targets = selected.map(p => {
    const tp = pods.find(x => x.pod === p)!;
    return { ...tp, cluster: resolveCluster(tp.clusterValue, clusters) };
  });
  const queries = useQueries({
    queries: targets.map(t => ({
      queryKey: ['trace-pod-metrics', t.cluster, t.namespace, t.pod, from, to],
      queryFn: () => api.clusterPodDetail(t.cluster, t.namespace, t.pod, from, to),
      enabled: !!t.cluster && !!t.namespace,
      staleTime: 60_000,
      refetchOnWindowFocus: false,
    })),
  });

  if (pods.length === 0) {
    return <Empty icon="—" title="Bu trace'in span'larında k8s.pod.name yok — pod metrikleri gösterilemiyor." />;
  }

  const cpu: SpanMetricSeries[] = [];
  const mem: SpanMetricSeries[] = [];
  const notes: string[] = [];
  const errs: string[] = [];
  let asked = 0;
  targets.forEach((t, i) => {
    if (!t.cluster) { notes.push(`${shortPod(t.pod)}: cluster "${t.clusterValue || '—'}" bir Remote Cluster kaydına eşlenmemiş`); return; }
    if (!t.namespace) { notes.push(`${shortPod(t.pod)}: span'larda k8s.namespace.name yok`); return; }
    asked++;
    const q = queries[i];
    // v0.10.962 — hata POD BAŞINA: tek sorgunun hatası diğer pod'ları
    // gizlemez ve "örnek yok" (boş sonuç) gibi de yazılmaz. Gövde
    // `HTTP 500: {"error":…}` (writeErr) → apiErrorDetail iç mesajı açar;
    // 120'de kesilen ham JSON gürültüsü yazılmaz.
    if (q?.isError) {
      const msg = apiErrorDetail(q.error).message.trim();
      errs.push(`${shortPod(t.pod)}: metrikler okunamadı (${msg.slice(0, 120)})`);
      return;
    }
    const trend = q?.data?.trend ?? [];
    if (q?.isSuccess && trend.length === 0) { notes.push(`${shortPod(t.pod)}: bu pencerede Thanos örneği yok`); return; }
    const label = shortPod(t.pod);
    if (trend.length) {
      cpu.push({ groupKey: [label], points: trend.map(p => ({ time: p.bucket * 1e9, value: p.cpuCores })) });
      mem.push({ groupKey: [label], points: trend.map(p => ({ time: p.bucket * 1e9, value: p.memBytes })) });
    }
  });
  const pending = queries.some(q => q.isPending && q.fetchStatus !== 'idle');
  const allFailed = asked > 0 && errs.length === asked;
  // v0.10.962 — pod başına nedenler: hata metni HATA renginde (`.is-err`),
  // "örnek yok" notları nötr; ikisi de yoksa gövde yok (boş <p> çizilmez).
  const reasons = errs.length || notes.length ? <>
    {errs.length > 0 && <span className="is-err">{errs.join(' · ')}</span>}
    {errs.length > 0 && notes.length > 0 && ' · '}
    {notes.join(' · ')}
  </> : undefined;
  // v0.10.962 — tavan: aynı servisten eklenemeyen pod varsa GÖRÜNÜR söylenir.
  const capMsg = `en çok ${TRACE_METRICS_MAX_PODS} pod üst üste çizilir — eklemek için önce birini çıkarın`;
  const atCap = pods.some(p => podAtCap(selected, p.pod, pods));
  const marker: DeployMarker[] = [{ timeUnixNs: startNs, label: 'trace', description: 'trace başlangıcı' }];
  const xRange = { from: from / 1e9, to: to / 1e9 };
  const selSvc = targets[0]?.service ?? '';

  return (
    <div style={{ display: 'grid', gap: 12 }}>
      <div style={{ display: 'grid', gap: 6 }}>
        {groups.map(g => (
          <div key={g.service} style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', fontSize: 12 }}>
            <span style={{ minWidth: 160, color: 'var(--text2)' }} className="mono">{g.service || '(servissiz)'}</span>
            {g.pods.map(p => {
              const on = selected.includes(p.pod);
              const capped = podAtCap(selected, p.pod, pods);
              const info = `${p.pod} · ${p.spans} span${p.errors ? ` · ${p.errors} hata` : ''}`;
              // v0.10.962 — `tone="accent"` YOK: `.ch-accent` ile `.active`
              // aynı tint, seçili/seçisiz ayırt edilmiyordu. Seçim = active.
              return (
                <Chip key={p.pod} active={on} disabled={capped}
                  title={capped ? `${info} · ${capMsg}` : info}
                  onClick={() => set('mpod', togglePod(selected, p.pod, pods).join(','))}>
                  {shortPod(p.pod)} · {p.spans}{p.errors > 0 && <span style={{ color: 'var(--err)' }}> ⚠{p.errors}</span>}
                </Chip>
              );
            })}
          </div>
        ))}
      </div>
      <div style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12, flexWrap: 'wrap' }}>
        <span style={{ color: 'var(--text3)' }}>pencere: trace ±</span>
        <SegmentedControl size="sm" aria-label="Metrik penceresi" value={String(win)}
          onChange={v => set('mwin', Number(v) === TRACE_METRICS_DEFAULT_WINDOW ? null : v)}
          options={TRACE_METRICS_WINDOWS.map(w => ({ value: String(w), label: w === 60 ? '1 sa' : `${w} dk` }))} />
        <span style={{ flex: 1 }} />
        <span style={{ color: 'var(--text3)' }}>{selSvc} · {selected.length}/{TRACE_METRICS_MAX_PODS} pod · {atCap ? capMsg : "aynı servisin pod'ları üst üste"}</span>
        {/* v0.10.962 — tavan duyurusu: HER ZAMAN bağlı tek canlı bölge (BulkBar
            v0.10.939 kalıbı; içerik değişmeden önce ağaçta olmalı). Devre dışı
            çip Tab sırasından düşer, title'ı klavyeyle okunmaz — neden burada
            duyurulur. Görünür sayaç canlı DEĞİL (çift duyuru olmasın). */}
        <span className="sr-only" role="status" aria-live="polite" aria-atomic="true">{atCap ? capMsg : ''}</span>
        {targets[0]?.cluster && (
          <Link className="sec" to={traceMetricsPodHref(targets[0], { from, to, startNs })}>Pod sayfasında aç ↗</Link>
        )}
      </div>
      {loading || pending ? <Spinner />
        : !entitiesOn ? <Empty icon="—" title="Cluster kayıtları (entity katmanı) kapalı — pod metrikleri Thanos'tan okunamıyor." />
        : allFailed ? <Empty icon="✗" title="Thanos pod metrikleri okunamadı.">{reasons}</Empty>
        // v0.10.962 — çizilecek seri yokken bir pod'un sorgusu düştüyse başlık
        // "metrik yok" DEMEZ (hata boş sonuç gibi yazılmaz). Bu dalda düşmeyen
        // her sorulan pod boş trend döndü (bekleyen = spinner, seri = grafik).
        : cpu.length === 0 ? (
          <Empty icon={errs.length > 0 ? '✗' : '—'}
            title={errs.length > 0
              ? `${errs.length}/${asked} pod'un Thanos metrikleri okunamadı; diğerlerinde bu pencerede örnek yok.`
              : "Seçili pod'lar için metrik yok."}>
            {reasons}
          </Empty>
        )
        : (
          <>
            {/* v0.10.916 (operator-reported) — bellek üstte; iki panel de y
                tabanı 0 (zeroBase): %0.1'lik oynama zirve gibi çizilmesin. */}
            <div>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>Memory (bytes)</div>
              <MultiLineChart series={mem} height={180} syncKey={`trace-metrics-${selSvc}`} unit="bytes" deploys={marker} xRange={xRange} zeroBase />
            </div>
            <div>
              <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>CPU (cores)</div>
              <MultiLineChart series={cpu} height={180} syncKey={`trace-metrics-${selSvc}`} unit="cores" deploys={marker} xRange={xRange} zeroBase />
            </div>
            {/* v0.10.923 — JVM heap (GC sonrası) + GC duraklaması; servis JVM değilse çizilmez. */}
            <TraceJvmPanel service={selSvc} pods={selected} from={from} to={to}
              syncKey={`trace-metrics-${selSvc}`} deploys={marker} xRange={xRange} />
            <div className="pod-cap">
              kaynak Thanos (Pod sayfasıyla aynı uç) · "trace" işareti trace başlangıcı
              {errs.length > 0 && <> · <span className="is-err">{errs.join(' · ')}</span></>}
              {notes.length > 0 && <> · {notes.join(' · ')}</>}
            </div>
          </>
        )}
    </div>
  );
}
