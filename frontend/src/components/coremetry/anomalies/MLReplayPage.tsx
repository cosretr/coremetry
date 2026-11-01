// MLReplayPage — /anomalies/ml/:id (C1, v0.10.1770): ML olayının tam sayfası
// (önceden ?replay= Sheet). İçerik MLReplayContent; geri = tarayıcı geçmişi.
import { Link, useLocation, useParams } from 'react-router-dom';
import { navHref } from '@/lib/navHref';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { MLReplayContent } from './MLReplaySheet';

export default function MLReplayPage() {
  const { id = '' } = useParams();
  const { search } = useLocation();
  return (
    <>
      <Topbar title="ML olayı" />
      <PageShell>
        <Link to={navHref('/anomalies?tab=ml', search)} className="text-sm">← ML olayları</Link>
        <MLReplayContent id={id} />
      </PageShell>
    </>
  );
}
