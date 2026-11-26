// AnomalyEventPage — /anomalies/:id (C1, v0.10.1770): eski dedektör olayının
// tam sayfası; AnomalyEventHost (kimlikle okur, 404 → "kayıt yok"). Geri:
// tarayıcı geçmişi (liste filtresi/scroll korunur), geçmiş yoksa Problems ›
// Sinyaller (v0.10.2481; eski /anomalies listesi).
import { useNavigate, useParams } from 'react-router';
import { AnomalyEventHost } from './AnomalyEventDetail';
import { useAuthRole } from '@/components/AuthProvider';
import { SIGNALS_HREF } from '@/lib/inboxTabs';
import { t, useLang } from '@/lib/i18n';

export default function AnomalyEventPage() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const isAdmin = useAuthRole() === 'admin';
  const lang = useLang();
  const back = () => { if (window.history.length > 1) navigate(-1); else navigate(SIGNALS_HREF, { replace: true }); };
  return <AnomalyEventHost key={id} id={id} isAdmin={isAdmin} onBack={back} backLabel={`← ${t('inbox.tab.signals', lang)}`} />;
}
