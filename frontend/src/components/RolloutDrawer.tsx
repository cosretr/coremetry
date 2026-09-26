// RolloutDrawer — v0.10.203 (ROLLOUTS Faz 4b; audit §4.2). Satır çekmecesi:
// rollout kimliği URL'de (?rollout=<codec> — rolloutRow.ts encode/decode,
// rolloutHref.test.ts pinler); içerik /api/rollout/detail (30 s sunucu
// cache'i, staleTime aynı). Eski Deployment Report gövdesinin tek-rollout
// daraltması: servis başına health verdict + önce/sonra RED + deploy'dan
// beri problem / anomali / yeni hata. Sinyal listeleri 20'de kesilir ve
// ilgili sayfaya köprü verir.
// v0.10.943 — tablo standardı T1: Geçiş öznitelik paneli KeyValue, servis
// listesi (sunucu en çok 200) useDataTable; sinyal önizlemesi statik tablo.
import { Link } from 'react-router-dom';
import { Drawer, DrawerSection, Badge, KeyValue, KeyValueRow } from '@/components/ui';
import { Spinner, Empty } from '@/components/Spinner';
import { serviceHref } from '@/lib/serviceHref';
import { fmtDateTime, fmtNum } from '@/lib/utils';
import { useRolloutDetail, useEntityClusters } from '@/lib/queries';
import { CopyButton } from '@/components/CopyButton';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import { statusTone, statusLabel, statusTitle, shortRevision, imageDiff, imageRef, rolloutChangeKind, changeKindLabel, changeKindTitle, changeKindTone, rolloutPlaceLabel } from '@/lib/rolloutRow';
import type { RolloutIdParam } from '@/lib/rolloutRow';
import type { ServiceReportSection } from '@/lib/types';

function pct(n: number) { return `%${n.toFixed(1)}`; }
function ms(n: number) { return `${n.toFixed(0)}ms`; }
function rps(n: number) { return `${n.toFixed(2)}/s`; }
const CAP = 20;

// v0.10.943 — initialSort yok: satırlar sunucunun sırasında. Önce/sonra
// hücreleri iki değer taşır; sıralama yalnız ad ve sağlıkta anlamlı. Sabit
// genişlikte (ve dar ekranda fit küçültünce) "…" sonraki değeri keserdi —
// hücre ok'tan sarar (truncate 'wrap').
const HEALTH_RANK: Record<ServiceReportSection['health'], number> = { red: 3, yellow: 2, green: 1, '': 0 };
const SVC_COLS: ColumnDef<ServiceReportSection>[] = [
  { id: 'service', label: 'Servis',    sortValue: s => s.service,             naturalDir: 'asc',  flex: true, mono: true },
  { id: 'health',  label: 'Sağlık',    sortValue: s => HEALTH_RANK[s.health], naturalDir: 'desc', width: 90 },
  { id: 'err',     label: 'Hata% ö/s', numeric: true, width: 130, truncate: 'wrap' },
  { id: 'p99',     label: 'p99 ö/s',   numeric: true, width: 140, truncate: 'wrap' },
  { id: 'rps',     label: 'İstek ö/s', numeric: true, width: 160, truncate: 'wrap' },
];

