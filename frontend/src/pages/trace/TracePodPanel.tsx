// TracePodPanel — v0.10.968 — Trace › Metrics seçili pod paneli (v0.10.968'de
// 400px yan panel, onaylı mockup Main.dc.html sağ panel; operatör "3 onay",
// 2026-09-27).
//
// v0.10.1096 — GRAFİKLER ÜSTTE, METİN TEK SATIR + KATLI AYRINTI (operatör,
// prod: "Çok fazla yazı var; sadece metrik yatayda inline gözükse olacak.
// Diğer yazılar aşağı olabilir."). v0.10.976'nın iki sütunlu gövdesi (solda üç
// yoğun metin bloğu, sağda sıkışmış iki grafik) kalktı. Yeni sıra:
//   • başlık satırı + alt satır/rozetler — AYNEN (eylemler yerinde);
//   • grafik başlık çubuğu: SAĞDA küçük "Karşılaştır" denetimi (çipler +
//     "Kardeş çizgileri", `CompareSection compact`) — blok değil;
//   • TEK yatay ızgara (`.tpp-chart-row`, dar ekranda sarar): Bellek · CPU
//     (+ JVM servisinde heap · GC); her grafiğin tek satır başlığı "Bellek
//     9,03 GiB · limit 16 GiB" (chartHeadline), trace bandı grafikte;
//   • TEK satır özet (`.tpp-facts`, podFacts): span · hata · kritik yol · en
//     büyük öz süre ↗ · faz · restart (· son sonlanma rozeti);
//   • kapalı "Teknik ayrıntı" (ayrıntı/sorun sayfalarının Sect deseni; kapalıyken
//     mount edilmez): Bu trace'te / Trace anında / Şu an blokları, grafik
//     açıklaması ("Mavi bant: …"), JVM-dışı runtime notu.
// Hiçbir sayı ya da bağlantı silinmedi; yalnız yer değiştirdi / katlandı.
// JVM artık kapalı açılır bölüm değil: seçimle açılan panelde grafik sırasında
// (§6 "fetch on open" — sorgu servis başına bir kez, çip değişimi istek atmaz).
//
// v0.10.976 — SATIR ALTI KOMPAKT AYRINTI (operatör: "inline daha iyi olur").
// Yan panel tabloyu sıkıştırıp CPU/Bellek hücrelerini "%…"ya kırpıyordu; panel
// artık TracePodTable'ın seçili satırının altında tam genişlikte (kabuk
// `detail` prop'uyla verir; bölge ve odak tabloda). Yerleşim YATAY (gövde
// kısmı v0.10.1096'te yukarıdaki sıraya döndü):
//   • başlık satırı — ad (ortadan kırpma), kopyala, "Odak görünümü",
//     "Pod sayfasında aç", "Span'ları Trace'te göster", ×;
//   • alt satır + rozetler;
//   • gövde iki sütun (`.tpp-detail-body`, ≤1024 alt alta): SOLDA üç özet
//     bloğu yan yana (`.tpp-kv3`: Bu trace'te / Trace anında / Şu an; dar
//     etiket sütunu), SAĞDA Karşılaştır (çipler, +N menüsü, Kardeş çizgileri)
//     + yan yana Bellek/CPU (168 px; limit çizgisi, etkin aralık bandı, kardeş
//     çizgileri). Metrik ok değilse sağ sütunda durum kutusu (StateBlock);
//   • JVM yalnız JVM servisinde, KAPALI açılır bölüm (DisclosureButton) —
//     açılmadan ClickHouse isteği yok (§6 "fetch on expand");
//   • "Kaynak / adım" dipnotu sekme altında kalır (kabuk).
// v0.10.968'in hiçbir parçası düşmedi; TracePodFocus paylaşılan bölümleri
// aynen (dikey) kullanır.
//
// v0.10.968 — Panel bir pod'un BU TRACE'teki rolünü (span, hata, kritik yol,
// öz süre, etkin aralık), trace anındaki CPU/belleğini (limite ve kardeşlere
// göre), aynı servisin en çok 4 pod'unu üst üste grafiği, JVM heap/GC'yi
// (yalnız JVM servislerinde) ve "Şu an" durumunu (restart, OOMKilled,
// limitler — trace anı DEĞİL) gösterir. Veri: tablo kabuğunun toplu Thanos
// cevabı (metrics(pod)); panel EK Thanos isteği atmaz, yalnız JVM
// (ClickHouse metric_points) seçimde okunur.
//
// v0.10.962 davranışları korunur: seçili çip görünür (Chip `active`),
// "Pod sayfasında aç" range+at taşır (traceMetricsPodHref), pod başına hata
// asla boş gösterilmez (stateMessage), tavanda çip devre dışı + gerekçe +
// canlı bölge duyurusu (announce), bellek CPU'dan önce ve zeroBase.
//
// Bölümler (Header, TraceFacts, MomentSection, CompareSection, StateBlock,
// JvmSection, NowSection) TracePodFocus ile PAYLAŞILIR; saf kararlar
// tracePodPanelModel.ts'te (tablo testli).
//
// v0.10.968 — inceleme turu: (1) karşılaştırma renkleri çakışmasız
// (compareColors; çip swatch'ı, Bellek/CPU ve JVM çizgileri tek haritadan);
// (2) panel duyuruları sr-only canlı bölgenin YANINDA çiplerin altında
// görünür not olarak da yazılır (mockup `pn.hasLive`; reddedilen son-çip
// tıkı gören kullanıcıya da sessiz kalmasın); (3) kopyalama lib/clipboard
// yedeğiyle ve yalnız başarıda "kopyalandı" der; (4) "+N" menüsü açıkken
// üyeliği donar (işaretlenen satır odağı <body>'ye düşürmez); (5) "Yeniden
// dene"/"Yükle" bastıktan sonra odak mesaj kutusunda kalır.
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Check, Copy, Maximize2, X } from 'lucide-react';
import { Badge, Button, Chip, DisclosureButton, IconButton, KeyValue, LinkButton, MenuItem, Popover, type KeyValueItem } from '@/components/ui';
import { MiddleEllipsis } from '@/components/ui/DataTable/MiddleEllipsis';
import { Skeleton } from '@/components/Skeleton';
import { copyToClipboard } from '@/lib/clipboard';
import { useThemeTick } from '@/lib/useThemeTick';
import { familyOf } from '@/pages/service/RuntimeCharts';
import {
  CRIT_FLAG_SHARE, TRACE_METRICS_MAX_COMPARE,
  type PodMetricSeries, type PodMetricState, type TraceMetricsModel, type TraceMetricsWindowInfo,
  type TracePodInfo, type TracePodPanelProps,
} from './traceMetricsModel';
import { fmtClockNs, fmtDurNs, fmtPct } from './traceMetricsFmt';
import { shortPod, togglePod, traceMetricsPodHref } from './traceMetrics';
import { TraceJvmPanel } from './TraceJvmPanel';
import { PodChart } from './TracePodCharts';
import { podChartsCaption, usePodChartBuilds } from './usePodChartBuilds';
import { usePanelNote } from './usePanelNote';
import {
  agoText, CAP_REASON, chartHeadline, clusterNameOf, compareChipLayout, compareColors, hhmm, momentRow, nowNote, nowSection,
  podBadges, podFacts, servicePods, siblingCopy, siblingStats, stateMessage, subLine, suffixLabels,
  type ChartHeadline, type CompareChip, type MomentRow, type NowRow,
} from './tracePodPanelModel';

