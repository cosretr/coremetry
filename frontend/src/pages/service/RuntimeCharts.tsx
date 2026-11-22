import { useMemo } from 'react';
import { Link, useSearchParams } from 'react-router';
import { useQuery, useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { useServiceDeploys } from '@/lib/queries';
import type { FilterExpr, RuntimeAlertConfig, SpanMetricSeries } from '@/lib/types';
import { runtimeThresholds } from './runtimeThresholds';
import { podLineLabel, podClusterOf, podScopeLabel, isAnonPodSeries, POD_SCOPE_KEYS } from './runtimePodLabel';
import { runtimeSourceBadge } from './runtimeSource';
import { useT } from '@/lib/i18n';
import { familyFromResource } from './runtimeFamily';
import { podPercentLines, flatSummary, parseRtView, rtViewParam, type FlatSummary, type HeapKeyedLine, type HeapPodLine, type RtView } from './runtimeHeapView';
import { RuntimePodChart, type RuntimeExtraLine } from './RuntimePodChart';
import type { PodSeries } from './runtimePodReduce';

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
// v0.10.2464 — [4..6] ortam + cluster etiketleri (POD_SCOPE_KEYS); etiket pod · cluster · ortam.
const POD_GROUP = `resource.k8s.pod.name,resource.host.name,resource.service.instance.id,cluster,${POD_SCOPE_KEYS}`;
// mode (v0.9.128, operatör talebi) — memory kartları (heap/committed, MB) =
// area (dolu kapasite okunur); GC pause / thread / object COUNT kartları =
// line (dolgu yanıltıcı, "biriken alan" anlamı yok). Verilmezse 'area'.
// v0.10.2436 — thresholdOf: sinyalin mutlak eşiği (kart biriminde). Yoksa pod
// görünümü göreli eşik kullanır (runtimePodReduce: tepe ortancasının 1,5 katı).
// v0.10.2443 — eşik operatörün runtime_alerts ayarından (runtimeThresholds;
// ayar yoksa chstore.DefaultRuntimeAlerts varsayılanı).
type Thresholds = ReturnType<typeof runtimeThresholds>;
interface CardSpec { key: string; title: string; unit: string; lines: LineSpec[]; mode?: 'area' | 'line'; thresholdOf?: (t: Thresholds) => number }

// v0.10.2436 — 40 pod tavanı (v0.9.95) kalktı: varsayılan görünüm medyan +
// en kötü + eşik üstü ≤5 pod (runtimePodReduce), "Tüm pod'lar" hepsini çizer.
// Heap sınırının yüzdesi için uyarı eşiği: runtime_alerts HeapRawWarnPct
// (ham heap, GC sonrası değil) — runtimeThresholds.heapRawPct.

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
    { key: 'gc', title: 'GC pause by pod (avg)', unit: ' ms', mode: 'line', thresholdOf: t => t.gcPauseMs, lines: [
      { metric: 'jvm.gc.duration', label: 'gc', color: 'var(--warn)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel, scale: 1000 },
    ] },
    { key: 'threads', title: 'Threads (by pod)', unit: '', mode: 'line', lines: [
      { metric: 'jvm.thread.count', label: 'threads', color: 'var(--teal)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
    ] },
  ],
  // .NET 2026-11-21'den beri DotnetRuntimePanel'de (runtimesig profili; eski +
  // yeni kuşak, Prom + noktalı ad) — burada kart yok.
  go: [
    // v0.10.2436 — her kart pod kırılımlı (madde 5): in-use pod başına; sys /
    // alloc servis ortalaması kesik referans çizgileri.
    { key: 'heap', title: 'Go heap in-use (by pod)', unit: ' MB', lines: [
      { metric: 'process.runtime.go.mem.heap_inuse', label: 'in-use', color: 'var(--accent)', scale: MB,
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
      { metric: 'process.runtime.go.mem.heap_sys', label: 'sys · servis ort.', color: 'var(--purple)', scale: MB, ref: true },
      { metric: 'process.runtime.go.mem.heap_alloc', label: 'alloc · servis ort.', color: 'var(--teal)', scale: MB, ref: true },
    ] },
    { key: 'gc', title: 'GC pause by pod (avg)', unit: ' ms', mode: 'line', lines: [
      { metric: 'process.runtime.go.gc.pause_ns', label: 'pause', color: 'var(--warn)', scale: 1 / 1e6,
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
    ] },
    { key: 'objects', title: 'Heap objects (by pod)', unit: '', mode: 'line', lines: [
      { metric: 'process.runtime.go.mem.heap_objects', label: 'objects', color: 'var(--teal)',
        groupBy: POD_GROUP, fanout: true, labelOf: podLineLabel },
    ] },
  ],
};

// v0.9.546 — dışa açıldı: Pods sekmesi "bu serviste dil-runtime ailesi
// VAR MI" sorusunu sorup yoksa Thanos kaynak grafiklerine düşüyor
// (PodResourceCharts). Tek kaynak, iki tüketici — aile listesi
// ayrışırsa bir servis hem runtime hem kaynak grafiği görür ya da
// hiçbirini görmez.
export function familyOf(language: string | undefined): string | null {
  // 2026-11-21 — tek tespit kaynağı runtimeFamily.ts; burada grafik ailesi
  // olanlar (JVM / .NET / Go). Node / Python → null (genel kaynak grafikleri).
  const f = familyFromResource(language);
  return f === 'jvm' || f === 'dotnet' || f === 'go' ? f : null;
}

const FAMILY_TITLE: Record<string, string> = { jvm: 'JVM', dotnet: '.NET', go: 'Go' };

// v0.10.2148 (S9) — JVM heap kartı heap sınırının YÜZDESİ (pod başına, kendi
// sınırına); sınır %100'de kesik çizgi. v0.10.2436 — tek görünüm: pod seçimi
// artık runtimePodReduce'ta (medyan + en kötü + eşik üstü; "Tüm pod'lar" hepsi),
// eski gri/renkli özet ve MB'lik `lines` kipi kalktı.
const isHeapPctCard = (family: string | null, card: CardSpec) => family === 'jvm' && card.key === 'heap';
// Okumalar: yüzde için pod başına kullanım ve sınır TOPLAMI (sunucudaki heap
// bandıyla aynı hesap); sınır hiç yoksa mutlak değere düşmek için ortalama.
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

export function RuntimeCharts({ service, from, to, onZoom, onZoomReset, hideHeader = false, family: familyProp, scopeFilters, alerts }: {
  service: string;
  /** v0.10.2443 — operatörün runtime_alerts eşikleri (/runtime-signals); yoksa varsayılan. */
  alerts?: RuntimeAlertConfig;
  // Follow-up 2026-11-21 — sunucunun (/runtime-signals) çözdüğü ortam / küme
  // kapsamı; her çizginin süzgecine aynen eklenir. Yokken sorgu bayt-aynı.
  scopeFilters?: FilterExpr[];
  // 2026-11-21 — üst bileşenin tespit ettiği aile (dil yoksa katalogdan); verilmezse dil.
  family?: string | null;
  from: number; // unix ns (parent'ın çözülmüş penceresi — RQ anahtarı hizalı)
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  // Grafana-parite M1 — çift-tık: Service.tsx zoom geri-yığınını pop eder.
  onZoomReset?: () => void;
  // v0.10.720 — başlık üst bileşende (SectionHead).
  hideHeader?: boolean;
}) {
  // Parent Service.tsx ile AYNI anahtar → RQ cache'inden dolar, ek istek yok.
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service,
    staleTime: 300_000,
  });
  const family = familyProp !== undefined ? familyProp : familyOf(runtimeQ.data?.language);
  const [params] = useSearchParams();
  const rtRaw = params.get('rtview');
  const view = useMemo(() => parseRtView(rtRaw), [rtRaw]);
  // Modül sabiti referansı ya da stabil boş dizi — render başına yeni []
  // kimliği aşağıdaki useMemo'ları boşa tetiklerdi (eslint exhaustive-deps).
  const cards = useMemo(() => {
    if (!family) return [];
    return (FAMILY_CARDS[family] ?? []).map(c => (isHeapPctCard(family, c) ? { ...c, lines: HEAP_SUMMARY_LINES } : c));
  }, [family]);

  // Madde 4 sweep — deploy ▼ marker'ları runtime panellerine: "heap spike
  // deploy'dan mı?" bir bakışta okunsun. AYNI (service, from, to) anahtarı
  // Overview'un useServiceDeploys'uyla RQ-dedupe olur — ek istek yok.
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
  // Follow-up 2026-11-21 — kart kaynak ipucu (metrik ailesi + kaynak).
  const tr = useT();
  const srcOf = (card: CardSpec) => runtimeSourceBadge(card.lines.map(l => l.metric), tr('rt.src.tip'));
  const lineFilters = (l: LineSpec): FilterExpr[] | undefined => {
    const fs = [...(scopeFilters ?? []), ...(l.filters ?? [])];
    return fs.length ? fs : undefined;
  };
  const queries = useQueries({
    queries: specs.map(({ line }) => ({
      // 'avg' anahtarı eskisiyle aynı (önbellek korunur); 'sum' ayrı anahtar.
      queryKey: ['svc-runtime-metric', service, line.metric, line.groupBy ?? '',
        lineFilters(line) ? JSON.stringify(lineFilters(line)) : '', from, to, ...(line.agg && line.agg !== 'avg' ? [line.agg] : [])],
      queryFn: () => api.metricQuery({
        name: line.metric,
        service,
        agg: line.agg ?? 'avg',
        filters: lineFilters(line) ? JSON.stringify(lineFilters(line)) : undefined,
        groupBy: line.groupBy,
        from, to, step: 0,
        // v0.9.707 — tam genişlik istifli kartlar → panelMaxDataPoints(1).
        maxDataPoints: panelMaxDataPoints(1),
      }).then(r => r ?? []),
      staleTime: 60_000,
      enabled: !!family,
    })),
  });

  const thr = useMemo(() => runtimeThresholds(alerts), [alerts]);
  const built = useMemo(() => {
    if (!family) return null;
    let anyData = false;
    const byCard = cards.map(card => {
      // Pod çizgileri ham (ölçekli) gelir; seçim + yumuşatma RuntimePodChart'ta.
      let pods: PodSeries[] = [];
      const extra: RuntimeExtraLine[] = [];
      let podless = false;
      // v0.9.532 — karttaki serilerin dokunduğu cluster'lar ("3 cluster" notu).
      const cardClusters = new Set<string>();
      const heapCard = isHeapPctCard(family, card);
      const absPods: HeapPodLine[] = [];
      const pctUsed: HeapKeyedLine[] = [];
      const pctLimit: HeapKeyedLine[] = [];
      // S10 — düzlük ölçüsü HAM (yumuşatılmamış) ve kesilmemiş kümeden.
      const flatInput: HeapPodLine[] = [];
      specs.forEach(({ card: c, line }, qi) => {
        if (c.key !== card.key) return;
        const data = (queries[qi]?.data ?? []) as SpanMetricSeries[];
        const scale = line.scale ?? 1;
        const scaled = (s: SpanMetricSeries) => s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale }));
        if (!line.fanout) {
          const s = data[0];
          // Referans (limit / servis ortalaması) kesikli; yumuşatma çizimde.
          if (s) extra.push({ label: line.label, color: line.color, dashed: !!line.ref, points: scaled(s) });
          return;
        }
        // Pod etiketi yok (host.name / k8s.pod.name / instance id üçü de boş):
        // tek çizgi, kart "pod etiketi yok" der.
        const anon = isAnonPodSeries(data);
        if (anon) podless = true;
        // v0.9.532 — cluster öneki kesilmemiş TAM kümeden.
        data.forEach(s => { const cl = podClusterOf(s.groupKey); if (cl) cardClusters.add(cl); });
        for (const s of data) {
          const base = anon ? line.label
            : line.labelOf ? line.labelOf(s.groupKey, line.label)
              : (s.groupKey.filter(Boolean).join('/') || line.label);
          const label = anon ? base : podScopeLabel(base, s.groupKey);
          const points = scaled(s);
          if (heapCard) {
            const keyed: HeapKeyedLine = { key: s.groupKey.join('\u0000'), label, points };
            if (line.role === 'pctUsed') pctUsed.push(keyed);
            else if (line.role === 'pctLimit') pctLimit.push(keyed);
            else absPods.push(keyed);
            continue;
          }
          pods.push({ label, points });
          if (isFlatCandidate(family, card)) flatInput.push({ label: s.groupKey.join('/'), points });
        }
      });
      const cardQs = specs
        .map(({ card: c }, qi) => ({ c, q: queries[qi] }))
        .filter(x => x.c.key === card.key);
      const status: 'loading' | 'error' | 'ready' =
        cardQs.some(x => x.q.isError) ? 'error'
        : cardQs.some(x => x.q.isPending) ? 'loading'
        : 'ready';
      let unit = card.unit;
      let threshold = card.thresholdOf?.(thr);
      let percent = false;
      // Heap: kartın TÜM okumaları oturunca kurulur — yüzde okuması gecikirse
      // önce mutlak değeri çizip sonra yüzdeye çevirmek ekseni zıplatırdı.
      if (heapCard && status !== 'loading') {
        const pct = podPercentLines(pctUsed, pctLimit);
        if (pct.length > 0) {
          pods = pct;
          percent = true;
          unit = '%';
          threshold = thr.heapRawPct;
          const times = [...new Set(pct.flatMap(p => p.points.map(x => x.time)))].sort((a, b) => a - b);
          extra.push({ label: 'sınır', color: 'var(--err)', dashed: true, points: times.map(time => ({ time, value: 100 })) });
        } else {
          pods = absPods;
        }
      }
      if (pods.some(p => p.points.length) || extra.some(x => x.points.length)) anyData = true;
      // Düzlük yalnız okumalar oturunca söylenir (yarım veriyle "düz" denmez).
      const flat: FlatSummary | null = isFlatCandidate(family, card) && status === 'ready' ? flatSummary(flatInput) : null;
      const note = [
        cardClusters.size > 1 ? `${cardClusters.size} cluster` : '',
        heapCard && !percent && status === 'ready' ? 'sınır bilinmiyor (MB)' : '',
      ].filter(Boolean).join(' · ') || null;
      return { card, pods, extra, podless, status, flat, unit, threshold, percent, note };
    });
    return { byCard, anyData };
  }, [family, cards, specs, queries, thr]);

  // Aile bilinmiyor ya da (yüklendi + hiç veri yok) → section hiç yok
  // (runtime metriği göndermeyen servise boş kart duvarı gösterme).
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
          {family === 'jvm' && <>Auto-instrumentation <code>jvm.*</code> metrics per pod (heap · GC · threads).</>}
          {family === 'go' && <>Go runtime <code>process.runtime.go.*</code> metrics.</>}
        </div>
      </div>}
      {/* Full-width istifli kartlar; ortak syncKey: bir karta hover TÜM
          kartlarda crosshair çizer (heap spike ↔ GC pause korele). */}
      <div className="rt-cards">
        {built.byCard.map(({ card, pods, extra, podless, status, flat, unit, threshold, percent, note }) => (
          flat && !view.lines && !view.nonheap ? (
            <div key={card.key} className="card" data-testid="rt-flat-card">
              <div className="ov-card-h"><h3 title={srcOf(card).tip}>{card.title}{card.unit ? ` (${card.unit.trim()})` : ''}</h3>
                <span className="badge b-gray" title={srcOf(card).tip}>{srcOf(card).label}</span></div>
              <div className="ov-card-b rt-flat">
                <span>
                  Düz: {flat.pods} pod, {flat.min === flat.max ? fmtMB(flat.min) : `${fmtMB(flat.min)}–${fmtMB(flat.max)}`}{card.unit} aralığında;
                  bu pencerede hiçbir pod %10&#39;dan fazla oynamadı.
                </span>
                <Link replace to={{ search: rtViewSearch(params, { ...view, nonheap: true }) }}>Grafiği göster</Link>
              </div>
            </div>
          ) : (
            <div key={card.key}>
              <RuntimePodChart
                title={percent ? 'JVM heap — sınırın yüzdesi (pod başına)' : `${card.title}${card.unit ? ` (${card.unit.trim()})` : ''}`}
                storageKey={`rt-${family}-${card.key}`}
                unit={unit}
                service={service} from={from} to={to}
                pods={pods} podless={podless} extra={extra}
                threshold={threshold}
                status={status}
                deploys={deployNs}
                mode={percent ? 'line' : card.mode ?? 'area'}
                note={note}
                source={srcOf(card)}
                testId={`rt-card-${card.key}`}
                onZoom={onZoom} onZoomReset={onZoomReset} />
              {flat && view.nonheap && !view.lines && (
                <div className="rt-view-note">
                  <Link replace to={{ search: rtViewSearch(params, { ...view, nonheap: false }) }}>Grafiği gizle</Link>
                </div>
              )}
            </div>
          )
        ))}
      </div>
    </>
  );
}
