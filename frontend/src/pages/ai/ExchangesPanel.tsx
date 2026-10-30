import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { rowActivation } from '@/lib/a11y';
import { timeRangeToNs, tsCompact, tsLong, fmtNum } from '@/lib/utils';
import type { AIExchange, AIExchangeCall, AIExchangeFeedbackFilter, TimeRange } from '@/lib/types';
import { Drawer, DrawerSection, SegmentedControl } from '@/components/ui';
import { Button } from '@/components/shadcn/button';
import { Pager } from '@/components/Pager';
import { PageControls } from '@/components/coremetry/PageControls';
import { Input } from '@/components/shadcn/input';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { KeyValue } from '@/components/coremetry/KeyValue';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import {
  NO_LLM_LABEL, EXCHANGE_FEEDBACK_FILTERS, EXCHANGE_TIER_OPTIONS, EXCHANGES_CSV_MAX_ROWS, EXCHANGES_MAX_OFFSET, EXCHANGES_PAGE_SIZE,
  aiExchangesCSVParams, aiExchangesQueryKey, aiExchangesRequestParams,
  exchangeCallsSummary, exchangeConversationHref, exchangeFeedbackCounts, exchangeFeedbackLabel,
  exchangeModelLabel, exchangeStatusBadge, exchangeTierLabel, exchangeTokensLabel, exchangeUsedLLM,
  exchangesQueryParams, exchangesUrlSig, readExchangesUrlState, writeExchangesUrlState,
  type ExchangesUrlState,
  EXCHANGE_SUBJECT_LABELS, exchangeSubjectRef,
} from './exchangeView';
import { ExchangeUserPicker } from './ExchangeUserPicker';
import { CopyButton } from '@/components/CopyButton';
import { traceHref } from '@/lib/traceHref';

// ExchangesPanel — v0.10.1153 (operatör, prod: "/ai her CoSRE etkileşimini
// göstermiyor"). Çağrı tablosu MODEL ÇAĞRISI başına bir satırdı: LLM'siz
// cevaplar (/help, "bunu mu demek istedin", "Wikide bulunamadı", guided
// şablonları) hiç görünmüyor, bir turun anlatım + seçim + sınıflandırıcı
// çağrıları ilişkisiz satırlar olarak dağılıyordu. Bu panel KULLANICI TURU
// başına bir satır (GET /api/ai/exchanges): soru, kademe/rota, Derin, model,
// süre, token (LLM kullanıldıysa), sonuç, 👍/👎. Satır → çekmece: cevap ve
// turun alt çağrıları; alt çağrı → mevcut çağrı çekmecesi (onOpenCall).
//
// Çekmece seçimi URL'de (?exchange=, replace) — liste pencere değişince
// yeniden okunur, seçili tur sayfada yoksa çekmece kapanır.
//
// v0.10.1280 (CoSRE adım 8) — 👍/👎 süzgeci (`?fb=down` = operatörün inceleme
// kuyruğu; sunucu tarafında, LIMIT'ten önce), görünen satırların 👍/👎 sayımı,
// 👎'nin hazır nedeni, oyun bağlandığı alt çağrı ve konuşma arşivi bağı.
// Sorgu anahtarı isteğin TÜM girdilerinden (aiExchangesQueryKey).
//
// v0.10.1646 (operatör onaylı) — pencerenin TÜM soruları: eski LIMIT 200
// eski turları gizliyordu. Sunucu sayfası (Pager offset, kesin toplam) +
// soru metninde arama (300 ms debounce, sunucu) + kullanıcı (sunucu
// taraflı seçici) ve kademe süzgeci + süzgeçli CSV (sunucu, 50.000 satır
// tavanı). Hepsi URL'de (fb/xq/xuser/xtier/xpage, replace; içe alma
// imzalı). Sıra sunucuda sabit (en yeni önce) → tablo YALNIZ boyutlanır
// (sunucu sayfalı tablo kuralı: bir sayfanın içini istemcide sıralamak
// yanıltır). Kolonlar ve çekmece aynı.

// Örnek blokları — çağrı çekmecesinin (CallDrawer) <pre> görünümüyle aynı.
const SAMPLE_STYLE: CSSProperties = {
  margin: 0, fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
  maxHeight: 280, overflowY: 'auto', background: 'var(--bg2)', padding: 10,
  borderRadius: 4, border: '1px solid var(--border)',
};

