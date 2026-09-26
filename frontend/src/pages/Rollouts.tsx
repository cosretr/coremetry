// Rollouts — v0.10.201 (ROLLOUTS Faz 4; docs/audits/rollouts-audit.md §4, §9, §14.1).
// «Deployment Report» yerine canlı, olay-tabanlı akış: workload_rollouts
// satırları (reconciler yazar — satır = gözlenmiş giriş olayı) + agregat
// sekmesi (deploy sıklığı, rollback oranı, süre — DORA zemini).
//
// Canlılık: Plan A (audit §3) — `event: rollout` yalnız INVALIDATION sinyali
// (eventInvalidations.ts → keys.rollouts.listAll); satırlar snapshot
// refetch'iyle gelir, 15 s poll SSE kaybının emniyeti; pencere refreshTick'le
// yürür (Dashboard emsali — mount'ta donmuş `to` canlı sekmeyi öldürür).
// Tablo SUNUCU sıralı (started_at DESC + LIMIT): istemci sıralaması kesik
// sayfada yalan söyler → serverSort (resize-only, Services emsali).
// URL = kaynak: ?tab, ?status, ?cluster, ?namespace (debounce), ?range.
// Bayrak kapalı → 404 {disabled:true} SENTİNELİ (hata değil; entities emsali)
// → Empty + admin için tek tık Enable (mevcut ayarları EZMEDEN: read-modify-write).
// Koşu durumu (runs) admin ucu: viewer'da hiç sorulmaz (403 döngüsü olmasın).
// Satır çekmecesi + health verdict + tekil `/api/rollout` istemcisi Faz 4b.
import { useEffect, useMemo, useState } from 'react';
import { TabStrip } from '@/components/ui/TabStrip'; // v0.10.456 (D5)
import { Link, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/ui/PageShell';
import { Spinner, Empty } from '@/components/Spinner';
import { Badge, Button, StatTile } from '@/components/ui';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useUrlRange } from '@/lib/useUrlRange';
import { timeRangeToNs } from '@/lib/utils';
import { fmtNum, fmtDateTime, fmtDurShort } from '@/lib/utils';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { keys, useRollouts, useRolloutStats, useRolloutRuns, useEntityClusters } from '@/lib/queries';
import { RolloutDrawer } from '@/components/RolloutDrawer';
import { tracesPivotHref } from '@/lib/pivotHref';
import { entityHref } from '@/lib/entityHref';
import { rolloutKey, statusTone, statusLabel, statusTitle, rolloutDurationSec, shortRevision, imageDiff, encodeRolloutParam, decodeRolloutParam, rolloutTracesFilters, rolloutChangeKind, changeKindLabel, changeKindTitle, changeKindTone } from '@/lib/rolloutRow';
import type { RolloutStats, WorkloadRollout } from '@/lib/types';

const STATUSES = ['', 'in_progress', 'completed', 'rolled_back', 'superseded', 'stalled'] as const;

// Sunucu sıralı sayfa: sortValue YOK (kesik 200 satırı sıralamak yanıltır —
// dataTable.ts serverSort sözleşmesi; Services emsali).
// v0.10.205 — operatör bildirimi: prod'da (uzun workload/imaj adları) fit
// kolonları 48px tabanına kadar ezdi, hücreler ve BAŞLIKLAR ortadan
// kırpıldı ("KA…", "SP…"). minWidth tabanı = fit'in ezemeyeceği okunabilir
// genişlik; toplam kabı aşarsa .table-wrap zaten overflow-x:auto kaydırır
// (v0.9.1078). Başlık kırpılması da biter: taban her başlığın tam adını
// taşıyacak kadar geniş.
const COLS: ColumnDef<WorkloadRollout>[] = [
  { id: 'status', label: 'Durum', width: 120, minWidth: 96 },
  { id: 'workload', label: 'Workload', width: 260, minWidth: 180 },
  // v0.10.565 (operatör-raporlu): cluster adı Workload hücresinin SONUNDA
  // soluk ek olarak duruyordu ve 300px kolonda ellipsis'e kurban gidiyordu
  // ("workload · ns · c…"). Kendi kolonu: her zaman okunur, ayrı
  // genişletilebilir, üstteki Cluster süzgeciyle aynı adı gösterir.
  { id: 'cluster', label: 'Cluster', width: 150, minWidth: 110 },
  { id: 'kind', label: 'Tür', width: 100, minWidth: 64 },
  { id: 'change', label: 'Değişiklik', width: 130, minWidth: 100 }, // v0.10.234 — imaj değişti mi (Deployment) / aynı mı (config)
  { id: 'revision', label: 'Revizyon', width: 150, minWidth: 120, mono: true },
  { id: 'image', label: 'İmaj (eski → yeni)', width: 300, minWidth: 170, mono: true },
  { id: 'started', label: 'Başladı', width: 170, minWidth: 140, numeric: true },
  { id: 'dur', label: 'Süre', width: 90, minWidth: 64, numeric: true },
  { id: 'spans', label: 'Span', width: 90, minWidth: 64, numeric: true },
  { id: 'problems', label: 'Problem', width: 90, minWidth: 64, numeric: true }, // v0.10.244 — D4: başlangıçtan beri açık problemler (çekmece sayımı)
  { id: 'by', label: 'Kaynak', width: 90, minWidth: 76 },
  { id: 'note', label: 'Not', width: 260, minWidth: 120 },
  { id: 'links', label: '', width: 120, minWidth: 96 },
];

