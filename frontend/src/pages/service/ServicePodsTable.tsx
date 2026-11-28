import { usePivotCtx, withPivotCtx } from '@/lib/pivotContext';
import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router';
import { DataTableHead, DataTableColgroup, DataTableState, type DataTable, type DataTableStateProps } from '@/components/ui/DataTable';
import { Badge } from '@/components/ui/Badge';
import { PodJmxInline } from './PodJmxInline';
import { rowActivation } from '@/lib/a11y';
import { fmtCores, fmtBps, podPhaseBadge, restartToneClass } from '@/pages/clusters/thresholds';
import { fmtBytes, fmtNum, fmtDateTime } from '@/lib/utils';
import { termReasonTone } from '@/lib/podTerm';
import { entityLiveness } from '@/lib/entityHref';
import { logsHref } from '@/lib/logsUrl';
import { podDetailPath } from './podDetailPath';
import type { MergedPodRow } from './podsMerge';
import type { TimeRange } from '@/lib/types';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

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
// Sıralama/genişlik TEK dt (üstte; "Kolonları sıfırla" tablo başlığının ⋯
// menüsünde, v0.10.939). Satır > 100: content-visibility.
//
// v0.10.2145 (servis sekmeleri S6, mockup operatör onayı 2026-11-13): tablo
// yalnız Infrastructure'ın seçili cluster satırının altında yaşar — "cluster'a
// göre grupla" kipi ve grup başlığı kalktı (grup artık üst tablonun satırı).
// Hücreler kolon kimliğiyle çizilir: Kaynak / Cluster / Node / Workload / Net
// varsayılan gizli, "Tüm kolonlar" ile gelir (columnModel, çağıranda).

function ms(v?: number): string {
  if (v === undefined || v <= 0) return '—';
  return v < 10 ? v.toFixed(1) : v.toFixed(0);
}

const SRC_LABEL: Record<MergedPodRow['source'], { text: string; title: string }> = {
  both: { text: 'E+T', title: 'Entity katmanı (span\'ler) ve Thanos envanteri ikisi de görüyor' },
  entity: { text: 'E', title: 'Yalnız entity katmanı (span\'ler gördü); Thanos envanterinde eşleşme yok — ad kalıbı ya da cluster eşlemesi' },
  thanos: { text: 'T', title: 'Yalnız Thanos envanteri; bu pencerede span görülmedi' },
};