// ── Paylaşılan parçalar ──────────────────────────────────────────────────

/** v0.10.1096 — satır altı ayrıntıda grafik yüksekliği (Bellek, CPU, JVM heap, GC). */
const DETAIL_CHART_H = 168;

/** v0.10.968 — "Pod adını kopyala": lib/clipboard (düz HTTP'de execCommand
 *  yedeği); "kopyalandı" YALNIZ kopya gerçekten yapıldıysa, değilse
 *  "kopyalanamadı" duyurulur (tablonun ⋯ menüsüyle aynı). */
export function CopyPodButton({ pod, announce }: { pod: string; announce: (m: string) => void }) {
  const copy = () => {
    void copyToClipboard(pod).then(ok => announce(ok ? `Pod adı kopyalandı: ${pod}` : `Pod adı kopyalanamadı: ${pod}`));
  };
  return <IconButton aria-label="Pod adını kopyala" tooltip="Pod adını kopyala" icon={<Copy size={14} />} onClick={copy} />;
}

/** v0.10.968 — "Pod sayfasında aç ↗" (range + at, v0.10.962 hata 2); cluster
 *  bilinmiyorsa çizilmez. */
export function PodPageLink({ p, state, window: w }: { p: TracePodInfo; state: PodMetricState; window: TraceMetricsWindowInfo }) {
  const cluster = clusterNameOf(state);
  if (!cluster) return null;
  const namespace = (state.kind === 'ok' ? state.data.namespace : '') || p.namespace;
  const href = traceMetricsPodHref(
    { pod: p.pod, cluster, namespace, service: p.service },
    { from: w.fromNs, to: w.toNs, startNs: w.startNs },
  );
  return <Link className="sec" to={href}>Pod sayfasında aç ↗</Link>;
}

