// PodContextTables — v0.10.160 (A anatomisi §6, §8, §9). Konteynerler (Thanos
// KSM anlık — zaman serisi YOK), kardeş pod'lar (entity siblings ≤50 ×
// /api/clusters/pods topk 500; listede olmayan pod'da faz/restart/cpu/mem
// BİLİNMİYOR), etiketler ve ömür tarihçesi. v0.10.135 PodEntityPanel'in çip/
// link satırları tabloya terfi etti; kardeş tablosunda «Node» sütunu bilinçli
// YOK (siblings EntityRecord döner, runs_on yalnız hedef pod için çözülür —
// inceleme must-fix). Konteyner tablosu ≤ ~10 satır ve sıralanmaz → ham
// <table> meşru; kardeşler useDataTable.
// v0.10.943 (tablo standardı dilim 3, T1) — etiketler bir öznitelik paneli →
// KeyValue; ömür tarihçesi statik tablo kalır (gerekçe tablonun içinde).
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Button, KeyValue } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import { fmtDateTime, fmtBytes } from '@/lib/utils';
import { fmtCores, podPhaseBadge } from '@/pages/clusters/thresholds';
import { entityHref, entityLiveness } from '@/lib/entityHref';
import type { EntityContainersResponse, EntityRecord, TimeRange } from '@/lib/types';
import type { SiblingRow } from './podPage';
import { waitingReasonTone } from './waitingReason';

// v0.10.929 (K5) — hazır + yeniden başlamamış konteyner normal hâl: nötr.
function containerTone(c: { readyKnown: boolean; ready: boolean; restarts: number; lastTermReason?: string }): 'neutral' | 'danger' | 'warning' {
  if (!c.readyKnown) return 'neutral';
  if (!c.ready) return 'danger';
  return c.restarts > 0 || c.lastTermReason ? 'warning' : 'neutral';
}

