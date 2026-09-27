// TracePodTable — v0.10.968 — Trace › Metrics'in servise göre gruplu pod tablosu.
//
// v0.10.968 (operatör onayı 2026-09-27, "3 onay") — çip duvarının yerine TEK
// kayıt listesi (tablo standardı: useDataTable / DataTable primitifleri; renk
// yalnız sapmada). Gruplar, "N pod daha" ve "Pod bilgisi olmayan span'lar"
// satırları aynı tbody'de TAM GENİŞLİK <tr> (ServicePodsTable emsali); veri
// hücreleri görünür kolon dizisinden çizilir, CPU/Bellek gizlenince hizası
// kaymaz.
//
// NEDEN HİÇBİR KOLON SIRALANMAZ (sortValue yok — bilinçli, gerekçe kayıtlı):
//   1. Onaylı ürün sırası SABİT: "Sıra: trace ilgisi" (hata kaynağı servis →
//      hatalı pod → kritik yol payı → öz süre → span). Başlık tıkıyla başka
//      bir sıra, onayın taşıdığı "önce neye bakmalı" cevabını bozar.
//   2. Metrikler Thanos'tan GEÇ gelir: CPU'ya göre sıralı bir tablo her dilim
//      cevabında satırları zıplatırdı (operatörün tıklamak üzere olduğu satır
//      kayar).
//   3. useDataTable'ın URL sıralama yazıcısı router'ın `prev`ini kopyalar;
//      bu sayfada `prev` bayat bir alt küme (Trace.tsx `span`/`tab`ı ham
//      history.replaceState ile yazar) — yazım `tab=metrics`i silerdi.
// Sıralanabilir trace kolonları operatör kararına ertelendi (docs/DECISIONS.md).
//
// Erişilebilirlik: role=treegrid; satırlar aria-level / aria-expanded /
// aria-selected / aria-setsize / aria-posinset taşır. Gezici tabindex (tek
// Tab durağı: imleç satırı, yoksa seçili satır, yoksa ilk satır); tbody'de
// TEK devredilmiş onKeyDown (karar saf treeNav'da) ve TEK devredilmiş tık.
// Satır ⋯ menüsü ui/Popover (tek örnek, tablonun yanında — satırın içinde
// değil). >100 satır: `.cv-row`. VirtualTable DEĞİL: derin linkle seçilen
// satır bağlı kalmalı.
//
// Tıklanabilir satır işareti `.tpm-click` (el imleci + hover), `data-row-action`
// DEĞİL: satırın işleyicisi tbody'ye DEVREDİLMİŞ (tek onClick/onKeyDown) ve
// rowActivation role=button basar — treegrid satırı role=row kalmalı. Satır
// başına onClick de yazılmaz (a11y.rowSites kapısı; 90 satır × kapanış).
//
// v0.10.968 — odak dönüşü: panel kapanınca / odak görünümünden çıkınca kabuk
// `focusRow` ister ({key, n} — aynı anahtar ikinci kez de istenebilsin diye
// sayaçlı); odak GERÇEKTEN kaybolmuşsa (activeElement body) o satıra, satır
// artık yoksa (seçili olduğu için görünen daraltılmış pod) grubuna, o da
// yoksa ilk satıra konur — yeniden bağlanan tabloda satır görünüre kayar.
// "N pod daha" fareyle tıklandığında da odak ilk açılan pod'a geçer
// (tıklanan satır DOM'dan kalkar). CPU/Bellek sayısal: sağa yaslı.
//
// v0.10.976 — SATIR ALTI AYRINTI (operatör: "inline daha iyi olur",
// 2026-09-27; prod v0.10.968'de 400 px yan panel tabloyu sıkıştırıp
// CPU/Bellek hücrelerini "%…"ya kırpıyordu). Seçili pod'un satırının HEMEN
// altında tam genişlik tek `<tr>` (tek `<td colSpan>`, ev kuralı
// `td.row-detail`); içeriği kabuk `detail` ile verir (TracePodPanel).
//   • Treegrid satırı DEĞİL: `role="presentation"` (td de), `data-ri` yok →
//     treeNav (rows üzerinden) ve tbody'nin devredilmiş tık/tuş işleyicisi
//     onu görmez; ok tuşları atlar. aria-hidden DEĞİL: ekran okuyucu içeriği
//     "<pod> ayrıntısı" bölgesi olarak okur.
//   • Odak: satırdan tık / Enter / Boşluk ile AÇILINCA bölgeye (tabIndex -1,
//     grup) geçer; soğuk derin link (mount'ta seçili) odak çalmaz. Esc / ×
//     kabuğun focusRow isteğiyle satıra döner (ayrıntı sökülünce odak
//     <body>'ye düşer, mevcut kural yakalar). Tab: satır → ayrıntı denetimleri
//     → tablodan çıkar (treegrid tek Tab durağı; sonraki satır ↓ ile).
//   • Satır ve ayrıntı tbody'nin AYNI dizisinde (flatMap; Fragment DEĞİL):
//     aynı anahtarla tr ⇄ Fragment geçişi React'ta satırı söküp yeniden
//     kuruyordu, seçili satırdan Enter / Boşluk / tık ile kapanışta odak
//     <body>'ye düşüyordu. Ayrıntı satırının anahtarı SABİT ("detail"): seçim
//     başka pod'a taşınınca React düğümü TAŞIR, yeniden bağlamaz — Karşılaştır
//     "+N" menüsü / odak devri ve JVM açık-kapalı durumu yaşar. Taşınan düğüm
//     odağı yitirir (tarayıcı ve jsdom); kabuk çip yolunda `d:<pod>` focusRow
//     isteği yollar, bölge yeniden odaklanır (çocuk etkisi "+N"ye almışsa
//     çalınmaz).
//   • Kaydırma YALNIZ tablonun kendi kabında (.tpm-scroll) ve yalnız satırdan
//     AÇILAN pod ya da ilk bağlanmada AÇIK derin link (mpod dolu) için; alt
//     kenar taşması kadar, seçili satır yapışkan başlığın altında görünür
//     kalır; prefers-reduced-motion → anlık. Varsayılan seçim, grup yeniden
//     açılışı, süzgeç geri-gösterimi ve kapanış KAYDIRMAZ. Element.scrollIntoView
//     KULLANILMAZ: her kaydırılabilir atayı (#content = sayfa) da kaydırırdı.
//   • CPU/Bellek kolonlarının küçültme tabanı = beyan genişliği (116 px):
//     sığdırma onları DEFAULT_MIN'e çekemez (tracePodTable.cols.test.ts).
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableState, MiddleEllipsis,
  type CellTone, type ColumnModel,
} from '@/components/ui/DataTable';
import { rowErrors, TRACE_POD_COLS } from './tracePodCols';
import { Badge, IconButton, MenuItem, Popover, PopoverLinkItem, Tooltip } from '@/components/ui';
import { copyToClipboard } from '@/lib/clipboard';
import { serviceHref } from '@/lib/serviceHref';
import { TraceMetricCell } from './TraceMetricCell';
import { fmtDurNs } from './traceMetricsFmt';
import {
  hottestPodState, metricCellTone, moreRowNotes, traceMetricsPodHref,
  type MetricKind, type TraceMetricsRow,
} from './traceMetrics';
import { treeNav } from './treeNav';
import type { PodMetricSeries, PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo } from './traceMetricsModel';

