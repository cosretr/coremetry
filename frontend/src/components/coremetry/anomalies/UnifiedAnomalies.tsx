// UnifiedAnomalies — /anomalies TEK liste (v0.10.2027; operatör: "Anomalies
// hâlâ karışık", "tekilleştirme çalışmadı"). Mockup anomaly-unified.html
// "Liste: tek sayfa": Başlangıç · Servis · Ne oldu · Sinyaller · Durum.
// Satır = olay (ML grubu + aynı servis/aynı zamandaki eski dedektör olayları).
// Süren olaylar üstte; bitenler kapalı "Bitenler (N)" bölümünde.
//
// v0.10.2051 — birleştirme, sıralama ve sayfalama SUNUCUDA
// (/api/anomalies/unified, internal/ml/unified.go). İstemci birleştirmesi
// kaynak başına 200 satırda sessizce kesiyordu ve sayfalanmıyordu. Tablo
// sunucu sayfalı → yalnız boyutlandırma (client sort YOK; sıra sunucunun:
// süren önce sonra yeni başlayan, bitenlerde yeni başlayan önce). Sayfa
// URL'de (?page= / ?donePage=, sinyal değişince sıfırlanır).
//
// v0.10.2083 — başlık sıralaması geri (operatör 2026-11-13), SUNUCUDA: DataTable
// serverSort kipi (`sort.server` + `urlOnly`), sıra URL'de (?sort= süren,
// ?doneSort= bitenler; varsayılan yazılmaz), sorgu doğrudan URL'den
// (lib/unifiedAnomalySort.ts). Sıra değişince O tablonun sayfası başa döner.
// Sıralanan: Başlangıç, Servis, Sinyaller, Durum. "Ne oldu" cümlesi tarayıcıda
// kurulur → sunucu sıralayamaz, başlığı düz kalır (sortValue yok).
//
// v0.10.2473 — Elenenler (view=held): gürültü kapılarında (az hacim / küçük
// mutlak fark / görünür sapma yok) elenen olaylar; "Elenme nedeni" sütunu
// (kalan kapı, değer, eşik) ve üstte sayaç (son 24 sa olay + kapı başına).
// Açıklama satırı kipe göre: on = elendi, shadow = elenecekti, off = kapalı.
import { useMemo, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router';
import { DataTable } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { Pager } from '@/components/Pager';
import { useUnifiedAnomalies } from '@/lib/queries';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import type { SignalKind } from '@/lib/signalKind';
import type { AnomalyView, UnifiedAnomalyRow } from '@/lib/types';
import { groupStatusText } from './mlGroupRows';
import { useUrlPage } from '@/lib/useUrlPage';
import { t as tr, tf, useLang } from '@/lib/i18n';
import { heldCounterText, heldReasonText } from '@/lib/anomalyView';
import { listHeadlineView, rowHref } from './unifiedRows';
import { OrigLang } from '@/features/anomalies/OrigLang'; // v0.10.2302
import {
  UNIFIED_SORT_DEFAULT, UNIFIED_SORT_IDS, UNIFIED_SORT_URL_KEY, parseUnifiedSort, unifiedSortApiParams,
} from '@/lib/unifiedAnomalySort';

const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

/** Sunucu sayfası boyu (uç tavanı 100). */
const UNIFIED_PAGE_SIZE = 50;

// v0.10.2027 — sütun kimlikleri v0.10.2021 ML listesiyle aynı; fazla sütun yok.
// v0.10.2083 — sortValue serverSort kipinde yalnız "sıralanabilir" işareti +
// naturalDir taşıyıcısı (hiç çağrılmaz); sıra sunucunun (SortUnified).
// v0.10.2258 — başlıklar arayüz dilinde (TR metin aynı).
function unifiedCols(t: (k: string) => string, held: boolean): ColumnDef<UnifiedAnomalyRow>[] {
  return [
    { id: 'started', label: t('anomalies.col.started'), width: 110, priority: 1, sortValue: r => r.start, naturalDir: 'desc' },
    { id: 'service', label: t('anomalies.col.service'), width: 260, minWidth: 160, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
    { id: 'headline', label: t('anomalies.col.headline'), flex: true, minWidth: 280 },
    // v0.10.2473 — yalnız Elenenler: neden sunucu sıralamasında yok (sortValue yok).
    ...(held ? [{ id: 'heldReason', label: t('anomalies.col.heldReason'), width: 300, minWidth: 200, priority: 1 }] : []),
    { id: 'signals', label: t('anomalies.col.signals'), width: 190, minWidth: 120, priority: 2, sortValue: r => r.signals.join(','), naturalDir: 'asc' },
    { id: 'status', label: t('anomalies.col.status'), width: 170, minWidth: 130, priority: 1, sortValue: r => r.status, naturalDir: 'desc' },
  ];
}

type SignalFilter = '' | Exclude<SignalKind, 'group'>;

/** detectorStatus: sayfanın en altındaki KAPALI "Teknik ayrıntı / Dedektör
 *  durumu" bölümünün içeriği (ML karnesi + eski dedektör kartları).
 *  v0.10.2243 (Dilim C) — view: '' servis listesi | platform | held; emptyText
 *  o görünümün boş durum metni, hint görünüm açıklaması (sayfa çevirir; held
 *  uyarı renginde — gölge karar olduğu açık). */
export function UnifiedAnomalies({ signal = '', onSignal, detectorStatus, view = '', emptyText, hint }: {
  signal?: SignalFilter; onSignal?: (s: SignalFilter) => void; detectorStatus?: ReactNode; view?: AnomalyView; emptyText?: string; hint?: string;
}) {
  const lang = useLang();
  const t = (k: string) => tr(k, lang);
  const cols = useMemo(() => unifiedCols(k => tr(k, lang), view === 'held'), [lang, view]);
  const [openPage, setOpenPage] = useUrlPage('page');
  const [donePage, setDonePage] = useUrlPage('donePage');
  const [sp] = useSearchParams();
  const openSort = parseUnifiedSort('open', sp.get(UNIFIED_SORT_URL_KEY.open));
  const doneSort = parseUnifiedSort('done', sp.get(UNIFIED_SORT_URL_KEY.done));

  const openQ = useUnifiedAnomalies({ section: 'open', signal, offset: openPage * UNIFIED_PAGE_SIZE, limit: UNIFIED_PAGE_SIZE, ...unifiedSortApiParams('open', openSort), view });
  const doneTotal = openQ.data?.doneTotal ?? 0;
  const doneQ = useUnifiedAnomalies({ section: 'done', signal, offset: donePage * UNIFIED_PAGE_SIZE, limit: UNIFIED_PAGE_SIZE, ...unifiedSortApiParams('done', doneSort), view }, doneTotal > 0);
  const open = useMemo(() => openQ.data?.items ?? [], [openQ.data]);
  const done = useMemo(() => doneQ.data?.items ?? [], [doneQ.data]);
  const nowNs = Date.now() * 1e6;

  const renderCell = (r: UnifiedAnomalyRow, col: ColumnDef<UnifiedAnomalyRow>) => {
    switch (col.id) {
      case 'started': return <span className="font-mono" title={fmtTs(r.start)}>{fmtClock(new Date(r.start / 1e6)).slice(0, 5)}</span>;
      case 'service': return <span className="dt-trunc font-mono" title={r.service}>{r.service || '—'}</span>;
      case 'headline': { const { text: h, serverText } = listHeadlineView(r, lang); return <Link to={rowHref(r)} className="dt-trunc" title={h}>{serverText ? <OrigLang>{h}</OrigLang> : h}</Link>; }
      case 'heldReason': { const why = heldReasonText(r.heldGates, lang); return why ? <span className="dt-trunc" title={why}>{why}</span> : <span className="text-muted-foreground">—</span>; }
      case 'signals': return (
        <span className="inline-flex flex-wrap items-center gap-2">
          {r.signals.map(k => <SignalKindBadge key={k} kind={k} />)}
        </span>
      );
      case 'status': {
        const st = groupStatusText(r, nowNs, lang);
        return r.status === 'running' ? <span className="text-[color:var(--warn)]">{st}</span> : <span className="text-muted-foreground">{st}</span>;
      }
      default: return null;
    }
  };

  const state = openQ.isLoading ? { kind: 'loading' as const }
    : openQ.isError ? { kind: 'error' as const, message: t('anomalies.list.error') }
      : { kind: 'empty' as const, message: emptyText ?? t('anomalies.list.empty') };
  const partialSrc = openQ.data?.partial;
  const partial = partialSrc === 'ml' ? t('anomalies.list.partialMl') : partialSrc === 'legacy' ? t('anomalies.list.partialLegacy') : '';
  const doneState = doneQ.isError ? { kind: 'error' as const, message: t('anomalies.list.doneError') }
    : doneQ.isLoading ? { kind: 'loading' as const } : { kind: 'empty' as const, message: t('anomalies.list.doneEmpty') };
  const openTotal = openQ.data?.total ?? 0;
  // v0.10.2473 — Elenenler: kip ve sayaç sunucudan (held görünümü yanıtı).
  const gatesMode = view === 'held' ? openQ.data?.gatesMode : undefined;
  const heldInfo = view === 'held' ? openQ.data?.held : undefined;
  const viewHint = gatesMode === 'on' ? t('anomalies.view.heldHintEnforced') : gatesMode === 'off' ? t('anomalies.view.heldHintOff') : hint;
  // Sıra değişince o tablonun sayfası başa (server.onSort yalnız başlık
  // eylemiyle koşar; geri/ileri ile gelen URL sayfayı silmez).
  const openSortOpts = {
    initial: UNIFIED_SORT_DEFAULT.open, ids: UNIFIED_SORT_IDS, urlKey: UNIFIED_SORT_URL_KEY.open, urlOnly: true,
    server: { page: openPage, pageSize: UNIFIED_PAGE_SIZE, hasMore: (openPage + 1) * UNIFIED_PAGE_SIZE < openTotal, onPage: setOpenPage, onSort: () => setOpenPage(0) },
  };
  const doneSortOpts = {
    initial: UNIFIED_SORT_DEFAULT.done, ids: UNIFIED_SORT_IDS, urlKey: UNIFIED_SORT_URL_KEY.done, urlOnly: true,
    server: { page: donePage, pageSize: UNIFIED_PAGE_SIZE, hasMore: (donePage + 1) * UNIFIED_PAGE_SIZE < doneTotal, onPage: setDonePage, onSort: () => setDonePage(0) },
  };

  return (
    <div data-testid="anomalies-unified" className="flex flex-col gap-3">
      {onSignal && (
        <SegmentedControl<SignalFilter> value={signal} onChange={onSignal} aria-label={t('anomalies.signal.aria')} size="sm"
          options={[{ value: '', label: t('anomalies.signal.all') }, { value: 'trace', label: 'Trace' }, { value: 'log', label: 'Log' }, { value: 'metric', label: t('signalKind.metric') }]} />
      )}
      {hint && (
        <p data-testid="anomalies-view-hint" className={view === 'held' && gatesMode !== 'on' ? 'm-0 text-sm text-[color:var(--warn)]' : 'm-0 text-sm text-muted-foreground'}>
          {/* kip on: olaylar gerçekten elendi (bilgi); shadow / off: uyarı renginde */}
          {viewHint}
        </p>
      )}
      {heldInfo && gatesMode !== 'off' && (
        <p data-testid="anomalies-held-counter" className="m-0 text-sm font-semibold">{heldCounterText(heldInfo, gatesMode === 'shadow', lang)}</p>
      )}
      {partial && <p className="m-0 text-sm text-[color:var(--warn)]">{tf('anomalies.list.partial', lang, { src: partial })}</p>}
      {/* v0.10.2030 — sayfada DOĞRUDAN duran tablo, diğer liste sayfaları gibi
          .table-wrap yüzeyinde (Inbox emsali; coremetry DataTable çıplak <table>). */}
      <div className="table-wrap">
        <DataTable<UnifiedAnomalyRow>
          storageKey={view === 'held' ? 'anomalies-held' : 'anomalies-unified'} columns={cols} rows={open} state={state} sort={openSortOpts}
          getRowHref={rowHref}
          getRowKey={r => r.key} renderCell={renderCell} ariaLabel={t('anomalies.list.openAria')} />
      </div>
      {openTotal > UNIFIED_PAGE_SIZE && (
        <Pager mode="offset" count="exact" total={openTotal} page={openPage} pageSize={UNIFIED_PAGE_SIZE}
          onPage={setOpenPage} />
      )}
      {doneTotal > 0 && (
        <details data-testid="anomalies-done" className="rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">{tf('anomalies.list.done', lang, { n: doneTotal })}</summary>
          <div className="mt-2">
            <DataTable<UnifiedAnomalyRow>
              storageKey={view === 'held' ? 'anomalies-held-done' : 'anomalies-unified-done'} columns={cols} rows={done} state={doneState} sort={doneSortOpts}
              getRowHref={rowHref}
              getRowKey={r => r.key} renderCell={renderCell} ariaLabel={t('anomalies.list.doneAria')} />
            {doneTotal > UNIFIED_PAGE_SIZE && (
              <Pager mode="offset" count="exact" total={doneTotal} page={donePage} pageSize={UNIFIED_PAGE_SIZE}
                onPage={setDonePage} />
            )}
          </div>
        </details>
      )}
      {openQ.data?.truncated && <p className="m-0 text-xs text-muted-foreground">{t('anomalies.list.truncated')}</p>}
      {detectorStatus && (
        <details data-testid="anomalies-detector-status" className="mt-2 rounded-md border border-border bg-card px-2.5 py-1.5">
          <summary className="cursor-pointer text-sm font-semibold">{t('anomalies.list.technical')}</summary>
          <div className="mt-2 flex flex-col gap-4">{detectorStatus}</div>
        </details>
      )}
    </div>
  );
}
