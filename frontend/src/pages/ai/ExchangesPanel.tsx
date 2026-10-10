import { useEffect, useMemo, useState, type CSSProperties } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api } from '@/lib/api';
import { rowActivation } from '@/lib/a11y';
import { timeRangeToNs, tsCompact, tsLong, fmtNum } from '@/lib/utils';
import type { AIExchange, AIExchangeCall, TimeRange } from '@/lib/types';
import { Drawer, DrawerSection } from '@/components/ui';
import { KeyValue } from '@/components/ui/KeyValue';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import {
  NO_LLM_LABEL, exchangeCallsSummary, exchangeFeedbackLabel, exchangeModelLabel,
  exchangeTierLabel, exchangeTokensLabel, exchangeUsedLLM,
} from './exchangeView';

// ExchangesPanel — v0.10.1153 (operatör, prod: "/ai her CoSRE etkileşimini
// göstermiyor"). Çağrı tablosu MODEL ÇAĞRISI başına bir satırdı: LLM'siz
// cevaplar (/help, "bunu mu demek istedin", "Wikide bulunamadı", guided
// şablonları) hiç görünmüyor, bir turun anlatım + seçim + sınıflandırıcı
// çağrıları ilişkisiz satırlar olarak dağılıyordu. Bu panel KULLANICI TURU
// başına bir satır (GET /api/ai/exchanges): soru, kademe/rota, Derin, model,
// süre, token (LLM kullanıldıysa), sonuç, 👍/👎. Satır → çekmece: cevap ve
// turun alt çağrıları; alt çağrı → mevcut çağrı çekmecesi (onOpenCall).
//
// Sunucu LIMIT 200 (kayıt listesi, useDataTable; satır > 100 olabilir →
// content-visibility). Çekmece seçimi URL'de (?exchange=, replace) — liste
// pencere değişince yeniden okunur, seçili tur listede yoksa çekmece kapanır.

const LIMIT = 200;

// Örnek blokları — çağrı çekmecesinin (CallDrawer) <pre> görünümüyle aynı.
const SAMPLE_STYLE: CSSProperties = {
  margin: 0, fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
  maxHeight: 280, overflowY: 'auto', background: 'var(--bg2)', padding: 10,
  borderRadius: 4, border: '1px solid var(--border)',
};

const COLS: ColumnDef<AIExchange>[] = [
  { id: 'time', label: 'Zaman', sortValue: x => x.createdAt, width: 120, minWidth: 96, priority: 3, tone: () => 'muted' },
  { id: 'question', label: 'Soru', sortValue: x => x.question, naturalDir: 'asc', flex: true, priority: 1 },
  { id: 'tier', label: 'Kademe', sortValue: x => exchangeTierLabel(x), naturalDir: 'asc', width: 190, minWidth: 120, priority: 1 },
  { id: 'model', label: 'Model', sortValue: x => exchangeModelLabel(x), naturalDir: 'asc', width: 170, minWidth: 100, priority: 2,
    tone: x => (exchangeUsedLLM(x) ? undefined : 'faint') },
  { id: 'duration', label: 'Süre', sortValue: x => x.durationMs, numeric: true, width: 90, minWidth: 70, priority: 2 },
  { id: 'tokens', label: 'Token (gir / çık)', sortValue: x => x.inputTokens + x.outputTokens, numeric: true, width: 130, minWidth: 90, priority: 2, tone: () => 'faint' },
  { id: 'status', label: 'Sonuç', sortValue: x => x.status, naturalDir: 'asc', width: 85, minWidth: 70, priority: 1 },
  { id: 'feedback', label: 'Oy', sortValue: x => x.feedback ?? 0, numeric: true, width: 60, minWidth: 50, priority: 2 },
  { id: 'user', label: 'Kullanıcı', sortValue: x => x.userEmail || x.userId || '', naturalDir: 'asc', width: 170, minWidth: 100, priority: 3, tone: () => 'faint' },
];

const CALL_COLS: ColumnDef<AIExchangeCall>[] = [
  { id: 'surface', label: 'Yüzey', sortValue: c => c.surface, naturalDir: 'asc', flex: true, mono: true, priority: 1 },
  { id: 'model', label: 'Model', sortValue: c => c.model, naturalDir: 'asc', width: 140, minWidth: 90, priority: 2, tone: () => 'muted' },
  { id: 'duration', label: 'Süre', sortValue: c => c.durationMs, numeric: true, width: 80, minWidth: 64, priority: 2 },
  { id: 'tokens', label: 'Token', sortValue: c => c.inputTokens + c.outputTokens, numeric: true, width: 100, minWidth: 80, priority: 2, tone: () => 'faint' },
  { id: 'status', label: 'Durum', sortValue: c => c.status, naturalDir: 'asc', width: 80, minWidth: 64, priority: 1 },
];

