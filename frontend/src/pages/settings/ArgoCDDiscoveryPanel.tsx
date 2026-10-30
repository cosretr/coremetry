import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { X } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import type { Status } from '@/lib/statusTone';
import { api, isCanceled } from '@/lib/api';
import type { ArgoCDCandidate } from '@/lib/types';
import { domKey, fmtHourMinute, hubName, trLocative, type HubDraft, type InstanceDraft, type RemoteCluster } from './argocdForm';
import {
  addableRows, completionText, hubView, markCandidates, panelMeta, parseDiscoverError, preflight, progressText, shownResult,
  type CandRow, type HubRun, type HubView,
} from './argocdDiscovery';
import { ArgoCDNote } from './ArgoCDSectionPanel';

// ArgoCDDiscoveryPanel — v0.10.974 — "Hub'larda instance ara" (mockup Main
// "Keşif sonucu" + States (b)). Salt okunur: hiçbir şey kaydedilmez; her
// POST sunucuda `settings.argocd.discover` denetim kaydı yazar.
//
//   • Hub başına TEK POST, SIRAYLA (pod başına tek keşif; aynı anda ikincisi
//     429), taslak hub sırasıyla. Gövde HEP `hubClusterId` + o hub'ın TASLAK
//     `injectClusterLabel`'ı (kaydedilmemiş işaret de denenir). Remote
//     Cluster kaydı silinmiş / devre dışı hub'a istek GİTMEZ
//     ("yapılandırılmamış").
//   • "etiketsiz yeniden ara" TEK SEFERLİK `injectClusterLabel: false`
//     gönderir; ayarı değiştirmez.
//   • Sonuçlar yalnız sayfa durumunda (URL'de değil: kaydedilmemiş satırlara
//     atıf yapar); panel kapalı başlar; sekme sökülünce istek AbortController
//     ile kesilir; "sn" istemci kronometresi.
//   • Adaylar TASLAĞA karşı yeniden işaretlenir (argocdDiscovery.ts): "Ekle"
//     kaydedilmemiş satır ekler (discovered: true) → "eklendi" + "Geri al".
//   • v0.10.974 — yeniden arama sırasında hub'ın ÖNCEKİ aday tablosu kalır
//     (mockup buildDisc: yalnız özet "aranıyor…", rozet/ayrıntı gizli). "Ekle"
//     koşu sırasında da çalışır (mockup addCand'de meşgul denetimi yok; gelen
//     sonuç taslağa karşı yeniden işaretlenir). Yeniden-ara düğmeleri koşu
//     sırasında aria-disabled + mockup'ın soluklaştırması (rerunOp 0,45) —
//     native disabled değil: tıklanan düğme etiketini değiştirirken odağı tutar.
//   • v0.10.978 — "yetki yok" (States (b) hub-1): sunucu 401/403'ü errorType
//     unauthorized + upstreamStatus ile döner; blok rozet + "HTTP 403" özeti +
//     alert kutusu (token / cluster-monitoring-view adımları, Remote clusters
//     bağlantısı, alert içi "<hub>'de yeniden ara") çizer. Öteki hub'ın sonucu
//     yerinde kalır (koşular hub başına).
//   • v0.10.990 (operatör: "tek tek ekle diyorum ve sadece ilk 50'yi bulduğu
//     için eksikleri oluyor") — hub bloğunda "Tümünü ekle (N)": namespace'i
//     belli olan TÜM yeni adayları tek tıkla taslağa koyar (yine kaydedilmemiş
//     satır; Kaydet'e kadar hiçbir şey yazılmaz). Namespace'i bilinmeyen aday
//     (durum B, çok namespace) toplu eklemeye girmez — "+ Ekle" ile alınıp
//     satırı elle tamamlanır. Sunucu tavanları 500 iş / 500 değer / 2.000 çağrı.

