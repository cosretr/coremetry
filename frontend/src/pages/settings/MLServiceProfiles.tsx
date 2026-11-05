// MLServiceProfiles — Settings › Anomaly › ML "Servis profilleri" (v0.10.1816,
// öğrenme döngüsü Faz 1 dilim 1.5, audit §4.4). Haftalık sınıflandırıcının
// sonucu + elle sabitleme. Binlerce servis: SUNUCU-sayfalı, sunucuda ad
// araması + profil süzgeci (tablo standardı: sunucu-sayfalı = resize-only).
// Renk yalnız sapmada (elle sabitlenen satırın Kaynak hücresi). Viewer
// durumu salt-okur görür; Sabitle / Sabitlemeyi kaldır yalnız admin.
// Profil bu sürümde HİÇBİR dedektöre girmez — ipucu satırı bunu söyler.
import { useEffect, useState } from 'react';
import { Search, X } from 'lucide-react';
import { useQueryClient } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Badge } from '@/components/shadcn/badge';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { Field, SelectField } from '@/components/coremetry/Field';
import { useAuthRole } from '@/components/AuthProvider';
import { Link } from 'react-router-dom';
import { api } from '@/lib/api';
import { keys, useMLProfiles } from '@/lib/queries';
import { serviceHref } from '@/lib/serviceHref';
import { tsCompact, tsLong } from '@/lib/utils';
import { PROFILE_TR, profileEvidenceText, profileSourceText, PROFILE_PINNABLE } from '@/lib/mlProfiles';
import type { MLServiceProfile } from '@/lib/types';
import { FlashBox, humanize } from './shared';

const PAGE = 50;
const DEBOUNCE_MS = 350;
const H3 = 'mt-4 mb-1 text-[length:var(--fs-md)] font-semibold';
const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';

// Sunucu-sayfalı → resize-only (tablo standardı): sortValue YOK; sunucu
// sırası servis adı. sortValue verilse başlık tıklanabilir görünür ama
// yalnız bir sayfayı sıralardı.
const COLS: ColumnDef<MLServiceProfile>[] = [
  { id: 'service', label: 'Servis', width: 220, minWidth: 140, flex: true, priority: 1, ownLink: true },
  { id: 'profile', label: 'Profil', width: 130, minWidth: 100, priority: 1 },
  { id: 'source', label: 'Kaynak', width: 170, minWidth: 110, priority: 2 },
  { id: 'changed', label: 'Son değişim', width: 140, minWidth: 110, priority: 3, numeric: true },
  { id: 'evidence', label: 'Kanıt', width: 300, minWidth: 160, priority: 2 },
];

