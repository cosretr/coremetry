import { useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Checkbox } from '@/components/shadcn/checkbox';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import type { ArgoCDPin } from '@/lib/types';
import { domKey, type HubDraft, type InstanceDraft, type Issue, type RemoteCluster } from './argocdForm';
import { ArgoCDNote, ArgoCDSectionPanel } from './ArgoCDSectionPanel';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2313

// ArgoCDHubsPanel — v0.10.974 — Ayarlar › Argo CD "Hub'lar" tablosu (mockup
// Main + States (c)). Hub = Argo CD'nin çalıştığı kümenin Thanos'unu gösteren
// sıradan bir Remote Cluster kaydı; küme etiketi enjeksiyonu HUB BAŞINA
// karar (iki hub, audit §5.6).
//
// Tablo türü: en çok 8 satırlık düzenleme listesi — yine de tablo standardı
// dilim 5 sonrası tek biçim `useDataTable` + `DataTableHead` (sıralanmaz,
// satır tıklanmaz) ve durum tablonun İÇİNDE (`DataTableState`). Silinmiş
// Remote Cluster tam genişlik hata satırıyla, Kaydet'i beklemeden görünür.
// Devre dışı kayıt PASİF hub'dır (v0.10.1009): soluk bilgi satırı, hata değil
// — instance'ları taranmaz, kaydı engellemez.
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
// Faz 2 dilim 1 R15: useDataTable + elle tbody → coremetry/DataTable
// (`renderCell`; silinmiş / pasif / hatalı hub'ın tam genişlik satırı =
// `detail`, hedef id'si içteki blokta), ham onay kutusu → shadcn Checkbox
// (aria-label aynı), ham select → shadcn NativeSelect, ui → shadcn.

interface HubRow { hub: HubDraft; rc: RemoteCluster | undefined; count: number; name: string }

// v0.10.2313 — kolon başlıkları arayüz dilinde (çizimde, dile göre memo).
const cols = (lang: Lang): ColumnDef<HubRow>[] => [
  { id: 'cluster', label: 'Remote Cluster', width: 240 },
  { id: 'label', label: t('acd.hubs.col.label', lang), flex: true },
  { id: 'count', label: 'Instance', numeric: true, width: 96 },
  { id: 'act', label: t('acd.hubs.col.act', lang), kind: 'actions', width: 120 },
];

