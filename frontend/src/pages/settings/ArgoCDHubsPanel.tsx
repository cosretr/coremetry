import { Fragment, useState } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Button } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import type { ArgoCDPin } from '@/lib/types';
import { domKey, type HubDraft, type InstanceDraft, type Issue, type RemoteCluster } from './argocdForm';
import { ArgoCDNote, ArgoCDSectionPanel } from './ArgoCDSectionPanel';

// ArgoCDHubsPanel — v0.10.974 — Ayarlar › Argo CD "Hub'lar" tablosu (mockup
// Main + States (c)). Hub = Argo CD'nin çalıştığı kümenin Thanos'unu gösteren
// sıradan bir Remote Cluster kaydı; küme etiketi enjeksiyonu HUB BAŞINA
// karar (iki hub, audit §5.6).
//
// Tablo türü: en çok 8 satırlık düzenleme listesi — yine de tablo standardı
// dilim 5 sonrası tek biçim `useDataTable` + `DataTableHead` (sıralanmaz,
// satır tıklanmaz) ve durum tablonun İÇİNDE (`DataTableState`). Silinmiş /
// devre dışı Remote Cluster tam genişlik hata satırıyla, Kaydet'i beklemeden
// görünür.
// v0.10.974 — engel iletisi sekmenin durumunda (`msg`/`onMsg`): "Değişiklikleri
// geri al" onu da siler (mockup revert: hubMsg + instMsg); geri alınan
// taslakta bayat "N instance bağlı" sayısı ekranda kalmaz.
// v0.10.997 (operatör: "Hub kaldıramıyorum instance varsa" — prod'da hub
// başına 190+ instance) — instance'ları bağlı hub'ın "Kaldır"ı artık çıkmaz
// sokak değil: iletinin altında "Hub'ı N instance ile birlikte kaldır" onayı
// çıkar ve hub + instance'ları TEK taslak değişikliğiyle kaldırır (Kaydet'e
// kadar yazılmaz; "Değişiklikleri geri al" geri getirir). Sunucu kuralı aynı
// (BE4): hub gövdeden çıkarken ona bağlı instance gövdede KALIRSA 400 —
// ikisi birlikte çıkınca kural zaten sağlanır. Tek engel pin: pin'i olan
// instance kaldırılamaz (pin editörü API'de), o hub'da onay sunulmaz.

interface HubRow { hub: HubDraft; rc: RemoteCluster | undefined; count: number; name: string }

const COLS: ColumnDef<HubRow>[] = [
  { id: 'cluster', label: 'Remote Cluster', width: 240 },
  { id: 'label', label: 'Argo sorgularına eklenen küme etiketi', flex: true },
  { id: 'count', label: 'Instance', numeric: true, width: 96 },
  { id: 'act', label: 'Eylemler', kind: 'actions', width: 120 },
];