export function RolloutDrawer({ id, onClose }: { id: RolloutIdParam; onClose: () => void }) {
  const q = useRolloutDetail(id);
  const d = q.data;
  // v0.10.338 — Operator-reported: aynı workload birden çok cluster'a çıkıyor,
  // çekmece hangisi olduğunu söylemiyordu. Küme adı entity listesinden
  // (Rollouts sayfasıyla aynı çözüm); liste yoksa/bayrak kapalıysa ID.
  const clustersQ = useEntityClusters();
  const clusterName = clustersQ.data?.clusters?.find(c => c.id === id.clusterId)?.name;
  const dt = useDataTable<ServiceReportSection>({ storageKey: 'rollout-drawer-services', columns: SVC_COLS, rows: d?.services ?? [] });
  return (
    <Drawer onClose={onClose} width={760} header={
      <>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 14, fontWeight: 600 }}>{id.workload}</span>
        <span className="field-hint" title={`küme ${clusterName || id.clusterId} · namespace ${id.namespace}`}>{clusterName || id.clusterId} · {id.namespace}</span>
        {d && <Badge tone={statusTone(d.rollout.status)}>{statusLabel(d.rollout.status)}</Badge>}
        {d && <span className="field-hint">{shortRevision(d.rollout.revision, d.rollout.workload)} · {imageDiff(d.rollout)} · {fmtDateTime(new Date(d.rollout.startedAt))}</span>}
      </>
    }>
      {q.isPending && <Spinner />}
      {q.isError && <Empty icon="!" title="Çekmece yüklenemedi">{(q.error as Error).message}</Empty>}
      {d && (d.rollout.note || d.note) && (
        <div className="field-hint" style={{ marginBottom: 8 }}>{[d.rollout.note, d.note].filter(Boolean).join(' · ')}</div>
      )}
      {d && (() => {
        // v0.10.234 — Operator-reported: "hangi versiyondan hangisine geçti
        // göremiyorum". Başlıktaki tek satır kısaltılmış revizyon + imaj
        // diff'iydi; tam kimlikler (revizyon, repo:tag) ve olayın TÜRÜ
        // (imaj değişti → Deployment / aynı → config rollout) burada.
        const r = d.rollout;
        const k = rolloutChangeKind(r);
        // v0.10.338 — Operator-reported: revizyon ve imaj "…" ile kırpılıyordu
        // (genel `tbody td` nowrap + 320px kuralı); tam kimlik yanında kopyalama
        // düğmesi. v0.10.943 — öznitelik paneli KeyValue: değer kırpılmaz, sarar.
        const curImage = imageRef(r.image, r.imageTag);
        return (
          <DrawerSection title="Geçiş">
            <KeyValue>
              <KeyValueRow k="tür" v={<><Badge tone={changeKindTone(k)}>{changeKindLabel(k)}</Badge> <span className="field-hint">{changeKindTitle(k)}</span></>} />
              <KeyValueRow k="durum" v={<><Badge tone={statusTone(r.status)}>{statusLabel(r.status)}</Badge> <span className="field-hint">{statusTitle(r.status)}</span></>} />
              <KeyValueRow k="küme" mono title={`cluster id ${r.clusterId}`} v={rolloutPlaceLabel(r, clusterName)} />
              <KeyValueRow k="revizyon" mono v={<>{r.prevRevision || '—'} → <b>{r.revision}</b> <CopyButton value={r.revision} title="Revizyonu kopyala" /></>} />
              <KeyValueRow k="imaj" mono v={<>{imageRef(r.prevImage, r.prevImageTag)} → <b>{curImage}</b> {curImage !== '—' && <CopyButton value={curImage} title="İmajı (repo:tag) kopyala" />}</>} />
              <KeyValueRow k="zaman" v={<span className="field-hint">başladı {fmtDateTime(new Date(r.startedAt))}{r.completedAt > 0 ? ` · tamamlandı ${fmtDateTime(new Date(r.completedAt))}` : ''}{r.detectedBy ? ` · kaynak ${r.detectedBy}` : ''}</span>} />
            </KeyValue>
          </DrawerSection>
        );
      })()}
      {d && d.services.length === 0 && (
        <Empty icon="∅" title="Bu revizyonun servisi çözülemedi">{d.note || 'MV bu revizyon için servis kaydı taşımıyor (etkinlik penceresi geçmiş olabilir).'}</Empty>
      )}
      {d && d.services.length > 0 && (
        <>
          <DrawerSection title={`Servis sağlığı — deploy öncesi/sonrası (${d.services.length})`}>
            <div className="table-wrap">
              <table {...dt.tableProps}>
                <DataTableColgroup dt={dt} />
                <DataTableHead dt={dt} />
                <tbody>
                  {dt.sortedRows.map(s => (
                    <tr key={s.service}>
                      <DataTableCell dt={dt} col="service" row={s} value={s.service}>
                        <Link to={serviceHref(s.service, { params: { range: `custom:${Math.round(d.since / 1e6)}-${Math.round(d.generatedAt / 1e6)}` } })} className="sec">{s.service}</Link>
                      </DataTableCell>
                      {/* v0.10.929 (K5) — green = sağlıklı durum, nötr; renk yalnız sapmada. */}
                      <DataTableCell dt={dt} col="health" row={s}><Badge tone={s.health === 'red' ? 'danger' : s.health === 'yellow' ? 'warning' : 'neutral'}>{s.health || 'n/a'}</Badge></DataTableCell>
                      {s.after.throughput === 0 ? (
                        <>
                          {/* deploy'dan sonra hiç span yok: sahte %0.0/0ms basma */}
                          <DataTableCell dt={dt} col="err" row={s}>{pct(s.before.errorRate)} → <span style={{ color: 'var(--warn)' }}>—</span></DataTableCell>
                          <DataTableCell dt={dt} col="p99" row={s}>{ms(s.before.p99Ms)} → <span style={{ color: 'var(--warn)' }}>—</span></DataTableCell>
                          <DataTableCell dt={dt} col="rps" row={s}>{rps(s.before.throughput)} → <span style={{ color: 'var(--warn)' }} title="deploy'dan sonra span görülmedi">yok</span></DataTableCell>
                        </>
                      ) : (
                        <>
                          <DataTableCell dt={dt} col="err" row={s}>{pct(s.before.errorRate)} → <span style={s.after.errorRate > s.before.errorRate ? { color: 'var(--err)' } : undefined}>{pct(s.after.errorRate)}</span></DataTableCell>
                          <DataTableCell dt={dt} col="p99" row={s}>{ms(s.before.p99Ms)} → <span style={s.after.p99Ms > s.before.p99Ms ? { color: 'var(--err)' } : undefined}>{ms(s.after.p99Ms)}</span></DataTableCell>
                          <DataTableCell dt={dt} col="rps" row={s}>{rps(s.before.throughput)} → {rps(s.after.throughput)}</DataTableCell>
                        </>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </DrawerSection>
          <SignalSection title="Deploy'dan beri açık problemler" rows={d.services.flatMap(s => s.problems.map(p => ({ key: p.id, svc: s.service, a: p.severity, b: p.ruleName, at: p.startedAt })))} moreHref="/problems" />
          <SignalSection title="Aktif anomaliler" rows={d.services.flatMap(s => s.anomalies.map(a => ({ key: `${s.service}/${a.id}`, svc: s.service, a: a.kind, b: a.pattern, at: a.startedAt })))} moreHref="/anomalies" />
          <SignalSection title="Yeni hatalar" rows={d.services.flatMap(s => s.newErrors.map(e => ({ key: `${s.service}/${e.fingerprint}`, svc: s.service, a: e.type, b: e.message, at: e.firstSeen })))} moreHref="/problems" />
        </>
      )}
    </Drawer>
  );
}

function SignalSection({ title, rows, moreHref }: { title: string; rows: { key: string | number; svc: string; a: string; b: string; at: number }[]; moreHref: string }) {
  return (
    <DrawerSection title={`${title} (${rows.length})`}>
      {rows.length === 0 ? (
        <div style={{ fontSize: 12, color: 'var(--text3)' }}>yok</div>
      ) : (
        <div className="table-wrap">
          {/* v0.10.943 — statik tablo (T1): başlıksız çekmece önizlemesi, en çok
              CAP satır, sıralanmaz; tam liste moreHref sayfasında. */}
          <table>
            <colgroup><col style={{ width: 160 }} /><col style={{ width: 120 }} /><col /><col style={{ width: 150 }} /></colgroup>
            <tbody>
              {rows.slice(0, CAP).map(r => (
                <tr key={r.key}>
                  <td className="mono">{r.svc}</td>
                  <td className="field-hint">{r.a}</td>
                  <td title={r.b}>{r.b}</td>
                  <td className="num">{fmtDateTime(new Date(Math.round(r.at / 1e6)))}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length > CAP && <div className="field-hint">{fmtNum(rows.length - CAP)} satır daha — <Link to={moreHref} className="sec">tam liste</Link></div>}
        </div>
      )}
    </DrawerSection>
  );
}
