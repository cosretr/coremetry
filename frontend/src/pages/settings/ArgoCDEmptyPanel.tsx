import { useState } from 'react';
import { Link } from 'react-router';
import { Button } from '@/components/shadcn/button';
import { SelectField } from '@/components/coremetry/Field';
import type { RemoteCluster } from './argocdForm';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2313

// ArgoCDEmptyPanel — v0.10.974 — hiçbir şey ayarlanmamışken TEK sonraki adım
// (mockup States (a)):
//   a1 — Remote Cluster kayıtları var, hub seçilmemiş → hub seç + "Hub olarak
//        ekle" (Kaydet'e kadar yazılmaz);
//   a2 — hiç Remote Cluster kaydı yok → Ayarlar › Remote clusters'a git.
// Hub seçilmeden keşif ve elle ekleme kapalı; tablo çizilmez.
// Faz 2 dilim 1 R15: CSSProperties → utility, ui/ → shadcn + coremetry/Field.

const PANEL = 'flex flex-col gap-3 rounded-[var(--radius)] border border-dashed border-(--border-strong) p-4';
const TITLE = 'm-0 text-[length:var(--fs-md)] font-semibold text-(--text)';
const TEXT = 'm-0 text-[length:var(--fs-sm)] leading-[18px] text-(--text2)';

export function ArgoCDEmptyPanel({ clusters, onAddHub }: {
  clusters: RemoteCluster[];
  onAddHub: (clusterId: string) => void;
}) {
  const lang = useLang();
  const [pick, setPick] = useState('');
  if (clusters.length === 0) {
    return (
      <div className={PANEL}>
        <h3 className={TITLE}>{t('acd.empty.needRc', lang)}</h3>
        <p className={TEXT}>
          {t('acd.empty.needRc.a', lang)}<code>argocd_*</code>{t('acd.empty.needRc.b', lang)}
        </p>
        <div className="flex items-center">
          <Link to="/settings/clusters" className="accent">{t('acd.empty.goRc', lang)}</Link>
        </div>
        <span className="text-[length:var(--fs-xs)] text-(--text3)">{t('acd.empty.nothing', lang)}</span>
      </div>
    );
  }
  return (
    <div className={PANEL}>
      <h3 className={TITLE}>{t('acd.empty.noHub', lang)}</h3>
      <p className={TEXT}>
        {t('acd.empty.noHub.a', lang)}<code>argocd_app_info</code>{t('acd.empty.noHub.b', lang)}
      </p>
      <div className="flex flex-wrap items-center gap-4">
        <SelectField id="acd-a1-sel" label={t('acd.empty.selLabel', lang)} className="w-auto min-w-[240px] max-w-[480px]" controlClassName="font-mono" value={pick}
          hint={tf('acd.empty.selHint', lang, { n: clusters.length })}
          onChange={e => setPick(e.target.value)}>
          <option value="">{t('acd.pickRc', lang)}</option>
          {clusters.map(c => <option key={c.id} value={c.id}>{c.name}{c.enabled ? '' : t('acd.disabledSuffix', lang)}</option>)}
        </SelectField>
        <Button disabled={!pick} aria-describedby="acd-a1-sel-hint" onClick={() => { if (pick) onAddHub(pick); }}>
          {t('acd.empty.addAsHub', lang)}
        </Button>
      </div>
    </div>
  );
}