export function ArgoCDHubsPanel({ hubs, instances, pins, clusters, enabled, issues, msg, onMsg, onAdd, onRemove, onRemoveWithInstances, onInject, announce, focus }: {
  hubs: HubDraft[];
  instances: InstanceDraft[];
  /** Kayıtlı pin'ler: pin'i olan instance (ve onu taşıyan hub) kaldırılamaz. */
  pins: ArgoCDPin[];
  clusters: RemoteCluster[];
  /** Taslaktaki entegrasyon bayrağı (devre dışı hub yalnız açıkken hata). */
  enabled: boolean;
  issues: Issue[];
  /** Hub ekleme/kaldırma engelinin iletisi (role=alert); sekme tutar. */
  msg: string;
  onMsg: (msg: string) => void;
  onAdd: (clusterId: string) => void;
  onRemove: (key: string) => void;
  /** v0.10.997 — hub'ı, ona bağlı TÜM instance'larla birlikte taslaktan kaldırır. */
  onRemoveWithInstances: (key: string) => void;
  onInject: (key: string, inject: boolean) => void;
  announce: (text: string) => void;
  focus: (id: string) => void;
}) {
  const [pick, setPick] = useState('');
  // Onay bekleyen hub (anahtar). İleti (`msg`) silinince — ör. "Değişiklikleri
  // geri al" — onay düğmeleri de gizlenir; başka bir ileti yazılırken sıfırlanır.
  const [confirm, setConfirm] = useState<string | null>(null);

  const rows: HubRow[] = hubs.map(h => {
    const rc = clusters.find(c => c.id === h.clusterId);
    return { hub: h, rc, name: rc ? rc.name : 'bilinmeyen kayıt', count: instances.filter(i => i.hubClusterId === h.clusterId).length };
  });
  const dt = useDataTable<HubRow>({ storageKey: 'settings.argocd.hubs', columns: COLS, rows });
  const isHub = (id: string) => hubs.some(h => h.clusterId === id);

  const add = () => {
    setConfirm(null);
    if (!pick) { onMsg('Önce listeden bir Remote Cluster seçin.'); return; }
    const rc = clusters.find(c => c.id === pick);
    if (isHub(pick)) { onMsg(`${rc?.name ?? pick} zaten hub.`); return; }
    onAdd(pick);
    setPick(''); onMsg('');
    announce(`${rc?.name ?? pick} hub olarak eklendi — kaydedilmedi.`);
  };
  const remove = (r: HubRow) => {
    if (r.count > 0) {
      const ids = new Set(instances.filter(i => i.hubClusterId === r.hub.clusterId).map(i => i.id));
      const pinned = pins.filter(p => ids.has(p.instanceId)).length;
      if (pinned > 0) {
        setConfirm(null);
        onMsg(`${r.name} kaldırılamaz: bu hub'a bağlı instance'larda ${pinned} pin var; önce API'den pin'leri kaldırın (PUT /api/settings/argocd, pins[]).`);
        return;
      }
      setConfirm(r.hub.key);
      onMsg(`${r.name} hub'ına ${r.count} instance bağlı. Hub'ı kaldırmak bu instance'ları da tablodan çıkarır; Kaydet'e basana kadar hiçbir şey yazılmaz.`);
      focus(`acd-hub-rmall-${domKey(r.hub.key)}`);
      return;
    }
    setConfirm(null);
    onRemove(r.hub.key);
    onMsg('');
    announce(`${r.name} hub listesinden çıkarıldı — kaydedilmedi.`);
    focus('acd-hub-add');
  };
  const pending = msg ? rows.find(r => r.hub.key === confirm && r.count > 0) : undefined;
  const removeAll = (r: HubRow) => {
    setConfirm(null);
    onRemoveWithInstances(r.hub.key);
    onMsg('');
    announce(`${r.name} hub'ı ve ${r.count} instance tablodan çıkarıldı — kaydedilmedi.`);
    focus('acd-hub-add');
  };
  const cancel = (r: HubRow) => {
    setConfirm(null);
    onMsg('');
    focus(`acd-hub-rm-${domKey(r.hub.key)}`);
  };

  return (
    <ArgoCDSectionPanel id="acd-hubs-h" title="Hub'lar" meta={`${hubs.length} hub`}
      desc="Argo CD'nin çalıştığı kümeler. Her hub ayrı bir Remote Cluster kaydıdır ve kendi Thanos'unu gösterir; /clusters'ta normal küme olarak da görünür. Keşif ve sorgular hub başına ayrı koşar.">
      <div className="table-wrap">
        <table {...dt.tableProps} aria-label="Hub'lar">
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? (
              <DataTableState dt={dt} kind="empty" message="Hub yok — aşağıdan bir Remote Cluster seçin." />
            ) : dt.sortedRows.map(r => {
              const k = domKey(r.hub.key);
              const issue = issues.find(i => i.target.kind === 'hub' && i.target.key === r.hub.key);
              const deleted = !r.rc;
              const disabled = !!r.rc && !r.rc.enabled;
              const errId = issue || deleted || disabled ? `acd-hub-err-${k}` : undefined;
              return (
                <Fragment key={r.hub.key}>
                  <tr>
                    <td {...dt.cellProps(r, 'cluster', r.name)} aria-describedby={errId}>
                      <span className={deleted ? 'mono is-err' : 'mono'}>{r.name}</span>{' '}
                      <span className="mono cell-faint">{r.hub.clusterId}</span>
                      {disabled && <>{' '}<Badge tone="neutral">devre dışı</Badge></>}
                    </td>
                    <td {...dt.cellProps(r, 'label')}>
                      {r.rc?.label ? (
                        <label className="row gap-2">
                          <input type="checkbox" checked={r.hub.inject} aria-describedby="acd-hub-inj-h"
                            aria-label={`${r.rc.label} — ${r.name} Argo sorgularına eklensin`}
                            onChange={e => onInject(r.hub.key, e.target.checked)} />
                          <span className={r.hub.inject ? 'mono' : 'mono cell-faint'}>{r.rc.label}</span>
                          <span className="cell-faint" aria-hidden="true">{r.hub.inject ? 'ekleniyor' : 'eklenmiyor'}</span>
                        </label>
                      ) : r.rc ? (
                        <span className="cell-faint">etiket yok — kayıt kendi Thanos URL'sini kullanıyor</span>
                      ) : <span className="cell-empty">—</span>}
                    </td>
                    <DataTableCell dt={dt} col="count" row={r} value={String(r.count)} />
                    <td {...dt.cellProps(r, 'act')}>
                      <Button variant="ghost" size="xs" id={`acd-hub-rm-${k}`} aria-describedby={errId}
                        aria-label={`${r.name} hub listesinden kaldır`} onClick={() => remove(r)}>
                        Kaldır
                      </Button>
                    </td>
                  </tr>
                  {errId && (
                    <tr>
                      <td colSpan={COLS.length} id={errId} tabIndex={-1}
                        className={issue || deleted || enabled ? 'td-full is-err' : 'td-full cell-muted'}>
                        {issue ? (
                          <><span className="mono">{issue.path}:</span> {issue.message}</>
                        ) : deleted ? (
                          <>“{r.hub.clusterId}” artık bir Remote Cluster kaydı değil (silinmiş). Bu hub satırını kaldırın; Argo bu kümede çalışıyorsa kaydı <Link to="/settings/clusters">Ayarlar › Remote clusters</Link>'ta yeniden ekleyip hub olarak seçin.</>
                        ) : (
                          <>{r.name} Remote Cluster kaydı devre dışı — Argo CD açıkken kaydedilemez. Kaydı <Link to="/settings/clusters">Ayarlar › Remote clusters</Link>'ta etkinleştirin ya da bu hub'ı kaldırın.</>
                        )}
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
      {msg && <div role="alert" className="field-error">{msg}</div>}
      {pending && (
        <div className="row gap-4 row-wrap">
          <Button variant="danger" size="sm" id={`acd-hub-rmall-${domKey(pending.hub.key)}`} onClick={() => removeAll(pending)}>
            Hub'ı {pending.count} instance ile birlikte kaldır
          </Button>
          <Button variant="ghost" size="sm" onClick={() => cancel(pending)}>Vazgeç</Button>
        </div>
      )}
      <div className="row gap-4 row-wrap">
        <label htmlFor="acd-hub-add" className="sr-only">Hub olarak eklenecek Remote Cluster</label>
        <select id="acd-hub-add" value={pick} onChange={e => { setPick(e.target.value); setConfirm(null); onMsg(''); }}>
          <option value="">Remote Cluster seç…</option>
          {clusters.map(c => (
            <option key={c.id} value={c.id} disabled={isHub(c.id)}>{c.name}{c.enabled ? '' : ' (devre dışı)'}</option>
          ))}
        </select>
        <Button variant="secondary" size="sm" onClick={add}>Hub ekle</Button>
        <span className="field-hint">Listede yoksa önce <Link to="/settings/clusters">Ayarlar › Remote clusters</Link> kaydını ekleyin.</span>
      </div>
      <ArgoCDNote id="acd-hub-inj-h">
        Remote Cluster kaydı bir Thanos küme etiketi taşıyorsa Coremetry onu her sorguya ekler. Argo serileri farklı dış
        etiketlerle geliyorsa (ör. user-workload monitoring) bu eşleştirici 0 seri döndürür — o hub için işareti kaldırın.
        Karar hub başınadır; keşif 0 seri dönen hub'da aynı ipucunu gösterir.
      </ArgoCDNote>
    </ArgoCDSectionPanel>
  );
}
