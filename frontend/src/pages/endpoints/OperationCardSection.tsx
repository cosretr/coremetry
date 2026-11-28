import { useEffect, useMemo, useRef } from 'react';
import { Link, useLocation } from 'react-router';
import { Badge, Card, PanelTitle } from '@/components/ui';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useOperationCard } from '@/lib/queries';
import { useT } from '@/lib/i18n';
import { fmtNum } from '@/lib/utils';
import { traceHref } from '@/lib/traceHref';
import type { OpCallEdge } from '@/lib/types';
import type { EndpointRef } from './endpointParam';
import { OPCARD_ANCHOR } from './links';
import { OperationCardCodeSection } from './OperationCardCodeSection';
import {
  opCallTreeCols, targetLabel, kindGlyph, errBadgeClass, indentPx, windowEdgesLine,
} from './opCallTreeCols';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

// OperationCardSection — v0.10.1491 (plan-operation-card §2.3.1 + §6, R3):
// /endpoint sayfasının "Kod ve çağrılar" bölümü. Trace yarısı: son ≤200
// trace'ten derinlik ≤3 çağrı ağacı (hedef · op · protokol · çağrı · hata %
// · p95 · örnek trace), başlıkta op kodu / sürüm / iş kodları, dipte MV'den
// pencere geneli servis→servis çağrı sayısı.
//
// Veri: GET /api/endpoints/operation-card — sayfa açılışında BİR KEZ
// (downstream / callers kardeşleri gibi), yoklama yok, staleTime = sunucu
// TTL'i (60 sn). Liste satırlarında ÖN-GETİRME YOK: Trace çipi ve
// Operations eylemi bu sayfaya #opcard çapasıyla gelir.
//
// Durumlar tablonun İÇİNDE (T12): yükleniyor / hata / boş (iki ayrı boş:
// örneklemde trace yok ≠ trace var ama alt çağrı yok).
//
// R4 (v0.10.1506): kod yarısı (giriş metodu imzası, istek/cevap alan adları
// + tipleri, çağrı yerleri) kartın altındaki "Kod" açılır bölümünde
// (OperationCardCodeSection, `data-opcard-slot="code"`); açılmadan istek yok.
export function OperationCardSection({ refObj, entry, from, to, env, cluster }: {
  refObj: EndpointRef; entry?: 'rpc'; from: number; to: number; env?: string; cluster?: string;
}) {
  const tr = useT();
  const params = useMemo(() => ({
    service: refObj.service, path: refObj.path, from, to,
    ...(refObj.sig ? { sig: '1' as const } : {}),
    ...(entry ? { entry } : {}),
    ...(env ? { env } : {}),
    ...(cluster ? { cluster } : {}),
  }), [refObj.service, refObj.path, refObj.sig, from, to, entry, env, cluster]);
  const q = useOperationCard(params);
  const d = q.data;
  const tree = d?.tree ?? null;
  const rows = useMemo(() => tree?.children ?? [], [tree]);
  const columns = useMemo(() => opCallTreeCols(tr), [tr]);
  const dt = useDataTable<OpCallEdge>({
    storageKey: 'endpoint-calltree',
    columns,
    rows,
    persistSort: false,
  });

  // #opcard çapası: bölüm veri gelince çizildiği için tarayıcının kendi
  // çapa kaydırması kaçar — bir kez elle kaydır.
  const { hash } = useLocation();
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const scrolled = useRef(false);
  useEffect(() => {
    if (hash !== `#${OPCARD_ANCHOR}` || scrolled.current || !anchorRef.current) return;
    scrolled.current = true;
    anchorRef.current.scrollIntoView?.({ block: 'start' });
  }, [hash, d]);

  const showRows = !q.isError && !!tree && rows.length > 0;
  const tableState: Omit<DataTableStateProps<OpCallEdge>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error', message: tr('endpoint.opcard.error') }
    : !tree ? { kind: 'error', message: d?.treeError || tr('endpoint.opcard.error') }
    : tree.sampledTraces === 0 ? { kind: 'empty', message: tr('endpoint.opcard.noSample') }
    : { kind: 'empty', message: tr('endpoint.opcard.empty') };

  const sub = tree && tree.sampledTraces > 0
    ? tr('endpoint.opcard.sub').replace('{n}', fmtNum(tree.sampledTraces))
      + (tree.sampleOrder === 'random' ? ` · ${tr('endpoint.opcard.random')}` : ` · ${tr('endpoint.opcard.slowest')}`)
    : undefined;
  const range = { fromNs: from, toNs: to };

  return (
    <div id={OPCARD_ANCHOR} ref={anchorRef} data-testid="opcard">
      <Card header={<PanelTitle sub={sub}>{tr('endpoint.opcard.title')}</PanelTitle>}>
        {tree && !q.isError && (tree.operationCode || tree.version || tree.codes.length > 0) && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
            {tree.operationCode && (
              <Badge tone="info" title={tr('endpoint.opcard.opCode')}>
                <span className="mono">{tree.operationCode}</span>
              </Badge>
            )}
            {tree.version && (
              <Badge title={tr('endpoint.opcard.version')}>v{tree.version}</Badge>
            )}
            {tree.codes.map(c => (
              <Badge key={`${c.kind}:${c.code}`}
                title={`${c.kind === 'function_code' ? 'FUNCTION_CODE' : 'CHANNEL_CODE'} · ${fmtNum(c.count)}`}>
                <span className="mono">{c.code}</span>
              </Badge>
            ))}
          </div>
        )}
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {!showRows ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(r => (
                <tr key={r.key} data-depth={r.depth} data-opcard-key={r.key}>
                  <td title={r.target}>
                    <span className="dt-trunc" style={{ paddingLeft: indentPx(r.depth) }}>
                      <span aria-hidden="true" style={{ color: 'var(--text3)', marginRight: 6 }}>
                        {r.depth > 1 ? '└ ' : ''}{kindGlyph(r.kind)}
                      </span>
                      {r.kind === 'service'
                        ? <EntityLink to={{ kind: 'service', name: r.target, opts: { range } }}>{r.target}</EntityLink>
                        : <span>{targetLabel(r.target)}</span>}
                    </span>
                  </td>
                  <td title={r.op}><span className="dt-trunc mono">{r.op || '—'}</span></td>
                  <td>{r.protocol || '—'}</td>
                  <td className="num">{fmtNum(r.calls)}</td>
                  <td className="num">
                    <span className={`badge ${errBadgeClass(r.errPct)}`}>{r.errPct.toFixed(1)}%</span>
                  </td>
                  <td className="num">{r.p95Ms.toFixed(1)} ms</td>
                  <td>
                    {r.sampleTraceIds.map((id, i) => (
                      <Link key={id} to={traceHref(id)} title={id} style={{ marginRight: 6 }}>
                        #{i + 1}
                      </Link>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {tree && !q.isError && (
          <div style={{ fontSize: 10.5, color: 'var(--text3)', marginTop: 8, lineHeight: 1.5 }}>
            {showRows && <div>{tr('endpoint.opcard.sampleNote')}</div>}
            {tree.others > 0 && <div>{tr('endpoint.opcard.others').replace('{n}', fmtNum(tree.others))}</div>}
            {tree.truncated && <div>{tr('endpoint.opcard.truncated')}</div>}
            {d?.windowClamped && <div>{tr('endpoint.opcard.clamped')}</div>}
            {tree.windowEdges.length > 0 && (
              <div data-testid="opcard-window">
                {tr('endpoint.opcard.window')}: {windowEdgesLine(tree.windowEdges, fmtNum)}
              </div>
            )}
            {d?.windowEdgesNote && <div>{d.windowEdgesNote}</div>}
          </div>
        )}
        {/* v0.10.1506 — R4 kod yarısı: "Kod" açılır bölümü (açılınca code=1). */}
        <OperationCardCodeSection params={params} treeRows={tree?.children} />
      </Card>
    </div>
  );
}