// `.card` kabı: içindeki aday tabloları çerçevesiz (tablo standardı T10, tek
// çerçeve); dolgu blokların kendisinde.
// Faz 2 dilim 1 R15: CSSProperties → utility; aday tablosu useDataTable +
// elle tbody → coremetry/DataTable (`renderCell`, `ariaLabel`), rozet
// ui Badge → StatusBadge (neutral → unknown, warning → warn, danger →
// critical), ✕ IconButton → shadcn ghost icon + lucide X.
// v0.10.974 — mockup rerunOp: koşu sürerken yeniden-ara düğmeleri soluk (aria-disabled'ın görünür hâli).
const DIM = 'aria-disabled:cursor-not-allowed aria-disabled:opacity-45';
const TEXT = 'text-[length:var(--fs-sm)] leading-[18px] text-(--text2)';
const NOTICE = `mx-4 mb-2.5 flex flex-col gap-1 rounded-[var(--radius-sm)] border border-(--border) bg-(--bg2) px-3 py-2.5 ${TEXT}`;
const HINT = 'text-[length:var(--fs-xs)] text-(--text3)';
/** Boş değer soluk "—" (DataTableCell'in T4 glifi; renderCell yolunda elle). */
const orDash = (v: string | undefined): ReactNode => v || <span className="cell-empty">—</span>;
const BADGE: Record<'neutral' | 'warning' | 'danger', Status> = { neutral: 'unknown', warning: 'warn', danger: 'critical' };

const CAND_COLS: ColumnDef<CandRow>[] = [
  { id: 'ns', label: 'Namespace', width: 150, mono: true },
  { id: 'job', label: 'metricsJob', width: 190, mono: true },
  { id: 'id', label: 'Kimlik (id)', width: 150, mono: true },
  { id: 'apps', label: 'Uygulama', numeric: true, width: 84 },
  { id: 'shards', label: 'Shard', numeric: true, width: 64 },
  { id: 'note', label: 'Not', flex: true },
  // v0.10.974 — "Durum" durum metni de taşır (kayıtlı / eklendi / —): başlığı
  // görünür, sola yaslı (mockup Main); yalnız-eylem kolonu değil.
  { id: 'act', label: 'Durum', width: 140 },
];

function HubCandidates({ name, rows, onAdd, onUndo }: {
  name: string;
  rows: CandRow[];
  onAdd: (r: CandRow) => void;
  onUndo: (r: CandRow) => void;
}) {
  const scroll = rows.length > 10;
  const cell = (r: CandRow, col: ColumnDef<CandRow>): ReactNode => {
    const k = domKey(r.key);
    switch (col.id) {
      case 'ns': return orDash(r.cand.hubNamespace);
      case 'job': return orDash(r.cand.metricsJob);
      case 'id': return <span className={r.status === 'saved' ? 'text-(--text2)' : undefined}>{orDash(r.id)}</span>;
      case 'apps': return <span title={r.countTitle || undefined}>{orDash(r.apps)}</span>;
      case 'shards': return <span title={r.countTitle || undefined}>{orDash(r.shards)}</span>;
      case 'note':
        return (
          <span title={r.noteTitle || undefined}
            className={r.noteTone === 'err' ? 'text-status-critical' : r.noteTone === 'warn' ? 'text-status-warn' : 'text-(--text2)'}>
            {orDash(r.note)}
          </span>
        );
      case 'act':
        if (r.status === 'new') {
          return (
            <Button variant="outline" size="xs" id={`acd-cand-add-${k}`}
              aria-label={`${name} · ${r.cand.hubNamespace || r.cand.metricsJob} adayını ekle`}
              onClick={() => onAdd(r)}>+ Ekle</Button>
          );
        }
        if (r.status === 'added') {
          return (
            <span className="inline-flex items-center gap-3">
              <span className="text-(--text2)">eklendi</span>
              <Button variant="ghost" size="xs" id={`acd-cand-undo-${k}`} aria-label={`${r.id} eklemesini geri al`}
                onClick={() => onUndo(r)}>Geri al</Button>
            </span>
          );
        }
        if (r.status === 'saved') return <span className="text-(--text3)">kayıtlı</span>;
        return <span className="cell-empty">—</span>;
      default:
        return null;
    }
  };
  const table = (
    <DataTable<CandRow> storageKey="settings.argocd.candidates" columns={CAND_COLS} rows={rows} ariaLabel={`${name} keşif adayları`}
      getRowKey={r => r.key} renderCell={cell} state={{ kind: 'empty', message: 'Aday yok.' }} />
  );
  return scroll ? (
    <div className="table-wrap is-scroll max-h-[360px]" tabIndex={0} role="region" aria-label={`${name} adayları (kaydırılabilir)`}>
      {table}
    </div>
  ) : <div className="table-wrap">{table}</div>;
}