export function PodBadges({ p }: { p: TracePodInfo }) {
  const badges = podBadges(p, CRIT_FLAG_SHARE);
  if (badges.length === 0) return null;
  return (
    <div className="tpp-badges">
      {badges.map(b => <Badge key={b.t} tone={b.tone}>{b.t}</Badge>)}
    </div>
  );
}

/** v0.10.968 — "Bu trace'te" (KeyValue): hata zamanı ve en büyük öz süreli
 *  span, Trace sekmesinde o span'i açan LinkButton'lar. */
export function TraceFacts({ model, p, onOpenSpan }: { model: TraceMetricsModel; p: TracePodInfo; onOpenSpan: (id: string) => void }) {
  const selfName = useMemo(
    () => model.podSpans.get(p.pod)?.find(s => s.spanId === p.maxSelfSpanId)?.name ?? '',
    [model, p.pod, p.maxSelfSpanId]);
  const fe = p.firstError;
  const items: KeyValueItem[] = [
    { id: 'spans', k: 'Span', v: String(p.spans) },
    {
      id: 'errors', k: 'Hata',
      v: fe
        ? <>{`${p.errors} · ilk: ${fe.name} · ${fe.status} · `}<LinkButton onClick={() => onOpenSpan(fe.spanId)}>{fmtClockNs(fe.timeNs)}</LinkButton></>
        : p.errors > 0 ? String(p.errors) : <span className="cell-faint">0</span>,
    },
    {
      id: 'self', k: 'En büyük öz süre',
      v: model.selfKnown && p.maxSelfSpanId
        ? <LinkButton onClick={() => onOpenSpan(p.maxSelfSpanId)}>{`${selfName || p.maxSelfSpanId} · ${fmtDurNs(p.maxSelfNs)} ↗`}</LinkButton>
        : null,
    },
    {
      id: 'crit', k: 'Kritik yol payı',
      v: p.critNs > 0 ? `${fmtPct(p.critShare)} · ${fmtDurNs(p.critNs)}` : <span className="cell-faint">— · kritik yolda değil</span>,
    },
    { id: 'active', k: 'Etkin aralık', v: `${fmtClockNs(p.activeFromNs)} → ${fmtClockNs(p.activeToNs)} · ${fmtDurNs(p.activeToNs - p.activeFromNs)}` },
  ];
  return (
    <section className="tpp-sec" aria-label="Bu trace'te">
      <div className="tpp-sec-title"><span>Bu trace'te</span></div>
      <KeyValue items={items} />
    </section>
  );
}

const LEVEL_CLASS: Record<MomentRow['level'], string> = { none: 'cell-strong', warn: 'cell-warn cell-strong', err: 'cell-err cell-strong' };

function MomentValue({ row }: { row: MomentRow }) {
  return (
    <>
      <span className={LEVEL_CLASS[row.level]}>{row.main}</span>
      <span className="cell-faint">{row.rest}</span>
      {row.cap && <><br /><span className="tpp-note">{row.cap}</span></>}
    </>
  );
}

/** v0.10.968 — "Trace anında · Thanos, ±1 adım ({step} sn)": seviye rengi
 *  (cell-warn / cell-err) YALNIZ limite göre sapmada; kardeş oranı ≥1,5 uyarı. */