export function ServicePodsTable({ dt, service, range, effNs, effDeploy, cFrom, cTo, rangeParam, state, jmx = true }: {
  /** 2026-11-21 — JMX (JVM/JBoss) yalnız JVM ya da runtime'ı bilinmeyen serviste;
   *  .NET / Go / Node / Python satırı yalnız pod kaynak kullanımını açar. */
  jmx?: boolean;
  dt: DataTable<MergedPodRow>;
  service: string;
  range: TimeRange;
  effNs: string;
  effDeploy: string;
  cFrom: number;
  cTo: number;
  rangeParam: string | null;
  /**
   * v0.10.967 — tablo standardı T12 (tarif P6, VirtualTable emsali): satır
   * yokken tablonun İÇİNDE çizilen durum; başlık bu dosyada olduğu için durum
   * satırı da burada. Verilmezse `{ kind: 'empty' }`.
   * v0.10.973 — göç gerçek: ServicePodsTab tabloyu her durumda bağlıyor ve
   * yükleniyor / hata / boş zincirini (eski Spinner / Empty sırası) `state`le
   * veriyor. Satırlar kazanır: durum yalnız birleşik küme boşken.
   */
  state?: Omit<DataTableStateProps<MergedPodRow>, 'dt' | 'leading' | 'trailing'>;
}) {
  const pctx = usePivotCtx();
  const [params, setParams] = useSearchParams();
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

  const many = dt.sortedRows.length > 100;

  const podHref = (r: MergedPodRow) => podDetailPath({
    pod: r.pod, cluster: r.cluster, namespace: r.namespace || undefined, service, deploy: effDeploy, range: rangeParam, from: 'pods',
  });
  const podFilters = (r: MergedPodRow) => JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [r.pod] }]);
  // v0.10.1233 — env ekrandan taşınır (cluster satırın kendisinden).
  const tracesHref = (r: MergedPodRow) => withPivotCtx(
    `/traces?service=${encodeURIComponent(service)}&filters=${encodeURIComponent(podFilters(r))}` +
    `${r.entity?.cluster ? `&cluster=${encodeURIComponent(r.entity.cluster)}` : ''}${rangeParam ? `&range=${encodeURIComponent(rangeParam)}` : ''}`,
    pctx, { carry: ['env'] });
  const cell = (r: MergedPodRow, id: string, open: boolean, expandable: boolean): ReactNode => {
    switch (id) {
      case 'pod': {
        const live = r.entity?.entity ? entityLiveness(r.entity.entity) : null;
        return (
          <td key={id} className="mono sticky-left"
            title={`${r.cluster} / ${r.namespace || '?'} / ${r.pod}`}
            // v0.10.1210 (operatör: "üzerine basınca açılsın") — hücrenin boşluğu
            // ve ok da satırı açar; yalnız pod adı bağlantısı pod sayfasına gider.
            onClick={e => { if ((e.target as HTMLElement).closest('a')) e.stopPropagation(); }}>
            {/* v0.10.943 — satır title'ı (T7) kalktı; açılır satırın açıklaması işarete, durumu aria-expanded'a taşındı (§3.10). */}
            {expandable && <span className="pods-caret" aria-hidden="true"
              title={open ? 'Metrikleri gizle' : 'Metrikleri göster'}>{open ? '▾' : '▸'}</span>}
            <Link to={podHref(r)} className="row-link">{r.pod}</Link>
            {live === 'gone' && <Badge tone="danger" className="pods-live" title={`Artık mevcut değil · son görülme ${fmtDateTime(new Date(r.entity!.entity!.lastSeen))}`}>gone</Badge>}
            {live === 'stale' && <Badge tone="warning" className="pods-live" title="Son senkronda görülmedi">stale</Badge>}
          </td>
        );
      }
      case 'status': return (
        <td key={id}>{r.statusKnown && r.phase
          ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span>
          : <span className="field-hint" title="Thanos'ta bu pod için seri yok (ölü ya da KSM dışı) — durum bilinmiyor">durum bilinmiyor</span>}</td>
      );
      // v0.10.973 — tablo standardı T5/T9: ton satır içi renk yerine sınıf
      // (restartColor ile aynı eşik ve token; bilinmeyen = soluk).
      case 'restarts': return (
        <td key={id} className={`num ${r.restartsUnknown ? 'cell-faint' : restartToneClass(r.restarts ?? 0)}`}
          title={r.restartsUnknown ? 'Restart serisi yok (KSM eksik ya da seri tavanı) — 0 değil, bilinmiyor.' : undefined}>
          {r.restartsUnknown ? '—' : fmtNum(r.restarts ?? 0)}
          {r.lastTermReason && (
            <span className={`badge b-${termReasonTone(r.lastTermReason)} pods-term`}
              title={`${r.lastTermReason} · Son sonlanma sebebi (kube-state-metrics)`}>
              {r.lastTermReason}</span>
          )}
        </td>
      );
      case 'src': {
        const src = SRC_LABEL[r.source];
        return <td key={id}><span className="badge b-gray" title={src.title}>{src.text}</span></td>;
      }
      case 'cluster': return (
        <td key={id} className="mono" onClick={e => e.stopPropagation()}>
          <EntityLink to={{ kind: 'record', rec: { type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.clusterId ?? r.cluster }, opts: { range } }} title="Cluster detayı">{r.cluster}</EntityLink>
        </td>
      );
      case 'node': return (
        <td key={id} className="mono" onClick={e => e.stopPropagation()}>
          {r.node && r.clusterId
            ? <EntityLink to={{ kind: 'record', rec: { type: 'node', id: `node:${r.clusterId}/${r.node}`, name: r.node, clusterId: r.clusterId }, opts: { range } }} className="sec">{r.node}</EntityLink>
            : (r.node ?? '—')}
        </td>
      );
      case 'workload': return (
        <td key={id} className="mono" onClick={e => e.stopPropagation()}>
          {r.workload
            ? <EntityLink to={{ kind: 'record', rec: { type: 'workload', id: r.workload.id, name: r.workload.name, namespace: r.workload.namespace, clusterId: r.workload.clusterId }, opts: { range } }} className="sec" title={`${r.workload.kind}/${r.workload.name}`}>{r.workload.name}</EntityLink>
            : <span className="field-hint">{r.entity?.entity?.parentId?.startsWith('ns:') ? '(no workload)' : '—'}</span>}
        </td>
      );
      case 'cpu': return <td key={id} className="num">{r.cpuCores != null ? fmtCores(r.cpuCores) : '—'}</td>;
      case 'mem': return <td key={id} className="num">{r.memBytes != null ? fmtBytes(r.memBytes) : '—'}</td>;
      case 'net': return (
        <td key={id} className="num">{(r.netInBps ?? 0) > 0 || (r.netOutBps ?? 0) > 0
          ? `${fmtBps(r.netInBps ?? 0)} / ${fmtBps(r.netOutBps ?? 0)}` : '—'}</td>
      );
      case 'spans': return <td key={id} className="num" title={r.spans == null ? 'Bu pencerede span görülmedi (entity katmanı)' : undefined}>{r.spans == null ? '—' : fmtNum(r.spans)}</td>;
      case 'err': {
        const errPct = r.spans ? (100 * (r.errors ?? 0)) / r.spans : null;
        return <td key={id} className={`num ${errPct != null && errPct >= 5 ? 'cell-err' : ''}`}>{errPct == null ? '—' : errPct.toFixed(1)}</td>;
      }
      case 'p95': return <td key={id} className="num">{ms(r.p95Ms)}</td>;
      case 'act': return (
        <td key={id} className="pods-act" onClick={e => e.stopPropagation()}>
          {r.spans != null && <Link to={tracesHref(r)} className="accent">Traces</Link>}
          <Link to={logsHref({ window: rangeParam ?? range, service, filters: podFilters(r) })} className="accent">Logs</Link>
          <Link to={podHref(r)} className="accent" title="Pod detay sayfası">/pod →</Link>
        </td>
      );
      default: return <td key={id} />;
    }
  };

  return (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {dt.sortedRows.length === 0 && <DataTableState dt={dt} {...(state ?? { kind: 'empty' })} />}
          {dt.sortedRows.map(r => {
            const open = openKey === r.key;
            const expandable = !!r.thanos;
            const onRow = expandable ? () => setOpenKey(open ? null : r.key) : () => { window.location.assign(podHref(r)); };
            return (
              <Fragment key={r.key}>
                <tr id={`pod-row-${r.pod}`} {...rowActivation(onRow)}
                  aria-expanded={expandable ? open : undefined}
                  className={many ? 'cv-row' : undefined}>
                  {dt.visibleColumns.map(col => cell(r, col.id, open, expandable))}
                </tr>
                {open && r.thanos && (
                  <tr>
                    {/* v0.10.973 — kenardan kenara (dolgusuz) JMX paneli; row-detail dolgulu ve --bg2. */}
                    <td colSpan={colCount} className="pods-jmx">
                      {/* ns = pod'un KENDİ namespace'i; çok-namespace serviste effNs yanlış olurdu. */}
                      <PodJmxInline cluster={r.cluster} ns={r.namespace || effNs} deploy={effDeploy}
                        pod={r.pod} cFrom={cFrom} cTo={cTo} jmx={jmx} onFull={() => { window.location.assign(podHref(r)); }} />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