// v0.10.1646 — sortValue YOK: sıra sunucuda (created_at ↓), tablo resize-only.
const COLS: ColumnDef<AIExchange>[] = [
  { id: 'time', label: 'Zaman', width: 120, minWidth: 96, priority: 3, tone: () => 'muted' },
  { id: 'question', label: 'Soru', flex: true, priority: 1 },
  { id: 'tier', label: 'Kademe', width: 190, minWidth: 120, priority: 1 },
  { id: 'model', label: 'Model', width: 170, minWidth: 100, priority: 2,
    tone: x => (exchangeUsedLLM(x) ? undefined : 'faint') },
  { id: 'duration', label: 'Süre', numeric: true, width: 90, minWidth: 70, priority: 2 },
  { id: 'tokens', label: 'Token (gir / çık)', numeric: true, width: 130, minWidth: 90, priority: 2, tone: () => 'faint' },
  { id: 'status', label: 'Sonuç', width: 85, minWidth: 70, priority: 1 },
  { id: 'feedback', label: 'Oy', numeric: true, width: 100, minWidth: 60, priority: 2 },
  { id: 'user', label: 'Kullanıcı', width: 170, minWidth: 100, priority: 3, tone: () => 'faint' },
];

const CALL_COLS: ColumnDef<AIExchangeCall>[] = [
  { id: 'surface', label: 'Yüzey', sortValue: c => c.surface, naturalDir: 'asc', flex: true, mono: true, priority: 1 },
  { id: 'model', label: 'Model', sortValue: c => c.model, naturalDir: 'asc', width: 140, minWidth: 90, priority: 2, tone: () => 'muted' },
  { id: 'duration', label: 'Süre', sortValue: c => c.durationMs, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'tokens', label: 'Token', sortValue: c => c.inputTokens + c.outputTokens, numeric: true, width: 100, minWidth: 80, priority: 2, tone: () => 'faint' },
  { id: 'status', label: 'Durum', sortValue: c => c.status, naturalDir: 'asc', width: 80, minWidth: 64, priority: 1 },
];

const SEARCH_DEBOUNCE_MS = 300;
// CSV tavanı tam sayıyla (fmtNum kısaltır: "50.0K" bir tavan beyanı için belirsiz).
const CSV_CAP_LABEL = EXCHANGES_CSV_MAX_ROWS.toLocaleString('tr-TR');