export function PodContainersTable({ ctr, pending, containerRecs }: {
  ctr?: EntityContainersResponse;
  pending: boolean;
  /** entity çocukları — KSM serisi yokken en azından adlar */
  containerRecs?: EntityRecord[];
}) {
  if (pending) return <Spinner />;
  const rows = ctr?.containers ?? [];
  return (
    <>
      {ctr?.error && <div className="pod-cap"><Badge tone="warning" title={ctr.error}>Thanos: durum alınamadı</Badge></div>}
      {rows.length === 0 ? (
        <div className="pod-cap">KSM serisi yok{containerRecs && containerRecs.length > 0 ? ` · konteynerler: ${containerRecs.map(c => c.name).join(', ')}` : ''}</div>
      ) : (
        <div className="table-wrap">
          {/* v0.10.943 — statik tablo (T1): pod başına ≤ ~10 konteyner, sıralanmaz. */}
          <table>
            <thead><tr><th>Ad</th><th>Ready</th><th className="num">Restarts</th><th>Waiting</th><th>Son sonlanma</th></tr></thead>
            <tbody>
              {rows.map(c => (
                <tr key={c.name}>
                  <td className="mono">{c.name}</td>
                  <td>{c.readyKnown ? <Badge tone={containerTone(c)}>{c.ready ? 'ready' : 'not ready'}</Badge> : <span className="field-hint" title="kube_pod_container_status_ready serisi yok">?</span>}</td>
                  <td className={c.restarts > 0 ? 'num cell-warn' : 'num'}>{c.restarts}</td>
                  {/* v0.10.929 (K5) — olağan başlangıç geçişi (ContainerCreating /
                      PodInitializing) nötr; kubelet arıza nedenleri kırmızı. */}
                  <td>{c.waitingReason ? <Badge tone={waitingReasonTone(c.waitingReason)}>{c.waitingReason}</Badge> : '—'}</td>
                  <td>{c.lastTermReason ? <Badge tone="warning">{c.lastTermReason}</Badge> : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="pod-cap">
        <code className="mono">GET /api/entity/containers</code> · kube_pod_container_status_{'{'}ready,restarts_total,waiting_reason,last_terminated_reason{'}'} · anlık — konteyner başına zaman serisi yok (CPU/Mem pod toplamıdır).
      </div>
    </>
  );
}

const SIB_COLS: ColumnDef<SiblingRow>[] = [
  { id: 'pod', label: 'Pod', width: 280, sortValue: r => r.name, naturalDir: 'asc' },
  { id: 'status', label: 'Durum', width: 90, sortValue: r => entityLiveness(r.rec) },
  { id: 'phase', label: 'Faz', width: 110, sortValue: r => r.phase ?? '' },
  { id: 'restarts', label: 'Restarts', width: 90, numeric: true, sortValue: r => r.restarts ?? -1, tone: r => ((r.restarts ?? 0) > 0 ? 'warn' : undefined) },
  { id: 'cpu', label: 'CPU', width: 90, numeric: true, sortValue: r => r.cpuCores ?? -1 },
  { id: 'mem', label: 'Mem', width: 100, numeric: true, sortValue: r => r.memBytes ?? -1 },
];

export function PodSiblingsTable({ rows, pageRange, at, clusterName, truncated }: {
  rows: SiblingRow[];
  pageRange: TimeRange;
  at?: number;
  clusterName: string;
  /** /api/clusters/pods topk 500'e dayandı — «listede yok» kanıt değil */
  truncated: boolean;
}) {
  const dt = useDataTable<SiblingRow>({ storageKey: 'pod-siblings', columns: SIB_COLS, rows, initialSort: { id: 'restarts', dir: 'desc' } });
  if (rows.length === 0) return <div className="pod-cap">Kardeş pod yok.</div>;
  return (
    <>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.map(r => {
              const live = entityLiveness(r.rec);
              return (
                <tr key={r.rec.id}>
                  <td className="mono" title={`${clusterName} / ${r.rec.namespace ?? ''} / ${r.name}`}>
                    {/* service TAŞINMAZ (inceleme #16): kardeş o servisi çalıştırmıyor olabilir; yanlış RED kapsamı + yanlış geri linki olurdu. */}
                    <Link to={entityHref(r.rec, { range: pageRange, at: at || undefined, clusterName })} className="sec">{r.name}</Link>
                  </td>
                  <td>{live === 'live' ? <Badge>live</Badge> : live === 'stale' ? <Badge tone="warning">stale</Badge> : <Badge tone="danger">gone</Badge>}</td>
                  <td>{r.known && r.phase ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span> : <span className="field-hint" title="topk 500 listesinde yok — faz bilinmiyor">—</span>}</td>
                  <DataTableCell dt={dt} col="restarts" row={r} title={r.lastTermReason ? `son: ${r.lastTermReason}` : undefined} value={r.restarts === null ? '—' : r.restarts} />
                  <DataTableCell dt={dt} col="cpu" row={r} value={r.cpuCores === null ? '—' : fmtCores(r.cpuCores)} />
                  <DataTableCell dt={dt} col="mem" row={r} value={r.memBytes === null ? '—' : fmtBytes(r.memBytes)} />
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="pod-cap">
        Kardeşler <code className="mono">/api/entity</code> (sunucu ≤ 50) · faz/restart/CPU/Mem <code className="mono">/api/clusters/pods</code> (topk 500) ile ad üzerinden{truncated ? ' — liste kesik: «—» yok demek değil' : ''}.
      </div>
    </>
  );
}

export function PodLabelsTable({ labels }: { labels: Record<string, string> | undefined }) {
  const [all, setAll] = useState(false);
  const entries = Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return <div className="pod-cap">Etiket yok (kube_pod_labels).</div>;
  const shown = all ? entries : entries.slice(0, 8);
  return (
    <>
      {/* v0.10.943 — etiket değerleri k8s kimlik belirteçleri (uygulama adı, hash) → mono; anahtar etiket, mono değil. */}
      <KeyValue labelWidth="wide" items={shown.map(([k, v]) => ({ id: k, k, v, mono: true }))} />
      {entries.length > 8 && (
        <div className="pod-cap"><Button variant="secondary" size="xs" onClick={() => setAll(v => !v)}>{all ? 'daha az' : `+${entries.length - 8} etiket daha`}</Button></div>
      )}
    </>
  );
}

export function PodLifetimesTable({ lifetimes, atMatch, at }: { lifetimes: EntityRecord[]; atMatch?: boolean; at?: number }) {
  return (
    <>
      <div className="table-wrap">
        {/* v0.10.943 — statik tablo (T1): aynı ada ait ömürler, sunucu ≤ 50 (pratikte 1–3), sunucu sırası anlamlı; otomatik düzen zaman damgası + uid'yi tam gösterir, dar yarım kolonda kaydırır. */}
        <table>
          <thead><tr><th>Valid from</th><th>Valid to</th><th>Kaynak</th><th>uid</th></tr></thead>
          <tbody>
            {lifetimes.map(l => (
              <tr key={`${l.validFrom}|${l.uid ?? ''}`}>
                <td className="mono">{fmtDateTime(new Date(l.validFrom))}</td>
                <td className="mono">{l.validTo ? fmtDateTime(new Date(l.validTo)) : '(açık)'}</td>
                <td>{l.source}</td>
                <td className="mono">{l.uid ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pod-cap">
        {lifetimes.length} ömür · aynı ad{!!at && atMatch === false ? ' · ?at= verilip o an geçerli ömür yoksa en yakın ömür gösterilir (atMatch=false)' : ''}.
      </div>
    </>
  );
}