export function ArgoCDDiscoveryPanel({ hubs, clusters, instances, leading, onAdd, onAddAll, onUndo, announce, focus }: {
  hubs: HubDraft[];
  clusters: RemoteCluster[];
  instances: InstanceDraft[];
  /** Araç satırının başı ("Instance ekle" — sekmenin düğmesi). */
  leading: ReactNode;
  onAdd: (hubClusterId: string, cand: ArgoCDCandidate, id: string) => void;
  /** v0.10.990 — hub'ın namespace'i belli tüm yeni adaylarını tek seferde taslağa ekler. */
  onAddAll: (hubClusterId: string, items: { cand: ArgoCDCandidate; id: string }[]) => void;
  onUndo: (instanceKey: string) => void;
  announce: (text: string) => void;
  focus: (id: string) => void;
}) {
  const [runs, setRuns] = useState<Record<string, HubRun>>({});
  const [open, setOpen] = useState(false);
  const [busyHub, setBusyHub] = useState<string | null>(null);
  const [progress, setProgress] = useState('');
  const [at, setAt] = useState('');
  const busyRef = useRef(false);
  const ctlRef = useRef<AbortController | null>(null);
  useEffect(() => () => ctlRef.current?.abort(), []);

  const shown = hubs.filter(h => runs[h.clusterId]);
  const marked = useMemo(() => markCandidates(
    hubs.flatMap(h => { const d = shownResult(runs[h.clusterId]); return d ? [{ clusterId: h.clusterId, candidates: d.result.candidates }] : []; }),
    instances, clusters,
  ), [hubs, runs, instances, clusters]);
  const labelOf = useCallback((id: string) => clusters.find(c => c.id === id)?.label ?? '', [clusters]);
  const viewOf = (h: HubDraft): HubView => hubView(runs[h.clusterId], marked.get(h.clusterId) ?? [],
    { name: hubName(h.clusterId, clusters), label: labelOf(h.clusterId), clusterId: h.clusterId });

  const run = async (targets: { clusterId: string; inject: boolean; oneShot: boolean }[]) => {
    if (busyRef.current || targets.length === 0) return;
    busyRef.current = true;
    const ctl = new AbortController();
    ctlRef.current = ctl;
    setOpen(true);
    const finished: string[] = [];
    const local: Record<string, HubRun> = {};
    for (const t of targets) {
      const name = hubName(t.clusterId, clusters);
      const pre = preflight(t.clusterId, clusters);
      if (pre) {
        local[t.clusterId] = { kind: 'skipped', reason: pre };
        setRuns(r => ({ ...r, [t.clusterId]: local[t.clusterId] }));
        finished.push(name);
        continue;
      }
      const text = progressText(finished, name);
      setBusyHub(t.clusterId); setProgress(text); announce(text);
      setRuns(r => ({ ...r, [t.clusterId]: { kind: 'running', oneShot: t.oneShot, prev: shownResult(r[t.clusterId]) } }));
      const t0 = performance.now();
      try {
        const result = await api.discoverArgoCD({ hubClusterId: t.clusterId, injectClusterLabel: t.inject }, ctl.signal);
        if (ctl.signal.aborted) return;
        local[t.clusterId] = { kind: 'done', result, ms: performance.now() - t0, oneShot: t.oneShot };
      } catch (e) {
        if (ctl.signal.aborted || isCanceled(e)) return;
        local[t.clusterId] = { kind: 'failed', err: parseDiscoverError(e), ms: performance.now() - t0, oneShot: t.oneShot };
      }
      setRuns(r => ({ ...r, [t.clusterId]: local[t.clusterId] }));
      finished.push(name);
    }
    busyRef.current = false;
    setBusyHub(null); setProgress('');
    setAt(fmtHourMinute(new Date()));
    const done = targets.map(t => t.clusterId);
    const m = markCandidates(done.flatMap(id => { const r = local[id]; return r?.kind === 'done' ? [{ clusterId: id, candidates: r.result.candidates }] : []; }), instances, clusters);
    announce(completionText(done.map(id => ({
      name: hubName(id, clusters),
      view: hubView(local[id], m.get(id) ?? [], { name: hubName(id, clusters), label: labelOf(id), clusterId: id }),
    }))));
  };
  const all = () => hubs.map(h => ({ clusterId: h.clusterId, inject: h.inject, oneShot: false }));
  const one = (h: HubDraft, oneShot = false) => [{ clusterId: h.clusterId, inject: oneShot ? false : h.inject, oneShot }];

  const busy = busyHub !== null;
  const toggle = () => {
    if (busyRef.current) return;
    if (open) { setOpen(false); return; }
    void run(all());
  };

  const add = (h: HubDraft, r: CandRow) => {
    onAdd(h.clusterId, r.cand, r.id);
    focus(`acd-cand-undo-${domKey(r.key)}`);
  };
  const addAll = (h: HubDraft, rows: CandRow[]) => {
    const list = addableRows(rows);
    if (list.length === 0) return;
    onAddAll(h.clusterId, list.map(r => ({ cand: r.cand, id: r.id })));
    focus(`acd-cand-undo-${domKey(list[0].key)}`);
  };
  const undo = (r: CandRow) => {
    if (!r.matchKey) return;
    onUndo(r.matchKey);
    focus(`acd-cand-add-${domKey(r.key)}`);
  };

  const views = shown.map(viewOf);
  const label = busy ? progress : open ? 'Keşif sonucunu gizle' : "Hub'larda instance ara";

  return (
    <>
      <div className="flex flex-wrap items-center gap-4">
        {leading}
        <Button variant="outline" size="sm" id="acd-disc-btn" aria-expanded={open} aria-controls="acd-disc-panel"
          disabled={hubs.length === 0} aria-disabled={busy || undefined} title={hubs.length === 0 ? 'Önce bir hub ekleyin' : undefined}
          onClick={toggle}>
          {label}
        </Button>
        <span className={HINT}>
          Keşif salt okunur ve hub başına ayrı bir istektir: hiçbir şey kaydedilmez; denetim kaydına hub başına bir <code>settings.argocd.discover</code> yazılır.
        </span>
      </div>
      {open && (
        <section id="acd-disc-panel" className="card p-0" aria-label="Keşif sonucu" aria-busy={busy}>
          <div className="flex items-center gap-2.5 border-b border-(--divider) py-2 pr-2 pl-4">
            <h4 className="m-0 text-[length:var(--fs-md)] font-semibold text-(--text)">Keşif sonucu</h4>
            <span className={HINT}>{at ? panelMeta(at, views) : 'aranıyor…'}</span>
            <span className="flex-1" />
            <Button variant="ghost" size="sm" className={DIM} aria-disabled={busy || undefined}
              onClick={() => { if (!busy) void run(all()); }}>Yeniden ara</Button>
            <Button variant="ghost" size="icon-sm" aria-label="Keşif sonucunu kapat" onClick={() => { setOpen(false); focus('acd-disc-btn'); }}>
              <X />
            </Button>
          </div>
          <p className={`m-0 max-w-[900px] px-4 py-2.5 ${TEXT}`}>
            Her hub'da <code>argocd_app_info</code> serilerinin <code>job</code>, <code>namespace</code> ve <code>exported_namespace</code> değerleri
            label-values API'siyle okunur (≤500 iş, iş başına ≤500 değer, çağrı başına 15 sn); ayrıca iş başına bir count sorgusu (uygulama) ile
            pod değerleri (shard) — hub başına ≤2.000 çağrı ve 60 sn. “Ekle” adayı, “Tümünü ekle” hub'ın namespace'i belli tüm yeni adaylarını
            tabloya kaydedilmemiş satır olarak koyar; Kaydet'e basmadan hiçbir şey yazılmaz.
          </p>
          {shown.map((h, ix) => {
            const v = views[ix];
            const name = hubName(h.clusterId, clusters);
            const r = runs[h.clusterId];
            const rows = marked.get(h.clusterId) ?? [];
            const running = r.kind === 'running';
            const nAdd = addableRows(rows).length;
            return (
              <div key={h.key} className="border-t border-(--divider)">
                <div className="flex flex-wrap items-center gap-2 px-4 pt-2 pb-1">
                  <h5 className="m-0 font-mono text-[length:var(--fs-sm)] font-semibold text-(--text)">{name}</h5>
                  {v.badge && <StatusBadge status={BADGE[v.badge.tone]}>{v.badge.text}</StatusBadge>}
                  <span className={TEXT}>{v.summary}</span>
                  <span className="flex-1" />
                  {nAdd > 0 && (
                    <Button variant="outline" size="xs" id={`acd-disc-addall-${domKey(h.clusterId)}`}
                      aria-label={`${name} · ${nAdd} yeni adayın tümünü ekle`}
                      title="Namespace'i belli tüm yeni adayları tabloya kaydedilmemiş satır olarak ekler; Kaydet'e basmadan hiçbir şey yazılmaz."
                      onClick={() => addAll(h, rows)}>
                      Tümünü ekle ({nAdd})
                    </Button>
                  )}
                  <Button variant="ghost" size="xs" className={DIM} aria-disabled={busy || undefined}
                    onClick={() => { if (!busy) void run(one(h)); }}>
                    {running ? `${name} aranıyor…` : `${name}${trLocative(name)} yeniden ara`}
                  </Button>
                </div>
                {v.detail.map((line, i) => <p key={i} className={`m-0 px-4 pb-1.5 ${TEXT}`}>{line}</p>)}
                {v.notice?.kind === 'unauthorized' && (
                  <div role="alert" className={NOTICE}>
                    <div className="text-(--text)">{name} Thanos'u isteği reddetti: kayıttaki token (ya da tokenRef) bu Thanos'ta okuma yetkisi taşımıyor. Aday listesi boş, ama bu “Argo CD yok” demek değil.</div>
                    <div>Yapılacak: <Link to="/settings/clusters">Ayarlar › Remote clusters › {name}</Link> kaydında token'ı ya da tokenRef'i <code>cluster-monitoring-view</code> yetkili bir ServiceAccount token'ıyla yenileyin, kaydedin, sonra yeniden arayın.</div>
                    <div className="flex flex-wrap items-center gap-4">
                      <Button variant="outline" size="xs" id={`acd-disc-retry-${domKey(h.clusterId)}`} className={DIM} aria-disabled={busy || undefined}
                        onClick={() => { if (!busy) void run(one(h)); }}>
                        {name}{trLocative(name)} yeniden ara
                      </Button>
                    </div>
                  </div>
                )}
                {v.notice?.kind === 'emptyLabel' && (
                  <div className={NOTICE}>
                    <div className="text-(--text)"><code>argocd_app_info</code> için 0 seri döndü. Sorgu hatasız; ama {name} kaydı her sorguya <code>{v.notice.label}</code> ekliyor. Argo serileri farklı dış etiketlerle geliyorsa (ör. user-workload monitoring) bu eşleştirici hepsini dışarıda bırakır.</div>
                    <div>Yapılacak: etiketsiz bir kez daha arayın. Aday çıkarsa Hub'lar tablosunda {name} için “Argo sorgularına eklenen küme etiketi” işaretini kaldırıp kaydedin. Etiketsiz arama da boşsa {name}{trLocative(name)} Argo metrikleri toplanmıyordur (ServiceMonitor).</div>
                    <div className="flex flex-wrap items-center gap-4">
                      <Button variant="outline" size="xs" className={DIM} aria-disabled={busy || undefined}
                        onClick={() => { if (!busy) void run(one(h, true)); }}>
                        {name}{trLocative(name)} etiketsiz yeniden ara
                      </Button>
                      <span className={HINT}>Tek seferlik; ayarı değiştirmez.</span>
                    </div>
                  </div>
                )}
                {v.notice?.kind === 'emptyNoLabel' && (
                  <div className={NOTICE}>
                    <code>argocd_app_info</code> için 0 seri döndü{v.notice.afterOneShot ? ' (etiketsiz arama)' : ''}. Sorgu hatasız ve küme etiketi eklenmedi: {name}{trLocative(name)} Argo metrikleri toplanmıyordur (ServiceMonitor) ya da hub'ın Thanos'u bu kümeyi görmüyor.
                  </div>
                )}
                {v.notice?.kind === 'oneShotFound' && (
                  <div className={NOTICE}>
                    Etiketsiz aramada aday çıktı: Hub'lar tablosunda {name} için “Argo sorgularına eklenen küme etiketi” işaretini kaldırıp kaydedin; yoksa işçiler bu hub'da 0 seri görür.
                  </div>
                )}
                {shownResult(r) && <HubCandidates name={name} rows={rows} onAdd={cr => add(h, cr)} onUndo={undo} />}
              </div>
            );
          })}
          <div className="border-t border-(--divider) px-4 py-2.5">
            <ArgoCDNote>
              Uygulama: iş başına tek anlık <code>{'count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{job="…"}))'}</code> · Shard: iş başına <code>pod</code> değerleri (≤100). Aynı namespace iki hub'da iki ayrı instance'tır; “kayıtlı” eşleşmesi yalnız o hub'daki kayıtlarla (namespace, metricsJob) yapılır.
            </ArgoCDNote>
          </div>
        </section>
      )}
    </>
  );
}