export function ArgoCDHubsPanel({ hubs, instances, pins, clusters, issues, msg, onMsg, onAdd, onRemove, onRemoveWithInstances, onInject, announce, focus }: {
  hubs: HubDraft[];
  instances: InstanceDraft[];
  /** Kayıtlı pin'ler: pin'i olan instance (ve onu taşıyan hub) kaldırılamaz. */
  pins: ArgoCDPin[];
  clusters: RemoteCluster[];
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
  const lang = useLang();
  const COLS = useMemo(() => cols(lang), [lang]);
  const [pick, setPick] = useState('');
  // Onay bekleyen hub (anahtar). İleti (`msg`) silinince — ör. "Değişiklikleri
  // geri al" — onay düğmeleri de gizlenir; başka bir ileti yazılırken sıfırlanır.
  const [confirm, setConfirm] = useState<string | null>(null);

  const rows: HubRow[] = hubs.map(h => {
    const rc = clusters.find(c => c.id === h.clusterId);
    return { hub: h, rc, name: rc ? rc.name : t('acd.hubs.unknownRec', lang), count: instances.filter(i => i.hubClusterId === h.clusterId).length };
  });
  const isHub = (id: string) => hubs.some(h => h.clusterId === id);

  const add = () => {
    setConfirm(null);
    if (!pick) { onMsg(t('acd.hubs.pickFirst', lang)); return; }
    const rc = clusters.find(c => c.id === pick);
    if (isHub(pick)) { onMsg(tf('acd.hubs.already', lang, { name: rc?.name ?? pick })); return; }
    onAdd(pick);
    setPick(''); onMsg('');
    announce(tf('acd.hubs.added', lang, { name: rc?.name ?? pick }));
  };
  const remove = (r: HubRow) => {
    if (r.count > 0) {
      const ids = new Set(instances.filter(i => i.hubClusterId === r.hub.clusterId).map(i => i.id));
      const pinned = pins.filter(p => ids.has(p.instanceId)).length;
      if (pinned > 0) {
        setConfirm(null);
        onMsg(tf('acd.hubs.pinned', lang, { name: r.name, n: pinned }));
        return;
      }
      setConfirm(r.hub.key);
      onMsg(tf('acd.hubs.confirm', lang, { name: r.name, n: r.count }));
      focus(`acd-hub-rmall-${domKey(r.hub.key)}`);
      return;
    }
    setConfirm(null);
    onRemove(r.hub.key);
    onMsg('');
    announce(tf('acd.hubs.removed', lang, { name: r.name }));
    focus('acd-hub-add');
  };
  const pending = msg ? rows.find(r => r.hub.key === confirm && r.count > 0) : undefined;
  const removeAll = (r: HubRow) => {
    setConfirm(null);
    onRemoveWithInstances(r.hub.key);
    onMsg('');
    announce(tf('acd.hubs.removedAll', lang, { name: r.name, n: r.count }));
    focus('acd-hub-add');
  };
  const cancel = (r: HubRow) => {
    setConfirm(null);
    onMsg('');
    focus(`acd-hub-rm-${domKey(r.hub.key)}`);
  };

  // Hub satırının tam genişlik notu: sunucu/istemci sorunu, silinmiş kayıt
  // (hata) ya da devre dışı kayıt (pasif hub, soluk bilgi).
  const note = (r: HubRow): { id: string; node: ReactNode } | null => {
    const k = domKey(r.hub.key);
    const issue = issues.find(i => i.target.kind === 'hub' && i.target.key === r.hub.key);
    const deleted = !r.rc;
    const disabled = !!r.rc && !r.rc.enabled;
    if (!issue && !deleted && !disabled) return null;
    const id = `acd-hub-err-${k}`;
    return {
      id,
      node: (
        <div id={id} tabIndex={-1} className={issue || deleted ? 'text-status-critical' : 'text-(--text2)'}>
          {issue ? (
            <><span className="font-mono">{issue.path}:</span> {issue.message}</>
          ) : deleted ? (
            <>{tf('acd.hubs.deleted.a', lang, { id: r.hub.clusterId })}<Link to="/settings/clusters">{t('acd.rcLink', lang)}</Link>{t('acd.hubs.deleted.b', lang)}</>
          ) : (
            <>{tf('acd.hubs.disabled.a', lang, { name: r.name })}<Link to="/settings/clusters">{t('acd.rcLink', lang)}</Link>{t('acd.hubs.disabled.b', lang)}</>
          )}
        </div>
      ),
    };
  };
  const cell = (r: HubRow, col: ColumnDef<HubRow>): ReactNode => {
    const k = domKey(r.hub.key);
    const errId = note(r)?.id;
    switch (col.id) {
      case 'cluster':
        return (
          <span aria-describedby={errId} title={r.name}>
            <span className={!r.rc ? 'font-mono text-status-critical' : 'font-mono'}>{r.name}</span>{' '}
            <span className="font-mono text-(--text3)">{r.hub.clusterId}</span>
            {!!r.rc && !r.rc.enabled && <>{' '}<Badge variant="secondary">{t('acd.hubs.passive', lang)}</Badge></>}
          </span>
        );
      case 'label':
        return r.rc?.label ? (
          <span className="inline-flex items-center gap-2">
            <Checkbox checked={r.hub.inject} aria-describedby="acd-hub-inj-h"
              aria-label={tf('acd.hubs.injectAria', lang, { label: r.rc.label, name: r.name })}
              onCheckedChange={v => onInject(r.hub.key, v === true)} />
            <span className={r.hub.inject ? 'font-mono' : 'font-mono text-(--text3)'}>{r.rc.label}</span>
            <span className="text-(--text3)" aria-hidden="true">{t(r.hub.inject ? 'acd.hubs.injecting' : 'acd.hubs.notInjecting', lang)}</span>
          </span>
        ) : r.rc ? (
          <span className="text-(--text3)">{t('acd.hubs.noLabel', lang)}</span>
        ) : <span className="cell-empty">—</span>;
      case 'count':
        return String(r.count);
      case 'act':
        return (
          <Button variant="ghost" size="xs" id={`acd-hub-rm-${k}`} aria-describedby={errId}
            aria-label={tf('acd.hubs.removeAria', lang, { name: r.name })} onClick={() => remove(r)}>
            {t('acd.hubs.remove', lang)}
          </Button>
        );
      default:
        return null;
    }
  };

  return (
    <ArgoCDSectionPanel id="acd-hubs-h" title={t('acd.hubs.title', lang)} meta={`${hubs.length} hub`}
      desc={t('acd.hubs.desc', lang)}>
      <div className="table-wrap">
        <DataTable<HubRow> storageKey="settings.argocd.hubs" columns={COLS} rows={rows} ariaLabel={t('acd.hubs.title', lang)}
          getRowKey={r => r.hub.key} renderCell={cell}
          state={{ kind: 'empty', message: t('acd.hubs.empty', lang) }}
          detail={{ isOpen: r => !!note(r), render: r => note(r)?.node, cellClassName: () => 'td-full' }} />
      </div>
      {msg && <div role="alert" className="text-[length:var(--fs-xs)] text-status-critical">{msg}</div>}
      {pending && (
        <div className="flex flex-wrap items-center gap-4">
          <Button variant="destructive" size="sm" id={`acd-hub-rmall-${domKey(pending.hub.key)}`} onClick={() => removeAll(pending)}>
            {tf('acd.hubs.removeWith', lang, { n: pending.count })}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => cancel(pending)}>{t('acd.cancel', lang)}</Button>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-4">
        <label htmlFor="acd-hub-add" className="sr-only">{t('acd.hubs.addLabel', lang)}</label>
        <NativeSelect id="acd-hub-add" size="sm" className="bg-field dark:bg-field text-[length:var(--fs-sm)]" value={pick}
          onChange={e => { setPick(e.target.value); setConfirm(null); onMsg(''); }}>
          <NativeSelectOption value="">{t('acd.pickRc', lang)}</NativeSelectOption>
          {clusters.map(c => (
            <NativeSelectOption key={c.id} value={c.id} disabled={isHub(c.id)}>{c.name}{c.enabled ? '' : t('acd.disabledSuffix', lang)}</NativeSelectOption>
          ))}
        </NativeSelect>
        <Button variant="outline" size="sm" onClick={add}>{t('acd.hubs.add', lang)}</Button>
        <span className="text-[length:var(--fs-xs)] text-(--text3)">{t('acd.hubs.notListed.a', lang)}<Link to="/settings/clusters">{t('acd.rcLink', lang)}</Link>{t('acd.hubs.notListed.b', lang)}</span>
      </div>
      <ArgoCDNote id="acd-hub-inj-h">
        {t('acd.hubs.note', lang)}
      </ArgoCDNote>
    </ArgoCDSectionPanel>
  );
}
