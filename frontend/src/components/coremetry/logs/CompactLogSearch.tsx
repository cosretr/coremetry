// coremetry/logs/CompactLogSearch — v0.10.1900 (Log Search Faz 3 dilim 3.10; audit
// §11 operatör kararı: "aynı log yapısı Service sayfasının Logs sekmesinde de").
// Log Search bileşiğinin KOMPAKT, servise SABİTLENMİŞ kipi: sorgu çubuğu
// (LogQueryInput — KQL / Lucene rozeti, yerel geçmiş) + seviye seçimi +
// histogram (LogsHistogram) + tablo (LogTable: trace çipi, exception rozeti) +
// satır detayı çekmecesi (LogDetailSheet: exception, çevre loglar) + alan
// kenar çubuğu (FieldSidebar, varsayılan KAPALI). "Logs'ta aç" tüm durumu
// taşır (servis, arama, seviye tabanı, pencere, env, dil, kolonlar).
//
// ES bütçesi (2026-11-06 prod olayının kuralı): AÇILIŞTA eskisi kadar —
// liste (api.logs, 200 satır) + histogram (api.logsTimeseries). Alan listesi
// (/api/logs/fields) ve alan değerleri yalnız kenar çubuğu AÇILINCA; çevre
// loglar yalnız bölüm açılınca; değer tamamlaması yalnız yazarken; "200 satır
// daha" yalnız tıkla. Arama artık yazarken (300 ms) DEĞİL Enter / Ara ile
// gider. Trace önizlemesi ClickHouse'tan (MV, satırlar geldikten sonra).
//
// URL (servis sayfasının paramları, replace): `lq` uygulanan arama, `llvl`
// seviye, `llang=lucene` zorla Lucene, `ldoc` açık satır (çekmece; açılış
// geçmişe girer). Kenar çubuğu açık/kapalı yerel tercih (`svc.logs.fields`).
import { useEffect, useMemo, useRef, useState, type ComponentProps } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { useSheetParam } from '@/components/coremetry/DetailSheet/useSheetParam';
import { LogsHistogram } from '@/components/LogsHistogram';
import { LogTable, DEFAULT_LOG_COLUMNS } from '@/components/LogTable';
import { Pager } from '@/components/Pager';
import { useAuth } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { useLogsTracePreview } from '@/lib/queries';
import { logsHref } from '@/lib/logsUrl';
import { logQueryErrorMessage } from '@/lib/logQueryError';
import { mergePatternQuery } from '@/lib/logFilters';
import { readLogsQueryHistory, recordLogsQuery } from '@/lib/logsQueryHistory';
import { levelBandOf, levelBandOfName, parseLevelBand, LOG_LEVEL_BANDS, LOG_LEVEL_MIN_SEV, type LogLevelBand } from '@/lib/logLevelBand';
import { neighborRow } from '@/lib/logDetail';
import { getRaw, setRaw } from '@/lib/storage';
import { rangeToSince } from '@/lib/utils';
import type { LogRow, TimeRange } from '@/lib/types';
import { LogQueryInput } from './LogQueryInput';
import { FieldSidebar } from './FieldSidebar';
import { LogDetailSheet } from './LogDetailSheet';
import { LogContextSection } from './LogContextSection';
import { LogRunbookSection } from './LogRunbookSection';
import { useServiceLogSummary, ServiceLogSummaryButton, ServiceLogSummaryResult } from './ServiceLogSummary';
import { LogExceptionSection } from './StackBlock';
import { TraceChip } from './TraceChip';
import { traceHref } from '@/lib/traceHref';

const PAGE = 200;
const NO_LOGS: LogRow[] = [];
const FIELDS_KEY = 'svc.logs.fields';

type TableState = ComponentProps<typeof LogTable>['state'];