export function ExchangesPanel({ range, onOpenCall }: { range: TimeRange; onOpenCall: (id: string) => void }) {
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get('exchange') ?? '';
  const st = readExchangesUrlState(searchParams);
  const setSelected = (id: string) => setSearchParams(prev => {
    const p = new URLSearchParams(prev);
    if (id) p.set('exchange', id);
    else p.delete('exchange');
    return p;
  }, { replace: true });
  // v0.10.1280 / v0.10.1646 — panelin tüm seçimleri URL'de (paylaşılan link
  // aynı görünümü açar); süzgeç değişince sayfa başa döner.
  const setUrl = (patch: Partial<ExchangesUrlState>) =>
    setSearchParams(prev => writeExchangesUrlState(prev, patch), { replace: true });
  const setFeedback = (v: AIExchangeFeedbackFilter) => setUrl({ feedback: v });

  // Arama + kullanıcı taslakları: yazarken yerel, URL'e debounce / seçimle.
  // URL→taslak içe alma İMZALI (v0.8.253): yalnız panelin parametreleri
  // değişince — aralık ya da çekmece yazımı yazılmakta olan metni ezmez.
  const [qText, setQText] = useState(st.q);
  const [userText, setUserText] = useState(st.user);
  const sig = exchangesUrlSig(searchParams);
  const lastSigRef = useRef(sig);
  useEffect(() => {
    if (sig === lastSigRef.current) return;
    lastSigRef.current = sig;
    const cur = readExchangesUrlState(searchParams);
    setQText(cur.q);
    setUserText(cur.user);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig]);
  const qTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(qTimer.current), []);
  const onSearch = (v: string) => {
    setQText(v);
    window.clearTimeout(qTimer.current);
    qTimer.current = window.setTimeout(() => setUrl({ q: v.trim() }), SEARCH_DEBOUNCE_MS);
  };
  const clearFilters = () => {
    window.clearTimeout(qTimer.current);
    setQText('');
    setUserText('');
    setUrl({ q: '', user: '', tier: '', feedback: '' });
  };

  // timeRangeToNs yalnız memo içinde (v0.5.184): pencere değişmedikçe sabit.
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const params = useMemo(
    () => exchangesQueryParams(win, { feedback: st.feedback, q: st.q, user: st.user, tier: st.tier, page: st.page }),
    [win, st.feedback, st.q, st.user, st.tier, st.page]);
  const q = useQuery({
    queryKey: aiExchangesQueryKey(params),
    queryFn: () => api.aiExchanges(aiExchangesRequestParams(params)),
    staleTime: 15_000,
  });
  const rows: AIExchange[] | null | undefined = useMemo(
    () => (q.isPending ? undefined : q.isError ? null : q.data?.rows ?? []),
    [q.isPending, q.isError, q.data]);
  const total = q.data?.total ?? 0;
  const hasMore = q.data?.hasMore ?? false;
  const filtered = !!(st.feedback || st.q || st.user || st.tier);

  const dt = useDataTable<AIExchange>({ storageKey: 'ai-exchanges', columns: COLS, rows: rows ?? [] });
  const state: Omit<DataTableStateProps<AIExchange>, 'dt'> =
    rows === undefined ? { kind: 'loading' }
    : rows === null ? { kind: 'error' }
    : st.page > 0 && total > 0
      ? { kind: 'empty', message: 'Bu sayfa boş — ilk sayfaya dönün.' }
    : filtered
      ? { kind: 'empty', message: 'Bu pencerede süzgece uyan CoSRE turu yok — süzgeçleri temizleyin ya da pencereyi genişletin.' }
      : { kind: 'empty', message: 'Bu pencerede CoSRE etkileşimi yok — CoSRE penceresinde ya da bir AI çekmecesinde soru sorulunca burada görünür.' };
  const noLLM = useMemo(() => (rows ?? []).filter(x => !exchangeUsedLLM(x)).length, [rows]);
  const votes = useMemo(() => exchangeFeedbackCounts(rows ?? []), [rows]);
  const selected = selectedId ? (rows ?? []).find(x => x.exchangeId === selectedId) : undefined;
  // Son sayfa yalnız KESİN ve sunucu ofset tavanı içinde ulaşılabilirken (Pager sözleşmesi).
  const lastPageRaw = total > 0 ? Math.ceil(total / EXCHANGES_PAGE_SIZE) - 1 : -1;
  const lastPage = lastPageRaw >= 0 && lastPageRaw * EXCHANGES_PAGE_SIZE <= EXCHANGES_MAX_OFFSET ? lastPageRaw : undefined;

  const [csvBusy, setCsvBusy] = useState(false);
  const [csvErr, setCsvErr] = useState<string | null>(null);
  const downloadCSV = () => {
    setCsvBusy(true);
    setCsvErr(null);
    api.aiExchangesCSV(aiExchangesCSVParams(params))
      .catch((e: unknown) => setCsvErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setCsvBusy(false));
  };

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>CoSRE etkileşimleri</h3>
        <span className="ov-sub">
          her kullanıcı turu bir satır — LLM'siz cevaplar dahil; alt çağrılar satırın çekmecesinde
        </span>
        <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 10, alignItems: 'center' }}>
          {rows && rows.length > 0 && (
            <span style={{ fontSize: 11, color: 'var(--text3)' }}
              title="Toplam: süzgece uyan bütün turlar. LLM yok ve 👍/👎 sayımı bu sayfanın satırları. v0.10.1393 — AI cevaplarında 👍/👎 artık sorulmuyor; sayımlar yalnız geçmiş oylar (yeni turlarda Oy '—')">
              {fmtNum(total)} tur · sayfada {fmtNum(noLLM)} {NO_LLM_LABEL} · 👍 {fmtNum(votes.up)} · 👎 {fmtNum(votes.down)}
            </span>
          )}
          <SegmentedControl size="sm" aria-label="Geri bildirim süzgeci"
            value={st.feedback} onChange={setFeedback} options={EXCHANGE_FEEDBACK_FILTERS} />
        </span>
      </div>
      <div className="ov-card-b">
        <PageControls className="ai-exch-controls" actions={
          <>
            {csvErr && <span className="ai-exch-csv-err" title={csvErr}>CSV indirilemedi</span>}
            <Button variant="secondary" size="sm" onClick={downloadCSV} loading={csvBusy}
              disabled={!rows || total === 0}
              title={`Süzgece uyan turları CSV olarak indir (en çok ${CSV_CAP_LABEL} satır, en yeni önce; fazlası kesilir)`}>
              {`↓ CSV (en çok ${CSV_CAP_LABEL})`}
            </Button>
          </>
        }>
          <Input type="search" className="ai-exch-search" value={qText} onChange={e => onSearch(e.target.value)}
            placeholder="Soru içinde ara…" aria-label="Soru içinde ara" />
          <ExchangeUserPicker value={userText} onChange={setUserText}
            onCommit={v => setUrl({ user: v })} from={win.from} to={win.to} />
          {/* Kapalı küme (aisurface kademeleri, ~15) — katalog değil, sunucu araması gerekmez. */}
          <NativeSelect size="sm" className="ai-exch-tier" value={st.tier} onChange={e => setUrl({ tier: e.target.value })}
            aria-label="Kademe süzgeci">
            {EXCHANGE_TIER_OPTIONS.map(o => <NativeSelectOption key={o.value} value={o.value}>{o.label}</NativeSelectOption>)}
          </NativeSelect>
          {filtered && (
            <Button variant="secondary" size="sm" onClick={clearFilters}>Temizle</Button>
          )}
        </PageControls>
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(x => (
                <tr key={x.exchangeId} className="cv-row" {...rowActivation(() => setSelected(x.exchangeId))}>
                  <DataTableCell dt={dt} col="time" row={x} value={tsCompact(x.createdAt)} title={tsLong(x.createdAt)} />
                  {/* v0.10.1645 — satır kompakt kalır; title açıklanan öznenin TAM kimliğini taşır */}
                  <DataTableCell dt={dt} col="question" row={x} value={x.question || null} title={exchangeSubjectRef(x)?.question || x.question || undefined} />
                  <DataTableCell dt={dt} col="tier" row={x}>
                    {exchangeTierLabel(x)}{x.deep && <span className="badge b-info" style={{ marginLeft: 6 }}>derin</span>}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="model" row={x} value={exchangeModelLabel(x)} />
                  <DataTableCell dt={dt} col="duration" row={x} value={`${fmtNum(x.durationMs)} ms`} />
                  <DataTableCell dt={dt} col="tokens" row={x} value={exchangeTokensLabel(x)} />
                  <DataTableCell dt={dt} col="status" row={x}>
                    <StatusBadge ex={x} />
                  </DataTableCell>
                  <DataTableCell dt={dt} col="feedback" row={x} value={exchangeFeedbackLabel(x)} />
                  <DataTableCell dt={dt} col="user" row={x} value={x.userEmail || x.userId || null} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {(st.page > 0 || hasMore) && (
          <Pager mode="offset" count="exact" total={total} page={st.page} pageSize={EXCHANGES_PAGE_SIZE}
            hasMore={hasMore} onPage={p => setUrl({ page: p })} lastReachablePage={lastPage} />
        )}
      </div>
      {selected && <ExchangeDrawer ex={selected} onClose={() => setSelected('')} onOpenCall={onOpenCall} />}
    </div>
  );
}