export function MomentSection({ model, p, data, metrics }: { model: TraceMetricsModel; p: TracePodInfo; data: PodMetricSeries; metrics: (pod: string) => PodMetricState }) {
  const stats = useMemo(() => siblingStats(model, p, metrics), [model, p, metrics]);
  const cpu = momentRow('cpu', data);
  const mem = momentRow('mem', data);
  const sib = stats ? siblingCopy(stats) : [];
  const items: KeyValueItem[] = [
    { id: 'cpu', k: 'CPU', v: <MomentValue row={cpu} />, title: cpu.title },
    { id: 'mem', k: 'Bellek', v: <MomentValue row={mem} />, title: mem.title },
    {
      id: 'sib', k: 'Kardeşlere göre', title: 'Eşik: 1,5 kat ve üstü uyarı rengi alır',
      v: <>{sib.map((s, i) => (
        <span key={i} className={s.tone === 'warn' ? 'cell-warn cell-strong' : s.tone === 'err' ? 'cell-err cell-strong' : s.tone === 'faint' ? 'cell-faint' : undefined}>{s.t}</span>
      ))}</>,
    },
  ];
  return (
    <section className="tpp-sec" aria-label="Trace anında">
      <div className="tpp-sec-title"><span>Trace anında · Thanos, ±1 adım ({data.stepSec} sn)</span></div>
      <KeyValue items={items} />
    </section>
  );
}

function ChipBody({ c, color }: { c: CompareChip; color: string | undefined }) {
  return (
    <>
      {c.on && <><Check size={12} aria-hidden="true" /><span className="tpp-swatch" aria-hidden="true" style={{ background: color }} /></>}
      <span className="mono">{c.label}</span>
      <span> · {c.spans}</span>
      {c.errors > 0 && <span className="cell-err"> · ⚠{c.errors}</span>}
    </>
  );
}

const CAP_ANNOUNCE = 'Karşılaştırmada en çok 4 pod.';

/** v0.10.968 — "Karşılaştır · aynı servisten (N/4)": çipler togglePod ile
 *  (v0.10.962 anlamı), seçili çip çıkınca sıradaki öne geçer, son çip
 *  çıkarılamaz (duyuru), tavan devre dışı + gerekçe + duyuru; ilk 6 + işaretli
 *  + karşılaştırılan görünür, gerisi "+N" menüsünde (menuitemcheckbox).
 *
 *  v0.10.968 — `note`: son duyuru çiplerin altında GÖRÜNÜR (mockup
 *  `pn.hasLive`); başarılı tık notu siler. Menü açıkken üyeliği donar
 *  (`menuPods` → compareChipLayout `pinned`); taşma boşalınca menü kapanır
 *  (çapasız açık kalıp sonra kendiliğinden açılıp odak çalmasın); yalnız
 *  karşılaştırmada olduğu için görünen çip çıkarılınca odak "+N"ye geçer.
 *
 *  v0.10.1096 — `compact`: satır altı ayrıntıda grafik sırasının başlık
 *  çubuğunda SAĞA yaslı tek satır denetim (etiket + xs çipler + "Kardeş
 *  çizgileri"); bölüm çizgisi/başlığı yok. Davranış ve notlar aynı. */