export default function RolloutsPage() {
  const [range, setRange] = useUrlRange('24h');
  const [sp, setSp] = useSearchParams();
  const tab = sp.get('tab') === 'stats' ? 'stats' : 'live';
  const status = sp.get('status') ?? '';
  const cluster = sp.get('cluster') ?? '';
  const ns = sp.get('namespace') ?? '';
  const setParam = (k: string, v: string) => setSp(prev => { const n = new URLSearchParams(prev); if (v) n.set(k, v); else n.delete(k); return n; }, { replace: true });
  // Pencere canlı yürür (Dashboard refreshTick emsali): donmuş `to` yeni
  // rollout'u sonsuza dek dışarıda bırakırdı. custom (elle) pencere sabit.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (range.preset === 'custom') return;
    const id = setInterval(() => { if (!document.hidden) setTick(t => t + 1); }, 60_000);
    return () => clearInterval(id);
  }, [range.preset]);
  const { from, to } = useMemo(() => timeRangeToNs(range), [range, tick]);
  // Namespace debounce: her tuş bir FINAL sorgusu olmasın
  const [nsDraft, setNsDraft] = useState(ns);
  useEffect(() => { setNsDraft(ns); }, [ns]);
  useEffect(() => {
    if (nsDraft === ns) return;
    const id = setTimeout(() => setParam('namespace', nsDraft), 350);
    return () => clearTimeout(id);
  }, [nsDraft]); // eslint-disable-line react-hooks/exhaustive-deps
  const auth = useAuth();
  const isAdmin = auth.user?.role === 'admin';
  const qc = useQueryClient();
  const clustersQ = useEntityClusters();
  const clusters = clustersQ.data?.clusters ?? [];
  const clusterName = (id: string) => clusters.find(c => c.id === id)?.name ?? id;
  const listQ = useRollouts({ from, to, cluster: cluster || undefined, namespace: ns || undefined, status: status || undefined, limit: 200 }, tab === 'live');
  const statsQ = useRolloutStats({ from, to, cluster: cluster || undefined, namespace: ns || undefined, topN: 10 }, tab === 'stats');
  const runsQ = useRolloutRuns(isAdmin); // admin ucu: viewer'da 403 döngüsü açma
  const rows = useMemo(() => listQ.data?.rollouts ?? [], [listQ.data]);
  const dt = useDataTable<WorkloadRollout>({ storageKey: 'rollouts-live', columns: COLS, rows, serverSort: true });
  const [enabling, setEnabling] = useState(false);
  const [enableErr, setEnableErr] = useState('');
  const err = (tab === 'live' ? listQ.error : statsQ.error) as Error | null;
  const disabled = tab === 'live' ? listQ.data?.disabled === true : statsQ.data?.disabled === true;
  const lastRun = runsQ.data?.runs?.[0];
  const drawerId = decodeRolloutParam(sp.get('rollout'));
  // v0.10.954 — tablo standardı T12: canlı listenin yükleniyor / hata / boş
  // durumu tablonun İÇİNDE, başlık durur ("Rollouts kapalı" kapısı dışarıda).
  // Zincir sırası aynı: hata bayat satırları da gizler (showLiveRows).
  // Cluster / durum / namespace SUNUCU süzgeci → sıfır satır "eşleşme yok"
  // (üç ayrı denetim, tek temizle eylemi yok); sunucu notu korunur.
  const liveFiltered = !!(cluster || ns || status);
  const showLiveRows = !listQ.isPending && !err && rows.length > 0;
  const liveState: Omit<DataTableStateProps<WorkloadRollout>, 'dt'> =
    listQ.isPending ? { kind: 'loading' }
    : err ? { kind: 'error', message: `Rollout listesi yüklenemedi: ${err.message}` }
    : liveFiltered ? { kind: 'no-match', ...(listQ.data?.note ? { message: `Eşleşme yok — ${listQ.data.note}` } : {}) }
    : { kind: 'empty', message: `Bu pencerede rollout yok — ${listQ.data?.note ?? 'Reconciler aktif kümeye giren yeni revizyon görmedi (giriş: ≥2 karar kovası + gözlenmiş yokluk).'}` };
  const enable = async () => {
    setEnabling(true);
    setEnableErr('');
    try {
      // Mevcut vidaları EZME: read-modify-write (tek tık Enable ayar sıfırlamasın)
      const cur = await api.rolloutSettings();
      await api.putRolloutSettings({ ...cur.settings, enabled: true });
      await qc.invalidateQueries({ queryKey: keys.rollouts.all });
    } catch (e) {
      setEnableErr(e instanceof Error ? e.message : String(e));
    } finally {
      setEnabling(false);
    }
  };
  return (
    <>
      <Topbar title="Rollouts" range={range} onRangeChange={setRange} />
      <PageShell>
        <TabStrip ariaLabel="Rollout görünümü" value={tab} onChange={k => setParam('tab', k === 'live' ? '' : k)} style={{ marginBottom: 10 }}
          tabs={[{ key: 'live', label: 'Canlı' }, { key: 'stats', label: 'Toplu' }]} />
        <div className="controls" style={{ marginBottom: 12, gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
            Cluster
            <select value={cluster} onChange={e => setParam('cluster', e.target.value)}>
              <option value="">tümü</option>
              {clusters.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </label>
          <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
            Durum
            <select value={status} onChange={e => setParam('status', e.target.value)}>
              {STATUSES.map(s => <option key={s} value={s}>{s ? statusLabel(s) : 'tümü'}</option>)}
            </select>
          </label>
          <label style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
            Namespace
            <input value={nsDraft} placeholder="tümü" onChange={e => setNsDraft(e.target.value)} style={{ width: 160 }} />
          </label>
          {isAdmin && (
            <span className="field-hint" style={{ marginLeft: 'auto' }}>
              {/* v0.10.929 (K5) — arka plan işinin 'ok' bitişi normal sonuç: nötr.
                  'skipped' de nötr: kapanışta (ctx iptali) yarıda kesilen tik,
                  arıza değil (internal/rollout/reconciler.go RunSkipped).
                  Sapma yalnız partial (uyarı) ve failed/bilinmeyen (hata). */}
              {lastRun
                ? <>reconciler son koşu {fmtDateTime(new Date(lastRun.startedAt))} · <span className={`badge ${lastRun.status === 'ok' || lastRun.status === 'skipped' ? 'b-gray' : lastRun.status === 'partial' ? 'b-warn' : 'b-err'}`}>{lastRun.status}</span></>
                : runsQ.isPending ? null : 'reconciler henüz koşmadı'}
            </span>
          )}
        </div>
        {disabled ? (
          <Empty icon="—" title="Rollouts kapalı"
            action={isAdmin ? <Button variant="primary" size="sm" loading={enabling} onClick={() => void enable()}>Etkinleştir</Button> : undefined}>
            Olay tablosu yazılmıyor. {isAdmin ? (enableErr ? `Açılamadı: ${enableErr}` : 'Mevcut ayarlar korunarak açılır.') : 'Bir admin Settings → Rollouts → Enable ile açar.'}
          </Empty>
        ) : tab === 'live' ? (
          <>
            {showLiveRows && listQ.data?.capped && <div className="pod-cap">Liste sunucuda kesildi (limit 200) — pencereyi daralt.</div>}
            <div className="table-wrap">
              <table {...dt.tableProps}>
                <DataTableColgroup dt={dt} />
                <DataTableHead dt={dt} />
                <tbody>
                  {!showLiveRows ? <DataTableState dt={dt} {...liveState} /> : dt.sortedRows.map(r => {
                    const cname = clusterName(r.clusterId);
                    const tracesHref = tracesPivotHref({
                      window: range, cluster: cname, rootOnly: false,
                      filters: JSON.stringify(rolloutTracesFilters(r)),
                    });
                    const wlHref = entityHref({ type: 'workload', id: `wl:${r.clusterId}/${r.namespace}/${r.kind || 'Deployment'}/${r.workload}`, name: r.workload, namespace: r.namespace, clusterId: r.clusterId }, { range });
                    return (
                      <tr key={rolloutKey(r)} className={rows.length > 100 ? 'cv-row' : undefined}
                        // v0.10.933 (tablo standardı T2) — elle onClick: imleç + hover bu işaretle (globals.css)
                        data-row-action
                        onClick={e => { if ((e.target as HTMLElement).closest('a, button')) return; setParam('rollout', encodeRolloutParam(r)); }}>
                        <DataTableCell dt={dt} col="status" row={r}><Badge tone={statusTone(r.status)} title={[statusTitle(r.status), r.completedAt ? `tamamlandı ${fmtDateTime(new Date(r.completedAt))}` : ''].filter(Boolean).join(' · ') || undefined}>{statusLabel(r.status)}</Badge></DataTableCell>
                        <DataTableCell dt={dt} col="workload" row={r} title={`${cname} / ${r.namespace} / ${r.workload}`}>
                          <Link to={wlHref} className="sec">{r.workload}</Link>
                          <span className="field-hint"> · {r.namespace}</span>
                        </DataTableCell>
                        {/* v0.10.565 — cluster kendi kolonunda; ad çözülemezse ham id (clusterName). */}
                        <DataTableCell dt={dt} col="cluster" row={r} value={cname} />
                        <DataTableCell dt={dt} col="kind" row={r} className="field-hint">{r.kind || '—'}</DataTableCell>
                        <DataTableCell dt={dt} col="change" row={r}>{(() => { const k = rolloutChangeKind(r); return <Badge tone={changeKindTone(k)} title={changeKindTitle(k)}>{changeKindLabel(k)}</Badge>; })()}</DataTableCell>
                        <DataTableCell dt={dt} col="revision" row={r} title={r.revision}>{shortRevision(r.revision, r.workload)}{r.prevRevision ? <span className="field-hint"> ← {shortRevision(r.prevRevision, r.workload)}</span> : null}</DataTableCell>
                        <DataTableCell dt={dt} col="image" row={r} title={r.image || undefined}>{imageDiff(r)}</DataTableCell>
                        {/* v0.10.945 — zaman damgası mono kalır (S2 yalnız sayıyı kapsar); kolon
                            `numeric` olduğundan DataTableCell `num` basar ve mono'yu nötrlerdi. */}
                        <td className="mono">{fmtDateTime(new Date(r.startedAt))}</td>
                        <DataTableCell dt={dt} col="dur" row={r} value={fmtDurShort(rolloutDurationSec(r, Date.now()))} />
                        <DataTableCell dt={dt} col="spans" row={r} value={fmtNum(r.spanCount)} />
                        <DataTableCell dt={dt} col="problems" row={r}>{r.problemsCaused
                          ? <Link to={`?${(() => { const p = new URLSearchParams(sp); p.set('rollout', encodeRolloutParam(r)); return p.toString(); })()}`}
                                  title="rollout başladığından beri açık problemler (servisleri) — çekmeceyi açar" style={{ textDecoration: 'none' }}>
                              <Badge tone="danger">{fmtNum(r.problemsCaused)}</Badge>
                            </Link>
                          : <span className="field-hint">—</span>}</DataTableCell>
                        <DataTableCell dt={dt} col="by" row={r} className="field-hint">{r.detectedBy}</DataTableCell>
                        <DataTableCell dt={dt} col="note" row={r} className="field-hint" title={r.note || undefined}>{r.note || ''}</DataTableCell>
                        <DataTableCell dt={dt} col="links" row={r}><Link to={tracesHref} className="sec">Traces →</Link></DataTableCell>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        ) : (
          statsQ.isPending ? <Spinner /> : err ? <Empty icon="!" title="İstatistik yüklenemedi">{err.message}</Empty> : !statsQ.data || statsQ.data.total === 0 ? (
            <Empty icon="∅" title="Bu pencerede rollout yok" />
          ) : (
            <StatsPanel st={statsQ.data} clusterName={clusterName} />
          )
        )}
        {drawerId && <RolloutDrawer id={drawerId} onClose={() => setParam('rollout', '')} />}
      </PageShell>
    </>
  );
}

// v0.10.945 (tablo standardı T1) — gün kırılımı pencere başına sınırsız
// (sunucu LIMIT 400 gün): kayıt listesi, useDataTable. initialSort yok —
// satırlar sunucunun gün sırasında kalır.
type DayRow = RolloutStats['byDay'][number];
const DAY_COLS: ColumnDef<DayRow>[] = [
  { id: 'day', label: 'Gün', sortValue: d => d.day, naturalDir: 'asc', flex: true },
  { id: 'total', label: 'Rollout', sortValue: d => d.total, naturalDir: 'desc', numeric: true, width: 90 },
  { id: 'rolledBack', label: 'Geri alınan', sortValue: d => d.rolledBack, naturalDir: 'desc', numeric: true, width: 110,
    tone: d => (d.rolledBack > 0 ? 'err' : undefined) },
];

function StatsPanel({ st, clusterName }: { st: NonNullable<ReturnType<typeof useRolloutStats>['data']>; clusterName: (id: string) => string }) {
  const dayDt = useDataTable<DayRow>({ storageKey: 'rollouts-by-day', columns: DAY_COLS, rows: st.byDay });
  return (
    <>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginBottom: 14 }}>
        <StatTile label="Rollout"><span className="mono">{fmtNum(st.total)}</span> <span className="field-hint">{st.perDay.toFixed(1)} / gün</span></StatTile>
        <StatTile label="Tamamlanan"><span className="mono">{fmtNum(st.completed)}</span></StatTile>
        <StatTile label="Geri alınan" tone={st.rolledBack > 0 ? 'err' : undefined}><span className="mono">{fmtNum(st.rolledBack)}</span> <span className="field-hint">oran %{(st.rollbackRate * 100).toFixed(1)}</span></StatTile>
        <StatTile label="Devralınan"><span className="mono">{fmtNum(st.superseded)}</span></StatTile>
        <StatTile label="Sürüyor"><span className="mono">{fmtNum(st.inProgress)}</span></StatTile>
        <StatTile label="Takılı" tone={st.stalled > 0 ? 'warn' : undefined}><span className="mono">{fmtNum(st.stalled)}</span></StatTile>
        <StatTile label="Ort. süre"><span className="mono">{fmtDurShort(Math.round(st.meanDurationSec))}</span> <span className="field-hint">p95 {fmtDurShort(Math.round(st.p95DurationSec))}</span></StatTile>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: 14 }}>
        <div>
          <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>En çok rollback alan workload'lar</h3>
          {/* v0.10.945 — statik tablo (T1): sunucunun sıraladığı topN=10 özeti, sıralanmaz. */}
          <table>
            <thead><tr><th>Workload</th><th className="num">Rollback</th></tr></thead>
            <tbody>
              {/* v0.10.954 — statik tablo durumu (T12); P-2 gelince DataTableState. */}
              {st.topRollback.length === 0 && (
                <tr data-dt-state="empty">
                  <td colSpan={2} className="dt-state"><div className="dt-state-body"><span>Bu pencerede rollback alan workload yok</span></div></td>
                </tr>
              )}
              {st.topRollback.map(w => <tr key={`${w.clusterId}/${w.namespace}/${w.workload}`}><td>{w.workload} <span className="field-hint">· {w.namespace} · {clusterName(w.clusterId)}</span></td><td className="num">{w.n}</td></tr>)}
            </tbody>
          </table>
        </div>
        <div>
          <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>En çok deploy alan workload'lar</h3>
          {/* v0.10.945 — statik tablo (T1): sunucunun sıraladığı topN=10 özeti, sıralanmaz. */}
          <table>
            <thead><tr><th>Workload</th><th className="num">Rollout</th></tr></thead>
            <tbody>
              {/* v0.10.954 — statik tablo durumu (T12); P-2 gelince DataTableState. */}
              {st.topDeploy.length === 0 && (
                <tr data-dt-state="empty">
                  <td colSpan={2} className="dt-state"><div className="dt-state-body"><span>Bu pencerede deploy alan workload yok</span></div></td>
                </tr>
              )}
              {st.topDeploy.map(w => <tr key={`${w.clusterId}/${w.namespace}/${w.workload}`}><td>{w.workload} <span className="field-hint">· {w.namespace} · {clusterName(w.clusterId)}</span></td><td className="num">{w.n}</td></tr>)}
            </tbody>
          </table>
        </div>
        <div>
          <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>Gün kırılımı</h3>
          <table {...dayDt.tableProps}>
            <DataTableColgroup dt={dayDt} />
            <DataTableHead dt={dayDt} />
            <tbody>
              {/* v0.10.954 — tablo standardı T12: elle yazılmış "yok" satırı yerine
                  standart durum satırı; colSpan görünür kolonlardan. */}
              {dayDt.sortedRows.length === 0 ? <DataTableState dt={dayDt} kind="empty" message="Bu pencerede rollout yok" /> : dayDt.sortedRows.map(d => (
                <tr key={d.day}>
                  {/* v0.10.945 — tarih damgası mono kalır (S2 yalnız sayıyı kapsar). */}
                  <DataTableCell dt={dayDt} col="day" row={d} value={d.day} className="mono" />
                  <DataTableCell dt={dayDt} col="total" row={d} value={d.total} />
                  <DataTableCell dt={dayDt} col="rolledBack" row={d} value={d.rolledBack} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
