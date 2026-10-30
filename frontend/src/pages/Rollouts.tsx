// Rollouts — v0.10.201 (ROLLOUTS Faz 4; docs/audits/rollouts-audit.md §4, §9, §14.1).
// «Deployment Report» yerine canlı, olay-tabanlı akış: workload_rollouts
// satırları (reconciler yazar — satır = gözlenmiş giriş olayı) + agregat
// sekmesi (deploy sıklığı, rollback oranı, süre — DORA zemini).
//
// Canlılık: Plan A (audit §3) — `event: rollout` yalnız INVALIDATION sinyali
// (eventInvalidations.ts → keys.rollouts.listAll); satırlar snapshot
// refetch'iyle gelir, 15 s poll SSE kaybının emniyeti; pencere refreshTick'le
// yürür (Dashboard emsali — mount'ta donmuş `to` canlı sekmeyi öldürür).
// Tablo SUNUCU sıralı (ORDER BY + LIMIT): istemci sıralaması kesik sayfada
// yalan söyler → serverSort. v0.10.1267 (operatör 2026-10-17): Başladı /
// Workload / Namespace / Cluster başlıkları GERÇEK sunucu sıralamasını sürer
// (?sort=<id>.<dir>, varsayılan started.desc URL'de yok; lib/rolloutSort.ts);
// öteki kolonlar sıralanmaz. Canlılık sıralamayı bozmaz: SSE olayı satır
// EKLEMEZ, yalnız listAll'ı geçersiz kılar → aynı sort'la yeniden çekilir.
// URL = kaynak: ?tab, ?status, ?cluster, ?namespace (debounce), ?range, ?sort.
// Bayrak kapalı → 404 {disabled:true} SENTİNELİ (hata değil; entities emsali)
// → Empty + admin için tek tık Enable (mevcut ayarları EZMEDEN: read-modify-write).
// Koşu durumu (runs) admin ucu: viewer'da hiç sorulmaz (403 döngüsü olmasın).
// Satır çekmecesi + health verdict + tekil `/api/rollout` istemcisi Faz 4b.
// v0.10.984 (Rollouts v2 P2.3; docs/rollouts/v2-audit.md §12.1): sayfa adı
// «Deployment/Rollouts» (nav, ⌘K, Topbar, kapalı durumu). Sunucu
// rollouts.source="v2" iken rollout_events okur ve `v2: true` döner → v2
// kolon seti (karar 4: "Kaynak" = detectedBy kolonu düşer — v2'de hep KSM;
// span sayısı yok; Nesil + replika gelir) AYRI storageKey'le (kayıtlı
// genişlikler öteki sete yanlış uygulanmasın). Durum süzgeci v1 sözlüğünde
// kalır (sunucu çevirir); satır kimliği / ?rollout= 6 parçalı (rolloutRow.ts).
// v0.10.1420 (operatör: "Workload seçilerek filtrelenebilsin" + "Deployment
// Workload basınca service'e girsin, şu an bulamıyor"): ?workload= SUNUCU
// süzgeci (liste 200'de kesilir — istemci süzgeci satır kaçırırdı), sunucu
// aramalı RolloutWorkloadPicker; Canlı + Toplu ikisi de daralır. Workload
// hapının BİRİNCİL tıkı satırın servisini açar (sunucu tek toplu MV sorgusuyla
// `service` basar); eşleşme yoksa entity sayfası (wl: yedeği). Yanındaki küçük
// süzgeç düğmesi ("Bu workload'a süz") ve Namespace hücresi ?workload= /
// ?namespace= yazar.
import { useEffect, useMemo, useState } from 'react';
import { TabStrip } from '@/components/ui/TabStrip'; // v0.10.456 (D5)
import { Link, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { Spinner, Empty } from '@/components/Spinner';
import { Badge, Button, Field, StatTile } from '@/components/ui';
import {
  useDataTable, DataTableColgroup, DataTableHead, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { useUrlRange } from '@/lib/useUrlRange';
import { timeRangeToNs } from '@/lib/utils';
import { fmtNum, fmtDateTime, fmtDurShort, tsCompact } from '@/lib/utils';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { keys, useRollouts, useRolloutStats, useRolloutRuns, useEntityClusters } from '@/lib/queries';
import { RolloutDrawer } from '@/components/RolloutDrawer';
import { RolloutWorkloadPicker } from '@/components/RolloutWorkloadPicker'; // v0.10.1420
import { IconButton } from '@/components/ui/IconButton';
import { LinkButton } from '@/components/ui/LinkButton';
import { Filter } from 'lucide-react';
import { serviceHref } from '@/lib/serviceHref';
import { SvcBadge } from '@/components/traces/shared'; // v0.10.1328 — Traces Service kolonuyla aynı hap
import { tracesPivotHref } from '@/lib/pivotHref';
import { entityHref } from '@/lib/entityHref';
import { rolloutKey, statusTone, statusLabel, statusTitle, rolloutDurationSec, shortRevision, imageDiff, encodeRolloutParam, decodeRolloutParam, rolloutTracesFilters, rolloutChangeKind, changeKindLabel, changeKindTitle, changeKindTone, originBadge, replicaSummary, parseNamespacePatterns, invalidNamespacePatterns, namespaceExcluded } from '@/lib/rolloutRow';
import { ROLLOUT_SORT_DEFAULT, ROLLOUT_SORT_IDS, ROLLOUT_SORT_URL_KEY, parseRolloutSort, rolloutSortApiParams } from '@/lib/rolloutSort';
import type { RolloutStats, WorkloadRollout } from '@/lib/types';

const STATUSES = ['', 'in_progress', 'completed', 'rolled_back', 'superseded', 'stalled'] as const;

// Sunucu sıralı sayfa: sortValue yalnız SUNUCUNUN sıralayabildiği kolonlarda
// (v0.10.1267 — serverSort kipinde erişimci hiç çağrılmaz; sıralanabilir
// işaret + naturalDir taşıyıcısı). Ötekilerde YOK: kesik 200 satırı istemcide
// sıralamak yanıltır (dataTable.ts serverSort sözleşmesi; T3).
// v0.10.205 — operatör bildirimi: prod'da (uzun workload/imaj adları) fit
// kolonları 48px tabanına kadar ezdi, hücreler ve BAŞLIKLAR ortadan
// kırpıldı ("KA…", "SP…"). minWidth tabanı = fit'in ezemeyeceği okunabilir
// genişlik; toplam kabı aşarsa .table-wrap zaten overflow-x:auto kaydırır
// (v0.9.1078). Başlık kırpılması da biter: taban her başlığın tam adını
// taşıyacak kadar geniş.
// v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor") — tabanların toplamı
// (1464 px) 1366'lık laptopta kabı aşıyor ve tablo yatay kayıyordu. Artık
// öncelikle gizlenir ("+N sütun"): önce Not / Kaynak / Span / Tür, sonra Süre
// ve Revizyon; Workload esneyen kimlik kolonu.
const COLS: ColumnDef<WorkloadRollout>[] = [
  // v0.10.1205 (operatör) — zaman EN BAŞTA, durum onun eski yerinde.
  { id: 'started', label: 'Başladı', width: 136, minWidth: 128, numeric: true, priority: 1, sortValue: r => r.startedAt, naturalDir: 'desc' }, // v0.10.1068 — yılsız damga (tsCompact), tam damga title'da
  { id: 'workload', label: 'Workload', flex: true, minWidth: 180, priority: 1, sortValue: r => r.workload, naturalDir: 'asc' },
  // v0.10.1262 (operatör 2026-10-17: "workload ve namespace ayrı kolonlarda
  // yazılabilir") — namespace Workload hücresinin soluk eki değil, kendi
  // kolonu: ayrı genişletilir (genişlik storageKey'de kalıcı). v0.10.1267 —
  // sıralanabilir, ama SUNUCUDA (ORDER BY namespace, started_at DESC … LIMIT).
  { id: 'namespace', label: 'Namespace', width: 190, minWidth: 120, priority: 1, sortValue: r => r.namespace, naturalDir: 'asc' },
  // v0.10.565 (operatör-raporlu): cluster adı Workload hücresinin SONUNDA
  // soluk ek olarak duruyordu ve 300px kolonda ellipsis'e kurban gidiyordu
  // ("workload · ns · c…"). Kendi kolonu: her zaman okunur, ayrı
  // genişletilebilir, üstteki Cluster süzgeciyle aynı adı gösterir.
  // v0.10.1267 — sunucu cluster_id'ye (EffectiveID) göre sıralar; ad çoğunlukla id'dir.
  { id: 'cluster', label: 'Cluster', width: 150, minWidth: 110, priority: 2, sortValue: r => r.clusterId, naturalDir: 'asc' },
  { id: 'kind', label: 'Tür', width: 100, minWidth: 64, priority: 4 },
  { id: 'change', label: 'Değişiklik', width: 130, minWidth: 100, priority: 2 }, // v0.10.234 — imaj değişti mi (Deployment) / aynı mı (config)
  { id: 'revision', label: 'Revizyon', width: 150, minWidth: 120, mono: true, priority: 3 },
  { id: 'image', label: 'İmaj (eski → yeni)', width: 300, minWidth: 170, mono: true, priority: 2 },
  { id: 'status', label: 'Durum', width: 120, minWidth: 96, priority: 2 },
  { id: 'dur', label: 'Süre', width: 90, minWidth: 64, numeric: true, priority: 3 },
  { id: 'spans', label: 'Span', width: 90, minWidth: 64, numeric: true, priority: 4 },
  { id: 'problems', label: 'Problem', width: 90, minWidth: 64, numeric: true, priority: 2 }, // v0.10.244 — D4: başlangıçtan beri açık problemler (çekmece sayımı)
  { id: 'by', label: 'Kaynak', width: 90, minWidth: 76, priority: 4 },
  { id: 'note', label: 'Not', width: 260, minWidth: 120, priority: 4 },
  { id: 'links', label: '', width: 120, minWidth: 96, priority: 2 },
];

// v0.10.984 — v2 kolon seti (rollout_events): span / Kaynak yok (karar 4 —
// P3.4'ün tetikleyici kolonu "Kaynak" adını YENİ kimlikle alacak), revizyondan
// sonra Nesil (+ güncel/hazır/istenen replika).
const COLS_V2: ColumnDef<WorkloadRollout>[] = COLS.flatMap(c =>
  c.id === 'spans' || c.id === 'by' ? []
  : c.id === 'revision' ? [c, { id: 'gen', label: 'Nesil', width: 130, minWidth: 96, numeric: true, priority: 3 }]
  : [c]);

const ROLLOUTS_PAGE_TITLE = 'Deployment/Rollouts';

export default function RolloutsPage() {
  const [range, setRange] = useUrlRange('24h');
  const [sp, setSp] = useSearchParams();
  const tab = sp.get('tab') === 'stats' ? 'stats' : 'live';
  const status = sp.get('status') ?? '';
  const cluster = sp.get('cluster') ?? '';
  const ns = sp.get('namespace') ?? '';
  const wl = sp.get('workload') ?? ''; // v0.10.1420 — sunucu süzgeci (birebir ad)
  // v0.10.1267 — sunucu sırası doğrudan URL'den (tek kaynak; tanınmayan →
  // varsayılan). Hook başlık tıklamasında aynı parametreyi yazar.
  const sortParam = sp.get(ROLLOUT_SORT_URL_KEY);
  const srvSort = useMemo(() => rolloutSortApiParams(parseRolloutSort(sortParam)), [sortParam]);
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
  const listQ = useRollouts({ from, to, cluster: cluster || undefined, namespace: ns || undefined, workload: wl || undefined, status: status || undefined, limit: 200, ...srvSort }, tab === 'live');
  const statsQ = useRolloutStats({ from, to, cluster: cluster || undefined, namespace: ns || undefined, workload: wl || undefined, topN: 10 }, tab === 'stats');
  const runsQ = useRolloutRuns(isAdmin); // admin ucu: viewer'da 403 döngüsü açma
  const rows = useMemo(() => listQ.data?.rollouts ?? [], [listQ.data]);
  // v0.10.984 — iki kanca da her render'da çağrılır (kanca sırası sabit);
  // görünen tablo sunucunun kaynağına göre seçilir, öteki boş satırla durur.
  const v2 = listQ.data?.v2 === true;
  // v0.10.1267 — sıra iki kancada da AYNI URL parametresinde (sortUrlKey):
  // kaynak çevrilince paylaşılan link aynı sırayı açar; kişisel kayıt yok
  // (persistSort:false — varsayılan "en yeni önce"), varsayılan URL'de yok.
  const sortOpts = { serverSort: true, initialSort: ROLLOUT_SORT_DEFAULT, persistSort: false, sortIds: ROLLOUT_SORT_IDS,
    sortUrlKey: ROLLOUT_SORT_URL_KEY, sortUrlOmitInitial: true } as const;
  const dtV1 = useDataTable<WorkloadRollout>({ storageKey: 'rollouts-live-t1', columns: COLS, rows: v2 ? [] : rows, ...sortOpts });
  const dtV2 = useDataTable<WorkloadRollout>({ storageKey: 'rollouts-live-v2-t1', columns: COLS_V2, rows: v2 ? rows : [], ...sortOpts });
  const dt = v2 ? dtV2 : dtV1;
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
  const liveFiltered = !!(cluster || ns || wl || status);
  const showLiveRows = !listQ.isPending && !err && rows.length > 0;
  const liveState: Omit<DataTableStateProps<WorkloadRollout>, 'dt'> =
    listQ.isPending ? { kind: 'loading' }
    : err ? { kind: 'error', message: `Rollout listesi yüklenemedi: ${err.message}` }
    : liveFiltered ? { kind: 'no-match', ...(listQ.data?.note ? { message: `Eşleşme yok — ${listQ.data.note}` } : {}) }
    : { kind: 'empty', message: `Bu pencerede rollout yok — ${listQ.data?.note ?? (v2
      ? 'KSM dedektörü bu pencerede nesil artışı görmedi (ölçek ve yalnız-annotation değişiklikleri rollout sayılmaz).'
      : 'Reconciler aktif kümeye giren yeni revizyon görmedi (giriş: ≥2 karar kovası + gözlenmiş yokluk).')}` };
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
      <Topbar title={ROLLOUTS_PAGE_TITLE} range={range} onRangeChange={setRange} />
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
          {/* v0.10.1420 — sunucu aramalı seçici; seçim ?workload= (replace) → liste/Toplu SUNUCUDA daralır. */}
          <div style={{ display: 'grid', gap: 4, fontSize: 11, color: 'var(--text3)' }}>
            Workload
            <RolloutWorkloadPicker value={wl} onCommit={v => setParam('workload', v)} from={from} to={to}
              cluster={cluster || undefined} namespace={ns || undefined} />
          </div>
          {isAdmin && (
            <span className="field-hint" style={{ marginLeft: 'auto' }}>
              {/* v0.10.929 (K5) — arka plan işinin 'ok' bitişi normal sonuç: nötr.
                  'skipped' de nötr: kapanışta (ctx iptali) yarıda kesilen tik,
                  arıza değil (internal/rollout/reconciler.go RunSkipped).
                  Sapma yalnız partial (uyarı) ve failed/bilinmeyen (hata). */}
              {/* v0.10.984 — v2'de koşular KSM dedektörünün (rollout_worker_runs, worker alanı dolu). */}
              {lastRun
                ? <>{lastRun.worker ? 'dedektör' : 'reconciler'} son koşu {fmtDateTime(new Date(lastRun.startedAt))} · <span className={`badge ${lastRun.status === 'ok' || lastRun.status === 'skipped' ? 'b-gray' : lastRun.status === 'partial' ? 'b-warn' : 'b-err'}`}>{lastRun.status}</span></>
                : runsQ.isPending ? null : `${v2 ? 'dedektör' : 'reconciler'} henüz koşmadı`}
            </span>
          )}
        </div>
        {!disabled && (
          <PlatformNsBar
            patterns={tab === 'live' ? listQ.data?.excludeNamespaces : statsQ.data?.excludeNamespaces}
            applied={(tab === 'live' ? listQ.data?.excludeApplied : statsQ.data?.excludeApplied) === true}
            explicitNs={ns} isAdmin={isAdmin}
            onSaved={() => qc.invalidateQueries({ queryKey: keys.rollouts.all })} />
        )}
        {disabled ? (
          <Empty icon="—" title={`${ROLLOUTS_PAGE_TITLE} kapalı`}
            action={isAdmin ? <Button variant="primary" size="sm" loading={enabling} onClick={() => void enable()}>Etkinleştir</Button> : undefined}>
            {/* v0.10.984 — Ayarlar'da Rollouts sekmesi yok (v2-audit §2.6 madde 5): gerçek yol bu sayfadaki düğme. */}
            Olay tablosu yazılmıyor. {isAdmin ? (enableErr ? `Açılamadı: ${enableErr}` : 'Mevcut ayarlar korunarak açılır.') : 'Bir admin bu sayfadaki «Etkinleştir» ile açar.'}
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
                        {/* v0.10.945 — zaman damgası mono kalır (S2 yalnız sayıyı kapsar); kolon
                            `numeric` olduğundan DataTableCell `num` basar ve mono'yu nötrlerdi. */}
                        <td className="mono" title={rolloutTimingTitle(r)}>
                          {tsCompact(r.startedAt * 1e6)}
                          {/* v0.10.1366 (operatör: "hazır +3 yazıyor, gerek yok") — alt satır kalktı;
                              hazır/trafik gecikmesi hücrenin title'ında ve çekmecenin zaman çizelgesinde. */}
                        </td>
                        {/* v0.10.1328 (operatör-raporlu, prod): workload kutu düğme (a.sec) değil,
                            Traces Service kolonunun servis-renkli hapı (SvcBadge; aynı hash →
                            aynı renk). Gerçek <Link> kalır; tam bağlam hapın title'ında. */}
                        {/* v0.10.1420 — birincil tık: satırın servisi (sunucu `service`); eşleşme yoksa
                            entity sayfası (wl: yedeği). İkincil: küçük süzgeç düğmesi (?workload=). */}
                        <DataTableCell dt={dt} col="workload" row={r} title={`${cname} / ${r.namespace} / ${r.workload}`}>
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, maxWidth: '100%' }}>
                            <SvcBadge name={r.workload} to={r.service ? serviceHref(r.service, { range }) : wlHref} title={workloadLinkTitle(r, cname)} />
                            <IconButton variant="bare" size="xs" aria-label="Bu workload'a süz" tooltip={`Bu workload'a süz: ${r.workload}`}
                              active={wl === r.workload} onClick={() => setParam('workload', wl === r.workload ? '' : r.workload)}
                              icon={<Filter size={11} aria-hidden />} />
                          </span>
                        </DataTableCell>
                        {/* v0.10.1262 — namespace kendi kolonunda. v0.10.1420 — tık = bu namespace'e süz. */}
                        <DataTableCell dt={dt} col="namespace" row={r} title={r.namespace}>
                          <LinkButton tone="muted" underline="dotted" title={`Bu namespace'e süz: ${r.namespace}`}
                            onClick={() => setParam('namespace', r.namespace)}>{r.namespace}</LinkButton>
                        </DataTableCell>
                        {/* v0.10.565 — cluster kendi kolonunda; ad çözülemezse ham id (clusterName). */}
                        <DataTableCell dt={dt} col="cluster" row={r} value={cname} />
                        <DataTableCell dt={dt} col="kind" row={r} className="field-hint">{r.kind || '—'}</DataTableCell>
                        <DataTableCell dt={dt} col="change" row={r}>{(() => { const k = rolloutChangeKind(r); const ob = originBadge(r); /* v0.10.1445 — git kaynağı rozeti yalnız sapmada */ return <><Badge tone={changeKindTone(k)} title={changeKindTitle(k)}>{changeKindLabel(k)}</Badge>{ob && <> <Badge tone={ob.tone} title={ob.title}>{ob.label}</Badge></>}</>; })()}</DataTableCell>
                        <DataTableCell dt={dt} col="revision" row={r} title={r.revision || undefined}>{r.revision ? shortRevision(r.revision, r.workload) : '—'}{r.prevRevision ? <span className="field-hint"> ← {shortRevision(r.prevRevision, r.workload)}</span> : null}</DataTableCell>
                        {v2 && <DataTableCell dt={dt} col="gen" row={r} title="nesil · güncel/hazır/istenen replika (KSM)">{fmtNum(r.generation ?? 0)}<span className="field-hint"> · {replicaSummary(r)}</span></DataTableCell>}
                        <DataTableCell dt={dt} col="image" row={r} title={r.image || undefined}>{imageDiff(r)}</DataTableCell>
                        <DataTableCell dt={dt} col="status" row={r}><Badge tone={statusTone(r.status)} title={[statusTitle(r.status, v2), r.completedAt ? `tamamlandı ${fmtDateTime(new Date(r.completedAt))}` : ''].filter(Boolean).join(' · ') || undefined}>{statusLabel(r.status)}</Badge></DataTableCell>
                        <DataTableCell dt={dt} col="dur" row={r} value={fmtDurShort(rolloutDurationSec(r, Date.now()))} />
                        {!v2 && <DataTableCell dt={dt} col="spans" row={r} value={fmtNum(r.spanCount)} />}
                        <DataTableCell dt={dt} col="problems" row={r}>{r.problemsCaused
                          ? <Link to={`?${(() => { const p = new URLSearchParams(sp); p.set('rollout', encodeRolloutParam(r)); return p.toString(); })()}`}
                                  title="rollout başladığından beri açık problemler (servisleri) — çekmeceyi açar" style={{ textDecoration: 'none' }}>
                              <Badge tone="danger">{fmtNum(r.problemsCaused)}</Badge>
                            </Link>
                          : <span className="field-hint">—</span>}</DataTableCell>
                        {!v2 && <DataTableCell dt={dt} col="by" row={r} className="field-hint">{r.detectedBy}</DataTableCell>}
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
  { id: 'rolledBack', label: statusLabel('rolled_back'), sortValue: d => d.rolledBack, naturalDir: 'desc', numeric: true, width: 110,
    tone: d => (d.rolledBack > 0 ? 'err' : undefined) },
];

function StatsPanel({ st, clusterName }: { st: NonNullable<ReturnType<typeof useRolloutStats>['data']>; clusterName: (id: string) => string }) {
  const dayDt = useDataTable<DayRow>({ storageKey: 'rollouts-by-day', columns: DAY_COLS, rows: st.byDay });
  // v0.10.1436 — Toplu sayaç kutuları da durum rozetiyle aynı İngilizce
  // sözlükten (statusLabel, v0.10.1421); 1421 Canlı rozetlerini çevirmiş,
  // buradaki Türkçe başlıkları (Tamamlanan / Geri alınan / …) bırakmıştı.
  return (
    <>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginBottom: 14 }}>
        <StatTile label="Rollout"><span className="mono">{fmtNum(st.total)}</span> <span className="field-hint">{st.perDay.toFixed(1)} / gün</span></StatTile>
        <StatTile label={statusLabel('completed')}><span className="mono">{fmtNum(st.completed)}</span></StatTile>
        <StatTile label={statusLabel('rolled_back')} tone={st.rolledBack > 0 ? 'err' : undefined}><span className="mono">{fmtNum(st.rolledBack)}</span> <span className="field-hint">oran %{(st.rollbackRate * 100).toFixed(1)}</span></StatTile>
        <StatTile label={statusLabel('superseded')}><span className="mono">{fmtNum(st.superseded)}</span></StatTile>
        <StatTile label={statusLabel('in_progress')}><span className="mono">{fmtNum(st.inProgress)}</span></StatTile>
        <StatTile label={statusLabel('stalled')} tone={st.stalled > 0 ? 'warn' : undefined}><span className="mono">{fmtNum(st.stalled)}</span></StatTile>
        <StatTile label="Ort. süre"><span className="mono">{fmtDurShort(Math.round(st.meanDurationSec))}</span> <span className="field-hint">p95 {fmtDurShort(Math.round(st.p95DurationSec))}</span></StatTile>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: 14 }}>
        <div>
          <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>En çok rollback alan workload'lar</h3>
          {/* v0.10.945 — statik tablo (T1): sunucunun sıraladığı topN=10 özeti, sıralanmaz. */}
          <table>
            <thead><tr><th>Workload</th><th className="num">Rollback</th></tr></thead>
            <tbody>
              {/* v0.10.967 (tablo standardı dilim 5, P-2) — statik tablonun durum satırı; colSpan = thead'deki 2 <th>.
                  Yükleniyor / hata bölümün (istatistik yanıtı KPI'ları da belirliyor) — dışarıda kalır. */}
              {st.topRollback.length === 0
                ? <DataTableState colSpan={2} kind="empty" message="Bu pencerede rollback alan workload yok" />
                : st.topRollback.map(w => <tr key={`${w.clusterId}/${w.namespace}/${w.workload}`}><td><SvcBadge name={w.workload} title={`${clusterName(w.clusterId)} / ${w.namespace} / ${w.workload}`} /> <span className="field-hint">· {w.namespace} · {clusterName(w.clusterId)}</span></td><td className="num">{w.n}</td></tr>)}
            </tbody>
          </table>
        </div>
        <div>
          <h3 style={{ fontSize: 13, fontWeight: 700, marginBottom: 6 }}>En çok deploy alan workload'lar</h3>
          {/* v0.10.945 — statik tablo (T1): sunucunun sıraladığı topN=10 özeti, sıralanmaz. */}
          <table>
            <thead><tr><th>Workload</th><th className="num">Rollout</th></tr></thead>
            <tbody>
              {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki 2 <th>. */}
              {st.topDeploy.length === 0
                ? <DataTableState colSpan={2} kind="empty" message="Bu pencerede deploy alan workload yok" />
                : st.topDeploy.map(w => <tr key={`${w.clusterId}/${w.namespace}/${w.workload}`}><td><SvcBadge name={w.workload} title={`${clusterName(w.clusterId)} / ${w.namespace} / ${w.workload}`} /> <span className="field-hint">· {w.namespace} · {clusterName(w.clusterId)}</span></td><td className="num">{w.n}</td></tr>)}
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