// v0.10.976 — kolon tanımları saf modülde (tracePodCols.ts): cpu/mem tabanı
// 116 px orada çivili (tracePodTable.cols.test.ts).
const COLS = TRACE_POD_COLS;
const METRIC_HIDDEN: ColumnModel = { v: 1, order: COLS.map(c => c.id), hidden: ['cpu', 'mem'], sig: 'trace-metrics-off' };

const TONE_CLASS: Record<CellTone, string> = { err: 'cell-err', warn: 'cell-warn', muted: 'cell-muted', faint: 'cell-faint' };
const cls = (...xs: (string | false | null | undefined)[]) => xs.filter(Boolean).join(' ') || undefined;

/** v0.10.968 — kritik yol yüzdesi; sıfırdan büyük ama %0,5 altı "<%1" (yuvarlama "%0" yalanı söylemesin). */
function critPct(share: number): string {
  if (share > 0 && share < 0.005) return '<%1';
  return `%${Math.round(share * 100)}`;
}

function headTip(id: string, stepSec: number | null): string | null {
  switch (id) {
    case 'self': return "Pod'daki span'ların en büyük öz süresi (alt çağrılar hariç)";
    case 'crit': return "Trace süresinin kritik yol üzerinde bu pod'da geçen payı; pod'lar ve 'Pod bilgisi olmayan span'lar' satırı birlikte %100";
    case 'cpu': return "Trace'i kesen örneklerin en büyüğü (±1 adım). Renk: şu anki limite oran; limit tanımsızsa mutlak değer. Thanos rate[5m]"
      + (stepSec ? `, ${stepSec} sn adım.` : '.');
    case 'mem': return "working_set, trace'i kesen örneklerin en büyüğü (±1 adım). Renk: şu anki limite oran.";
    default: return null;
  }
}