export function CompareSection({
  model, p, compare, metrics, siblingLines, onCompareChange, onToggleSiblingLines, announce, note = '', setNote, compact = false,
}: {
  model: TraceMetricsModel;
  p: TracePodInfo;
  compare: string[];
  metrics: (pod: string) => PodMetricState;
  siblingLines: boolean;
  onCompareChange: (next: string[]) => void;
  onToggleSiblingLines: () => void;
  announce: (m: string) => void;
  note?: string;
  setNote?: (m: string) => void;
  compact?: boolean;
}) {
  const themeTick = useThemeTick();
  const pods = useMemo(() => servicePods(model, p.service), [model, p.service]);
  const labels = useMemo(() => suffixLabels(pods), [pods]);
  // Tema değişince palet yeniden çözülür (seriesColorsFor chartTheme okur).
  // eslint-disable-next-line react-hooks/exhaustive-deps -- v0.10.968: themeTick yalnız yeniden çözme tetikleyicisi
  const colors = useMemo(() => compareColors(compare, labels), [compare, labels, themeTick]);
  const [menuPods, setMenuPods] = useState<ReadonlySet<string> | null>(null);
  const [seenSvc, setSeenSvc] = useState(p.service);
  if (seenSvc !== p.service) { setSeenSvc(p.service); setMenuPods(null); }
  const { visible, overflow } = compareChipLayout(pods, compare, metrics, labels, menuPods ?? undefined);
  const moreRef = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (overflow.length === 0) setMenuPods(null); }, [overflow.length]);
  // Çıkarılan çip "+N"ye kaydıysa odak oraya (karşılaştırma yazımı commit olunca).
  const pendingMore = useRef<{ pod: string; sig: string } | null>(null);
  const compareSig = compare.join(',');
  useEffect(() => {
    const pm = pendingMore.current;
    if (!pm || pm.sig === compareSig) return;
    pendingMore.current = null;
    if (!visible.some(c => c.pod === pm.pod)) moreRef.current?.focus({ preventScroll: true });
  });
  const toggle = (c: CompareChip) => {
    if (c.disabled) { announce(c.title === CAP_REASON ? CAP_ANNOUNCE : c.title); return; }
    if (c.on && compare.length === 1) { announce('Son pod karşılaştırmadan çıkarılamaz.'); return; }
    const next = togglePod(compare, c.pod, pods);
    if (next === compare) { announce(CAP_ANNOUNCE); return; }
    pendingMore.current = menuPods === null && c.visibleOnlyByOn ? { pod: c.pod, sig: compareSig } : null;
    onCompareChange(next);
    setNote?.('');
    if (!c.on && next.length >= TRACE_METRICS_MAX_COMPARE) announce(CAP_ANNOUNCE);
  };
  const capBlocked = [...visible, ...overflow].some(c => c.disabled && c.title === CAP_REASON);
  const showNote = note !== '' && !(capBlocked && note === CAP_ANNOUNCE);
  const chipSize = compact ? 'xs' : undefined;
  const label = <span>Karşılaştır · aynı servisten ({compare.length}/{TRACE_METRICS_MAX_COMPARE})</span>;
  const siblingsChip = <Chip size="xs" active={siblingLines} onClick={onToggleSiblingLines}>Kardeş çizgileri</Chip>;
  const chips = (
    <div className="tpp-chips">
      {visible.map(c => (
        <Chip key={c.pod} size={chipSize} active={c.on} disabled={c.disabled} title={c.title} onClick={() => toggle(c)}>
          <ChipBody c={c} color={colors.get(c.label)} />
        </Chip>
      ))}
      {overflow.length > 0 && (
        <>
          <Chip ref={moreRef} size={chipSize} aria-haspopup="menu" aria-expanded={menuPods !== null} title={`Diğer ${overflow.length} pod`}
            onClick={() => setMenuPods(m => (m ? null : new Set(overflow.map(c => c.pod))))}>
            +{overflow.length}
          </Chip>
          <Popover anchorRef={moreRef} open={menuPods !== null} onClose={() => setMenuPods(null)} kind="menu" ariaLabel={`${p.service} için diğer pod'lar`} width={280}>
            {overflow.map(c => (
              <MenuItem key={c.pod} role="menuitemcheckbox" aria-checked={c.on} aria-disabled={c.disabled || undefined}
                title={c.title} onClick={() => toggle(c)}>
                <span className="mono">{c.label}</span> · {c.spans}{c.errors > 0 && <span className="cell-err"> · ⚠{c.errors}</span>}
                {c.note && <span className="cell-faint"> · {c.note}</span>}
              </MenuItem>
            ))}
          </Popover>
        </>
      )}
    </div>
  );
  const notes = (
    <>
      {capBlocked && <div className="tpp-note">{CAP_REASON}</div>}
      {showNote && <div className="tpp-note" data-panel-note="">{note}</div>}
    </>
  );
  if (compact) {
    return (
      <section className="tpp-cmp" aria-label="Karşılaştır">
        <div className="tpp-cmp-line">
          <span className="tpp-cmp-label">{label}</span>
          {chips}
          {siblingsChip}
        </div>
        {notes}
      </section>
    );
  }
  return (
    <section className="tpp-sec" aria-label="Karşılaştır">
      <div className="tpp-sec-title">
        {label}
        {siblingsChip}
      </div>
      {chips}
      {notes}
    </section>
  );
}

/** v0.10.968 — metriği ok olmayan pod: durum mesajı (hata ASLA boş değil).
 *  "Yeniden dene"/"Yükle" basılınca durum loading'e döner ve düğme DOM'dan
 *  kalkar; odak <body>'ye düşmesin diye iki dalın kökü AYNI düğüm (React
 *  yeniden kullanır, tabIndex -1) ve düğme kendini tutan odağı ona devreder. */
