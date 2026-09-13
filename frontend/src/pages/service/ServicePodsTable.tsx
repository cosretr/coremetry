import { Fragment, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { DataTableHead, DataTableColgroup, type DataTable } from '@/components/ui/DataTable';
import { Badge } from '@/components/ui/Badge';
import { PodJmxInline } from './PodJmxInline';
import { rowActivation } from '@/lib/a11y';
import { fmtCores, fmtBps, podPhaseBadge, restartColor } from '@/pages/clusters/thresholds';
import { fmtBytes, fmtNum, fmtDateTime } from '@/lib/utils';
import { termReasonTone } from '@/lib/podTerm';
import { entityHref, entityLiveness } from '@/lib/entityHref';
import { logsHref } from '@/lib/logsUrl';
import { podDetailPath } from './podDetailPath';
import { groupByCluster, type MergedPodRow, type PodView } from './podsMerge';
import type { TimeRange } from '@/lib/types';

// ServicePodsTable — v0.10.720 (servis sekmeleri etüdü, Pods dilimi; mockup
// 3b03fe22 Pods şerh 1; operatör onayı 2026-09-13). Entity tablosu
// (ServiceEntityPods, 15 sütun, sabit sütunsuz) + Thanos akordeonu
// (ServiceClusterPods) TEK tabloya indi: yapışık Pod sütunu, ↻/OOM ayrı
// sütun, Kaynak rozeti (entity / Thanos / ikisi), Cluster · Node · Workload
// entity linkleri, Traffic (Spans · Err % · P95) ve satır eylemleri Traces ·
// Logs · /pod →. Kip: düz ya da cluster'a göre grupla (grup başlığı ara
// toplam + Traces/Infra →). Satır tıkı: Thanos'ta görülen pod'da YERİNDE
// JVM/JBoss JMX (PodJmxInline) — aynı anda tek satır açık; yalnız-entity
// satırında (Thanos eşleşmesi yok) tık /pod sayfasına gider.
//
// ?jpod= derin linki (Problems → "pod'a bak", v0.9.533): ilgili satırı
// otomatik açar + kaydırır; TEK SEFERLİK ve TÜKETİLİR (replace:true, yabancı
// paramlar korunur). Pod envanterde yoksa param yine temizlenir.
//
// Sıralama/genişlik TEK dt (üstte, ResetLayout başlıkta); gruplu kipte her
// grup aynı sıralamayı izler. Satır > 100: content-visibility.

function ms(v?: number): string {
  if (v === undefined || v <= 0) return '—';
  return v < 10 ? v.toFixed(1) : v.toFixed(0);
}

const SRC_LABEL: Record<MergedPodRow['source'], { text: string; title: string }> = {
  both: { text: 'E+T', title: 'Entity katmanı (span\'ler) ve Thanos envanteri ikisi de görüyor' },
  entity: { text: 'E', title: 'Yalnız entity katmanı (span\'ler gördü); Thanos envanterinde eşleşme yok — ad kalıbı ya da cluster eşlemesi' },
  thanos: { text: 'T', title: 'Yalnız Thanos envanteri; bu pencerede span görülmedi' },
};

export function ServicePodsTable({ dt, view, service, range, effNs, effDeploy, cFrom, cTo, rangeParam }: {
  dt: DataTable<MergedPodRow>;
  view: PodView;
  service: string;
  range: TimeRange;
  effNs: string;
  effDeploy: string;
  cFrom: number;
  cTo: number;
  rangeParam: string | null;
}) {
  const [params, setParams] = useSearchParams();
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [openKey, setOpenKey] = useState<string | null>(null);
  const colCount = dt.visibleColumns.length;

  // ?jpod= — tek seferlik derin link (ServiceClusterPods'tan taşındı).
  const jpodConsumed = useRef(false);
  useEffect(() => {
    if (jpodConsumed.current) return;
    const jpod = (params.get('jpod') ?? '').trim();
    if (!jpod) { jpodConsumed.current = true; return; }
    if (dt.sortedRows.length === 0) return; // envanter henüz yüklenmedi
    jpodConsumed.current = true;
    const r = dt.sortedRows.find(x => x.pod === jpod);
    if (r) {
      if (r.thanos) setOpenKey(r.key);
      setCollapsed(s => ({ ...s, [r.cluster]: false }));
      setTimeout(() => {
        document.getElementById(`pod-row-${jpod}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
      }, 80);
    }
    setParams(prev => {
      const next = new URLSearchParams(prev);
      next.delete('jpod');
      return next;
    }, { replace: true });
  }, [params, dt.sortedRows, setParams]);

  const groups = useMemo(
    () => (view === 'cluster' ? groupByCluster(dt.sortedRows) : [{ cluster: '', rows: dt.sortedRows, totals: null }]),
    [view, dt.sortedRows]);
  const many = dt.sortedRows.length > 100;

  const podHref = (r: MergedPodRow) => podDetailPath({
    pod: r.pod, cluster: r.cluster, namespace: r.namespace || undefined, service, deploy: effDeploy, range: rangeParam, from: 'pods',
  });
  const podFilters = (r: MergedPodRow) => JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [r.pod] }]);
  const tracesHref = (r: MergedPodRow) =>
    `/traces?service=${encodeURIComponent(service)}&filters=${encodeURIComponent(podFilters(r))}` +
    `${r.entity?.cluster ? `&cluster=${encodeURIComponent(r.entity.cluster)}` : ''}${rangeParam ? `&range=${encodeURIComponent(rangeParam)}` : ''}`;
  const clusterTracesHref = (c: string) =>
    `/traces?service=${encodeURIComponent(service)}&cluster=${encodeURIComponent(c)}${rangeParam ? `&range=${encodeURIComponent(rangeParam)}` : ''}`;
  const infraHref = (c: string) => {
    const n = new URLSearchParams(params); n.set('tab', 'infra'); n.set('icluster', c); n.delete('jpod');
    return `/service?${n.toString()}`;
  };

  return (
    <div className="table-wrap">
      <table style={{ tableLayout: 'fixed', width: '100%' }}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {groups.map(g => {
            const isCol = !!g.cluster && !!collapsed[g.cluster];
            const t = g.totals;
            return (
              <Fragment key={g.cluster || '·'}>
                {g.cluster && t && (
                  <tr className="pods-group" id={`pods-group-${g.cluster}`}>
                    <td colSpan={colCount}>
                      <div className="pods-group__row">
                        <button type="button" className="pods-group__caret" aria-expanded={!isCol}
                          aria-label={isCol ? `${g.cluster} grubunu aç` : `${g.cluster} grubunu kapat`}
                          onClick={() => setCollapsed(s => ({ ...s, [g.cluster]: !s[g.cluster] }))}>
                          {isCol ? '▸' : '▾'}
                        </button>
                        <Link to={entityHref({ type: 'cluster', id: g.cluster, name: g.cluster, clusterId: g.cluster }, { range })}
                          className="mono pods-group__name" title="Cluster detayı">{g.cluster}</Link>
                        <span className={`badge ${t.phaseKnown ? (t.failing > 0 ? 'b-err' : 'b-ok') : 'b-gray'}`}>
                          {t.phaseKnown ? `${t.running} / ${t.pods} running` : `${t.pods} pod`}
                        </span>
                        <span className="pods-group__stats">
                          <span style={{ color: restartColor(t.restarts ?? 0) }}
                            title={t.restartsPartial ? 'Bazı pod\'ların restart serisi yok — toplam alt sınırdır.' : 'Toplam restart'}>
                            ↻ {t.restarts == null ? '—' : fmtNum(t.restarts)}{t.restartsPartial && t.restarts != null ? '+' : ''}
                          </span>
                          <span>CPU <b className="mono">{fmtCores(t.cpuCores)}</b></span>
                          <span>Mem <b className="mono">{fmtBytes(t.memBytes)}</b></span>
                          <span title="Bu pencerede span (entity katmanı)">Spans <b className="mono">{fmtNum(t.spans)}</b></span>
                          {t.errPct != null && <span>Err <b className="mono" style={t.errPct >= 5 ? { color: 'var(--err)' } : undefined}>{t.errPct.toFixed(1)}%</b></span>}
                        </span>
                        <span className="pods-group__act">
                          <Link to={clusterTracesHref(g.cluster)} className="accent">Traces →</Link>
                          <Link to={infraHref(g.cluster)} className="accent" title="Infrastructure sekmesi, bu cluster kapsamında">Infra →</Link>
                        </span>
                      </div>
                    </td>
                  </tr>
                )}
                {!isCol && g.rows.map(r => {
                  const open = openKey === r.key;
                  const expandable = !!r.thanos;
                  const live = r.entity?.entity ? entityLiveness(r.entity.entity) : null;
                  const errPct = r.spans ? (100 * (r.errors ?? 0)) / r.spans : null;
                  const src = SRC_LABEL[r.source];
                  const onRow = expandable ? () => setOpenKey(open ? null : r.key) : () => { window.location.assign(podHref(r)); };
                  return (
                    <Fragment key={r.key}>
                      <tr id={`pod-row-${r.pod}`} {...rowActivation(onRow)}
                        title={expandable ? 'Metrikleri göster · JVM · GC · datasource' : 'Pod detayı'}
                        style={{ cursor: 'pointer', ...(many ? { contentVisibility: 'auto', containIntrinsicSize: 'auto 36px' } : {}) }}>
                        <td className="mono sticky-left" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                          title={`${r.cluster} / ${r.namespace || '?'} / ${r.pod}`} onClick={e => e.stopPropagation()}>
                          {expandable && <span className="pods-caret" aria-hidden="true" onClick={() => setOpenKey(open ? null : r.key)}>{open ? '▾' : '▸'}</span>}
                          <Link to={podHref(r)} className="row-link">{r.pod}</Link>
                          {live === 'gone' && <Badge tone="danger" style={{ marginLeft: 6 }} title={`Artık mevcut değil · son görülme ${fmtDateTime(new Date(r.entity!.entity!.lastSeen))}`}>gone</Badge>}
                          {live === 'stale' && <Badge tone="warning" style={{ marginLeft: 6 }} title="Son senkronda görülmedi">stale</Badge>}
                        </td>
                        <td>{r.statusKnown && r.phase
                          ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span>
                          : <span className="field-hint" title="Thanos'ta bu pod için seri yok (ölü ya da KSM dışı) — durum bilinmiyor">—</span>}</td>
                        <td className="num mono"
                          title={r.restartsUnknown ? 'Restart serisi yok (KSM eksik ya da seri tavanı) — 0 değil, bilinmiyor.' : undefined}
                          style={{ color: r.restartsUnknown ? 'var(--text3)' : restartColor(r.restarts ?? 0) }}>
                          {r.restartsUnknown ? '—' : fmtNum(r.restarts ?? 0)}
                          {r.lastTermReason && (
                            <span className={`badge b-${termReasonTone(r.lastTermReason)} pods-term`}
                              title={`${r.lastTermReason} · Son sonlanma sebebi (kube-state-metrics)`}>
                              {r.lastTermReason}</span>
                          )}
                        </td>
                        <td><span className="badge b-gray" title={src.title}>{src.text}</span></td>
                        <td className="mono" onClick={e => e.stopPropagation()}>
                          <Link to={entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.clusterId ?? r.cluster }, { range })} title="Cluster detayı">{r.cluster}</Link>
                        </td>
                        <td className="mono" onClick={e => e.stopPropagation()}>
                          {r.node && r.clusterId
                            ? <Link to={entityHref({ type: 'node', id: `node:${r.clusterId}/${r.node}`, name: r.node, clusterId: r.clusterId }, { range })} className="sec">{r.node}</Link>
                            : (r.node ?? '—')}
                        </td>
                        <td className="mono" onClick={e => e.stopPropagation()}>
                          {r.workload
                            ? <Link to={entityHref({ type: 'workload', id: r.workload.id, name: r.workload.name, namespace: r.workload.namespace, clusterId: r.workload.clusterId }, { range })} className="sec">{r.workload.kind}/{r.workload.name}</Link>
                            : <span className="field-hint">{r.entity?.entity?.parentId?.startsWith('ns:') ? '(no workload)' : '—'}</span>}
                        </td>
                        <td className="num mono">{r.cpuCores != null ? fmtCores(r.cpuCores) : '—'}</td>
                        <td className="num mono">{r.memBytes != null ? fmtBytes(r.memBytes) : '—'}</td>
                        <td className="num mono">{(r.netInBps ?? 0) > 0 || (r.netOutBps ?? 0) > 0
                          ? `${fmtBps(r.netInBps ?? 0)} / ${fmtBps(r.netOutBps ?? 0)}` : '—'}</td>
                        <td className="num mono" title={r.spans == null ? 'Bu pencerede span görülmedi (entity katmanı)' : undefined}>{r.spans == null ? '—' : fmtNum(r.spans)}</td>
                        <td className="num mono" style={errPct != null && errPct >= 5 ? { color: 'var(--err)' } : undefined}>{errPct == null ? '—' : errPct.toFixed(1)}</td>
                        <td className="num mono">{ms(r.p95Ms)}</td>
                        <td onClick={e => e.stopPropagation()} style={{ whiteSpace: 'nowrap' }}>
                          {r.spans != null && <Link to={tracesHref(r)} className="accent" style={{ fontSize: 11, padding: '2px 6px' }}>Traces</Link>}
                          <Link to={logsHref({ window: rangeParam ?? range, service, filters: podFilters(r) })} className="accent" style={{ fontSize: 11, padding: '2px 6px' }}>Logs</Link>
                          <Link to={podHref(r)} className="accent" style={{ fontSize: 11, padding: '2px 6px' }} title="Pod detay sayfası">/pod →</Link>
                        </td>
                      </tr>
                      {open && r.thanos && (
                        <tr>
                          <td colSpan={colCount} style={{ padding: 0, background: 'var(--bg)' }}>
                            {/* ns = pod'un KENDİ namespace'i; çok-namespace serviste effNs yanlış olurdu. */}
                            <PodJmxInline cluster={r.cluster} ns={r.namespace || effNs} deploy={effDeploy}
                              pod={r.pod} cFrom={cFrom} cTo={cTo} onFull={() => { window.location.assign(podHref(r)); }} />
                          </td>
                        </tr>
                      )}
                    </Fragment>
                  );
                })}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