export interface CompactLogSearchProps {
  /** Sabit servis (kaldırılamaz). */
  service: string;
  range: TimeRange;
  /** Sayfanın memo'lu penceresi (ns) — remount'ta yeni anahtar basılmasın. */
  from: number;
  to: number;
  env?: string;
  /** Histogram sürükle = sayfa zoom'u (unix SANİYE). */
  onZoom?(fromSec: number, toSec: number): void;
  onZoomReset?(): void;
  /** "Logs'ta aç" pencere paramı (encodeRange). */
  rangeParam: string;
}

/** KQL değeri tırnakla (iç tırnak / ters eğik kaçışlı). SAF. */
export function kqlTerm(key: string, value: string): string {
  return `${key}:"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

/** "Logs'ta aç" — kompakt kipin tüm durumu /logs paramlarına. SAF. */
export function compactLogsHref(p: {
  service: string; search: string; lvl: 'all' | LogLevelBand; rangeParam: string;
  env?: string; lucene: boolean; cols: readonly string[];
}): string {
  const cols = p.cols.join(',') === DEFAULT_LOG_COLUMNS.join(',') ? '' : p.cols.join(',');
  return logsHref({
    service: p.service, q: p.search || undefined, window: p.rangeParam || null,
    severity: p.lvl === 'all' ? undefined : LOG_LEVEL_MIN_SEV[p.lvl] || undefined,
    env: p.env || undefined, cols: cols || undefined, lang: p.lucene ? 'lucene' : undefined,
  });
}

export function CompactLogSearch({ service, range, from, to, env, onZoom, onZoomReset, rangeParam }: CompactLogSearchProps) {
  const navigate = useNavigate();
  const userId = useAuth().user?.id;
  const [sp, setSp] = useSearchParams();
  const setParam = (k: string, v: string | null) => setSp(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set(k, v); else next.delete(k);
    return next;
  }, { replace: true });

  // Uygulanan arama URL'de; taslak yerel. Dış ?lq= değişimi (geri/ileri,
  // kayıtlı görünüm) taslağa da yansır (v0.10.1220 tek-yön okuma sınıfı).
  const search = sp.get('lq') ?? '';
  const [draft, setDraft] = useState(search);
  const lastSearch = useRef(search);
  useEffect(() => {
    if (search !== lastSearch.current) { lastSearch.current = search; setDraft(search); }
  }, [search]);
  const lvl = parseLevelBand(sp.get('llvl'));
  const lucene = sp.get('llang') === 'lucene';
  const minSev = lvl === 'all' ? undefined : (LOG_LEVEL_MIN_SEV[lvl] || undefined);
  const lang = lucene ? 'lucene' as const : undefined;

  const [history, setHistory] = useState(() => readLogsQueryHistory(userId));
  const historyTexts = useMemo(() => history.map(e => e.q), [history]);
  const applySearch = (q: string) => {
    if (q.trim()) setHistory(recordLogsQuery(userId, q));
    setDraft(q);
    lastSearch.current = q;
    setParam('lq', q.trim() ? q : null);
  };
  const addTerm = (key: string, value: string, exclude: boolean) => applySearch(mergePatternQuery(search, kqlTerm(key, value), exclude));
  const addExists = (key: string, negated: boolean) => applySearch(mergePatternQuery(search, `${key}:*`, negated));

  // Liste — tek ES isteği (eskisiyle aynı: limit 200, servis + arama + seviye
  // tabanı + env). RQ sinyali: süzgeç / pencere değişince ya da çıkışta iptal.
  const listParams = { limit: PAGE, from, to, service, search: search || undefined, severity: minSev, env, lang };
  const q = useQuery({
    queryKey: ['service-tab-logs', service, from, to, search, minSev ?? 0, env ?? '', lang ?? ''],
    queryFn: ({ signal }) => api.logs(listParams, signal),
    enabled: !!service,
    staleTime: 15_000,
    refetchOnWindowFocus: false, // /api/logs önbelleksiz; ES PIT açar (v0.8.3)
  });
  // "200 satır daha" — kullanıcı tetiklemeli keyset (v0.9.406; ilk sayfa
  // cursor'suz, PIT yalnız ilk tıkta).
  const [extraRows, setExtraRows] = useState<LogRow[]>([]);
  const [pagingBusy, setPagingBusy] = useState(false);
  const [pagingDone, setPagingDone] = useState(false);
  const cursorRef = useRef('');
  const pagingCtl = useRef<AbortController | null>(null);
  useEffect(() => {
    pagingCtl.current?.abort(); pagingCtl.current = null;
    setExtraRows([]); setPagingDone(false); cursorRef.current = '';
  }, [service, from, to, search, minSev, env, lang]);
  useEffect(() => () => pagingCtl.current?.abort(), []);
  const loadMore = async () => {
    if (pagingBusy || pagingDone) return;
    setPagingBusy(true);
    const ctl = new AbortController();
    pagingCtl.current = ctl;
    try {
      if (!cursorRef.current) {
        const first = await api.logs({ ...listParams, paging: true }, ctl.signal);
        cursorRef.current = first?.nextCursor ?? '';
        if (!cursorRef.current) { setPagingDone(true); return; }
      }
      const next = await api.logs({ ...listParams, after: cursorRef.current, paging: true }, ctl.signal);
      if (ctl.signal.aborted) return;
      setExtraRows(r => [...r, ...(next?.logs ?? [])]);
      cursorRef.current = next?.nextCursor ?? '';
      if (!cursorRef.current || (next?.logs ?? []).length === 0) setPagingDone(true);
    } catch {
      if (!ctl.signal.aborted) setPagingDone(true);
    } finally {
      setPagingBusy(false);
    }
  };
  const logs = useMemo(() => [...(q.data?.logs ?? []), ...extraRows], [q.data, extraRows]);
  const rows = useMemo(() => (lvl === 'all' ? logs : logs.filter(r => levelBandOf(r) === lvl)), [logs, lvl]);

  // Seviye sayıları histogramın ZATEN çektiği seriden (pencerenin tamamı);
  // gelene kadar sayfa-türevi (v0.9.358).
  const [bandTotals, setBandTotals] = useState<Record<string, number> | null>(null);
  const onHistSeries = useMemo(() => (series: { name: string; total: number }[] | null) => {
    if (series === null) { setBandTotals(null); return; }
    const c: Record<string, number> = { all: 0, error: 0, warn: 0, info: 0, debug: 0 };
    for (const s of series) { c[levelBandOfName(s.name)] += s.total; c.all += s.total; }
    setBandTotals(c);
  }, []);
  const counts = useMemo(() => {
    if (bandTotals) return bandTotals;
    const c: Record<string, number> = { all: logs.length, error: 0, warn: 0, info: 0, debug: 0 };
    for (const r of logs) c[levelBandOf(r)]++;
    return c;
  }, [bandTotals, logs]);
  const histFilter = useMemo(
    () => ({ service, search, severity: 0, traceId: '', spanId: '', env, lang }),
    [service, search, env, lang]);

  // Trace çipi: görünen satırların TEK önizleme isteği (ClickHouse MV).
  const previewItems = useMemo(() => {
    const latest = new Map<string, number>();
    for (const r of rows) if (r.traceId && (latest.get(r.traceId) ?? 0) < r.timestamp) latest.set(r.traceId, r.timestamp);
    return [...latest].map(([id, tsNs]) => ({ id, tsNs }));
  }, [rows]);
  const previewQ = useLogsTracePreview(previewItems, !!q.data);
  // Trace çipi / çekmece "Trace + loglar" → /logs birleşik görünümü (3.6).
  const openTraceView = (tid: string, tsNs?: number) => navigate(logsHref({
    service, window: rangeParam || null, traceView: tid, traceHintSec: tsNs ? tsNs / 1e9 : undefined,
  }));

  // Kolonlar (yerel; "Logs'ta aç" taşır) + alan kenar çubuğu (kapalı başlar).
  const [cols, setCols] = useState<string[]>(DEFAULT_LOG_COLUMNS);
  const toggleColumn = (id: string) => setCols(c => (c.includes(id) ? c.filter(x => x !== id) : [...c, id]));
  const [fieldsOpen, setFieldsOpen] = useState(() => getRaw(FIELDS_KEY) === '1');
  const setFieldsPanel = (open: boolean) => { setRaw(FIELDS_KEY, open ? '1' : '0'); setFieldsOpen(open); };
  const fieldsQ = useQuery({
    queryKey: ['logs', 'fields'],
    queryFn: () => api.logsFields(),
    enabled: fieldsOpen, // ES-maliyet: yalnız açılınca
    staleTime: 300_000,
    refetchOnWindowFocus: false,
  });

  // Satır detayı çekmecesi (`ldoc` = satır id).
  const [docParam, setDocParam] = useSheetParam('ldoc');
  const docRow = useMemo(() => (docParam ? rows.find(r => String(r.id) === docParam) ?? null : null), [docParam, rows]);
  const openRow = (r: LogRow) => setDocParam(String(r.id));
  // v0.10.1984 (operatör: "servis Logs sekmesinde loglar yandaki çekmeceden açılıyor, o da inline olsun") —
  // satır tıkı yeniden SATIR İÇİ (/logs 1970 ile aynı); çekmece yalnız paylaşılan ?doc= ve çevre loglardan.
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const toggleRow = (id: number) => setExpanded(prev => { const n = new Set(prev); if (n.has(id)) n.delete(id); else n.add(id); return n; });

  const showRows = !q.isLoading && !q.data?.degraded && rows.length > 0;
  const state: TableState =
    q.isLoading ? { kind: 'loading', skeletonRows: 10 }
    : q.isError ? {
        kind: 'error',
        message: logQueryErrorMessage(q.error, 'Log sorgusu çalışmadı — log backend sorguyu reddetti ya da zamanında cevap vermedi.'),
        onRetry: () => void q.refetch(),
      }
    : q.data?.degraded ? {
        kind: 'error',
        message: `Log backend'i yanıt veremedi — ${q.data.reason || 'Elasticsearch yavaş ya da erişilemez — pencerede log olmadığı anlamına gelmez.'}`,
      }
    : (search || lvl !== 'all') ? { kind: 'no-match', message: `Bu pencerede ${service} için süzgeçle eşleşen log yok` }
    : { kind: 'empty', message: `Bu pencerede ${service} için log yok` };

  const lvlOptions = [
    { value: 'all' as const, label: `Tümü · ${counts.all.toLocaleString('tr-TR')}` },
    ...LOG_LEVEL_BANDS.map(l => ({ value: l, label: `${l.toUpperCase()} · ${(counts[l] ?? 0).toLocaleString('tr-TR')}` })),
  ];
  const href = compactLogsHref({ service, search, lvl, rangeParam, env, lucene, cols });
  // Log Search 6.7 — "Log özeti" (yalnız tıkla; 6.3 ile ortak sunucu katmanı).
  const summary = useServiceLogSummary({ service, env, from, to });
  const total = q.data?.total ?? 0;

  return (
    <div className="cls" data-testid="compact-log-search">
      <div className="cls-bar">
        <span className="cls-svc" title="Servis süzgeci sabit">service: <b>{service}</b></span>
        <LogQueryInput className="cls-q" value={draft} onChange={setDraft} onSubmit={() => applySearch(draft)}
          since={rangeToSince(range).since} fields={fieldsQ.data?.fields} fieldsTotal={fieldsQ.data?.total}
          history={historyTexts} lang={lucene ? 'lucene' : (q.data?.queryLang ?? 'kql')} forcedLucene={lucene}
          onToggleLang={() => setParam('llang', lucene ? null : 'lucene')}
          placeholder='Ara… (KQL: level:error and message:"timeout")' />
        <Button size="sm" onClick={() => applySearch(draft)}>Ara</Button>
        <Button variant="outline" size="sm" asChild>
          <Link to={href} title="Aynı arama, seviye, pencere ve kolonlarla Log Search">Logs'ta aç →</Link>
        </Button>
        <ServiceLogSummaryButton ctl={summary} />
      </div>
      <ServiceLogSummaryResult ctl={summary} service={service} onSearchPattern={q => applySearch(mergePatternQuery(search, q, false))} />
      <div className="cls-levels">
        <SegmentedControl<'all' | LogLevelBand> aria-label="Log seviyesi" size="sm" value={lvl}
          onChange={v => setParam('llvl', v === 'all' ? null : v)} options={lvlOptions} />
      </div>
      <div className="card cls-hist">
        <LogsHistogram range={{ from, to }} filter={histFilter} onSeries={onHistSeries} height={96}
          onRangeSelect={onZoom ? (f, t) => onZoom(f / 1e9, t / 1e9) : undefined} onZoomReset={onZoomReset} />
      </div>
      <div className="cls-body">
        <FieldSidebar open={fieldsOpen} onOpenChange={setFieldsPanel}
          fields={fieldsQ.data?.fields ?? []} fieldsTotal={fieldsQ.data?.total} types={fieldsQ.data?.types}
          columns={cols} windowTotal={total || undefined}
          scope={{ from, to, service, env, search: search || undefined }}
          paused={!fieldsOpen}
          onToggleColumn={toggleColumn}
          onPillAdd={(k, v) => addTerm(k, v, false)} onPillExclude={(k, v) => addTerm(k, v, true)}
          onExists={addExists} />
        <div className="card cls-main">
          <div className="cls-main-h">
            <h3>Logs</h3>
            <span className="cls-sub">
              {rows.length.toLocaleString('tr-TR')} satır{lvl !== 'all' ? ` · ${lvl}` : ''}
              {total > logs.length && ` · pencerede ${total.toLocaleString('tr-TR')}${q.data?.totalIsLowerBound ? '+' : ''} satır, en yeni ${logs.length} gösteriliyor`}
            </span>
          </div>
          <LogTable logs={showRows ? rows : NO_LOGS} state={showRows ? undefined : state} columns={cols}
            exceptions
            renderTrace={l => (
              <TraceChip traceId={l.traceId} preview={previewQ.data?.traces?.[l.traceId]} href={traceHref(l.traceId)} /* v0.10.1968 */
                onOpen={tid => openTraceView(tid, l.timestamp)} />
            )}
            expandedIds={expanded}
            onToggleExpand={toggleRow}
            expandedSection={l => <LogRunbookSection key={`${l.timestamp}.${l.id}`} row={l} env={env} /> /* Log Search 6.4 */} />
          {showRows && (
            <Pager mode="cursor" count="skip" hasMore={!pagingDone} onMore={loadMore} loading={pagingBusy}
              loaded={logs.length} moreLabel="200 satır daha" />
          )}
        </div>
      </div>
      <LogDetailSheet row={docRow} onClose={() => setDocParam(null)} columns={cols}
        resizeKey="svc.logs.detailSheet.w"
        onFilter={(k, v) => { setDocParam(null); addTerm(k, v, false); }}
        onExclude={(k, v) => { setDocParam(null); addTerm(k, v, true); }}
        onToggleColumn={toggleColumn}
        onPrev={() => { if (docRow) { const n = neighborRow(rows, docRow.id, -1); if (n) openRow(n); } }}
        onNext={() => { if (docRow) { const n = neighborRow(rows, docRow.id, 1); if (n) openRow(n); } }}
        onOpenTrace={tid => openTraceView(tid, docRow?.timestamp)}
        exception={docRow?.exception ? <LogExceptionSection row={docRow} /> : undefined}
        context={docRow && !docRow.origin ? (
          <>
            <LogRunbookSection key={`rb.${docRow.id}`} row={docRow} env={env} />
            <LogContextSection key={docRow.id} pivot={docRow} env={env} onOpenRow={openRow}
              onOpenWindow={(f, t, svc) => navigate(logsHref({ window: { fromNs: f, toNs: t + 1_000_000 }, service: svc }))} />
          </>
        ) : undefined} />
    </div>
  );
}
