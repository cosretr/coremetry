import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3)
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { navHref } from '@/lib/navHref';
import { Topbar } from '@/components/Topbar';
import { Button } from '@/components/ui';
// `api` importu KALKTI (v0.9.1137): sayfanın tek doğrudan çağrısı
// api.copilotExplainSlowQuery'ydi ve o insight kartına devredildi. Satır
// verisi useSlowQueries üzerinden (React Query) geliyor.
import { useDBStatements } from '@/lib/queries';
import { Pager } from '@/components/Pager';
import { timeRangeToNs, fmtNum } from '@/lib/utils';
import { slowQueryTracesHref } from '@/pages/slowqueries/tracesHref';
import { useUrlRange, DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { stmtDetailHref } from '@/pages/slowqueries/stmtParam';
import { useStmtParamRedirect } from '@/pages/slowqueries/useStmtParamRedirect';
import type { SlowQueryRow, TimeRange } from '@/lib/types';
import { PageControls } from '@/components/coremetry/PageControls';
import { serviceHref } from '@/lib/serviceHref';
import { PageShell } from '@/components/coremetry/PageShell';
import { useT } from '@/lib/i18n';
// v0.9.1137 (AI Faz 2.4) satır-içi ✨ Explain yerine insight kartı gelmişti;
// v0.10.652 (operatör) kart yuvası + "Ne oldu?" çipi bu sayfadan söküldü.

// Columns for the shared sortable + resizable DataTable primitive.
// Default order matches the backend's total-wall-clock sort so the
// first paint is unchanged; the operator can now re-sort/resize any.
// v0.10.943 — tablo standardı dilim 3: hücre görünümü kolon bayraklarında
// (mono / numeric / tone); satır içi küçük punto yerine renk (S3).
// v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor") — okunurluk tabanları +
// öncelik: Statement en az 220 px; kap tabanları taşıyamazsa önce P50, sonra
// Avg düşer ("+N sütun"). Engine rozeti ("postgresql") 96 px'te kırpılmaz.
const SLOW_COLS: ColumnDef<SlowQueryRow>[] = [
  { id: 'service',    label: 'Service',                sortValue: r => r.service,    naturalDir: 'asc', width: 180, minWidth: 140, priority: 1, mono: true },
  { id: 'dbSystem',   label: 'Engine',                 sortValue: r => r.dbSystem,   naturalDir: 'asc', width: 100, minWidth: 96, priority: 2 },
  // v0.9.272 — the column that used to say "oracle" on every row now says
  // which database it actually was. 'Engine' above keeps the old value under
  // an honest label rather than being repurposed.
  { id: 'dbName',     label: 'Database',               sortValue: r => r.dbName ?? '', naturalDir: 'asc', width: 130, minWidth: 110, priority: 2, mono: true, tone: () => 'muted' },
  // v0.10.1525 — sıra sunucuda (serverSort); normalize metin MV'de saklanmıyor
  // (örnekten türetiliyor), o yüzden bu kolon sıralanmaz.
  { id: 'statement',  label: 'Statement (normalised)', flex: true, minWidth: 220, priority: 1, mono: true },
  { id: 'count',      label: 'Calls',      sortValue: r => r.count,      numeric: true, width: 90, minWidth: 72, priority: 2 },
  { id: 'avgMs',      label: 'Avg ms',     sortValue: r => r.avgMs,      numeric: true, width: 90, minWidth: 72, priority: 3 },
  // v0.9.265 — P50 next to Avg so a row reads "typical" then "tail".
  { id: 'p50Ms',      label: 'P50 ms',     sortValue: r => r.p50Ms,      numeric: true, width: 90, minWidth: 72, priority: 4 },
  { id: 'p99Ms',      label: 'P99 ms',     sortValue: r => r.p99Ms,      numeric: true, width: 90, minWidth: 72, priority: 2,
    tone: r => (r.p99Ms > 1000 ? 'err' : r.p99Ms > 200 ? 'warn' : undefined) },
  { id: 'totalMs',    label: 'Total time', sortValue: r => r.totalMs,    numeric: true, width: 110, minWidth: 96, priority: 1 },
  { id: 'errorCount', label: 'Errors',     sortValue: r => r.errorCount, numeric: true, width: 90, minWidth: 64, priority: 2,
    tone: r => (r.errorCount > 0 ? 'err' : 'faint') },
  // v0.10.652 (operatör) — trace araması satırın kendisinde, en sağda; link kolonu sıralanmaz.
  { id: 'traces',     label: 'Traces',     width: 84, minWidth: 80, priority: 2 },
];

// v0.9.1137 (AI Faz 2.4) — SATIR-İÇİ ✨ EXPLAIN SÖKÜLDÜ.
//
// Buradaki `ExplainState` makinesi (idle/busy/text/error) + askCopilot +
// api.copilotExplainSlowQuery çağrısı, insight kartıyla DEĞİŞTİRİLDİ:
// kart aynı sistem prompt'unu (SystemPromptSlowQuery) kullanıyor ama
// kanıtı da sunucu topluyor, üstüne akan anlatı + deterministik sinyaller
// + sunucu-üretimi pivotlar + 👍/👎 getiriyor. Yani kartın kapsamı eski
// panelin ÜST KÜMESİ; iki AI yüzeyini aynı satırda tutmak (v0.9.306
// sınıfı) gereksiz olurdu.
//
// SESSİZ BİR KUSURU DA GÖTÜRDÜ: eski panel cevabı `pre-wrap` ile HAM
// basıyordu, yani modelin `**kalın**` işaretleri ekranda yıldız olarak
// görünüyordu (v0.9.641 → 696 → Faz 0.5'te üç kez düzeltilen sınıf).
// Bu dosya markdownSurfaces kapısının ÜÇ ailesinin de dışındaydı
// (ne `aiSummary`, ne `{busy,text,err}` adlandırması, ne kart) — yani
// kusur ölçülmüyordu. Kart RenderedMarkdown'dan geçiyor ve kapıya
// (InsightCard ailesi) DAHİL.

// /databases/slow-queries — global slow-query catalog (v0.5.165).
// Answers "what query class is burning the most DB time across
// the whole install?". Per-service view stays at /service?name=…
// (the existing DBQueriesPanel); this one is cross-service so
// the platform team can see "payments-api's stale join is
// number-one across all our DB time" without per-service
// pivoting.
//
// Sorted by total wall-clock time (count × avg ms) because that's
// what's actually worth fixing. A 5ms query running a million
// times beats a 5s query running once.
const PAGE_SIZE = 200;

// v0.10.1525 — sunucu sırasının beyaz listesi (chstore dbStatementsSortCols ile
// birebir; kolon kimliği = sunucu anahtarı). Varsayılan toplam süre ↓.
const STMT_SORT_KEYS = ['totalMs', 'count', 'avgMs', 'p50Ms', 'p99Ms', 'errorCount', 'service', 'dbSystem', 'dbName'] as const;
const DEFAULT_STMT_SORT = { id: 'totalMs', dir: 'desc' as const };

export default function SlowQueriesPage() {
  const tr = useT();
  const [range, setRange] = useUrlRange(DEFAULT_RANGE_PRESET);
  // v0.9.399 (desen paritesi) — aynı filtre çifti /databases'te URL'de
  // (dbsys/dbname), kardeş sayfada oturum state'indeydi. Aynı adlarla
  // URL'e alındı; copy-link filtreleri taşır.
  const [sqParams, setSqParams] = useSearchParams();
  const dbSystem = sqParams.get('dbsys') ?? '';
  const dbName = sqParams.get('dbname') ?? '';
  // v0.10.1162 — operatör: "sadece slow queries değil, bir veritabanının TÜM
  // sorgularına ulaşabilmeliyim". Liste zaten eşiksizdi (toplam süreye göre
  // sıralı her ifade) ama ilk 200'de bitiyordu ve aranamıyordu: `q` örnek
  // SQL'de sunucu tarafı arama, `page` sunucu sayfası (URL'de).
  const q = sqParams.get('q') ?? '';
  const page = Math.max(0, Math.floor(Number(sqParams.get('page')) || 0));
  // Süzgeç değişince sayfa başa döner (bayat sayfa boş liste gösterirdi).
  const setUrlParam = (k: string, v: string) => setSqParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set(k, v); else next.delete(k);
    if (k !== 'page') next.delete('page');
    return next;
  }, { replace: true });
  const setPage = (p: number) => setUrlParam('page', p > 0 ? String(p) : '');
  const [qText, setQText] = useState(q);
  const qTimer = useRef<number | undefined>(undefined);
  // v0.10.1220 — dış URL değişimini içe al; kendi (debounce'lu) yazımımızın
  // yankısı lastQRef ile ayırt edilir, yoksa yazmaya devam eden metni ezerdi.
  const lastQRef = useRef(q);
  useEffect(() => {
    if (q === lastQRef.current) return;
    lastQRef.current = q;
    setQText(q);
  }, [q]);
  useEffect(() => () => window.clearTimeout(qTimer.current), []);
  const onSearch = (v: string) => {
    setQText(v);
    window.clearTimeout(qTimer.current);
    qTimer.current = window.setTimeout(() => { lastQRef.current = v.trim(); setUrlParam('q', v.trim()); }, 300);
  };
  const setDbSystem = (v: string) => setUrlParam('dbsys', v);
  const setDbName = (v: string) => setUrlParam('dbname', v);
  // v0.10.954 — "Clear" düğmesi ve tablo içi "Filtreleri temizle" AYNI eylem,
  // TEK URL yazımı: iki ayrı setUrlParam çağrısı aynı render'ın bayat
  // searchParams'ından türüyordu ve ikinci yazım ilkini geri alıyordu
  // (dbsys silinmiyordu).
  const clearFilters = () => {
    window.clearTimeout(qTimer.current);
    setQText('');
    setSqParams(prev => {
      const next = new URLSearchParams(prev);
      next.delete('dbsys');
      next.delete('dbname');
      next.delete('q');
      next.delete('page');
      return next;
    }, { replace: true });
  };
  const dbSystemRef = useRef<HTMLSelectElement>(null);
  // Bounds memoized on [range] so the query key stays stable across
  // renders (the v0.5.184 incident shape).
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const [expanded, setExpanded] = useState<string | null>(null);

  // v0.10.1525 — SUNUCU SIRASI (Endpoints v0.8.356 / exception inbox v0.8.318
  // kalıbı). Eskiden sayfa toplam süreye göre sunucudan geliyor, başlık
  // tıklaması yalnız o 200 satırı istemcide yeniden diziyordu — "en çok
  // çağrılan" diye toplam-süre diliminin içini gösteriyordu. Hook başlık
  // UX'ini + `s_slowqueries` URL paramını taşır, sıra çifti fetch'e gider;
  // hook sorgudan ÖNCE geldiği için satırlar bir state aynasından.
  const [tableRows, setTableRows] = useState<SlowQueryRow[]>([]);
  const dt = useDataTable<SlowQueryRow>({
    storageKey: 'slowqueries',
    columns: SLOW_COLS,
    rows: tableRows,
    serverSort: true,
    sortIds: STMT_SORT_KEYS,
    initialSort: DEFAULT_STMT_SORT,
    // v0.9.404 (desen paritesi) — j/k/Enter: Enter satırı genişletir.
    onOpen: (r) => {
      const key = `${r.service}::${r.statement}`;
      setExpanded(cur => (cur === key ? null : key));
    },
  });
  const sortOk = (STMT_SORT_KEYS as readonly string[]).includes(dt.sort.id ?? '');
  const sortBy = sortOk ? (dt.sort.id as string) : DEFAULT_STMT_SORT.id;
  const sortDir = sortOk ? dt.sort.dir : DEFAULT_STMT_SORT.dir;
  // Sıra değişince sayfa başa döner — effect İÇİNDE (hook'un URL yazımıyla
  // aynı tikte ikinci bir setSearchParams onu ezerdi, v0.8.253 sınıfı);
  // derin-linkli ?page= mount'ta korunur.
  const sortSig = `${sortBy}.${sortDir}`;
  const lastSortSigRef = useRef(sortSig);
  useEffect(() => {
    if (lastSortSigRef.current === sortSig) return;
    lastSortSigRef.current = sortSig;
    if (page !== 0) setPage(0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sortSig]);
  const rowsQ = useDBStatements({
    from, to,
    db_system: dbSystem || undefined,
    db_name: dbName || undefined,
    q: q || undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    sort: sortBy,
    dir: sortDir,
  });
  // Memo: `?? []` her render'da yeni dizi üretip ayna effect'ini döngüye sokmasın.
  const rows: SlowQueryRow[] | null | undefined = useMemo(
    () => (rowsQ.isPending ? undefined : rowsQ.isError ? null : rowsQ.data?.rows ?? []),
    [rowsQ.isPending, rowsQ.isError, rowsQ.data]);
  useEffect(() => { setTableRows(rows ?? []); }, [rows]);
  const hasMore = rowsQ.data?.hasMore ?? false;
  const systems = rows
    ? Array.from(new Set(rows.map(r => r.dbSystem).filter(Boolean))).sort()
    : [];
  // Same shape as `systems` above. 'default' is the MV's own sentinel for a
  // span that carried no db.name, so it is offered but reads as what it is.
  const dbNames = rows
    ? Array.from(new Set(rows.map(r => r.dbName).filter(Boolean))).sort()
    : [];

  // v0.8.378 — URL-first statement detail, keyed on the v0.8.375
  // persistent identity so a copied link resolves the same statement
  // class in any window. The expand chevron keeps its inline sample +
  // Copilot strip — two affordances, one row.
  //
  // v0.9.1374 — hedef ÇEKMECE değil SAYFA. Kimlik grameri
  // (`?stmt=<hash>|<system>`) aynı kaldı; yalnız varış yeri taşındı.
  const [params] = useSearchParams();
  const navigate = useNavigate();
  // Eski `?stmt=` linkleri (yer imi / paylaşılmış URL) sayfaya taşınır.
  useStmtParamRedirect(range);
  const openStmt = (r: SlowQueryRow) => {
    const href = stmtDetailHref({ hash: r.stmtHash, system: dbSystem }, range);
    if (href) navigate(href);
  };

  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş / eşleşme yok
  // tablonun İÇİNDE, başlık durur. dbsys/dbname sunucuya giden süzgeç:
  // seçiliyken boş sonuç "eşleşme yok" + sayfanın kendi Clear eylemi.
  const tableState: Omit<DataTableStateProps<SlowQueryRow>, 'dt'> =
    rows === undefined ? { kind: 'loading' }
    : rows === null ? { kind: 'error' }
    : (dbSystem || dbName || q) ? { kind: 'no-match', onClearFilters: clearFilters, returnFocusRef: dbSystemRef }
    : {
        kind: 'empty',
        message: "Bu pencerede DB span'i yok — ya trafik yok ya da enstrümante uygulamalar db.statement özniteliği yaymadı.",
      };

  return (
    <>
      {/* v0.10.1525 (operatör) — "Top statements" / "Slow queries" değil: liste
          eşiksiz ve sunucu-sayfalı, penceredeki HER ifade. */}
      <Topbar title={tr('stmt.allStatements')} range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          Every statement class in the window, across services. Sorted by total
          wall-clock time by default (server-side, across all pages) — what's
          actually worth optimising. Click a row to open its detail; the chevron
          expands a real sample with literals.
        </div>
        {/* v0.9.405 — URL-state (dbsys/dbname, v0.9.399) taşıyan sayfa
            görünüm kaydedebilmeli (Endpoints emsali). */}
        <SavedViewsBar page="slowqueries" />

        <PageControls sticky style={{ marginBottom: 12 }}>
          <select ref={dbSystemRef} value={dbSystem} onChange={e => setDbSystem(e.target.value)}
            style={{ fontSize: 12, padding: '3px 8px' }}>
            <option value="">All databases</option>
            {systems.map(s => <option key={s} value={s}>{s}</option>)}
          </select>
          {/* v0.9.272 — narrowing by the real database, which is the question an
              operator actually has ("what is slow on COREBANK"), not "what is
              slow on Oracle". Options come from the rows currently loaded, the
              same way the engine picker works; the filter itself is applied
              SERVER-side so it narrows the query rather than the page. */}
          <select value={dbName} onChange={e => setDbName(e.target.value)}
            style={{ fontSize: 12, padding: '3px 8px' }}
            aria-label="Filter by database">
            <option value="">All db names</option>
            {dbNames.map(d => <option key={d} value={d}>{d}</option>)}
          </select>
          <input type="search" value={qText} onChange={e => onSearch(e.target.value)}
            placeholder="SQL içinde ara…" aria-label="Search statements"
            style={{ fontSize: 12, padding: '3px 8px', minWidth: 200 }} />
          {(dbSystem || dbName || q) && (
            <Button variant="secondary" size="sm"
              onClick={clearFilters}>Clear</Button>
          )}
          {/* v0.9.1320 — geri linki pencereyi + env'i taşır (navHref). */}
          <Link to={navHref('/databases', sqParams.toString())} className="sec"
            style={{ marginLeft: 'auto', fontSize: 11, padding: '4px 10px', textDecoration: 'none' }}>
            ← Database overview
          </Link>
        </PageControls>

        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} leading={[36]} />
            <DataTableHead dt={dt} leading={<th style={{ width: 36 }}></th>} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} leading={[36]} {...tableState} /> : dt.sortedRows.map(r => {
                const key = `${r.service}::${r.statement}`;
                const isExpanded = expanded === key;
                const totalSec = r.totalMs / 1000;
                const totalLabel = totalSec >= 60
                  ? `${(totalSec / 60).toFixed(1)} min`
                  : totalSec >= 1
                  ? `${totalSec.toFixed(1)} s`
                  : `${r.totalMs.toFixed(0)} ms`;
                return (
                  // v0.9.869 (tutarlılık denetimi MT4) — burası keyless bir
                  // <> fragment'ıydı: key içteki <tr>'lerdeydi, listenin
                  // ELEMANINDA değil. React uyarı basıyor ve sıralama
                  // değiştiğinde reconcile yanlış eşleşiyordu — açık olan
                  // satırın genişletilmiş gövdesi başka bir ifadenin
                  // altında kalabiliyordu. Fragment key'i dışa taşındı.
                  <Fragment key={key}>
                    {/* v0.8.378 — row click opens the statement detail
                        drawer (URL-first, keyed on stmtHash); the chevron
                        cell keeps the inline sample+Copilot expand. Rows
                        from a pre-D1 cache entry (no stmtHash) fall back
                        to the expand toggle. */}
                    <tr key={key}
                      {...rowActivation(() => r.stmtHash
                        ? openStmt(r)
                        : setExpanded(isExpanded ? null : key))}
                      className="cv-row">
                      <td onClick={e => {
                        e.stopPropagation();
                        setExpanded(isExpanded ? null : key);
                      }}
                        title={isExpanded ? 'Hide sample' : 'Show a real sample inline'}>
                        <span style={{ fontSize: 10, color: 'var(--text3)' }}>
                          {isExpanded ? '▼' : '▶'}
                        </span>
                      </td>
                      <DataTableCell dt={dt} col="service" row={r} value={r.service}>
                        <Link to={serviceHref(r.service, { range })}
                          onClick={e => e.stopPropagation()}>
                          {r.service}
                        </Link>
                      </DataTableCell>
                      <DataTableCell dt={dt} col="dbSystem" row={r}>
                        <span className="badge b-gray mono">{r.dbSystem || '?'}</span>
                      </DataTableCell>
                      {/* v0.9.272 — the database itself. dbNameCount > 1 means this
                          (service, statement) pair ran against more than one, and the
                          name shown is one of them: grouping still folds db_name, so
                          the ambiguity is stated instead of silently resolved. */}
                      <DataTableCell dt={dt} col="dbName" row={r} value={r.dbName}>
                        {r.dbName
                          ? <>
                              {r.dbName}
                              {r.dbNameCount > 1 && (
                                <span style={{ color: 'var(--text3)', marginLeft: 4 }}
                                      title={`This statement ran against ${r.dbNameCount} databases in this window; showing one of them.`}>
                                  +{r.dbNameCount - 1}
                                </span>
                              )}
                            </>
                          : undefined}
                      </DataTableCell>
                      {/* v0.10.943 — maxWidth yerleşim (sınıf karşılığı yok), satır içinde kalır. */}
                      <td {...dt.cellProps(r, 'statement', r.statement)} style={{ maxWidth: 540 }}>{r.statement}</td>
                      <DataTableCell dt={dt} col="count" row={r} value={fmtNum(r.count)} />
                      <DataTableCell dt={dt} col="avgMs" row={r} value={r.avgMs.toFixed(1)} />
                      <DataTableCell dt={dt} col="p50Ms" row={r} value={r.p50Ms.toFixed(1)} />
                      <DataTableCell dt={dt} col="p99Ms" row={r} value={r.p99Ms.toFixed(0)} />
                      <DataTableCell dt={dt} col="totalMs" row={r} value={totalLabel} className="cell-strong" />
                      <DataTableCell dt={dt} col="errorCount" row={r} value={fmtNum(r.errorCount)} />
                      {/* v0.10.652 (operatör) — trace araması her satırda, en sağda. */}
                      <DataTableCell dt={dt} col="traces" row={r}>
                        <Link to={slowQueryTracesHref(r, range)} onClick={e => e.stopPropagation()}
                          title="Bu sorguyu içeren trace'leri ara" style={{ fontSize: 11, whiteSpace: 'nowrap' }}>
                          Traces →
                        </Link>
                      </DataTableCell>
                    </tr>
                    {isExpanded && (
                      <tr key={key + ':sample'}>
                        {/* 12 = 1 (chevron) + 11 kolon (v0.10.652 Traces).
                            v0.10.973 — tablo standardı T6: genişletilmiş detay
                            hücresi `row-detail` (bg2 aynı; 12px 16px dolgu, üst
                            ayraç, satır sarar) — satır içi bg2 + 12px yerine. */}
                        <td colSpan={12} className="row-detail">
                          <div style={{
                            fontSize: 10, color: 'var(--text3)',
                            textTransform: 'uppercase', letterSpacing: 0.5,
                            marginBottom: 4,
                          }}>Real sample (literals shown)</div>
                          <pre style={{
                            margin: 0,
                            whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                            color: 'var(--text2)',
                          }}>{r.sampleStatement}</pre>
                          <div style={{ marginTop: 8, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', fontSize: 11, color: 'var(--text3)' }}>
                            <span>Max: {r.maxMs.toFixed(0)} ms · P95: {r.p95Ms.toFixed(0)} ms · P50: {r.p50Ms.toFixed(0)} ms</span>
                          </div>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
        {/* v0.10.1162 — tavan beyanı (v0.9.1018: "en yavaş 200, daha fazlası
            için daralt") yerine sunucu sayfalaması: 201. ifade de ulaşılır.
            v0.10.1525 — kolon sıralaması da sunucuda, tüm küme üzerinden. */}
        {rows && (page > 0 || hasMore) && (
          <Pager mode="offset" count="skip" page={page} pageSize={PAGE_SIZE} hasMore={hasMore} onPage={setPage} />
        )}

      </PageShell>
    </>
  );
}