export function ExchangesPanel({ range, onOpenCall }: { range: TimeRange; onOpenCall: (id: string) => void }) {
  const [rows, setRows] = useState<AIExchange[] | null | undefined>(undefined);
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get('exchange') ?? '';
  const setSelected = (id: string) => setSearchParams(prev => {
    const p = new URLSearchParams(prev);
    if (id) p.set('exchange', id);
    else p.delete('exchange');
    return p;
  }, { replace: true });

  useEffect(() => {
    let cancelled = false;
    setRows(undefined);
    const { from, to } = timeRangeToNs(range);
    api.aiExchanges({ from, to, limit: LIMIT })
      .then(r => { if (!cancelled) setRows(r ?? []); })
      .catch(() => { if (!cancelled) setRows(null); });
    return () => { cancelled = true; };
  }, [range]);

  const dt = useDataTable<AIExchange>({
    storageKey: 'ai-exchanges', columns: COLS, rows: rows ?? [],
    initialSort: { id: 'time', dir: 'desc' },
  });
  const state: Omit<DataTableStateProps<AIExchange>, 'dt'> =
    rows === undefined ? { kind: 'loading' }
    : rows === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Bu pencerede CoSRE etkileşimi yok — CoSRE penceresinde ya da bir AI çekmecesinde soru sorulunca burada görünür.' };
  const noLLM = useMemo(() => (rows ?? []).filter(x => !exchangeUsedLLM(x)).length, [rows]);
  const selected = selectedId ? (rows ?? []).find(x => x.exchangeId === selectedId) : undefined;

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>CoSRE etkileşimleri</h3>
        <span className="ov-sub">
          her kullanıcı turu bir satır — LLM'siz cevaplar dahil; alt çağrılar satırın çekmecesinde
        </span>
        {rows && rows.length > 0 && (
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--text3)' }}>
            {fmtNum(rows.length)} tur · {fmtNum(noLLM)} {NO_LLM_LABEL}
          </span>
        )}
      </div>
      <div className="ov-card-b">
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(x => (
                <tr key={x.exchangeId} className="cv-row" {...rowActivation(() => setSelected(x.exchangeId))}>
                  <DataTableCell dt={dt} col="time" row={x} value={tsCompact(x.createdAt)} title={tsLong(x.createdAt)} />
                  <DataTableCell dt={dt} col="question" row={x} value={x.question || null} />
                  <DataTableCell dt={dt} col="tier" row={x}>
                    {exchangeTierLabel(x)}{x.deep && <span className="badge b-info" style={{ marginLeft: 6 }}>derin</span>}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="model" row={x} value={exchangeModelLabel(x)} />
                  <DataTableCell dt={dt} col="duration" row={x} value={`${fmtNum(x.durationMs)} ms`} />
                  <DataTableCell dt={dt} col="tokens" row={x} value={exchangeTokensLabel(x)} />
                  <DataTableCell dt={dt} col="status" row={x}>
                    {x.status === 'ok'
                      ? <span className="badge b-gray">ok</span>
                      : <span className="badge b-err" title={x.errorMsg}>hata</span>}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="feedback" row={x} value={exchangeFeedbackLabel(x)} />
                  <DataTableCell dt={dt} col="user" row={x} value={x.userEmail || x.userId || null} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {rows && rows.length >= LIMIT && (
          <div className="pager" style={{ color: 'var(--text3)' }}>
            son {LIMIT} tur gösteriliyor — tavana dayandı; daha fazlası için pencereyi daraltın
          </div>
        )}
      </div>
      {selected && <ExchangeDrawer ex={selected} onClose={() => setSelected('')} onOpenCall={onOpenCall} />}
    </div>
  );
}

function ExchangeDrawer({ ex, onClose, onOpenCall }: { ex: AIExchange; onClose: () => void; onOpenCall: (id: string) => void }) {
  const callDt = useDataTable<AIExchangeCall>({ storageKey: 'ai-exchange-calls', columns: CALL_COLS, rows: ex.calls ?? [] });
  return (
    <Drawer onClose={onClose} width={680} header={
      <>
        <span className={`badge ${ex.status === 'ok' ? 'b-gray' : 'b-err'}`}>{ex.status === 'ok' ? 'ok' : 'hata'}</span>
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
          { k: 'Kullanıcı', v: ex.userEmail || ex.userId || null },
          { k: 'Exchange', v: ex.exchangeId, mono: true },
        ]} />
        {ex.errorMsg && (
          <DrawerSection title="Hata">
            <pre style={{ ...SAMPLE_STYLE, color: 'var(--err)' }}>{ex.errorMsg}</pre>
          </DrawerSection>
        )}
        <DrawerSection title="Soru">
          <pre style={SAMPLE_STYLE}>{ex.question || '(boş)'}</pre>
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
                      <DataTableCell dt={callDt} col="surface" row={c} value={c.llm ? c.surface : `${c.surface} (işaret)`} />
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
