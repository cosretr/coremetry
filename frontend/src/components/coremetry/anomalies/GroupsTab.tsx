// GroupsTab — /anomalies?tab=groups (v0.10.1828, Incidents emekliliği E6;
// operatör kararı 2026-11-05: "Incident sekmesi yerine Anomalies olsun").
// Anomali grupları Faz 3'te dolar (docs/anomaly/learning-loop-audit.md
// §10); şimdilik veri bağlantısı YOK, yalnız tasarlanmış boş durum —
// emekli /incidents adresi buraya iner, boş sayfa göstermez.
import { Layers } from 'lucide-react';
import { Link } from 'react-router-dom';
import { Empty } from '@/components/Spinner';

export const GROUPS_EMPTY_TITLE = 'Henüz anomali grubu yok';
export const GROUPS_EMPTY_BODY = 'Anomali grupları yakında burada: aynı zamanda başlayan, birbirini çağıran servislerdeki anomaliler tek grupta toplanır.';

export function GroupsTab() {
  return (
    <Empty icon={<Layers size={28} strokeWidth={1.5} />} title={GROUPS_EMPTY_TITLE}
      action={<Link to="/anomalies">Dedektör anomalilerine dön</Link>}>
      {GROUPS_EMPTY_BODY}
    </Empty>
  );
}