export function MLServiceProfiles() {
  const isAdmin = useAuthRole() === 'admin';
  const qc = useQueryClient();
  const [q, setQ] = useState('');
  const [debounced, setDebounced] = useState('');
  const [profile, setProfile] = useState('');
  const [page, setPage] = useState(0);
  const [pin, setPin] = useState<{ service: string; profile: string; reason: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    const id = window.setTimeout(() => { setDebounced(q.trim()); setPage(0); }, DEBOUNCE_MS);
    return () => window.clearTimeout(id);
  }, [q]);

  const res = useMLProfiles({ q: debounced, profile, offset: page * PAGE, limit: PAGE });
  const rows = res.data?.items ?? [];
  const total = res.data?.total ?? 0;
  const hasMore = (page + 1) * PAGE < total;
  const filtered = debounced !== '' || profile !== '';
  const state: DataTableStateInput<MLServiceProfile> =
    res.isLoading ? { kind: 'loading' }
      : res.isError ? { kind: 'error', message: humanize(res.error), onRetry: () => { void res.refetch(); } }
        : filtered ? { kind: 'no-match', onClearFilters: () => { setQ(''); setProfile(''); } }
          : { kind: 'empty', message: 'Baseline henüz toplanmadı — profil, günlük baseline işi trace çağrı serisini (_calls) en az iki hafta topladıktan sonra çıkar.' };

  const refresh = () => qc.invalidateQueries({ queryKey: keys.anomalies.mlProfiles });
  const savePin = async () => {
    if (!pin) return;
    setBusy(true); setFlash(null);
    try { await api.mlProfilePin(pin.service, { profile: pin.profile, reason: pin.reason }); setPin(null); await refresh(); } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setBusy(false); }
  };
  const unpin = async (service: string) => {
    setBusy(true); setFlash(null);
    try { await api.mlProfileUnpin(service); await refresh(); } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setBusy(false); }
  };

  const cell = (r: MLServiceProfile, col: ColumnDef<MLServiceProfile>) => {
    switch (col.id) {
      case 'service': return <Link to={serviceHref(r.service)} className="dt-trunc" title={r.service}>{r.service}</Link>;
      case 'profile': return <Badge variant="outline">{PROFILE_TR[r.profile] ?? r.profile}</Badge>;
      case 'source': return <span className={r.pinned ? 'cell-warn' : undefined} title={r.reason || undefined}>{profileSourceText(r)}</span>;
      case 'changed': return r.changedAt ? <span title={tsLong(r.changedAt * 1e9)}>{tsCompact(r.changedAt * 1e9)}</span> : '—';
      case 'evidence': return <span className="dt-trunc" title={profileEvidenceText(r.evidence)}>{profileEvidenceText(r.evidence)}</span>;
      default: return null;
    }
  };

  return (
    <div data-testid="ml-service-profiles">
      <h3 className={H3}>Servis profilleri</h3>
      <p className={NOTE}>
        Haftalık sınıflandırma (son 4 hafta, trace çağrı serisi): batch → az çağrılan → mesai/şube → 7/24; değişim iki ardışık haftada
        aynı sonucu ister. Profiller şu an yalnız gösterilir; tespit kuralı sonraki sürümde bağlanır.
      </p>
      <div className="mb-1.5 flex flex-wrap items-center gap-2.5">
        <span className="relative inline-flex w-[240px] max-w-full items-center">
          <Search aria-hidden="true" className="pointer-events-none absolute left-2 size-3.5 text-(--text3)" />
          <Input type="search" value={q} onChange={e => setQ(e.target.value)} placeholder="servis ara…" aria-label="Servis ara"
            className="h-8 bg-field border-(--border-control) pr-7 pl-7 text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field" />
          {q !== '' && (
            <Button variant="ghost" size="icon-xs" aria-label="Aramayı temizle" className="absolute right-1" onClick={() => setQ('')}>
              <X aria-hidden="true" />
            </Button>
          )}
        </span>
        <SelectField label="Profil" small value={profile} onChange={e => { setProfile(e.target.value); setPage(0); }}>
          <option value="">Hepsi</option>
          {[...PROFILE_PINNABLE, 'unknown'].map(p => <option key={p} value={p}>{PROFILE_TR[p]}</option>)}
        </SelectField>
        <span className="text-[length:var(--fs-sm)] text-(--text3)">{res.data ? `${total.toLocaleString('tr-TR')} servis` : ''}</span>
      </div>
      <div className="table-wrap">
        <DataTable<MLServiceProfile> storageKey="settings-ml-profiles" columns={COLS} rows={rows}
          sort={{ server: { page, pageSize: PAGE, hasMore, onPage: setPage } }}
          getRowKey={r => r.service} state={state} renderCell={cell} ariaLabel="Servis profilleri"
          rowActions={isAdmin ? {
            label: 'Profil eylemleri',
            triggerLabel: r => `${r.service} profil eylemleri`,
            items: r => r.pinned
              ? [{ id: 'unpin', label: 'Sabitlemeyi kaldır', onSelect: () => { void unpin(r.service); } }]
              : [{ id: 'pin', label: 'Sabitle…', onSelect: () => setPin({ service: r.service, profile: r.profile === 'unknown' ? 'always_on' : r.profile, reason: '' }) }],
          } : undefined} />
      </div>
      {(page > 0 || hasMore) && (
        <div className="mt-1.5 flex gap-2">
          <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(p => Math.max(0, p - 1))}>← Önceki</Button>
          <Button variant="outline" size="sm" disabled={!hasMore} onClick={() => setPage(p => p + 1)}>Sonraki →</Button>
        </div>
      )}
      {pin && (
        <div className="mt-2 grid max-w-[520px] gap-2" data-testid="ml-profile-pin">
          <div className="text-[length:var(--fs-sm)] font-medium">{pin.service} profilini sabitle</div>
          <SelectField label="Profil" value={pin.profile} onChange={e => setPin({ ...pin, profile: e.target.value })}>
            {PROFILE_PINNABLE.map(p => <option key={p} value={p}>{PROFILE_TR[p]}</option>)}
          </SelectField>
          <Field label="Gerekçe" hint="Zorunlu; denetim kaydına düşer" value={pin.reason} onChange={e => setPin({ ...pin, reason: e.target.value })} />
          <div className="flex gap-2">
            <Button size="sm" onClick={savePin} disabled={busy || pin.reason.trim() === ''}>Sabitle</Button>
            <Button variant="outline" size="sm" onClick={() => setPin(null)} disabled={busy}>Vazgeç</Button>
          </div>
        </div>
      )}
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </div>
  );
}
