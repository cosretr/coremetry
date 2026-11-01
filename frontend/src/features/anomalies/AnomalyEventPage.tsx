// AnomalyEventPage — /anomalies/:id (C1, v0.10.1770): eski dedektör olayının
// tam sayfası; AnomalyEventHost (kimlikle okur, 404 → "kayıt yok"). Geri:
// tarayıcı geçmişi (liste filtresi/scroll korunur), geçmiş yoksa /anomalies.
import { useNavigate, useParams } from 'react-router-dom';
import { AnomalyEventHost } from './AnomalyEventDetail';
import { useAuthRole } from '@/components/AuthProvider';

export default function AnomalyEventPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const isAdmin = useAuthRole() === 'admin';
  const back = () => { if (window.history.length > 1) navigate(-1); else navigate('/anomalies', { replace: true }); };
  return <AnomalyEventHost key={id} id={id} isAdmin={isAdmin} onBack={back} backLabel="← Anomalies" />;
}
