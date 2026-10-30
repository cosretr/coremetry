import { useMemo, useState } from 'react';
import { Badge, Button, DisclosureButton } from '@/components/ui';
import { KeyValue } from '@/components/coremetry/KeyValue';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { SafeHref } from '@/components/ui/SafeHref';
import { CodeSourceFooter } from '@/components/ai/CodeSourceFooter';
import { useOperationCardCode } from '@/lib/queries';
import { useT } from '@/lib/i18n';
import type { CallSite, MethodUsages, OpCallEdge } from '@/lib/types';
import {
  callSiteCols, callSiteKindGlyph, codeNote, dtoRows, fileTail, footerContext, matchedEdge, matchedLabel, usagesNote,
} from './opCardCode';

// OperationCardCodeSection — v0.10.1506 (plan-operation-card §6, R4): kartın
// "Kod" açılır bölümü. Kapalıyken İSTEK YOK (Azure DevOps okuması: ≤4 Code
// Search + ≤8 dosya); açılınca GET …/operation-card?…&code=1 bir kez,
// staleTime sunucu TTL'i, yoklama yok.
//
// Gösterilen: giriş metodunun İMZASI (dosya:satır DevOps linki), istek /
// cevap alan adı + tipi (KeyValue), çağrı yerleri (DataTable
// 'endpoint-callsites': dosya:satır, tür, alıcı.metot, koddaki literal
// rota/topic, eşleşen ağaç satırı → o satıra odak), kaynak satırı
// (CodeSourceFooter). Kod GÖVDESİ payload'da yok (Go operationCardCodeDTO);
// burada da çizilecek bir içerik alanı yok. Kod okunamadıysa dürüst not
// başta (Code Search kapalı / giriş bulunamadı / kısmi).
export function OperationCardCodeSection({ params, treeRows }: {
  params: Parameters<typeof useOperationCardCode>[0];
  treeRows: OpCallEdge[] | null | undefined;
}) {
  const tr = useT();
  const [open, setOpen] = useState(false);
  const q = useOperationCardCode(params, open);
  const code = q.data?.code ?? null;
  const rows = useMemo(() => code?.callSites ?? [], [code]);
  const columns = useMemo(() => callSiteCols(tr), [tr]);
  const dt = useDataTable<CallSite>({ storageKey: 'endpoint-callsites', columns, rows, persistSort: false });

  const note = code ? codeNote(code, tr) : '';
  const focusRow = (key: string) => {
    const el = document.querySelector<HTMLElement>(`[data-opcard-key="${CSS.escape(key)}"]`);
    if (!el) return;
    el.scrollIntoView?.({ block: 'center' });
    // Geçici vurgu: yeni CSS sınıfı yerine token'lı satır içi çerçeve.
    el.style.outline = '2px solid var(--accent)';
    window.setTimeout(() => { el.style.outline = ''; }, 1600);
  };
  const tableState: Omit<DataTableStateProps<CallSite>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error', message: tr('endpoint.opcard.code.error') }
    : { kind: 'empty', message: tr('endpoint.opcard.code.noCallSites') };

  return (
    <div data-opcard-slot="code" style={{ marginTop: 10, borderTop: '1px dashed var(--border)', paddingTop: 6 }}>
      <DisclosureButton anatomy="row" expanded={open} onClick={() => setOpen(v => !v)} data-testid="opcard-code-toggle">
        {tr('endpoint.opcard.code.title')}
      </DisclosureButton>
      {!open && (
        <div style={{ fontSize: 10.5, color: 'var(--text3)', marginTop: 4 }}>{tr('endpoint.opcard.code.hint')}</div>
      )}
      {open && (
        <div data-testid="opcard-code" style={{ marginTop: 8 }}>
          {q.isError && <div style={{ color: 'var(--text3)', fontSize: 11.5 }}>{tr('endpoint.opcard.code.error')}</div>}
          {note && (
            <div data-testid="opcard-code-note" style={{ fontSize: 11.5, color: 'var(--warn, var(--text2))', marginBottom: 8 }}>{note}</div>
          )}
          {code?.entry && (
            <div style={{ marginBottom: 10 }}>
              <div style={{ fontSize: 10.5, color: 'var(--text3)', marginBottom: 2 }}>{tr('endpoint.opcard.code.entry')}</div>
              <div className="mono" style={{ fontSize: 11.5, overflowWrap: 'anywhere' }} data-testid="opcard-code-sig">
                {code.entry.signature}
              </div>
              <div style={{ fontSize: 10.5, marginTop: 2 }}>
                <SafeHref href={code.entry.url} title={code.entry.path}
                  fallback={<span className="mono">{fileTail(code.entry.path)}:{code.entry.fromLine}</span>}>
                  <span className="mono">{fileTail(code.entry.path)}:{code.entry.fromLine}</span> ↗
                </SafeHref>
              </div>
            </div>
          )}
          {code?.entry && (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: 12, marginBottom: 10 }}>
              <div>
                <div style={{ fontSize: 10.5, color: 'var(--text3)', marginBottom: 2 }}>{tr('endpoint.opcard.code.request')}</div>
                {code.request.length > 0
                  ? <KeyValue items={dtoRows(code.request)} data-testid="opcard-code-request" />
                  : <div style={{ color: 'var(--text3)', fontSize: 11 }}>{tr('endpoint.opcard.code.noFields')}</div>}
              </div>
              <div>
                <div style={{ fontSize: 10.5, color: 'var(--text3)', marginBottom: 2 }}>{tr('endpoint.opcard.code.response')}</div>
                {code.response.length > 0
                  ? <KeyValue items={dtoRows(code.response)} data-testid="opcard-code-response" />
                  : <div style={{ color: 'var(--text3)', fontSize: 11 }}>{tr('endpoint.opcard.code.noFields')}</div>}
              </div>
            </div>
          )}
          {(q.isPending || q.isError || code?.entry) && (
            <div className="table-wrap">
              <table {...dt.tableProps}>
                <DataTableColgroup dt={dt} />
                <DataTableHead dt={dt} />
                <tbody>
                  {rows.length === 0 || q.isError ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(r => {
                    const m = matchedEdge(r, treeRows);
                    return (
                      <tr key={`${r.path}:${r.line}:${r.callee}`} data-kind={r.kind}>
                        <td title={`${r.path}:${r.line}`}>
                          <span className="dt-trunc mono">
                            <SafeHref href={r.url} fallback={<>{fileTail(r.path)}:{r.line}</>}>{fileTail(r.path)}:{r.line}</SafeHref>
                          </span>
                        </td>
                        <td><span aria-hidden="true" style={{ color: 'var(--text3)', marginRight: 4 }}>{callSiteKindGlyph(r.kind)}</span>{r.kind}</td>
                        <td title={r.callee}><span className="dt-trunc mono">{r.callee}</span></td>
                        <td title={r.target ?? ''}>
                          <span className="dt-trunc mono">{r.target || (r.service ? `@${r.service}` : '—')}</span>
                        </td>
                        <td>
                          {m
                            ? <Button variant="ghost" size="xs" title={tr('endpoint.opcard.code.focusRow')}
                                onClick={() => focusRow(m.key)}>
                                <span className="dt-trunc">{matchedLabel(m)}</span>
                              </Button>
                            : <span style={{ color: 'var(--text3)' }} title={tr('endpoint.opcard.code.unmatchedHint')}>{tr('endpoint.opcard.code.unmatched')}</span>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          {code?.entry && (
            <div style={{ fontSize: 10.5, color: 'var(--text3)', marginTop: 6 }}>{tr('endpoint.opcard.code.matchNote')}</div>
          )}
          {code?.usages && <MethodUsagesBlock u={code.usages} />}
          {code && <CodeSourceFooter code={footerContext(code)} />}
        </div>
      )}
    </div>
  );
}

// MethodUsagesBlock — v0.10.1509: "Bu metodu kim kullanıyor?" (Code Search
// ref:). ≤10 satırlık bir KONUM listesi (kayıt tablosu değil: sıralanmaz,
// sunucu kanıta göre sıraladı — çağıran servis deposu önce). Ad bazlı
// eşleşme olduğu için her satır "olası".
function MethodUsagesBlock({ u }: { u: MethodUsages }) {
  const tr = useT();
  const note = usagesNote(u, tr);
  return (
    <div data-testid="opcard-usages" style={{ marginTop: 12 }}>
      <div style={{ fontSize: 10.5, color: 'var(--text3)', marginBottom: 4 }}>
        {tr('endpoint.opcard.usages.title').replace('{m}', u.method)}
      </div>
      {note && <div data-testid="opcard-usages-note" style={{ fontSize: 11, color: 'var(--text3)' }}>{note}</div>}
      {u.hits.map(h => (
        <div key={`${h.repo}:${h.path}:${h.line ?? 0}`} data-usage-caller={h.caller ? '1' : undefined}
          style={{ display: 'flex', gap: 6, alignItems: 'baseline', fontSize: 11.5, padding: '2px 0', minWidth: 0 }}>
          <Badge title={tr('endpoint.opcard.usages.possibleHint')}>{tr('endpoint.opcard.usages.possible')}</Badge>
          {h.caller && <Badge tone="info" title={tr('endpoint.opcard.usages.callerHint')}>{tr('endpoint.opcard.usages.caller')}</Badge>}
          <strong style={{ whiteSpace: 'nowrap' }}>{h.repo}</strong>
          <span className="dt-trunc mono" title={`${h.path}${h.line ? `:${h.line}` : ''}`}>
            <SafeHref href={h.url} fallback={<>{fileTail(h.path)}{h.line ? `:${h.line}` : ''}</>}>
              {fileTail(h.path)}{h.line ? `:${h.line}` : ''} ↗
            </SafeHref>
          </span>
        </div>
      ))}
      {u.total > u.hits.length && (
        <div style={{ fontSize: 10.5, color: 'var(--text3)' }}>
          {tr('endpoint.opcard.usages.more').replace('{n}', String(u.total - u.hits.length))}
        </div>
      )}
    </div>
  );
}
