import { useState, type CSSProperties } from 'react';
import { Link } from 'react-router-dom';
import { Button, SelectField } from '@/components/ui';
import type { RemoteCluster } from './argocdForm';

// ArgoCDEmptyPanel — v0.10.974 — hiçbir şey ayarlanmamışken TEK sonraki adım
// (mockup States (a)):
//   a1 — Remote Cluster kayıtları var, hub seçilmemiş → hub seç + "Hub olarak
//        ekle" (Kaydet'e kadar yazılmaz);
//   a2 — hiç Remote Cluster kaydı yok → Ayarlar › Remote clusters'a git.
// Hub seçilmeden keşif ve elle ekleme kapalı; tablo çizilmez.

const PANEL: CSSProperties = { padding: 'var(--sp-7)', border: '1px dashed var(--border-strong)', borderRadius: 'var(--radius)' };
const TITLE: CSSProperties = { margin: 0, fontSize: 'var(--fs-md)', fontWeight: 600, color: 'var(--text)' };
const TEXT: CSSProperties = { margin: 0, fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };

export function ArgoCDEmptyPanel({ clusters, onAddHub }: {
  clusters: RemoteCluster[];
  onAddHub: (clusterId: string) => void;
}) {
  const [pick, setPick] = useState('');
  if (clusters.length === 0) {
    return (
      <div className="stack gap-3" style={PANEL}>
        <h3 style={TITLE}>Önce bir Remote Cluster kaydı gerekli</h3>
        <p style={TEXT}>
          Hub, Argo CD'nin çalıştığı kümenin <code>argocd_*</code> serilerini tutan Thanos'u gösteren bir Remote Cluster kaydıdır
          (URL + token). Argo iki kümede çalışıyorsa iki kayıt ekleyin; sonra buraya dönüp ikisini de hub olarak seçin.
        </p>
        <div className="row">
          <Link to="/settings/clusters" className="accent">Ayarlar › Remote clusters'a git ↗</Link>
        </div>
        <span className="field-hint">Instance eklemek için bu sayfada yapılacak başka bir şey yok; hub seçilmeden keşif ve elle ekleme kapalıdır.</span>
      </div>
    );
  }
  return (
    <div className="stack gap-3" style={PANEL}>
      <h3 style={TITLE}>Henüz hub yok</h3>
      <p style={TEXT}>
        Argo CD metrikleri (<code>argocd_app_info</code>) Argo'nun çalıştığı kümenin Thanos'unda durur. İlk adım o kümeyi hub olarak
        seçmek; instance'ları ardından keşifle bulursunuz.
      </p>
      <div className="row gap-4 row-wrap">
        <SelectField id="acd-a1-sel" label="Hub olarak seçilecek Remote Cluster" className="mono" value={pick}
          hint={`Önce listeden bir Remote Cluster seçin (${clusters.length} kayıt var); düğme ancak seçimden sonra açılır. Seçilen kayıt /clusters'ta normal küme olarak görünmeye devam eder; hub, Kaydet'e kadar yazılmaz.`}
          onChange={e => setPick(e.target.value)}>
          <option value="">Remote Cluster seç…</option>
          {clusters.map(c => <option key={c.id} value={c.id}>{c.name}{c.enabled ? '' : ' (devre dışı)'}</option>)}
        </SelectField>
        <Button variant="primary" disabled={!pick} aria-describedby="acd-a1-sel-hint" onClick={() => { if (pick) onAddHub(pick); }}>
          Hub olarak ekle
        </Button>
      </div>
    </div>
  );
}
