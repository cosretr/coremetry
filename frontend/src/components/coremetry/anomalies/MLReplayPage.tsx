// MLReplayPage — /anomalies/ml/:id (C1, v0.10.1770): ML olayının tam sayfası
// (önceden ?replay= Sheet). İçerik MLReplayContent; geri = tarayıcı geçmişi.
import { Link, useParams } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { MLReplayContent } from './MLReplaySheet';

export default function MLReplayPage() {
  const { id = '' } = useParams();
  return (
    <>
      <Topbar title="ML olayı" />
      <PageShell>
        <Link to="/anomalies?tab=ml" className="text-sm">← ML olayları</Link>
        <MLReplayContent id={id} />
      </PageShell>
    </>
  );
}
