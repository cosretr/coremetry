// TraceMetricsPanel — v0.10.913: Trace sayfası "Metrics" sekmesi (revize
// mockup Onay 2026-09-25).
//
// v0.10.968 — YENİDEN TASARIM (operatör onayı 2026-09-27, "3 onay"; mockup
// Main.dc.html 64 pod / 25 servis + PodDetail.dc.html). Çip duvarı yerine
// servise göre gruplu TEK tablo (TracePodTable), sıra trace ilgisine göre ve
// trace'ten HEMEN bilinir (hata → kritik yol payı → öz süre); metrikler
// Thanos'tan geç gelir ve sırayı DEĞİŞTİRMEZ. Seçili pod satırının altında
// (TracePodPanel, v0.10.976), `mview=pod` ile odak görünümünde (TracePodFocus).
//
// v0.10.976 — SATIR ALTI AYRINTI (operatör: "inline daha iyi olur",
// 2026-09-27). Prod v0.10.968'de 400 px yan panel tabloyu sıkıştırıp CPU/Bellek
// hücrelerini "%…"ya kırpıyordu; yan yerleşim (.tpm-body.has-side / .tpm-side)
// söküldü. Seçili pod'un ayrıntısı TracePodTable'a `detail` ile verilir ve
// seçili satırın hemen altında tam genişlikte çizilir; tabloya tıklanan /
// Enter'lanan pod zaten seçiliyse KAPANIR (mpod=-). URL durumu (mpod, replace)
// ve odak görünümü aynen; odak dönüşü (focusRow) ayrıntı için de geçerli.
//   • Satırdan kapanış da restoreTo ister (Esc / × ile simetrik): satır
//     yeniden bağlanmaz ama odak bir yerde yitmişse aynı kural yakalar.
//   • Çip yolu seçimi taşıyınca (compare[0] çıkarıldı, ≥2 pod) ayrıntı başka
//     satırın altına gider ve düğüm taşınır (odak düşer): kabuk `d:<pod>`
//     focusRow isteğiyle odağı yeni bölgeye alır. Odak görünümünde tablo yok:
//     istek üretilmez.
//   • Tablo kaydırmayı yalnız kendi kabında yapar; `detailFromUrl` (mpod
//     açıkça dolu) ilk bağlanmadaki derin link kaydırmasını ayırır (varsayılan
//     seçim sekmeye girişte sayfayı kaydırmasın).
//
// Veri: pod başına /api/clusters/pods/detail fan-out'u yerine TOPLU uç
// (/api/trace-pods/metrics; cluster değeri + ≤64 pod dilimi başına tek
// istek). Cluster eşlemesi sunucuda — sekme entity katmanına bağlı DEĞİL.
//
// Kabuk bu dosyada: özet şeridi, R1 notları, araç çubuğu, gövde, dipnot,
// canlı bölge, Esc katmanı, URL durumu ve veri kancası. v0.10.962'nin beş
// düzeltmesi yeni tasarımda sürer: seçim görünür (row-selected +
// aria-selected), pod linki range+at taşır, pod başına hata boş sonuç gibi
// yazılmaz ("okunamadı" + "N servis okunamadı"), tavan geri bildirimi (her
// zaman bağlı canlı bölge), sıra trace ilgisi.
//
// v0.10.968 — inceleme turu:
//   • URL okuması da ÜST KÜMEDEN (window.location): Trace.tsx sekmeden
//     çıkarken m* parametrelerini ham replaceState ile siler, router'ın
//     `sp`i bayat kalır; geri dönünce panel URL'de OLMAYAN süzgeç/seçimi
//     gösterip ilk yazımda sessizce düşürüyordu (v0.8.256 sınıfı).
//   • Odak görünümünde tablo araç çubuğu yok (PodDetail mockup'ı): süzgeç,
//     arama, gruplama odak rayını etkilemez.
//   • Odak dönüşü: panel kapanınca / odak görünümünden çıkınca odak
//     <body>'ye düşmez, kapanan pod'un satırına döner (TracePodTable
//     focusRow).
//   • "Yeniden dene" bitince sonuç duyurulur (düğme DOM'dan kalkıyordu).
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { SpanRow, TraceAnalysis } from '@/lib/types';
import type { CriticalPath } from '@/lib/criticalPath';
import { useEscLayer } from '@/lib/escLayer';
import { useTracePodMetricsChunks } from '@/lib/queries/tracePodMetrics';
import { Empty } from '@/components/Spinner';
import { Button, SearchField, SegmentedControl, Tooltip } from '@/components/ui';
import { TracePodTable } from './TracePodTable';
import { TracePodPanel } from './TracePodPanel';
import { TracePodFocus } from './TracePodFocus';
import { TraceMetricsCoverage } from './TraceMetricsCoverage';
import {
  buildRows, buildTraceMetricsModel, coverageSummary, defaultOpenGroups, defaultTracePod, podMetricState,
  selectPod, traceWindowNs, tracePodChunks,
} from './traceMetrics';
import { applyTraceMetricsPatch, parseTraceMetricsUrl, type TraceMetricsUrlPatch } from './traceMetricsUrl';
import { trPossessive } from './trSuffix';
import {
  CRIT_FLAG_SHARE, TRACE_METRICS_MDP, TRACE_METRICS_WINDOWS,
  type PodMetricState, type TraceMetricsFilter, type TraceMetricsWindow, type TraceMetricsWindowInfo, type TracePodPanelProps,
} from './traceMetricsModel';

