import { useEffect, useRef, useState } from 'react';
import { api } from '@/lib/api';
import { Combobox } from '@/components/Combobox';
import { usePickerSearch } from '@/components/usePickerSearch';
import { t, useLang } from '@/lib/i18n'; // v0.10.2304 — seçici metinleri arayüz dilinde (TR aynı)

// RolloutWorkloadPicker — v0.10.1420 (operatör, /rollouts Canlı sekmesi:
// "Workload seçilerek filtrelenebilsin").
//
// SUNUCU-taraflı seçici (CLAUDE.md "Picker = server-side search"): yazılan
// desen 180 ms durulunca /api/rollouts/workloads'a gider (usePickerSearch —
// Service/Operation seçicileriyle aynı kanca); liste o pencerede rollout'u
// olan adlardır, tam katalog ASLA önden çekilmez. Kapsam = cluster +
// namespace (değişince yeniden arar); pencere en taze değerden okunur ama
// kapsamda DEĞİL — 60 s'lik canlı tik her dakika arama tetiklemesin.
//
// Seçim ≠ yazma: sunucu süzgeci BİREBİR eşleşir (?workload=), yani yarım
// yazılmış ad uygulanmaz. Uygulanan değer yalnız listeden seçimde (onPick),
// Enter'da (yazılan ad aynen) ve temizlemede ('' — tüm liste) değişir; alan
// uygulanmamış metinle bırakılırsa (blur) uygulanan değere geri döner.
// Değer URL'de yaşar (çağıran ?workload= yazar, replace:true); `value`
// değişince taslak ona eşitlenir (URL → durum).
export const ROLLOUT_WORKLOAD_RECENT_KEY = 'rollout-workload';

export function RolloutWorkloadPicker({ value, onCommit, from, to, cluster, namespace, width = 200 }: {
  value: string;
  onCommit: (workload: string) => void;
  from: number; to: number; // ns
  cluster?: string; namespace?: string;
  width?: number | string;
}) {
  const lang = useLang();
  const [draft, setDraft] = useState(value);
  useEffect(() => { setDraft(value); }, [value]);
  const winRef = useRef({ from, to });
  winRef.current = { from, to };
  const cappedRef = useRef(false);
  const search = usePickerSearch(draft, `${cluster ?? ''}\u0000${namespace ?? ''}`, q =>
    api.rolloutWorkloads({ q: q.trim(), from: winRef.current.from, to: winRef.current.to, cluster: cluster || undefined, namespace: namespace || undefined, limit: 50 })
      .then(r => {
        cappedRef.current = r.capped === true;
        const names = r.names ?? [];
        return { items: names, total: names.length };
      }));
  const commit = (v: string) => { if (v !== value) onCommit(v); };
  return (
    <Combobox
      value={draft}
      onChange={next => { setDraft(next); if (next === '') commit(''); }}
      onPick={v => commit(v)}
      onEnter={() => commit(draft.trim())}
      onBlurCommit={v => { if (v.trim() !== value) setDraft(value); }}
      options={search.items}
      serverFiltered
      resultCount={search.total}
      status={search.status}
      recentKey={ROLLOUT_WORKLOAD_RECENT_KEY}
      placeholder={t('facet.all', lang)}
      ariaLabel="Workload"
      width={width}
      footer={cappedRef.current ? t('ro.pick.more', lang) : undefined}
      title={t('ro.pick.title', lang)}
    />
  );
}