// v0.10.1262 — operatör 2026-10-17: "OpenShift'e ait projeler rollout
// listelemesin". Sunucu system_settings["rollouts"].excludeNamespaces
// glob'larını Canlı + Toplu sorgusunda (LIMIT'ten önce) uygular ve uygulanan
// listeyi cevapta döndürür — viewer burada SALT OKUNUR görür, admin düzenler
// (read-modify-write: tam blob PUT'u öteki vidaları ezmesin; PUT audit'li).
// Açık namespace süzgeci gizlemeyi geçersiz kılar (sunucu kuralı).
function PlatformNsBar({ patterns, applied, explicitNs, isAdmin, onSaved }: {
  patterns: string[] | undefined; applied: boolean; explicitNs: string; isAdmin: boolean; onSaved: () => Promise<unknown>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState('');
  if (!patterns) return null; // cevap henüz yok / eski sunucu
  const save = async () => {
    const list = parseNamespacePatterns(draft);
    const bad = invalidNamespacePatterns(list);
    if (bad.length) { setErr(`Geçersiz desen: ${bad.join(', ')} (küçük harf, rakam, '-', '.', '*', '?')`); return; }
    setSaving(true);
    setErr('');
    try {
      const cur = await api.rolloutSettings();
      await api.putRolloutSettings({ ...cur.settings, excludeNamespaces: list });
      setEditing(false);
      await onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };
  if (editing) {
    return (
      <div className="controls" style={{ marginBottom: 12, gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <div style={{ flex: '1 1 360px', maxWidth: 560 }}>
          <Field label="Gizlenen platform namespace'leri (glob)" value={draft} onChange={e => setDraft(e.target.value)}
            placeholder="openshift, openshift-*, kube-*" className="mono" error={err || undefined}
            hint="Virgül ya da boşlukla ayır; * = herhangi dizi, ? = tek karakter, tam ad eşleşir. Boş = hiçbirini gizleme." />
        </div>
        <Button variant="primary" size="sm" loading={saving} onClick={() => void save()}>Kaydet</Button>
        <Button variant="secondary" size="sm" disabled={saving} onClick={() => { setEditing(false); setErr(''); }}>Vazgeç</Button>
      </div>
    );
  }
  const overridden = !!explicitNs && !applied && namespaceExcluded(patterns, explicitNs);
  return (
    <div className="field-hint" style={{ marginBottom: 10, display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
      {patterns.length === 0
        ? <span>Platform namespace gizleme kapalı — tüm namespace'ler listelenir.</span>
        : <span>Gizlenen platform namespace'leri: <span className="mono">{patterns.join(', ')}</span>
            {overridden && <> — açık namespace süzgeci «{explicitNs}» bu gizlemeyi geçersiz kılıyor</>}</span>}
      {isAdmin && <Button variant="ghost" size="sm" onClick={() => { setDraft(patterns.join(', ')); setEditing(true); }}>Düzenle</Button>}
    </div>
  );
}

// v0.10.1420 — Workload hapının title'ı: nereye gidiyor (servis / entity yedeği).
function workloadLinkTitle(r: WorkloadRollout, cname: string): string {
  const where = `${cname} / ${r.namespace} / ${r.workload}`;
  if (!r.service) return `${where}\nServis eşleşmedi — envanter kaydını açar`;
  const others = (r.services ?? []).filter(s => s !== r.service);
  return `${where}\nServis sayfasını açar: ${r.service}` + (others.length ? `\nDiğer servisler: ${others.join(', ')}` : '');
}

// v0.10.1212 — başladı / hazır / trafik tam zamanları (hücre title'ı).
function rolloutTimingTitle(r: WorkloadRollout): string {
  const ln = [`Başladı: ${fmtDateTime(new Date(r.startedAt))}`];
  if (r.podsReadyAt) ln.push(`Pod'lar hazır: ${fmtDateTime(new Date(r.podsReadyAt))}`);
  if (r.firstSpanAt) ln.push(`Trafik yeni sürüme geçti (ilk span): ${fmtDateTime(new Date(r.firstSpanAt))}`);
  return ln.join('\n');
}