export interface TraceMetricsPanelProps {
  traceId: string;
  spans: SpanRow[];
  analysis?: TraceAnalysis;
  criticalPath: CriticalPath | null;   // from lib/criticalPath
  spanCapped: boolean;
  onShowPodSpans: (pod: string) => void;
  onOpenSpan: (spanId: string) => void;
}

const LOADING: PodMetricState = { kind: 'loading' };
const EMPTY_SET: ReadonlySet<string> = new Set();

export function TraceMetricsPanel({ traceId, spans, analysis, criticalPath, spanCapped, onShowPodSpans, onOpenSpan }: TraceMetricsPanelProps) {
  const [sp, setSp] = useSearchParams();
  const model = useMemo(
    () => buildTraceMetricsModel(spans, analysis, criticalPath, spanCapped),
    [spans, analysis, criticalPath, spanCapped]);
  const defPod = useMemo(() => defaultTracePod(model), [model]);
  // v0.10.968 — okuma da yazıcıyla AYNI üst kümeden: `sp` yalnız router
  // yazımlarında yeniden hesap tetikleyicisi (Trace.tsx'in ham replaceState'i
  // router'a haber vermez).
  const url = useMemo(() => {
    const live = typeof window === 'undefined' ? sp : new URLSearchParams(window.location.search);
    return parseTraceMetricsUrl(live, model.byPod, defPod);
  }, [sp, model, defPod]);

  // v0.10.968 — yazıcı ÜST KÜMEDEN kurar (traceMetricsUrl.ts başlığı):
  // router'ın `prev`i bu sayfada bayat bir alt küme.
  const write = useCallback((patch: TraceMetricsUrlPatch) => {
    const next = applyTraceMetricsPatch(window.location.search, patch);
    setSp(() => next, { replace: true });
  }, [setSp]);

  // ── canlı bölge (v0.10.962'den: HER ZAMAN bağlı) ─────────────────────
  // Önce boşaltılır sonra yazılır: aynı cümle art arda da duyurulur.
  const liveRef = useRef<HTMLSpanElement>(null);
  const liveTimer = useRef(0);
  const announce = useCallback((msg: string) => {
    const el = liveRef.current;
    if (!el) return;
    el.textContent = '';
    window.clearTimeout(liveTimer.current);
    liveTimer.current = window.setTimeout(() => { if (liveRef.current) liveRef.current.textContent = msg; }, 50);
  }, []);
  useEffect(() => () => window.clearTimeout(liveTimer.current), []);

  // ── yerel görünüm durumu (trace değişince sıfırlanır) ────────────────
  const [openGroups, setOpenGroups] = useState<ReadonlySet<string> | null>(null);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(EMPTY_SET);
  const [noPodOpen, setNoPodOpen] = useState(false);
  const [loadRest, setLoadRest] = useState(false);
  const [forced, setForced] = useState<ReadonlySet<string>>(EMPTY_SET);
  const [draft, setDraft] = useState(url.query);
  const [seenTrace, setSeenTrace] = useState(traceId);
  if (seenTrace !== traceId) {
    setSeenTrace(traceId);
    setOpenGroups(null); setExpanded(EMPTY_SET); setNoPodOpen(false); setLoadRest(false); setForced(EMPTY_SET);
  }

  // Arama: yerel taslak anında süzer, URL'e 200 ms gecikmeyle yazılır. URL →
  // taslak içe aktarımı İMZA KORUMALI (yalnız mq bizim yazmadığımız bir
  // değere döndüyse — geri/ileri), v0.8.253 kalıbı.
  const lastQ = useRef(url.query);
  useEffect(() => {
    if (url.query !== lastQ.current) { lastQ.current = url.query; setDraft(url.query); }
  }, [url.query]);
  useEffect(() => {
    if (draft === lastQ.current) return;
    const t = window.setTimeout(() => { lastQ.current = draft; write({ query: draft }); }, 200);
    return () => window.clearTimeout(t);
  }, [draft, write]);
  const clearQuery = useCallback(() => {
    setDraft('');
    if (lastQ.current !== '') { lastQ.current = ''; write({ query: '' }); }
  }, [write]);

  // ── pencere ─────────────────────────────────────────────────────────
  const baseWin = useMemo(() => {
    const w = traceWindowNs(model.traceStartNs, model.traceEndNs, url.win);
    const nowMs = Date.now();
    return {
      fromNs: w.from, toNs: w.to, startNs: w.startNs, endNs: w.endNs, win: url.win,
      openWindow: w.to > (nowMs - 10 * 60_000) * 1e6,
      agoMin: Math.max(0, Math.round((nowMs - model.traceEndNs / 1e6) / 60_000)),
    };
  }, [model, url.win]);

  // ── veri ────────────────────────────────────────────────────────────
  const chunks = useMemo(() => tracePodChunks(model), [model]);
  const q = useTracePodMetricsChunks(chunks, {
    fromNs: baseWin.fromNs, toNs: baseWin.toNs, mdp: TRACE_METRICS_MDP,
    openWindow: baseWin.openWindow, loadRest, forced, active: model.pods.length > 0,
  });
  const podChunk = useMemo(() => {
    const m = new Map<string, number>();
    chunks.forEach((c, i) => c.pods.forEach(p => m.set(p.pod, i)));
    return m;
  }, [chunks]);
  const metricsMap = useMemo(() => {
    const m = new Map<string, PodMetricState>();
    const trace = { startNs: model.traceStartNs, endNs: model.traceEndNs };
    for (const p of model.pods) m.set(p.pod, podMetricState(p, q.snapshots[podChunk.get(p.pod) ?? -1], trace));
    return m;
  }, [model, podChunk, q.snapshots]);
  const metrics = useCallback((pod: string) => metricsMap.get(pod) ?? LOADING, [metricsMap]);
  const stepSec = useMemo(() => {
    for (const s of q.snapshots) if (s.data?.mapped && s.data.step) return s.data.step;
    return null;
  }, [q.snapshots]);
  const thanosOff = q.snapshots.some(s => s.data !== undefined && !s.data.thanos);
  const windowInfo = useMemo<TraceMetricsWindowInfo>(() => ({
    fromNs: baseWin.fromNs, toNs: baseWin.toNs, startNs: baseWin.startNs, endNs: baseWin.endNs,
    win: baseWin.win, stepSec, openWindow: baseWin.openWindow,
  }), [baseWin, stepSec]);

  // Okunamayan dilimler + servisler (özet şeridi). Yeniden deneme sürerken
  // dilim "pending"e döner (React Query v5: verisiz sorgunun hatası yeni
  // istekte sıfırlanır) — şerit düğmesi "Yükleniyor…" + aria-busy ile kalsın
  // diye denenen dilimler (pencereye bağlı kimlikle) ayrıca izlenir.
  const failedChunks = q.snapshots.map((s, i) => (s.status === 'error' ? i : -1)).filter(i => i >= 0);
  const chunkId = (i: number) => `${chunks[i]?.key}|${baseWin.fromNs}|${baseWin.toNs}`;
  const [retryIds, setRetryIds] = useState<ReadonlySet<string>>(EMPTY_SET);
  const retryIdx = chunks.map((_, i) => (retryIds.has(chunkId(i)) ? i : -1)).filter(i => i >= 0);
  const retrying = retryIdx.some(i => { const s = q.snapshots[i]; return !!s && (s.fetching || s.status === 'pending'); });
  const retryDone = retryIds.size > 0 && retryIdx.every(i => q.snapshots[i]?.status === 'success' && !q.snapshots[i].fetching);
  useEffect(() => { if (retryDone) setRetryIds(EMPTY_SET); }, [retryDone]);
  const failedServices = useMemo(() => {
    const out = new Set<string>();
    for (const p of model.pods) {
      const s = q.snapshots[podChunk.get(p.pod) ?? -1];
      if (s?.status === 'error') out.add(p.service);
    }
    return out;
  }, [model, podChunk, q.snapshots]);
  const idleChunks = q.snapshots
    .map((s, i) => (!s.eligible && s.status === 'pending' && !s.fetching ? i : -1)).filter(i => i >= 0);
  const idlePods = idleChunks.reduce((a, i) => a + chunks[i].pods.length, 0);
  const retryFailed = () => {
    setRetryIds(new Set(failedChunks.map(chunkId)));
    for (const i of failedChunks) q.refetch(i);
  };
  // v0.10.968 — yeniden deneme bitince SONUÇ duyurulur: başarıda şerit düğmesi
  // DOM'dan kalkar, klavye / ekran okuyucu kullanıcısı sessizce odağı yitirirdi.
  const wasRetrying = useRef(false);
  useEffect(() => {
    if (wasRetrying.current && !retrying) {
      announce(failedServices.size > 0 ? `${failedServices.size} servis hâlâ okunamadı.` : 'Metrikler yeniden yüklendi.');
    }
    wasRetrying.current = retrying;
  }, [retrying, failedServices.size, announce]);

  // ── seçim ───────────────────────────────────────────────────────────
  const compare = url.compare;
  const selectedPod = compare[0] ?? '';
  // v0.10.968 — odak dönüşü isteği: panel / odak görünümü kalkınca tablo o
  // pod'un satırını odaklar. Sayaç tekdüze artar (aynı pod ikinci kez de).
  const [focusReq, setFocusReq] = useState<{ key: string; n: number } | null>(null);
  const focusReqN = useRef(0);
  const restoreTo = useCallback((pod: string) => {
    if (pod) setFocusReq({ key: `p:${pod}`, n: ++focusReqN.current });
  }, []);
  // v0.10.976 — çip yolu seçimi taşıyınca (compare[0] çıkarıldı, ≥2 pod)
  // ayrıntı başka satırın altına gider ve düğüm taşınır; odak <body>'ye
  // düşmesin, yeni ayrıntının bölgesine geçsin (satırdan açılışla aynı yer).
  const focusDetailOf = useCallback((pod: string) => {
    if (pod) setFocusReq({ key: `d:${pod}`, n: ++focusReqN.current });
  }, []);
  const [seenFocus, setSeenFocus] = useState(url.focus);
  if (seenFocus !== url.focus) {
    setSeenFocus(url.focus);
    if (url.focus) setFocusReq(null); // odak görünümüne girişte bayat istek kalmasın
  }
  const selectedInfo = selectedPod ? model.byPod.get(selectedPod) : undefined;
  const onSelect = useCallback((pod: string) => {
    const { next, serviceChanged } = selectPod(compare, pod, model, p => metrics(p).kind === 'ok');
    write({ compare: next });
    if (serviceChanged) announce(`Karşılaştırma servisi değişti: ${model.byPod.get(pod)?.service ?? ''}.`);
  }, [compare, model, metrics, write, announce]);
  // v0.10.976 — tablo satırından seçim: açık olan pod'a yeniden tık / Enter
  // ayrıntıyı KAPATIR (yalnız satır yolu; odak görünümünün rayı / önceki-sonraki
  // onSelect'i kullanır ve seçimi düşürmez). Kapanış Esc / × ile simetrik:
  // restoreTo — satır yerinde kalır, yitmiş odağı mevcut kural satıra döndürür.
  const onSelectRow = useCallback((pod: string) => {
    if (pod === selectedPod) { restoreTo(pod); write({ compare: 'closed' }); return; }
    onSelect(pod);
  }, [selectedPod, onSelect, write, restoreTo]);
  const onRetry = useCallback((pod: string) => {
    const i = podChunk.get(pod);
    if (i === undefined) return;
    const s = q.snapshots[i];
    if (s?.status === 'error' || s?.status === 'success') { q.refetch(i); return; }
    const key = chunks[i].key;
    setForced(prev => (prev.has(key) ? prev : new Set([...prev, key])));
  }, [podChunk, q, chunks]);
  const onRetryService = useCallback((svc: string) => {
    const idx = new Set<number>();
    for (const p of model.pods) {
      if (p.service !== svc || metricsMap.get(p.pod)?.kind !== 'error') continue;
      const i = podChunk.get(p.pod);
      if (i !== undefined) idx.add(i);
    }
    idx.forEach(i => q.refetch(i));
  }, [model, metricsMap, podChunk, q]);

  // ── satırlar ────────────────────────────────────────────────────────
  const effOpen = useMemo(() => openGroups ?? defaultOpenGroups(model, selectedPod), [openGroups, model, selectedPod]);
  const rows = useMemo(() => buildRows(model, {
    grouping: url.grouping, filter: url.filter, query: draft,
    openGroups: effOpen, expanded, noPodOpen, selected: selectedPod,
  }), [model, url.grouping, url.filter, draft, effOpen, expanded, noPodOpen, selectedPod]);
  const onToggleGroup = useCallback((svc: string, open: boolean) => {
    setOpenGroups(prev => {
      const s = new Set(prev ?? defaultOpenGroups(model, selectedPod));
      if (open) s.add(svc); else s.delete(svc);
      return s;
    });
  }, [model, selectedPod]);
  const onExpandMore = useCallback((svc: string) => setExpanded(prev => new Set([...prev, svc])), []);
  const collapsible = model.groups.filter(g => g.pods.length > 3).map(g => g.service);
  const allOpen = model.groups.every(g => effOpen.has(g.service)) && collapsible.every(s => expanded.has(s));
  const toggleAll = () => {
    if (allOpen) { setOpenGroups(new Set()); setExpanded(EMPTY_SET); }
    else { setOpenGroups(new Set(model.groups.map(g => g.service))); setExpanded(new Set(collapsible)); }
    if (url.grouping !== 'service') write({ grouping: 'service' });
  };
  const setFilter = (f: TraceMetricsFilter) => {
    write({ filter: f });
    if (f === 'all') { setOpenGroups(null); setExpanded(EMPTY_SET); }
  };
  const clearFilters = useCallback(() => {
    setDraft('');
    lastQ.current = '';
    write({ query: '', filter: 'all' });
    setOpenGroups(null);
    setExpanded(EMPTY_SET);
  }, [write]);

  // ── Esc: odak görünümü → arama → seçim (tek katman, kabuğun) ─────────
  useEscLayer(model.pods.length > 0 && (url.focus || draft !== '' || compare.length > 0), () => {
    if (url.focus) { restoreTo(selectedPod); write({ focus: false }); return; }
    if (draft !== '') { clearQuery(); return; }
    if (compare.length > 0) { restoreTo(selectedPod); write({ compare: 'closed' }); }
  });

  const coverage = useMemo(() => coverageSummary(model, metrics), [model, metrics]);

  if (model.pods.length === 0) {
    return (
      <div className="tpm">
        <Empty icon="—" title="Bu trace'in span'larında k8s.pod.name yok — pod metrikleri gösterilemiyor." />
        <span ref={liveRef} className="sr-only" role="status" aria-live="polite" aria-atomic="true" />
      </div>
    );
  }

  const critPods = model.pods.filter(p => p.critShare >= CRIT_FLAG_SHARE);
  const critSum = Math.round(critPods.reduce((a, p) => a + p.critShare, 0) * 100);
  const clusterCount = model.clusterValues.filter(Boolean).length;
  const filterOpts: { value: TraceMetricsFilter; label: string; title?: string; disabled?: boolean }[] = [
    { value: 'all', label: `Hepsi ${model.counts.all}` },
    { value: 'err', label: `Hatalı ${model.counts.err}`, title: "En az bir hatalı span taşıyan pod'lar" },
    { value: 'crit', label: `Kritik yol ${model.counts.crit}`, title: "Trace süresinin en az %3'ü kritik yol üzerinde bu pod'da geçti" },
    model.selfKnown
      ? { value: 'slow', label: `Yavaş ${model.counts.slow}`, title: "Trace'in öz süresi en büyük 5 span'ından birini taşıyan pod'lar" }
      : { value: 'slow', label: 'Yavaş', title: 'Sunucu analizi yok: öz süre gösterilemiyor', disabled: true },
  ];

  const panelProps: TracePodPanelProps | null = selectedInfo ? {
    model, selected: selectedInfo, compare, metrics, window: windowInfo,
    siblingLines: url.siblingLines, focus: url.focus,
    onCompareChange: next => {
      // v0.10.976 — seçim (compare[0]) taşındıysa odak yeni ayrıntının bölgesine;
      // odak görünümünde (TracePodFocus de bu yolu kullanır) tablo yok: istek yok.
      if (!url.focus && next.length > 0 && next[0] !== selectedPod) focusDetailOf(next[0]);
      write({ compare: next.length ? next : 'closed' });
    },
    onSelect,
    onClose: () => { restoreTo(selectedPod); write({ compare: 'closed' }); },
    onToggleFocus: () => { if (url.focus) restoreTo(selectedPod); write({ focus: !url.focus }); },
    onToggleSiblingLines: () => write({ siblingLines: !url.siblingLines }),
    onShowSpans: onShowPodSpans,
    onOpenSpan,
    onRetry,
    announce,
  } : null;
  // v0.10.968 — odak görünümü tablo + araç çubuğunun YERİNE geçer (PodDetail mockup'ı).
  const showFocus = url.focus && panelProps !== null;

  return (
    <div className="tpm">
      <div className="tpm-strip">
        <div className="tpm-strip-left">
          <span>{model.pods.length} pod · {model.groups.length} servis · {clusterCount} cluster</span>
          {critPods.length > 0 && (
            <><span aria-hidden="true">·</span><span>Kritik yolun %{critSum}{trPossessive(critSum)} {critPods.length} pod'da</span></>
          )}
          <span aria-hidden="true">·</span>
          <TraceMetricsCoverage summary={coverage} />
          {(failedServices.size > 0 || retrying) && (
            <>
              <span aria-hidden="true">·</span>
              {failedServices.size > 0 && <span className="cell-err tpm-strong">{failedServices.size} servis okunamadı</span>}
              <Button variant="ghost" size="xs" aria-busy={retrying || undefined} disabled={retrying} onClick={retryFailed}>
                {retrying ? 'Yükleniyor…' : 'Yeniden dene'}
              </Button>
            </>
          )}
        </div>
        <div className="tpm-strip-right">
          <span className="tpm-muted">Pencere: trace ±</span>
          <SegmentedControl<string> size="sm" aria-label="Metrik penceresi" value={String(url.win)}
            onChange={v => write({ win: Number(v) as TraceMetricsWindow })}
            options={TRACE_METRICS_WINDOWS.map(w => ({ value: String(w), label: w === 60 ? '1 sa' : `${w} dk` }))} />
        </div>
      </div>

      {(!model.selfKnown || model.truncated || baseWin.openWindow || thanosOff) && (
        <div className="tpm-notes">
          {!model.selfKnown && <span className="cell-warn">Sunucu analizi yok: öz süre gösterilemiyor</span>}
          {model.truncated && <span className="cell-warn">Trace kırpılmış: sıra kısmi veriye dayanır</span>}
          {baseWin.openWindow && <span className="tpm-muted">Trace {baseWin.agoMin} dk önce — metrikler henüz tamamlanmamış olabilir</span>}
          {thanosOff && (
            <span className="tpm-muted">
              Thanos Remote Cluster tanımlı değil — CPU ve Bellek kolonları gizlendi; tablo yalnız trace verisiyle sıralandı.
            </span>
          )}
        </div>
      )}

      {!showFocus && (
        <div className="tpm-toolbar">
          <SegmentedControl<TraceMetricsFilter> size="sm" aria-label="Pod süzgeci" value={url.filter}
            onChange={setFilter} options={filterOpts} />
          <SearchField value={draft} onChange={setDraft} hint="/" width={240}
            aria-label="Servis veya pod ara" placeholder="Servis veya pod ara" data-shortcut-search=""
            onKeyDown={e => {
              if (e.key !== 'Escape') return;
              e.preventDefault();
              if (draft !== '') clearQuery();
              else e.currentTarget.blur();
            }} />
          <span className="tpm-toolbar-right">
            <Tooltip content="Önce hatanın kaynağı olan servis, sonra hatalı pod'u olan servisler; ardından kritik yol payı, en büyük öz süre, span sayısı. Metrikler sırayı değiştirmez.">
              <span className="tpm-order" tabIndex={0}>Sıra: trace ilgisi ⓘ</span>
            </Tooltip>
            <SegmentedControl<string> size="sm" aria-label="Gruplama" value={url.grouping}
              onChange={v => write({ grouping: v === 'flat' ? 'flat' : 'service' })}
              options={[{ value: 'service', label: 'Servis' }, { value: 'flat', label: 'Düz' }]} />
            <Button variant="ghost" size="sm" onClick={toggleAll}>{allOpen && url.grouping === 'service' ? 'Tümünü kapat' : 'Tümünü aç'}</Button>
            {idleChunks.length > 0 && !loadRest && (
              <Button variant="secondary" size="sm" onClick={() => {
                setLoadRest(true);
                announce(`${idlePods} pod'un metrikleri yükleniyor.`);
              }}>
                Kalan {idlePods} pod'un metriklerini yükle ({idleChunks.length} istek)
              </Button>
            )}
          </span>
        </div>
      )}

      {showFocus && panelProps ? (
        <TracePodFocus {...panelProps} />
      ) : (
        /* v0.10.976 — tek sütun: ayrıntı tablonun içinde, seçili satırın altında. */
        <section aria-label="Pod tablosu" className="tpm-main">
          <TracePodTable model={model} rows={rows} selected={selectedPod} metrics={metrics}
            hideMetrics={thanosOff} window={windowInfo} grouping={url.grouping}
            onSelectPod={onSelectRow} onToggleGroup={onToggleGroup} onExpandMore={onExpandMore}
            onToggleNoPod={setNoPodOpen} onShowPodSpans={onShowPodSpans} onRetryService={onRetryService}
            onClearFilters={clearFilters} announce={announce} focusRow={focusReq}
            detail={panelProps ? <TracePodPanel {...panelProps} /> : null}
            detailFromUrl={!url.isDefault} />
        </section>
      )}

      <div className="tpm-foot">
        Kaynak: trace span'ları (sıra, hata, kritik yol) · Thanos (CPU, bellek{stepSec ? `; ${stepSec} sn adım` : ''}) · limitler şu anki değerdir.
      </div>
      {/* v0.10.962 — HER ZAMAN bağlı tek canlı bölge (içerik değişmeden önce ağaçta olmalı). */}
      <span ref={liveRef} className="sr-only" role="status" aria-live="polite" aria-atomic="true" />
    </div>
  );
}