export function StateBlock({ state, pod, onRetry, height = 140 }: { state: PodMetricState; pod: string; onRetry: (pod: string) => void; height?: number }) {
  const boxRef = useRef<HTMLDivElement>(null);
  const msg = stateMessage(state);
  if (!msg) return null;
  if (msg.loading) {
    return (
      <div ref={boxRef} tabIndex={-1} className="tpp-chart" role="img" aria-label="Yükleniyor" aria-busy="true">
        <Skeleton height={height} />
        <Skeleton height={height} />
      </div>
    );
  }
  return (
    <div ref={boxRef} tabIndex={-1} className={msg.tone === 'err' ? 'tpp-msg is-err' : 'tpp-msg'} data-state={state.kind}>
      <div className="cell-strong">{msg.title}</div>
      {msg.sub && <div>{msg.sub}</div>}
      {(msg.settings || msg.action) && (
        <div className="tpp-actions">
          {msg.settings && <Link to="/settings/clusters">Ayarlar › Remote Cluster ↗</Link>}
          {msg.action && (
            <Button variant="secondary" size="sm" onClick={e => {
              if (document.activeElement === e.currentTarget) boxRef.current?.focus();
              onRetry(pod);
            }}>
              {msg.action === 'retry' ? 'Yeniden dene' : 'Yükle'}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

/** v0.10.968 — JVM heap + GC: yalnız familyOf(runtime) === 'jvm' (Thanos
 *  durumundan BAĞIMSIZ); JVM değilse tek satır not; runtime bilinmiyorsa hiç.
 *  Seri etiketi ve rengi çip / Bellek / CPU ile AYNI (suffixLabels +
 *  compareColors): bir pod dört grafikte de tek ad, tek renk.
 *
 *  v0.10.976'nın `collapsible`ı (kapalı açılır bölüm) v0.10.1096'te kalktı:
 *  `inline` — satır altı ayrıntıda heap ve GC, Bellek/CPU ile AYNI yatay
 *  ızgarada birer hücre (bölüm başlığı yok, yükseklik 168); JVM değilse hiç
 *  (not "Teknik ayrıntı"da, JvmNote). Odak görünümü bölümlü (dikey) kullanır. */
export function JvmSection({ model, p, compare, window: w, inline = false }: {
  model: TraceMetricsModel; p: TracePodInfo; compare: string[]; window: TraceMetricsWindowInfo; inline?: boolean;
}) {
  const themeTick = useThemeTick();
  const svcPods = useMemo(() => servicePods(model, p.service), [model, p.service]);
  const queryPods = useMemo(() => svcPods.map(x => x.pod), [svcPods]);
  const labels = useMemo(() => suffixLabels(svcPods), [svcPods]);
  const labelOf = useCallback((pod: string) => labels.get(pod) ?? shortPod(pod), [labels]);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- v0.10.968: themeTick yalnız yeniden çözme tetikleyicisi
  const colors = useMemo(() => compareColors(compare, labels), [compare, labels, themeTick]);
  const deploys = useMemo(() => [{ timeUnixNs: w.startNs, label: 'trace' }], [w.startNs]);
  const xRange = useMemo(() => ({ from: w.fromNs / 1e9, to: w.toNs / 1e9 }), [w.fromNs, w.toNs]);
  if (!p.runtime) return null;
  if (familyOf(p.runtime) !== 'jvm') return inline ? null : <JvmNote runtime={p.runtime} />;
  const panel = (
    <TraceJvmPanel service={p.service} pods={compare} queryPods={queryPods} runtime={p.runtime}
      from={w.fromNs} to={w.toNs} syncKey={`trace-metrics-${p.service}`} deploys={deploys} xRange={xRange}
      labelOf={labelOf} seriesColors={colors} chartHeight={inline ? DETAIL_CHART_H : undefined} />
  );
  if (inline) return panel;
  return (
    <section className="tpp-sec" aria-label="JVM · heap ve GC">
      <div className="tpp-sec-title"><span>JVM · heap ve GC</span><span>OTel jvm.* · ClickHouse</span></div>
      {panel}
    </section>
  );
}

/** v0.10.968 — JVM olmayan runtime'ın tek satır notu; runtime bilinmiyorsa hiç. */
function JvmNote({ runtime }: { runtime: string }) {
  if (!runtime || familyOf(runtime) === 'jvm') return null;
  return <div className="tpp-note">JVM paneli yok · runtime: {runtime}</div>;
}

function NowValue({ r }: { r: NowRow }) {
  return (
    <>
      {r.badge && <Badge tone={r.badge.tone}>{r.badge.t}</Badge>}
      <span className={r.tone === 'warn' ? 'cell-warn cell-strong' : r.tone === 'faint' ? 'cell-faint' : undefined}>{r.v}</span>
    </>
  );
}

/** v0.10.968 — "Şu an (trace anı değil) · HH:MM itibarıyla · trace N dk önce".
 *  "N dk önce" Date.now() ile useMemo İÇİNDE (render'da saat okunmaz). */
export function NowSection({ state, traceStartNs, columns = false }: { state: PodMetricState; traceStartNs: number; columns?: boolean }) {
  const data = state.kind === 'ok' ? state.data : null;
  const fetchedAtMs = data?.fetchedAtMs ?? 0;
  const head = useMemo(() => {
    const ago = agoText(Date.now(), traceStartNs / 1e6);
    return fetchedAtMs ? ` · ${hhmm(fetchedAtMs)} itibarıyla · trace ${ago}` : ` · trace ${ago}`;
  }, [fetchedAtMs, traceStartNs]);
  let body: ReactNode;
  if (state.kind === 'loading') body = <Skeleton height={60} />;
  else if (!data) {
    const n = nowNote(state);
    body = n ? <div className={n.err ? 'tpp-note is-err' : 'tpp-note'}>{n.t}</div> : null;
  } else {
    const { rows, note } = nowSection(data, traceStartNs);
    const kv = (rs: NowRow[]) => <KeyValue items={rs.map(r => ({ id: r.k, k: r.k, v: <NowValue r={r} /> }))} />;
    body = (
      <>
        {note && <div className="tpp-note">{note}</div>}
        {columns ? <div className="tpp-cols">{kv(rows.slice(0, 3))}{kv(rows.slice(3))}</div> : kv(rows)}
      </>
    );
  }
  return (
    <section className="tpp-sec" aria-label="Şu an">
      <div className="tpp-sec-title"><span>Şu an (trace anı değil){head}</span></div>
      {body}
    </section>
  );
}

// ── Panel ─────────────────────────────────────────────────────────────────

const HEAD_LEVEL: Record<ChartHeadline['level'], string> = { none: 'cell-strong', warn: 'cell-warn cell-strong', err: 'cell-err cell-strong' };

/** v0.10.1096 — tek satır grafik başlığı: "Bellek 9,03 GiB · limit 16 GiB".
 *  Renk yalnız limite göre sapmada; tam "Trace anında" cümlesi ipucunda. */
function ChartHead({ h }: { h: ChartHeadline }) {
  return (
    <span className="tpp-chart-head" title={h.title}>
      <span>{h.k}</span>{' '}
      <span className={HEAD_LEVEL[h.level]}>{h.value}</span>
      {h.limit && <span className="cell-faint"> · {h.limit}</span>}
    </span>
  );
}

/** v0.10.1096 — grafiklerin altındaki TEK satır özet (podFacts). Span'e giden
 *  segmentler LinkButton (Trace sekmesinde o span'i açar). */
function PodFactsRow({ model, p, state, onOpenSpan }: {
  model: TraceMetricsModel; p: TracePodInfo; state: PodMetricState; onOpenSpan: (id: string) => void;
}) {
  const selfName = useMemo(
    () => model.podSpans.get(p.pod)?.find(s => s.spanId === p.maxSelfSpanId)?.name ?? '',
    [model, p.pod, p.maxSelfSpanId]);
  const now = state.kind === 'ok' ? state.data.now : null;
  const segs = podFacts(p, { selfKnown: model.selfKnown, selfName, now, traceStartNs: model.traceStartNs });
  return (
    <ul className="tpp-facts" aria-label="Özet">
      {segs.map(s => {
        const tone = s.tone === 'err' ? 'cell-err' : s.tone === 'warn' ? 'cell-warn' : s.tone === 'faint' ? 'cell-faint' : undefined;
        let body: ReactNode;
        if (s.badge) body = <Badge tone={s.badge}>{s.t}</Badge>;
        else if (s.spanId) {
          const id = s.spanId;
          body = <LinkButton className={tone} title={s.title} onClick={() => onOpenSpan(id)}>{s.t}</LinkButton>;
        } else body = <span className={tone}>{s.t}</span>;
        return (
          <li key={s.id} data-fact={s.id} title={s.spanId ? undefined : s.title}>
            {s.label && <span className="cell-faint">{s.label} </span>}
            {body}
          </li>
        );
      })}
    </ul>
  );
}

/** v0.10.1096 — "Teknik ayrıntı": ayrıntı/sorun sayfalarının Sect deseni
 *  (ui/DisclosureButton, KAPALI başlar, kapalıyken gövde mount edilmez).
 *  Açıklık yerel durum — paylaşılan link bölüm durumunu taşımaz. */
function TechFold({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <section className="tpp-sec" aria-label="Teknik ayrıntı">
      <div className="tpp-sec-title">
        <DisclosureButton anatomy="row" expanded={open} onClick={() => setOpen(o => !o)}>Teknik ayrıntı</DisclosureButton>
      </div>
      {open && <div className="tpp-fold-body">{children}</div>}
    </section>
  );
}

/** v0.10.976 — satır altı kompakt ayrıntı; v0.10.1096 yerleşimi dosya
 *  başlığında. Bölge (aria-label "<pod> ayrıntısı") ve odak TracePodTable'da;
 *  burası içerik. */
export function TracePodPanel(props: TracePodPanelProps) {
  const {
    model, selected: p, compare, metrics, window: w, siblingLines,
    onCompareChange, onClose, onToggleFocus, onToggleSiblingLines, onShowSpans, onOpenSpan, onRetry, announce,
  } = props;
  const state = metrics(p.pod);
  const sub = subLine(p, state);
  const { note, setNote, say } = usePanelNote(p.pod, announce);
  const builds = usePodChartBuilds({ model, selected: p, compare, metrics, window: w, siblingLines });
  const ok = state.kind === 'ok';
  return (
    <div className="tpp tpp-detail" data-pod={p.pod}>
      <div className="tpp-head">
        <span className="tpp-name"><MiddleEllipsis text={p.pod} /></span>
        <CopyPodButton pod={p.pod} announce={say} />
        <Button variant="ghost" size="xs" leftIcon={<Maximize2 size={12} aria-hidden="true" />} title="Pod odak görünümünde aç"
          onClick={onToggleFocus}>Odak görünümü</Button>
        <PodPageLink p={p} state={state} window={w} />
        <Button variant="ghost" size="xs" onClick={() => onShowSpans(p.pod)}>Span'ları Trace'te göster ({p.spans})</Button>
        <IconButton aria-label="Ayrıntıyı kapat" tooltip="Kapat (Esc)" icon={<X size={14} />} onClick={onClose} />
      </div>
      <div className="tpp-sub">
        {sub.map((s, i) => <span key={i}>{i > 0 ? '· ' : ''}{s}</span>)}
        <PodBadges p={p} />
      </div>
      {ok && (
        <div className="tpp-chart-bar">
          <CompareSection model={model} p={p} compare={compare} metrics={metrics} siblingLines={siblingLines}
            onCompareChange={onCompareChange} onToggleSiblingLines={onToggleSiblingLines} announce={say}
            note={note} setNote={setNote} compact />
        </div>
      )}
      {/* v0.10.916 (operator-reported) — bellek önce, CPU sonra; ikisi de zeroBase. */}
      <div className="tpp-chart-row">
        {state.kind === 'ok' ? (
          <>
            <PodChart build={builds.mem} height={DETAIL_CHART_H} syncKey={builds.syncKey} xRange={builds.xRange}
              head={<ChartHead h={chartHeadline('mem', state.data)} />} legendLimit={false} />
            <PodChart build={builds.cpu} height={DETAIL_CHART_H} syncKey={builds.syncKey} xRange={builds.xRange}
              head={<ChartHead h={chartHeadline('cpu', state.data)} />} legendLimit={false} />
          </>
        ) : (
          <StateBlock state={state} pod={p.pod} onRetry={onRetry} height={80} />
        )}
        <JvmSection model={model} p={p} compare={compare} window={w} inline />
      </div>
      <PodFactsRow model={model} p={p} state={state} onOpenSpan={onOpenSpan} />
      <TechFold>
        <div className="tpp-kv3">
          <TraceFacts model={model} p={p} onOpenSpan={onOpenSpan} />
          {state.kind === 'ok' && <MomentSection model={model} p={p} data={state.data} metrics={metrics} />}
          <NowSection state={state} traceStartNs={model.traceStartNs} />
        </div>
        {ok && <p className="tpp-caption">{podChartsCaption(builds, w, p.service)}</p>}
        <JvmNote runtime={p.runtime} />
      </TechFold>
    </div>
  );
}