/** v0.10.968 — pod adı: soluk hash parçası (replicaset), ortadan kırpma
 *  (MiddleEllipsis baş kısmı kırpar, hash+ek hiç kırpılmaz). Servis kipinde
 *  baş (deployment adı) grup başlığında zaten yazılı → yalnız hash-ek;
 *  Düz kipte tam ad. Birincil olmayan cluster değeri önek olarak. */
function PodName({ p, flat, primaryCluster }: { p: TracePodInfo; flat: boolean; primaryCluster: string }) {
  const pre = p.clusterValue && p.clusterValue !== primaryCluster ? `${p.clusterValue} · ` : '';
  const { head, rs, tail } = p.nameParts;
  if (!rs) return <MiddleEllipsis text={pre + p.pod} title={p.pod} className="tpm-pod-name" />;
  const lead = flat ? `${pre}${head}-` : pre;
  const end = `${rs}-${tail}`;
  return (
    <span className="tpm-pod" style={{ '--tpm-pod-tail-w': `${end.length}ch` } as CSSProperties} title={p.pod}>
      {lead && <MiddleEllipsis text={lead} tail={0} title={p.pod} className="tpm-pod-head" />}
      <span className="tpm-pod-rs">{rs}</span>-{tail}
    </span>
  );
}

export interface TracePodTableProps {
  model: TraceMetricsModel;
  rows: TraceMetricsRow[];
  selected: string;
  metrics: (pod: string) => PodMetricState;
  hideMetrics: boolean;
  window: TraceMetricsWindowInfo;
  grouping: 'service' | 'flat';
  onSelectPod: (pod: string) => void;
  onToggleGroup: (service: string, open: boolean) => void;
  onExpandMore: (service: string) => void;
  onToggleNoPod: (open: boolean) => void;
  onShowPodSpans: (pod: string) => void;
  onRetryService: (service: string) => void;
  onClearFilters: () => void;
  announce: (msg: string) => void;
  /** v0.10.968 — kabuğun odak dönüşü isteği (`p:<pod>`; n her istekte artar). */
  focusRow?: { key: string; n: number } | null;
  /** v0.10.976 — seçili pod'un satır altı ayrıntısı (TracePodPanel); null = kapalı.
   *  Satır görünür değilse (grubu kapalı, süzgeç dışı) ayrıntı da satırıyla gizlenir. */
  detail?: ReactNode;
  /** v0.10.976 — seçim URL'den AÇIKÇA geldi (mpod dolu); varsayılan seçim false.
   *  Yalnız ilk bağlanmadaki kaydırma kararında okunur (derin link görünsün). */
  detailFromUrl?: boolean;
}

