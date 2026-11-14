import { useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { useServiceDeploys } from '@/lib/queries';
import { Spinner } from '@/components/Spinner';
import { TimeSeriesPanel, type TSSeries } from '@/components/viz/TimeSeriesPanel';
import type { FilterExpr, SpanMetricSeries } from '@/lib/types';
import { podLineLabel, podClusterOf, withClusterPrefix } from './runtimePodLabel';
import { smoothPoints } from './runtimeSmooth';
import { heapSummary, podPercentLines, flatSummary, parseRtView, rtViewParam, type FlatSummary, type HeapKeyedLine, type HeapPodLine, type HeapSummary, type RtView } from './runtimeHeapView';

// RuntimeCharts (v0.9.87, operatör talebi) — Service Overview'da dil
// ailesine göre JVM / .NET / Go heap+GC runtime grafikleri.
//
// Teknoloji tespiti: parent Service.tsx'in zaten çektiği
// ['svc-runtime', service] RQ cache'i (DetailsPropsStrip deseni — aynı
// anahtar, staleTime aynı ⇒ SIFIR ek ağ isteği). language =
// telemetry.sdk.language verbatim (java/kotlin → JVM, dotnet/csharp →
// .NET, go → Go).
//
// Metrik adları:
//   JVM  — stable jvm.* semconv (OTel javaagent 2.x; operatör ortamı
//          2.10 + Java 17/21). process.runtime.jvm.* (agent 1.x) BİLE
//          BİLE yok sayılır — operatör teyidi: kullanılmıyor.
//   .NET — process.runtime.dotnet.* (OpenTelemetry.Instrumentation.
//          Runtime, .NET 6/8; stable dotnet.* ancak .NET 9'da geldi).
//   Go   — process.runtime.go.* (otel-go runtime; coremetry'nin kendisi
//          bunu emit eder → lokalde dogfood ile doğrulanabilir).
//
// Sorgu semantiği (keşif bulgusu): histogram instrument'ta value kolonu
// per-export ORTALAMA (Sum/Count, convert.go) ⇒ jvm.gc.duration'a
// agg=avg "ortalama GC pause" verir — DOĞRU; agg=sum "ortalamaların
// toplamı" olurdu (yanlış). Monotonic counter'lar (collections.count,
// exceptions.count…) kümülatif tırmanan çizgi çizerdi — backend'de
// rate/delta yok, v1'de bilerek dışarıda.
//
// Hizalama: her çizgi AYRI /api/metrics/query fetch'i; bucket kümeleri
// farklılaşabilir (zero-fill yok). ChartCard index-eşler ⇒ alignToUnion
// ile union eksene hizalanır, eksik bucket null (grafikte boşluk).

const MB = 1 / (1024 * 1024);
const HEAP: FilterExpr[] = [{ k: 'jvm.memory.type', op: '=', v: ['heap'] }];
// v0.9.1271 (Dynatrace-parite #2) — non-heap: Metaspace/CodeCache/
// compressed-class. JBoss'un klasik classloader-leak'i HEAP kartında
// GÖRÜNMEZ (heap sağlıklıyken Metaspace şişer, sonuç OOM:Metaspace);
// Dynatrace bu kartı varsayılan gösterir. Semconv değeri 'non_heap'.
const NONHEAP: FilterExpr[] = [{ k: 'jvm.memory.type', op: '=', v: ['non_heap'] }];

interface LineSpec {
  metric: string;
  label: string;      // fanout'ta groupKey ezer
  color: string;      // fanout'ta palet ezer
  filters?: FilterExpr[];
  groupBy?: string;
  scale?: number;     // değer çarpanı (By→MB, s→ms, ns→ms)
  fanout?: boolean;   // groupBy fan-out: dönen her seri ayrı çizgi
  // Fanout etiketi üreticisi (pod kırılımı); yoksa groupKey.join('/').
  labelOf?: (gk: string[], fallback: string) => string;
  // Referans çizgisi (ör. heap limit) — kesikli çizilir, pod çizgileri
  // ona yaklaşınca "doldu" gözle okunur.
  ref?: boolean;
  // v0.10.2148 (S9) — toplama; verilmezse 'avg'. Özet heap görünümünün yüzde
  // okumaları 'sum' (pod başına havuzların toplamı; runtimeHeapView başlığı).
  agg?: 'avg' | 'sum';
  // Özet heap görünümünde okumanın rolü (çizgi değil, hesap girdisi).
  role?: 'abs' | 'pctUsed' | 'pctLimit';
}

// v0.9.89 (operatör talebi) — JVM/.NET kartları POD BAZLI: "herhangi
// bir pod heap dolduğunda ya da GC yaptığında ayırt edebileyim".
// ÜÇLÜ groupBy (v0.9.91 — operatör "4abcd5 UUID görünüyor, pod ismi
// yazmıyor" raporu): k8s.pod.name (collector k8sattributes'a bağlı, her
// ortamda yok) + host.name (container hostname = k8s pod adı, javaagent
// default) + service.instance.id (UUID son çare). Etiket önceliği
// podLineLabel'da okunabilir pod adına yeğler.
// v0.9.532 — 4. anahtar "cluster" (operatör: "jvm var ama hangi cluster
// belli değil"). Sunucuda metricClusterExpr'e çözümlenir (6-permütasyon
// coalesce, spans clusterDeriveExpr'in ikizi). groupKey[0..2] indeksleri
// DEĞİŞMEZ — podLineLabel sözleşmesi bozulmaz; [3] cluster önekidir ve
// yalnız seri kümesi birden çok cluster'a yayılıyorsa basılır.
const POD_GROUP = 'resource.k8s.pod.name,resource.host.name,resource.service.instance.id,cluster';
// mode (v0.9.128, operatör talebi) — memory kartları (heap/committed, MB) =
// area (dolu kapasite okunur); GC pause / thread / object COUNT kartları =
// line (dolgu yanıltıcı, "biriken alan" anlamı yok). Verilmezse 'area'.
interface CardSpec { key: string; title: string; unit: string; lines: LineSpec[]; mode?: 'area' | 'line' }

// v0.9.95 — 30+ pod'lu servisler için üst sınır yükseldi (eski 8, operatör
// "sadece 8 pod gösteriyor"); pod çizgilerinin rengini TimeSeriesPanel'in
// stabil seriesColor paleti verir (etiket→renk sabit), interaktif lejand
// tablosu pod ayırt/seç eder. Aşırı yüksek pod sayısında (>cap) grafik
// erimesin diye sınır + "N of M" notu.
const FANOUT_MAX = 40;

const FAMILY_CARDS: Record<string, CardSpec[]> = {
  jvm: [
    { key: 'heap', title: 'JVM heap (by pod)', unit: ' MB', lines: [
      { metric: 'jvm.memory.used', label: 'used', color: 'var(--accent)', filters: HEAP,
        scale: MB, groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
      // limit tüm pod'larda aynı JVM ayarı — tek kesikli referans çizgisi;
      // pod çizgisi limite yaklaşınca "doldu" gözle okunur.
      { metric: 'jvm.memory.limit', label: 'limit', color: 'var(--err)', filters: HEAP, scale: MB, ref: true },
    ] },
    { key: 'nonheap', title: 'JVM non-heap (by pod)', unit: ' MB', lines: [
      { metric: 'jvm.memory.used', label: 'used', color: 'var(--purple)', filters: NONHEAP,
        scale: MB, groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
      // Metaspace limiti çoğu kurulumda ayarsız (-1 → seri yok) — çizgi
      // o zaman hiç gelmez; MaxMetaspaceSize verilen kurulumda referans.
      { metric: 'jvm.memory.limit', label: 'limit', color: 'var(--err)', filters: NONHEAP, scale: MB, ref: true },
    ] },
    { key: 'gc', title: 'GC pause by pod (avg)', unit: ' ms', mode: 'line', lines: [
      { metric: 'jvm.gc.duration', label: 'gc', color: 'var(--warn)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, scale: 1000 },
    ] },
    { key: 'threads', title: 'Threads (by pod)', unit: '', mode: 'line', lines: [
      { metric: 'jvm.thread.count', label: 'threads', color: 'var(--teal)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
    ] },
  ],
  dotnet: [
    { key: 'heap', title: '.NET heap (by pod)', unit: ' MB', lines: [
      { metric: 'process.runtime.dotnet.gc.heap.size', label: 'heap', color: 'var(--accent)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, scale: MB },
    ] },
    { key: 'committed', title: 'GC committed (by pod)', unit: ' MB', lines: [
      { metric: 'process.runtime.dotnet.gc.committed_memory.size', label: 'committed', color: 'var(--purple)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, scale: MB },
    ] },
    { key: 'threads', title: 'ThreadPool threads (by pod)', unit: '', mode: 'line', lines: [
      { metric: 'process.runtime.dotnet.thread_pool.threads.count', label: 'threads', color: 'var(--teal)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
    ] },
  ],
  go: [
    { key: 'heap', title: 'Go heap', unit: ' MB', lines: [
      { metric: 'process.runtime.go.mem.heap_inuse', label: 'in-use', color: 'var(--accent)', scale: MB },
      { metric: 'process.runtime.go.mem.heap_sys', label: 'sys', color: 'var(--purple)', scale: MB },
      { metric: 'process.runtime.go.mem.heap_alloc', label: 'alloc', color: 'var(--teal)', scale: MB },
    ] },
    { key: 'gc', title: 'GC pause (avg)', unit: ' ms', mode: 'line', lines: [
      { metric: 'process.runtime.go.gc.pause_ns', label: 'pause', color: 'var(--warn)', scale: 1 / 1e6 },
    ] },
    { key: 'objects', title: 'Heap objects', unit: '', mode: 'line', lines: [
      { metric: 'process.runtime.go.mem.heap_objects', label: 'objects', color: 'var(--teal)' },
    ] },
  ],
};

// v0.9.546 — dışa açıldı: Pods sekmesi "bu serviste dil-runtime ailesi
// VAR MI" sorusunu sorup yoksa Thanos kaynak grafiklerine düşüyor
// (PodResourceCharts). Tek kaynak, iki tüketici — aile listesi
// ayrışırsa bir servis hem runtime hem kaynak grafiği görür ya da
// hiçbirini görmez.
export function familyOf(language: string | undefined): string | null {
  const l = (language || '').toLowerCase();
  if (l === 'java' || l === 'kotlin') return 'jvm';
  if (l === 'dotnet' || l === 'csharp') return 'dotnet';
  if (l === 'go') return 'go';
  return null;
}

const FAMILY_TITLE: Record<string, string> = { jvm: 'JVM', dotnet: '.NET', go: 'Go' };

// v0.10.2148 (servis sekmeleri S9; plan K12 — operatör kararı 2026-11-14,
// seçenek B) — JVM heap kartının VARSAYILAN görünümü: her pod ince gri çizgi,
// en yüksek üç pod renkli ve adıyla, değer heap sınırının yüzdesi, sınır %100'de
// kesik çizgi (runtimeHeapView.heapSummary). Hiçbir pod çizimden düşmez.
// `?rtview=lines` eski pod başına renkli çizgileri (40 tavanlı, MB) açar.
const isHeapSummaryCard = (family: string | null, card: CardSpec, view: RtView) =>
  family === 'jvm' && card.key === 'heap' && !view.lines;
// Özet görünümün okumaları: yüzde için pod başına kullanım ve sınır TOPLAMI
// (sunucudaki heap bandıyla aynı hesap); sınır hiç yoksa mutlak değere düşmek
// için ortalama kullanım (çizgi kipiyle AYNI sorgu anahtarı — önbellek ortak).
const HEAP_SUMMARY_LINES: LineSpec[] = [
  { metric: 'jvm.memory.used', label: 'used', color: 'var(--accent)', filters: HEAP, scale: MB, groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, role: 'abs' },
  { metric: 'jvm.memory.used', label: 'used', color: 'var(--accent)', filters: HEAP, groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, agg: 'sum', role: 'pctUsed' },
  { metric: 'jvm.memory.limit', label: 'limit', color: 'var(--err)', filters: HEAP, groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, agg: 'sum', role: 'pctLimit' },
];

// v0.10.2149 (servis sekmeleri S10) — JVM non-heap kartı pencere boyunca DÜZSE
// (her pod (max − min) / ortanca < %10; runtimeHeapView.flatSummary, eşik sabit)
// grafik yerine tek cümle: düz bir çizgi yığını bilgi taşımaz, yer kaplar.
// Oynayan tek bir pod bile grafiği geri getirir (Metaspace sızıntısı gizlenmez).
// "Grafiği göster" `?rtview=nonheap`; `?rtview=lines` de grafiği açar.
const isFlatCandidate = (family: string | null, card: CardSpec) => family === 'jvm' && card.key === 'nonheap';
const fmtMB = (v: number) => (v >= 10 ? v.toFixed(0) : v.toFixed(1));

/** `?rtview` bağlantısı: yabancı parametreler korunur, varsayılanda parametre silinir. */
function rtViewSearch(params: URLSearchParams, next: RtView): string {
  const p = new URLSearchParams(params);
  const v = rtViewParam(next);
  if (v) p.set('rtview', v); else p.delete('rtview');
  const q = p.toString();
  return q ? `?${q}` : '';
}

export function RuntimeCharts({ service, from, to, onZoom, onZoomReset, hideHeader = false }: {
  service: string;
  from: number; // unix ns (parent'ın çözülmüş penceresi — RQ anahtarı hizalı)
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  // Grafana-parite M1 — çift-tık: Service.tsx zoom geri-yığınını pop eder.
  onZoomReset?: () => void;
  // v0.10.720 — başlık üst bileşende (SectionHead); yalnız Pods sekmesi geçer.
  hideHeader?: boolean;
}) {
  // Parent Service.tsx ile AYNI anahtar → RQ cache'inden dolar, ek istek yok.
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service,
    staleTime: 300_000,
  });
  const family = familyOf(runtimeQ.data?.language);
  const [params] = useSearchParams();
  const rtRaw = params.get('rtview');
  const view = useMemo(() => parseRtView(rtRaw), [rtRaw]);
  // Modül sabiti referansı ya da stabil boş dizi — render başına yeni []
  // kimliği aşağıdaki useMemo'ları boşa tetiklerdi (eslint exhaustive-deps).
  const cards = useMemo(() => {
    if (!family) return [];
    return FAMILY_CARDS[family].map(c => (isHeapSummaryCard(family, c, view) ? { ...c, lines: HEAP_SUMMARY_LINES } : c));
  }, [family, view]);

  // Madde 4 sweep — deploy ▼ marker'ları runtime panellerine: "heap spike
  // deploy'dan mı?" bir bakışta okunsun. AYNI (service, from, to) anahtarı
  // Overview'un useServiceDeploys'uyla RQ-dedupe olur — ek istek yok.
  // TSP `deploys` bare unix-ns bekler; memo, taze dizi kimliğinin rebuild
  // tetiklemesini önler.
  const deploysQ = useServiceDeploys(service, from, to);
  const deployNs = useMemo(() => {
    const d = deploysQ.data;
    return d && d.length ? d.map(x => x.timeUnixNs) : undefined;
  }, [deploysQ.data]);

  // Kart başına değil ÇİZGİ başına sorgu (metrik+filtre+groupBy farklı).
  const specs = useMemo(
    () => cards.flatMap(c => c.lines.map(l => ({ card: c, line: l }))),
    [cards],
  );
  const queries = useQueries({
    queries: specs.map(({ line }) => ({
      // 'avg' anahtarı eskisiyle aynı (önbellek korunur); 'sum' ayrı anahtar.
      queryKey: ['svc-runtime-metric', service, line.metric, line.groupBy ?? '',
        line.filters ? JSON.stringify(line.filters) : '', from, to, ...(line.agg && line.agg !== 'avg' ? [line.agg] : [])],
      queryFn: () => api.metricQuery({
        name: line.metric,
        service,
        agg: line.agg ?? 'avg',
        filters: line.filters ? JSON.stringify(line.filters) : undefined,
        groupBy: line.groupBy,
        from, to, step: 0,
        // v0.9.707 — default 1500 nokta tek aşan yüzeydi (~1.3 nokta/px).
        // Tam-genişlik istifli kartlar → panelMaxDataPoints(1).
        maxDataPoints: panelMaxDataPoints(1),
      }).then(r => r ?? []),
      staleTime: 60_000,
      enabled: !!family,
    })),
  });

  const built = useMemo(() => {
    if (!family) return null;
    let anyData = false;
    const byCard = cards.map(card => {
      // Her çizgi → bir TSSeries. TSP x-eksenini KENDİ union'lar (ayrı
      // fetch'lerin farklı bucket kümeleri iç hizalanır) — elle alignToUnion
      // GEREKMEZ. points ns cinsinde kalır (TSSeries ns bekler). Fanout pod
      // çizgilerinin rengini vermeyiz → TSP seriesColor(label) stabil palet
      // atar; lejand tablosu pod ayırt/seç eder.
      const tsSeries: TSSeries[] = [];
      let podTotal = 0;
      let capped = false;
      // v0.9.532 — karttaki serilerin dokunduğu cluster'lar ("40 / 52
      // pod · 3 cluster" notu için).
      const cardClusters = new Set<string>();
      const summaryMode = isHeapSummaryCard(family, card, view);
      const absPods: HeapPodLine[] = [];
      const pctUsed: HeapKeyedLine[] = [];
      const pctLimit: HeapKeyedLine[] = [];
      // S10 — düzlük ölçüsü HAM (yumuşatılmamış) ve kesilmemiş kümeden.
      const flatInput: HeapPodLine[] = [];
      specs.forEach(({ card: c, line }, qi) => {
        if (c.key !== card.key) return;
        const data = (queries[qi]?.data ?? []) as SpanMetricSeries[];
        const scale = line.scale ?? 1;
        // v0.9.127 (operatör: "çok zigzag") — heap/GC gauge'u gerçek GC
        // sawtooth'u; hareketli-ortalama ile yumuşat (smoothPoints, pencere
        // serinin kendi nokta sayısından). Referans çizgisi (limit) sabit →
        // SMA değiştirmez. Ölçek önce, sonra smooth.
        if (line.fanout) {
          podTotal += data.length;
          if (data.length > FANOUT_MAX) capped = true;
          // v0.9.532 — cluster öneki: kesilmemiş TAM kümeden hesaplanır
          // (ilk 40'ı tek cluster'a düşen çok-cluster'lı bir servis
          // yanlışlıkla "tek cluster" sayılmasın).
          const clusterSet = new Set(data.map(s => podClusterOf(s.groupKey)).filter(Boolean));
          clusterSet.forEach(c => cardClusters.add(c));
          const multi = clusterSet.size > 1;
          if (summaryMode) {
            // Özet görünüm TÜM pod'ları alır (40 tavanı yalnız çizgi kipinde);
            // ham değer — yüzde ve sıralama yumuşatmadan önce.
            for (const s of data) {
              const base = line.labelOf ? line.labelOf(s.groupKey, line.label) : (s.groupKey.filter(Boolean).join('/') || line.label);
              const keyed: HeapKeyedLine = {
                key: s.groupKey.join('\u0000'),
                label: withClusterPrefix(base, podClusterOf(s.groupKey), multi),
                points: s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale })),
              };
              if (line.role === 'pctUsed') pctUsed.push(keyed);
              else if (line.role === 'pctLimit') pctLimit.push(keyed);
              else absPods.push(keyed);
            }
            return;
          }
          if (isFlatCandidate(family, card)) {
            for (const s of data) {
              flatInput.push({ label: s.groupKey.join('/'), points: s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale })) });
            }
          }
          data.slice(0, FANOUT_MAX).forEach(s => {
            const base = line.labelOf
              ? line.labelOf(s.groupKey, line.label)
              : (s.groupKey.filter(Boolean).join('/') || line.label);
            tsSeries.push({
              label: withClusterPrefix(base, podClusterOf(s.groupKey), multi),
              points: smoothPoints(s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale }))),
            });
          });
        } else {
          const s = data[0];
          if (s) tsSeries.push({
            label: line.label,
            color: line.color,
            dash: line.ref ? [5, 4] : undefined, // referans (limit) kesikli
            points: smoothPoints(s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale }))),
          });
        }
      });
      const cardQs = specs
        .map(({ card: c }, qi) => ({ c, q: queries[qi] }))
        .filter(x => x.c.key === card.key);
      let summary: HeapSummary | null = null;
      // Özet, kartın TÜM okumaları oturunca kurulur: yüzde okuması gecikirse
      // önce mutlak değeri çizip sonra yüzdeye çevirmek ekseni zıplatırdı.
      if (summaryMode && !cardQs.some(x => x.q.isPending)) {
        // Yüzde: her pod kendi sınırına (toplam / toplam). Hiçbir pod'un sınırı
        // yoksa mutlak değer (MB). Pod çizgileri eskisi gibi SMA; sınır düz 100.
        const pct = podPercentLines(pctUsed, pctLimit);
        summary = pct.length > 0 ? heapSummary(pct, true, smoothPoints) : heapSummary(absPods, false, smoothPoints);
        tsSeries.push(...summary.lines);
      }
      if (tsSeries.some(s => s.points.length)) anyData = true;
      const status: 'loading' | 'error' | 'ready' =
        cardQs.some(x => x.q.isError) ? 'error'
        : cardQs.some(x => x.q.isPending) ? 'loading'
        : 'ready';
      // Düzlük yalnız okumalar oturunca söylenir (yarım veriyle "düz" denmez).
      const flat: FlatSummary | null = isFlatCandidate(family, card) && status === 'ready' ? flatSummary(flatInput) : null;
      return { card, series: tsSeries, status, podTotal, capped, clusterCount: cardClusters.size, summary, flat };
    });
    return { byCard, anyData };
  }, [family, cards, specs, queries, service, view]);

  // Aile bilinmiyor ya da (yüklendi + hiç veri yok) → section hiç yok
  // (ServiceInfraTab:327 emsali — runtime metriği göndermeyen servise
  // boş kart duvarı gösterme).
  const settled = queries.length > 0 && queries.every(q => !q.isPending);
  if (!family || !built || (settled && !built.anyData)) return null;
  if (!settled && !built.anyData) return null; // ilk yükleme: flash yok

  return (
    <>
      {!hideHeader && <div className="rt-head">
        <div className="rt-head__title">
          Runtime — {FAMILY_TITLE[family]}
        </div>
        <div className="rt-head__sub">
          {family === 'jvm' && <>Auto-instrumentation <code>jvm.*</code> metrics per pod (heap · GC · threads). Lejantta bir pod'a tıkla = izole.</>}
          {family === 'dotnet' && <>Runtime instrumentation <code>process.runtime.dotnet.*</code> metrics per pod.</>}
          {family === 'go' && <>Go runtime <code>process.runtime.go.*</code> metrics.</>}
        </div>
      </div>}
      {/* Full-width istifli kartlar — 30+ pod çizgisi + lejand tablosu için
          3-across dar kalıyordu (operatör raporu). Ortak syncKey: bir karta
          hover TÜM kartlarda crosshair çizer (heap spike ↔ GC pause korele). */}
      <div className="rt-cards">
        {built.byCard.map(({ card, series, status, podTotal, capped, clusterCount, summary, flat }) => (
          flat && !view.lines && !view.nonheap ? (
            <div key={card.key} className="card" data-testid="rt-flat-card">
              <div className="ov-card-h"><h3>{card.title}{card.unit ? ` (${card.unit.trim()})` : ''}</h3></div>
              <div className="ov-card-b rt-flat">
                <span>
                  Düz: {flat.pods} pod, {flat.min === flat.max ? fmtMB(flat.min) : `${fmtMB(flat.min)}–${fmtMB(flat.max)}`}{card.unit} aralığında;
                  bu pencerede hiçbir pod %10&#39;dan fazla oynamadı.
                </span>
                <Link replace to={{ search: rtViewSearch(params, { ...view, nonheap: true }) }}>Grafiği göster</Link>
              </div>
            </div>
          ) : (
          <div key={card.key} className="card">
            <div className="ov-card-h">
              <h3>{summary?.percent ? 'JVM heap — sınırın yüzdesi (pod başına)' : `${card.title}${card.unit ? ` (${card.unit.trim()})` : ''}`}</h3>
              {/* v0.10.2148 (S9) — özet görünümün notu + eski görünüme geçiş (URL kaynak, replace). */}
              {summary && (
                <span className="rt-view-note">
                  <span title={summary.percent
                    ? "Yüzde = pod'un heap kullanımı / kendi heap sınırı (jvm.memory.used / jvm.memory.limit, havuzların toplamı)."
                    : 'jvm.memory.limit serisi yok: değerler mutlak (MB), yüzdeye çevrilmedi.'}>
                    {summary.total} pod · en yüksek {summary.top.length} renkli
                    {summary.drawn < summary.total ? ` · ${summary.drawn} / ${summary.total} çizildi` : ''}
                    {clusterCount > 1 ? ` · ${clusterCount} cluster` : ''}
                    {summary.percent ? '' : ' · sınır bilinmiyor (MB)'}
                  </span>
                  <Link replace to={{ search: rtViewSearch(params, { ...view, lines: true }) }}>Pod başına çizgiler</Link>
                </span>
              )}
              {!summary && family === 'jvm' && card.key === 'heap' && (
                <span className="rt-view-note">
                  <Link replace to={{ search: rtViewSearch(params, { ...view, lines: false }) }}>Özet görünüm</Link>
                </span>
              )}
              {/* v0.9.532 — cluster sayısı: çok-cluster'lı serviste not,
                  kesme olmasa da görünür (operatör "hangi cluster belli
                  değil" — cevabın yarısı bu sayı, yarısı seri önekleri). */}
              {flat && view.nonheap && !view.lines && (
                <span className="rt-view-note">
                  <Link replace to={{ search: rtViewSearch(params, { ...view, nonheap: false }) }}>Grafiği gizle</Link>
                </span>
              )}
              {!summary && (capped || clusterCount > 1) && (
                <span className="rt-pod-note">
                  {capped ? `${FANOUT_MAX} / ${podTotal} pod gösteriliyor` : `${podTotal} pod`}
                  {clusterCount > 1 ? ` · ${clusterCount} cluster` : ''}
                </span>
              )}
            </div>
            <div className="ov-card-b rt-card-b">
              {status === 'loading' && series.length === 0 ? (
                <div className="rt-slot"><Spinner /></div>
              ) : status === 'error' && series.length === 0 ? (
                <div className="rt-slot rt-slot--err">
                  Failed to load metrics
                </div>
              ) : series.length === 0 ? (
                <div className="rt-slot rt-slot--empty">
                  No data in this window
                </div>
              ) : (
                <TimeSeriesPanel
                  series={series}
                  height={240}
                  mode={summary ? 'line' : card.mode ?? 'area'}
                  syncKey={`runtime:${service}`}
                  deploys={deployNs}
                  onZoom={onZoom}
                  onZoomReset={onZoomReset}
                  smooth
                />
              )}
            </div>
          </div>
          )
        ))}
      </div>
    </>
  );
}
