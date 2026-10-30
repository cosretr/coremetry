import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { SelectField } from '@/components/coremetry/Field';
import type { RemoteCluster } from './argocdForm';

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
  const [pick, setPick] = useState('');
  if (clusters.length === 0) {
    return (
      <div className={PANEL}>
        <h3 className={TITLE}>Önce bir Remote Cluster kaydı gerekli</h3>
        <p className={TEXT}>
          Hub, Argo CD'nin çalıştığı kümenin <code>argocd_*</code> serilerini tutan Thanos'u gösteren bir Remote Cluster kaydıdır
          (URL + token). Argo iki kümede çalışıyorsa iki kayıt ekleyin; sonra buraya dönüp ikisini de hub olarak seçin.
        </p>
        <div className="flex items-center">
          <Link to="/settings/clusters" className="accent">Ayarlar › Remote clusters'a git ↗</Link>
        </div>
        <span className="text-[length:var(--fs-xs)] text-(--text3)">Instance eklemek için bu sayfada yapılacak başka bir şey yok; hub seçilmeden keşif ve elle ekleme kapalıdır.</span>
      </div>
    );
  }
  return (
    <div className={PANEL}>
      <h3 className={TITLE}>Henüz hub yok</h3>
      <p className={TEXT}>
        Argo CD metrikleri (<code>argocd_app_info</code>) Argo'nun çalıştığı kümenin Thanos'unda durur. İlk adım o kümeyi hub olarak
        seçmek; instance'ları ardından keşifle bulursunuz.
      </p>
      <div className="flex flex-wrap items-center gap-4">
        <SelectField id="acd-a1-sel" label="Hub olarak seçilecek Remote Cluster" className="w-auto min-w-[240px] max-w-[480px]" controlClassName="font-mono" value={pick}
          hint={`Önce listeden bir Remote Cluster seçin (${clusters.length} kayıt var); düğme ancak seçimden sonra açılır. Seçilen kayıt /clusters'ta normal küme olarak görünmeye devam eder; hub, Kaydet'e kadar yazılmaz.`}
          onChange={e => setPick(e.target.value)}>
          <option value="">Remote Cluster seç…</option>
          {clusters.map(c => <option key={c.id} value={c.id}>{c.name}{c.enabled ? '' : ' (devre dışı)'}</option>)}
        </SelectField>
        <Button disabled={!pick} aria-describedby="acd-a1-sel-hint" onClick={() => { if (pick) onAddHub(pick); }}>
          Hub olarak ekle
        </Button>
      </div>
    </div>
  );
}