export function TracePodTable(props: TracePodTableProps) {
  const {
    model, rows, selected, metrics, hideMetrics, window: w, grouping,
    onSelectPod, onToggleGroup, onExpandMore, onToggleNoPod, onShowPodSpans, onRetryService, onClearFilters, announce,
    focusRow, detail = null, detailFromUrl = false,
  } = props;
  const columnModel = useMemo(() => ({ value: hideMetrics ? METRIC_HIDDEN : null }), [hideMetrics]);
  const dt = useDataTable<TraceMetricsRow>({
    storageKey: 'trace-metrics-pods', columns: COLS, rows, persistSort: false, columnModel,
  });
  const visible = dt.visibleColumns;
  const many = rows.length > 100;
  const flat = grouping === 'flat';

  // ── gezici tabindex + klavye ──────────────────────────────────────────
  const tbodyRef = useRef<HTMLTableSectionElement>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const pendingFocus = useRef<string | null>(null);
  const selIdx = rows.findIndex(r => r.kind === 'pod' && r.pod.pod === selected);
  const curIdx = cursor ? rows.findIndex(r => r.key === cursor) : -1;
  const stopIdx = curIdx >= 0 ? curIdx : selIdx >= 0 ? selIdx : 0;

  const rowEl = (i: number) => tbodyRef.current?.querySelector<HTMLElement>(`tr[data-ri="${i}"]`) ?? null;
  useEffect(() => {
    const k = pendingFocus.current;
    if (!k) return;
    const i = rows.findIndex(r => r.key === k);
    if (i < 0) return;
    pendingFocus.current = null;
    rowEl(i)?.focus();
  }, [rows]);

  // v0.10.976 — satır altı ayrıntı: bölge (odaklanabilir grup) ve açılış
  // isteği. `pendingDetail` yalnız satırdan AÇILAN pod için dolar; soğuk derin
  // linkte boş kalır (odak çalınmaz). `firstDetail`: ilk çizilen ayrıntı —
  // derin link kaydırması yalnız o anda. Bildirimler focusRow etkisinden ÖNCE:
  // o etki `detailPod`a bağlı (d:<pod> isteği).
  const detailRef = useRef<HTMLElement>(null);
  const pendingDetail = useRef<string | null>(null);
  const firstDetail = useRef(true);
  const detailPod = detail !== null && rows.some(r => r.kind === 'pod' && r.pod.pod === selected) ? selected : '';

  // v0.10.968 — kabuğun odak dönüşü (panel kapandı / odak görünümünden çıkıldı).
  const lastFocusN = useRef(0);
  const focusN = focusRow?.n ?? 0;
  const focusKey = focusRow?.key ?? '';
  useEffect(() => {
    if (!focusN || lastFocusN.current === focusN) return;
    const a = document.activeElement;
    if (a && a !== document.body) { lastFocusN.current = focusN; return; } // odak başka yerde: çalma
    lastFocusN.current = focusN;
    const pod = /^[pd]:/.test(focusKey) ? focusKey.slice(2) : '';
    // v0.10.976 — `d:<pod>`: ayrıntı bölgesi (çip yolu seçimi taşıdı; taşınan
    // düğümde odak düşmüştü). Bölge çizilmemişse satır kuralına düşer.
    if (focusKey.startsWith('d:') && pod === detailPod && detailRef.current) {
      detailRef.current.focus({ preventScroll: true });
      return;
    }
    const svc = pod ? model.byPod.get(pod)?.service : undefined;
    let i = rows.findIndex(r => r.key === `p:${pod}`);
    if (i < 0 && svc !== undefined) i = rows.findIndex(r => r.key === `g:${svc}`);
    if (i < 0) i = rows.length ? 0 : -1;
    if (i < 0) return;
    setCursor(rows[i].key);
    tbodyRef.current?.querySelector<HTMLElement>(`tr[data-ri="${i}"]`)?.focus();
  }, [focusN, focusKey, rows, model, detailPod]);

  // v0.10.976 — açılış kaydırması + odak. Kaydırma YALNIZ tablonun kendi
  // kabında (.tpm-scroll) ve yalnız (a) satırdan AÇILAN pod (pendingDetail) ya
  // da (b) ilk bağlanmada AÇIK derin link (mpod dolu) için. Varsayılan seçim,
  // grup yeniden açılışı ve süzgeç geri-gösterimi kaydırmaz.
  // Element.scrollIntoView KULLANILMAZ: her kaydırılabilir atayı (#content =
  // sayfa kaydırıcısı) da kaydırır; sekmeye girişte özet şeridi / araç çubuğu
  // görünümden çıkıyordu. Miktar: alt kenar taşması kadar; üst sınır seçili
  // satırın yapışkan başlığın altında görünür kalması (ayrıntı kaptan uzunsa).
  useEffect(() => {
    const el = detailRef.current;
    if (!detailPod || !el) return;
    const opened = pendingDetail.current === detailPod;
    const deepLink = firstDetail.current && detailFromUrl;
    firstDetail.current = false;
    if (!opened && !deepLink) return;
    const wrap = el.closest<HTMLElement>('.tpm-scroll');
    if (wrap) {
      const reduce = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      const w = wrap.getBoundingClientRect();
      const headH = wrap.querySelector('thead')?.getBoundingClientRect().height ?? 0;
      const rowTop = (el.closest('tr')?.previousElementSibling ?? el).getBoundingClientRect().top;
      const d = Math.max(0, Math.min(el.getBoundingClientRect().bottom - w.bottom, rowTop - (w.top + headH)));
      if (d > 0) {
        if (typeof wrap.scrollBy === 'function') wrap.scrollBy({ top: d, behavior: reduce ? 'auto' : 'smooth' });
        else wrap.scrollTop += d; // jsdom
      }
    }
    if (opened) {
      pendingDetail.current = null;
      el.focus({ preventScroll: true });
    }
  }, [detailPod, detailFromUrl]);

  const activate = useCallback((i: number, fromKeyboard: boolean) => {
    const r = rows[i];
    if (!r) return;
    switch (r.kind) {
      case 'pod':
        setCursor(r.key);
        // v0.10.976 — açılış / başka pod'a geçiş: odak yeni ayrıntıya; seçili
        // pod'a yeniden basmak kabukta KAPATIR (odak satırda kalır).
        pendingDetail.current = r.pod.pod === selected ? null : r.pod.pod;
        onSelectPod(r.pod.pod);
        break;
      case 'group': setCursor(r.key); onToggleGroup(r.group.service, !r.open); break;
      case 'nopod': setCursor(r.key); onToggleNoPod(!r.open); break;
      case 'more': {
        const first = r.hidden[0];
        onExpandMore(r.service);
        if (first) {
          setCursor(`p:${first.pod}`);
          // v0.10.968 — fareyle tıklanan satır da odağı tutar (tabIndex'li <tr>)
          // ve açılınca DOM'dan kalkar: odak ilk açılan pod'a taşınır.
          const self = tbodyRef.current?.querySelector(`tr[data-ri="${i}"]`);
          if (fromKeyboard || (!!self && self === document.activeElement)) pendingFocus.current = `p:${first.pod}`;
        }
        break;
      }
      default: break;
    }
  }, [rows, selected, onSelectPod, onToggleGroup, onToggleNoPod, onExpandMore]);

  const onKeyDown = (e: KeyboardEvent<HTMLTableSectionElement>) => {
    const tr = e.target instanceof HTMLElement ? e.target.closest<HTMLElement>('tr[data-ri]') : null;
    if (!tr || tr !== e.target) return; // satır içi düğme kendi tuşunu korur
    const i = Number(tr.dataset.ri);
    const res = treeNav(rows, i, e.key);
    if (!res) return;
    e.preventDefault();
    if (res.kind === 'focus') {
      const r = rows[res.index];
      if (r) { setCursor(r.key); rowEl(res.index)?.focus(); }
    } else if (res.kind === 'toggle') {
      const r = rows[res.index];
      if (r.kind === 'group') onToggleGroup(r.group.service, res.open);
      else if (r.kind === 'nopod') onToggleNoPod(res.open);
      setCursor(r.key);
    } else {
      activate(res.index, true);
    }
  };

  const onClick = (e: MouseEvent<HTMLTableSectionElement>) => {
    const t = e.target instanceof Element ? e.target : null;
    if (!t || t.closest('button, a, input, [role="menu"], [role="dialog"]')) return;
    const tr = t.closest<HTMLElement>('tr[data-ri]');
    if (!tr) return;
    activate(Number(tr.dataset.ri), false);
  };

  // ── satır ⋯ menüsü (tek Popover örneği) ──────────────────────────────
  const [menu, setMenu] = useState<string | null>(null);
  const anchorRef = useRef<HTMLElement | null>(null);
  const menuRow = menu ? rows.find(r => r.key === menu) : undefined;
  const closeMenu = useCallback(() => setMenu(null), []);
  const menuButton = (r: TraceMetricsRow, label: string, stop: boolean) => (
    <IconButton size="xs" icon="⋯" aria-label={label} aria-haspopup="menu" aria-expanded={menu === r.key}
      tabIndex={stop ? 0 : -1}
      onClick={e => { anchorRef.current = e.currentTarget; setMenu(m => (m === r.key ? null : r.key)); }} />
  );

  const range = { fromNs: w.fromNs, toNs: w.toNs };
  const clusterOf = (st: PodMetricState): string =>
    st.kind === 'ok' ? st.data.cluster.name : st.kind === 'no_samples' || st.kind === 'ambiguous' ? st.cluster.name : '';
  const nsOf = (p: TracePodInfo, st: PodMetricState) => (st.kind === 'ok' ? st.data.namespace : p.namespace);

  const markerAt = useCallback((d: PodMetricSeries) => (d.stepSec > 0
    ? ((model.traceStartNs + model.traceEndNs) / 2 / 1e9 - d.startSec) / d.stepSec
    : undefined), [model.traceStartNs, model.traceEndNs]);

  // ── hücreler ──────────────────────────────────────────────────────────
  const metricTd = (id: MetricKind, st: PodMetricState | null, key: string) => {
    const { tone, strong } = metricCellTone(st, id);
    return (
      <td key={key} className={cls('tpm-mtd', tone && TONE_CLASS[tone], strong && 'tpm-strong')}>
        <TraceMetricCell state={st} which={id} markerAt={markerAt} />
      </td>
    );
  };
  const noPodMetricTd = (key: string) => (
    <td key={key} className="tpm-mtd cell-faint">
      <Tooltip content="Bu span'larda k8s.pod.name yok"><span className="tpm-mcell">—</span></Tooltip>
    </td>
  );
  const numTd = (r: TraceMetricsRow, id: string, content: ReactNode, faint = false) => {
    const cp = dt.cellProps(r, id);
    return <td key={id} className={cls(cp.className, faint && 'cell-faint')}>{content}</td>;
  };
  const selfTd = (r: TraceMetricsRow, ns: number) => (model.selfKnown
    ? numTd(r, 'self', fmtDurNs(ns))
    : numTd(r, 'self', '—', true));
  const critTd = (r: TraceMetricsRow, share: number) => {
    if (!(share > 0)) return numTd(r, 'crit', '—', true);
    const bw = Math.max(2, Math.round(Math.min(1, share) * 36));
    return numTd(r, 'crit', (
      <span className="tpm-crit">
        <span>{critPct(share)}</span>
        <span className="tpm-bar" aria-hidden="true"><span className="tpm-bar-fill" style={{ width: bw }} /></span>
      </span>
    ));
  };

  const cellsFor = (r: TraceMetricsRow, stop: boolean): ReactNode[] => visible.map(c => {
    switch (c.id) {
      case 'chev': {
        const open = r.kind === 'group' || r.kind === 'nopod' ? r.open : null;
        return <td key="chev" className="tpm-chev">{open !== null && <span className="tpm-caret" aria-hidden="true">{open ? '▾' : '▸'}</span>}</td>;
      }
      case 'pod': {
        if (r.kind === 'pod') {
          const p = r.pod;
          return (
            <td key="pod" {...dt.cellProps(r, 'pod', p.pod)}>
              <span className={cls('tpm-podcell', r.level === 2 && 'is-l2')}>
                <PodName p={p} flat={flat} primaryCluster={model.primaryCluster} />
                {p.rootCause && <Badge tone="danger" className="tpm-badge">kök hata</Badge>}
                {p.entry && <Badge tone="neutral" className="tpm-badge">giriş</Badge>}
              </span>
            </td>
          );
        }
        if (r.kind === 'group') {
          const n = r.group.pods.length;
          return (
            <td key="pod" {...dt.cellProps(r, 'pod', r.group.service)}>
              <span className="tpm-podcell">
                <span className="tpm-gname">{r.group.service || '(servissiz)'}</span>
                <span className="tpm-gcount">{r.filtered ? `${r.pods.length}/${n} pod` : `${n} pod`}</span>
              </span>
            </td>
          );
        }
        if (r.kind === 'nopod') {
          return (
            <td key="pod" className="tpm-nopod-label">
              Pod bilgisi olmayan span'lar · {r.summary.spans} span · {r.summary.services.length} servis
            </td>
          );
        }
        if (r.kind === 'nopod-svc') {
          return <td key="pod" className="tpm-nopod-label"><span className="tpm-podcell is-l2">{r.svc.service || '(servissiz)'}</span></td>;
        }
        return <td key="pod" />;
      }
      case 'spans': {
        const v = r.kind === 'pod' ? r.pod.spans : r.kind === 'group' ? r.pods.reduce((a, p) => a + p.spans, 0)
          : r.kind === 'nopod' ? r.summary.spans : r.kind === 'nopod-svc' ? r.svc.spans : null;
        return numTd(r, 'spans', v ?? '');
      }
      case 'errors': return numTd(r, 'errors', rowErrors(r) ?? '');
      case 'self': {
        const v = r.kind === 'pod' ? r.pod.maxSelfNs : r.kind === 'group' ? r.pods.reduce((a, p) => Math.max(a, p.maxSelfNs), 0)
          : r.kind === 'nopod' ? r.summary.maxSelfNs : r.kind === 'nopod-svc' ? r.svc.maxSelfNs : 0;
        return selfTd(r, v);
      }
      case 'crit': {
        const v = r.kind === 'pod' ? r.pod.critShare : r.kind === 'group' ? r.pods.reduce((a, p) => a + p.critShare, 0)
          : r.kind === 'nopod' ? r.summary.critShare : r.kind === 'nopod-svc' ? r.svc.critShare : 0;
        return critTd(r, v);
      }
      case 'cpu': case 'mem': {
        const which = c.id;
        if (r.kind === 'pod') return metricTd(which, metrics(r.pod.pod), which);
        if (r.kind === 'group') return metricTd(which, hottestPodState(r.pods.map(p => p.pod), metrics, which), which);
        return noPodMetricTd(which);
      }
      case 'actions': {
        const a = dt.cellProps(r, 'actions');
        if (r.kind === 'pod') return <td key="actions" {...a}>{menuButton(r, `${r.pod.pod} için eylemler`, stop)}</td>;
        if (r.kind === 'group') return <td key="actions" {...a}>{menuButton(r, `${r.group.service} için eylemler`, stop)}</td>;
        return <td key="actions" {...a} />;
      }
      default: return <td key={c.id} />;
    }
  });

  const renderRow = (r: TraceMetricsRow, i: number): ReactNode[] => {
    const stop = i === stopIdx;
    const sel = r.kind === 'pod' && r.pod.pod === selected;
    const common = {
      'data-ri': i,
      'data-key': r.key,
      role: 'row',
      'aria-level': r.level,
      'aria-setsize': r.setsize,
      'aria-posinset': r.posinset,
      tabIndex: stop ? 0 : -1,
    } as const;
    if (r.kind === 'more') {
      const notes = moreRowNotes(r.hidden, metrics);
      return [
        <tr key={r.key} {...common} className={cls('tpm-more', 'tpm-click', many && 'cv-row')}>
          {visible[0]?.id === 'chev' && <td className="tpm-chev" />}
          <td colSpan={Math.max(1, visible.length - (visible[0]?.id === 'chev' ? 1 : 0))} className="tpm-more-cell">
            <span className="tpm-podcell is-l2">
              <span>{r.hidden.length} pod daha (işaretsiz)</span>
              {notes.map(n => (
                <span key={n.text} className={cls('tpm-note', n.tone === 'err' ? 'cell-err tpm-strong' : n.tone === 'warn' ? 'cell-warn' : 'cell-faint')}>
                  · {n.text}
                </span>
              ))}
            </span>
          </td>
        </tr>,
      ];
    }
    const expandable = r.kind === 'group' || r.kind === 'nopod';
    const tr = (
      <tr key={r.key} {...common}
        aria-expanded={expandable ? r.open : undefined}
        aria-selected={r.kind === 'pod' ? sel : undefined}
        className={cls(
          r.kind !== 'nopod-svc' && 'tpm-click',
          r.kind === 'group' && 'tpm-group',
          r.kind === 'nopod' && 'tpm-nopod',
          r.kind === 'nopod-svc' && 'tpm-nopod-svc',
          sel && 'row-selected',
          many && 'cv-row',
        )}>
        {cellsFor(r, stop)}
      </tr>
    );
    if (!sel || detail === null || r.kind !== 'pod') return [tr];
    // v0.10.976 — satır ve ayrıntı AYNI dizide (Fragment DEĞİL): aynı anahtarla
    // tr ⇄ Fragment geçişi React'ta satırı yeniden bağlar ve odak <body>'ye
    // düşerdi. Ayrıntı satırı: treegrid satırı değil (presentation, data-ri
    // yok), cv-row DEĞİL (grafik ölçümü kırpılmasın); hücre ev kuralı
    // td.row-detail; bölge "<pod> ayrıntısı" odaklanabilir grup. Anahtar SABİT
    // ("detail", satır anahtarlarıyla çakışmaz: p:/g:/m:/n:): seçim taşınınca
    // düğüm taşınır, alt ağaç (TracePodPanel durumu) yaşar.
    return [
      tr,
      <tr key="detail" role="presentation" className="tpm-detail-row" data-detail-for={r.pod.pod}>
        <td role="presentation" colSpan={visible.length} className="row-detail tpm-detail-cell">
          <section ref={detailRef} tabIndex={-1} className="tpm-detail" aria-label={`${r.pod.pod} ayrıntısı`}>
            {detail}
          </section>
        </td>
      </tr>,
    ];
  };

  // Menü içeriği (açık satıra göre).
  let menuBody: ReactNode = null;
  let menuLabel = '';
  if (menuRow?.kind === 'pod') {
    const p = menuRow.pod;
    const st = metrics(p.pod);
    const cluster = clusterOf(st);
    menuLabel = `${p.pod} için eylemler`;
    menuBody = (
      <>
        {cluster && (
          <PopoverLinkItem onSelect={closeMenu}
            to={traceMetricsPodHref({ pod: p.pod, cluster, namespace: nsOf(p, st), service: p.service },
              { from: w.fromNs, to: w.toNs, startNs: w.startNs })}>
            Pod sayfasında aç ↗
          </PopoverLinkItem>
        )}
        <MenuItem onClick={() => { closeMenu(); onShowPodSpans(p.pod); }}>Span'ları Trace'te göster ({p.spans})</MenuItem>
        <MenuItem onClick={() => {
          closeMenu();
          void copyToClipboard(p.pod).then(ok => announce(ok ? `Pod adı kopyalandı: ${p.pod}` : `Pod adı kopyalanamadı: ${p.pod}`));
        }}>Pod adını kopyala</MenuItem>
      </>
    );
  } else if (menuRow?.kind === 'group') {
    const svc = menuRow.group.service;
    const anyErr = menuRow.group.pods.some(p => metrics(p.pod).kind === 'error');
    menuLabel = `${svc} için eylemler`;
    menuBody = (
      <>
        <PopoverLinkItem onSelect={closeMenu} to={serviceHref(svc, { range })}>Servis sayfasına git</PopoverLinkItem>
        <PopoverLinkItem onSelect={closeMenu} to={serviceHref(svc, { tab: 'infra', range })}>Deployment trendini aç ↗</PopoverLinkItem>
        {anyErr && (
          <MenuItem onClick={() => { closeMenu(); onRetryService(svc); }}>Bu servisin metriklerini yeniden dene</MenuItem>
        )}
      </>
    );
  }

  return (
    <div className="table-wrap is-scroll tpm-scroll">
      <table {...dt.tableProps} role="treegrid" aria-label="Bu trace'teki pod'lar">
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} renderLabel={c => {
          const tip = headTip(c.id, w.stepSec);
          const label = c.id === 'cpu' || c.id === 'mem'
            ? <>{c.label} <span className="tpm-th-note">(trace anı)</span></>
            : c.label;
          return tip ? <Tooltip content={tip}><span className="tpm-th">{label}</span></Tooltip> : label;
        }} />
        <tbody ref={tbodyRef} onKeyDown={onKeyDown} onClick={onClick}>
          {rows.length === 0
            ? <DataTableState dt={dt} kind="no-match" onClearFilters={onClearFilters} />
            : rows.flatMap(renderRow)}
        </tbody>
      </table>
      {/* Satır değişince yüzey yeniden bağlanır: yerleşim ve odak dönüşü yeni çapaya. */}
      <Popover key={menu ?? ''} anchorRef={anchorRef} open={!!menuBody} onClose={closeMenu} kind="menu" ariaLabel={menuLabel} width={232}>
        {menuBody}
      </Popover>
    </div>
  );
}