function StatusBadge({ ex }: { ex: AIExchange }) {
  const b = exchangeStatusBadge(ex);
  return <span className={`badge ${b.cls}`} title={ex.errorMsg || undefined}>{b.label}</span>;
}

function ExchangeDrawer({ ex, onClose, onOpenCall }: { ex: AIExchange; onClose: () => void; onOpenCall: (id: string) => void }) {
  const callDt = useDataTable<AIExchangeCall>({ storageKey: 'ai-exchange-calls', columns: CALL_COLS, rows: ex.calls ?? [] });
  const convHref = exchangeConversationHref(ex);
  // v0.10.1645 — açıklanan öznenin tam kimliği (eski satırda bağlam kimliğinden).
  const subj = exchangeSubjectRef(ex);
  return (
    <Drawer onClose={onClose} width={680} header={
      <>
        <StatusBadge ex={ex} />
        <span style={{ fontWeight: 700, fontSize: 13 }}>{exchangeTierLabel(ex)}</span>
        {!exchangeUsedLLM(ex) && <span className="badge b-gray">{NO_LLM_LABEL}</span>}
      </>
    }>
      <div style={{ paddingTop: 10, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <KeyValue items={[
          { k: 'Zaman', v: tsLong(ex.createdAt) },
          { k: 'Kademe', v: exchangeTierLabel(ex) },
          { k: 'Derin düşün', v: ex.deep ? 'açık' : 'kapalı' },
          { k: 'Model', v: exchangeModelLabel(ex) },
          { k: 'Süre', v: `${fmtNum(ex.durationMs)} ms` },
          { k: 'Token (gir / çık)', v: exchangeTokensLabel(ex) },
          { k: 'Geri bildirim', v: ex.feedbackComment ? `${exchangeFeedbackLabel(ex)} — ${ex.feedbackComment}` : exchangeFeedbackLabel(ex) },
          // v0.10.1280 — oyun bağlandığı ai_calls satırı (boş = exchange düzeyinde oy).
          { k: 'Oylanan çağrı', v: ex.feedbackCallId || null, mono: true },
          { k: 'Kullanıcı', v: ex.userEmail || ex.userId || null },
          {
            k: 'Konuşma',
            v: convHref
              ? <Link to={convHref} title="Konuşma arşivi yalnız sahibine açılır">{ex.turn ? `${ex.turn}. tur · arşivi aç` : 'arşivi aç'}</Link>
              : null,
          },
          { k: 'Exchange', v: ex.exchangeId, mono: true },
          ...(subj ? [{
            k: EXCHANGE_SUBJECT_LABELS[subj.kind],
            v: !subj.full
              ? <span title="Bu eski kayıtta tam kimlik saklanmamış">{subj.id}</span>
              : subj.kind === 'trace'
                ? <Link to={traceHref(subj.id)} title="Açıklanan trace'i aç">{subj.id}</Link>
                : subj.id,
            mono: true,
            actions: subj.full ? <CopyButton value={subj.id} title="Kimliği kopyala" /> : undefined,
          }] : []),
        ]} />
        {ex.errorMsg && (
          <DrawerSection title="Hata">
            <pre style={{ ...SAMPLE_STYLE, color: 'var(--err)' }}>{ex.errorMsg}</pre>
          </DrawerSection>
        )}
        <DrawerSection title="Soru">
          <pre style={SAMPLE_STYLE}>{subj?.question || ex.question || '(boş)'}</pre>
        </DrawerSection>
        <DrawerSection title="Cevap (örnek)">
          <pre style={SAMPLE_STYLE}>{ex.answer || '(boş)'}</pre>
        </DrawerSection>
        <DrawerSection title={`Alt çağrılar — ${exchangeCallsSummary(ex)}`}>
          <div className="table-wrap">
            <table {...callDt.tableProps}>
              <DataTableColgroup dt={callDt} />
              <DataTableHead dt={callDt} />
              <tbody>
                {callDt.sortedRows.length === 0
                  ? <DataTableState dt={callDt} kind="empty" message={`${NO_LLM_LABEL} — bu tur model çağırmadan cevaplandı.`} />
                  : callDt.sortedRows.map(c => (
                    <tr key={c.id} className="cv-row" {...rowActivation(() => onOpenCall(c.id))}>
                      <DataTableCell dt={callDt} col="surface" row={c}>
                        {c.llm ? c.surface : `${c.surface} (işaret)`}
                        {/* v0.10.1280 — oyun bağlandığı çağrı (cevabı üreten satır) */}
                        {ex.feedbackCallId === c.id && ex.feedback && (
                          <span className={`badge ${ex.feedback === -1 ? 'b-err' : 'b-ok'}`} style={{ marginLeft: 6 }}
                            title="Geri bildirim bu çağrıya bağlı">{exchangeFeedbackLabel(ex)}</span>
                        )}
                      </DataTableCell>
                      <DataTableCell dt={callDt} col="model" row={c} value={c.model || null} />
                      <DataTableCell dt={callDt} col="duration" row={c} value={`${fmtNum(c.durationMs)} ms`} />
                      <DataTableCell dt={callDt} col="tokens" row={c} value={c.llm ? `${c.inputTokens} / ${c.outputTokens}` : '—'} />
                      <DataTableCell dt={callDt} col="status" row={c}>
                        {c.status === 'error' ? <span className="badge b-err">error</span> : <span className="badge b-gray">{c.status || 'ok'}</span>}
                      </DataTableCell>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </DrawerSection>
      </div>
    </Drawer>
  );
}
