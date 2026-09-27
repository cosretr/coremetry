import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Button, IconButton } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { api, isCanceled } from '@/lib/api';
import type { ArgoCDCandidate } from '@/lib/types';
import { domKey, fmtHourMinute, hubName, trLocative, type HubDraft, type InstanceDraft, type RemoteCluster } from './argocdForm';
import {
  completionText, hubView, markCandidates, panelMeta, parseDiscoverError, preflight, progressText, shownResult,
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

// `.card` kabı: içindeki aday tabloları çerçevesiz (tablo standardı T10, tek
// çerçeve); dolgu blokların kendisinde.
const PANEL: CSSProperties = { padding: 0 };
const PANEL_HEAD: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--sp-5)', padding: 'var(--sp-4) var(--sp-4) var(--sp-4) var(--sp-7)', borderBottom: '1px solid var(--divider)' };
const PANEL_TITLE: CSSProperties = { margin: 0, fontSize: 'var(--fs-md)', fontWeight: 600, color: 'var(--text)' };
const INTRO: CSSProperties = { margin: 0, maxWidth: 900, padding: 'var(--sp-5) var(--sp-7)', fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };
const BLOCK: CSSProperties = { borderTop: '1px solid var(--divider)' };
const BLOCK_HEAD: CSSProperties = { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--sp-4)', padding: 'var(--sp-4) var(--sp-7) var(--sp-2)' };
const HUB_TITLE: CSSProperties = { margin: 0, fontSize: 'var(--fs-sm)', fontWeight: 600, color: 'var(--text)' };
const TEXT: CSSProperties = { fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };
const DETAIL: CSSProperties = { margin: 0, padding: '0 var(--sp-7) var(--sp-3)', fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };
const NOTICE: CSSProperties = { margin: '0 var(--sp-7) var(--sp-5)', padding: 'var(--sp-5) var(--sp-6)', borderRadius: 'var(--radius-sm)', background: 'var(--bg2)', border: '1px solid var(--border)', fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };
const STRONG: CSSProperties = { color: 'var(--text)' };
const FOOT: CSSProperties = { padding: 'var(--sp-5) var(--sp-7)', borderTop: '1px solid var(--divider)' };
// v0.10.974 — mockup rerunOp: koşu sürerken yeniden-ara düğmeleri soluk (aria-disabled'ın görünür hâli).
const DIM: CSSProperties = { opacity: 0.45, cursor: 'not-allowed' };

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
  const dt = useDataTable<CandRow>({ storageKey: 'settings.argocd.candidates', columns: CAND_COLS, rows });
  const scroll = rows.length > 10;
  const table = (
    <table {...dt.tableProps} aria-label={`${name} keşif adayları`}>
      <DataTableColgroup dt={dt} />
      <DataTableHead dt={dt} />
      <tbody>
        {rows.length === 0 ? <DataTableState dt={dt} kind="empty" message="Aday yok." /> : dt.sortedRows.map(r => {
          const k = domKey(r.key);
          return (
            <tr key={r.key}>
              <DataTableCell dt={dt} col="ns" row={r} value={r.cand.hubNamespace} />
              <DataTableCell dt={dt} col="job" row={r} value={r.cand.metricsJob} />
              <DataTableCell dt={dt} col="id" row={r} value={r.id} className={r.status === 'saved' ? 'cell-muted' : undefined} />
              <DataTableCell dt={dt} col="apps" row={r} value={r.apps} title={r.countTitle || undefined} />
              <DataTableCell dt={dt} col="shards" row={r} value={r.shards} title={r.countTitle || undefined} />
              <DataTableCell dt={dt} col="note" row={r} value={r.note} title={r.noteTitle || undefined}
                className={r.noteTone === 'err' ? 'cell-err' : r.noteTone === 'warn' ? 'cell-warn' : 'cell-muted'} />
              <td {...dt.cellProps(r, 'act')}>
                {r.status === 'new' && (
                  <Button variant="secondary" size="xs" id={`acd-cand-add-${k}`}
                    aria-label={`${name} · ${r.cand.hubNamespace || r.cand.metricsJob} adayını ekle`}
                    onClick={() => onAdd(r)}>+ Ekle</Button>
                )}
                {r.status === 'added' && (
                  <span className="row gap-3">
                    <span className="cell-muted">eklendi</span>
                    <Button variant="ghost" size="xs" id={`acd-cand-undo-${k}`} aria-label={`${r.id} eklemesini geri al`}
                      onClick={() => onUndo(r)}>Geri al</Button>
                  </span>
                )}
                {r.status === 'saved' && <span className="cell-faint">kayıtlı</span>}
                {r.status === 'error' && <span className="cell-empty">—</span>}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
  return scroll ? (
    <div className="table-wrap is-scroll" style={{ maxHeight: 360 }} tabIndex={0} role="region" aria-label={`${name} adayları (kaydırılabilir)`}>
      {table}
    </div>
  ) : <div className="table-wrap">{table}</div>;
}

export function ArgoCDDiscoveryPanel({ hubs, clusters, instances, leading, onAdd, onUndo, announce, focus }: {
  hubs: HubDraft[];
  clusters: RemoteCluster[];
  instances: InstanceDraft[];
  /** Araç satırının başı ("Instance ekle" — sekmenin düğmesi). */
  leading: ReactNode;
  onAdd: (hubClusterId: string, cand: ArgoCDCandidate, id: string) => void;
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
  const undo = (r: CandRow) => {
    if (!r.matchKey) return;
    onUndo(r.matchKey);
    focus(`acd-cand-add-${domKey(r.key)}`);
  };

  const views = shown.map(viewOf);
  const label = busy ? progress : open ? 'Keşif sonucunu gizle' : "Hub'larda instance ara";

  return (
    <>
      <div className="row gap-4 row-wrap">
        {leading}
        <Button variant="secondary" size="sm" id="acd-disc-btn" aria-expanded={open} aria-controls="acd-disc-panel"
          disabled={hubs.length === 0} aria-disabled={busy || undefined} title={hubs.length === 0 ? 'Önce bir hub ekleyin' : undefined}
          onClick={toggle}>
          {label}
        </Button>
        <span className="field-hint">
          Keşif salt okunur ve hub başına ayrı bir istektir: hiçbir şey kaydedilmez; denetim kaydına hub başına bir <code>settings.argocd.discover</code> yazılır.
        </span>
      </div>
      {open && (
        <section id="acd-disc-panel" className="card" aria-label="Keşif sonucu" aria-busy={busy} style={PANEL}>
          <div style={PANEL_HEAD}>
            <h4 style={PANEL_TITLE}>Keşif sonucu</h4>
            <span className="field-hint">{at ? panelMeta(at, views) : 'aranıyor…'}</span>
            <span className="row-grow" />
            <Button variant="ghost" size="sm" aria-disabled={busy || undefined} style={busy ? DIM : undefined}
              onClick={() => { if (!busy) void run(all()); }}>Yeniden ara</Button>
            <IconButton size="sm" icon="✕" aria-label="Keşif sonucunu kapat" onClick={() => { setOpen(false); focus('acd-disc-btn'); }} />
          </div>
          <p style={INTRO}>
            Her hub'da <code>argocd_app_info</code> serilerinin <code>job</code>, <code>namespace</code> ve <code>exported_namespace</code> değerleri
            label-values API'siyle okunur (≤50 iş, iş başına ≤100 değer, çağrı başına 15 sn); ayrıca iş başına bir count sorgusu (uygulama) ile
            pod değerleri (shard) — hub başına ≤150 çağrı ve 60 sn. “Ekle” adayı tabloya kaydedilmemiş satır olarak koyar; Kaydet'e basmadan
            hiçbir şey yazılmaz.
          </p>
          {shown.map((h, ix) => {
            const v = views[ix];
            const name = hubName(h.clusterId, clusters);
            const r = runs[h.clusterId];
            const rows = marked.get(h.clusterId) ?? [];
            const running = r.kind === 'running';
            return (
              <div key={h.key} style={BLOCK}>
                <div style={BLOCK_HEAD}>
                  <h5 className="mono" style={HUB_TITLE}>{name}</h5>
                  {v.badge && <Badge tone={v.badge.tone}>{v.badge.text}</Badge>}
                  <span style={TEXT}>{v.summary}</span>
                  <span className="row-grow" />
                  <Button variant="ghost" size="xs" aria-disabled={busy || undefined} style={busy ? DIM : undefined}
                    onClick={() => { if (!busy) void run(one(h)); }}>
                    {running ? `${name} aranıyor…` : `${name}${trLocative(name)} yeniden ara`}
                  </Button>
                </div>
                {v.detail.map((line, i) => <p key={i} style={DETAIL}>{line}</p>)}
                {v.notice?.kind === 'unauthorized' && (
                  <div role="alert" style={NOTICE}>
                    <div style={STRONG}>{name} Thanos'u isteği reddetti: kayıttaki token (ya da tokenRef) bu Thanos'ta okuma yetkisi taşımıyor. Aday listesi boş, ama bu “Argo CD yok” demek değil.</div>
                    <div>Yapılacak: <Link to="/settings/clusters">Ayarlar › Remote clusters › {name}</Link> kaydında token'ı ya da tokenRef'i <code>cluster-monitoring-view</code> yetkili bir ServiceAccount token'ıyla yenileyin, kaydedin, sonra yeniden arayın.</div>
                    <div className="row gap-4 row-wrap">
                      <Button variant="secondary" size="xs" id={`acd-disc-retry-${domKey(h.clusterId)}`} aria-disabled={busy || undefined} style={busy ? DIM : undefined}
                        onClick={() => { if (!busy) void run(one(h)); }}>
                        {name}{trLocative(name)} yeniden ara
                      </Button>
                    </div>
                  </div>
                )}
                {v.notice?.kind === 'emptyLabel' && (
                  <div style={NOTICE}>
                    <div style={STRONG}><code>argocd_app_info</code> için 0 seri döndü. Sorgu hatasız; ama {name} kaydı her sorguya <code>{v.notice.label}</code> ekliyor. Argo serileri farklı dış etiketlerle geliyorsa (ör. user-workload monitoring) bu eşleştirici hepsini dışarıda bırakır.</div>
                    <div>Yapılacak: etiketsiz bir kez daha arayın. Aday çıkarsa Hub'lar tablosunda {name} için “Argo sorgularına eklenen küme etiketi” işaretini kaldırıp kaydedin. Etiketsiz arama da boşsa {name}{trLocative(name)} Argo metrikleri toplanmıyordur (ServiceMonitor).</div>
                    <div className="row gap-4 row-wrap">
                      <Button variant="secondary" size="xs" aria-disabled={busy || undefined} style={busy ? DIM : undefined}
                        onClick={() => { if (!busy) void run(one(h, true)); }}>
                        {name}{trLocative(name)} etiketsiz yeniden ara
                      </Button>
                      <span className="field-hint">Tek seferlik; ayarı değiştirmez.</span>
                    </div>
                  </div>
                )}
                {v.notice?.kind === 'emptyNoLabel' && (
                  <div style={NOTICE}>
                    <code>argocd_app_info</code> için 0 seri döndü{v.notice.afterOneShot ? ' (etiketsiz arama)' : ''}. Sorgu hatasız ve küme etiketi eklenmedi: {name}{trLocative(name)} Argo metrikleri toplanmıyordur (ServiceMonitor) ya da hub'ın Thanos'u bu kümeyi görmüyor.
                  </div>
                )}
                {v.notice?.kind === 'oneShotFound' && (
                  <div style={NOTICE}>
                    Etiketsiz aramada aday çıktı: Hub'lar tablosunda {name} için “Argo sorgularına eklenen küme etiketi” işaretini kaldırıp kaydedin; yoksa işçiler bu hub'da 0 seri görür.
                  </div>
                )}
                {shownResult(r) && <HubCandidates name={name} rows={rows} onAdd={cr => add(h, cr)} onUndo={undo} />}
              </div>
            );
          })}
          <div style={FOOT}>
            <ArgoCDNote>
              Uygulama: iş başına tek anlık <code>{'count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{job="…"}))'}</code> · Shard: iş başına <code>pod</code> değerleri (≤100). Aynı namespace iki hub'da iki ayrı instance'tır; “kayıtlı” eşleşmesi yalnız o hub'daki kayıtlarla (namespace, metricsJob) yapılır.
            </ArgoCDNote>
          </div>
        </section>
      )}
    </>
  );
}
