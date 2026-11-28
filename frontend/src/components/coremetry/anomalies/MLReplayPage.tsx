// MLReplayPage — /anomalies/ml/:id (C1, v0.10.1770): ML olayının tam sayfası
// (önceden ?replay= Sheet). İçerik MLReplayContent; geri = tarayıcı geçmişi.
// v0.10.2022 — sayfa GRUBU gösterir: herhangi bir üyenin kimliğiyle açılınca
// o üyenin grubu (liste ucunun son 24 sa satırlarından; aynı önbellek, ek
// sorgu yok), grafik ve "Gerçek / Değil" grubun en güçlü üyesinden. Grup
// listede yoksa (24 saatten eski) tek olay olarak açılır.
import { useMemo } from 'react';
import { Link, useLocation, useParams } from 'react-router';
import { navHref } from '@/lib/navHref';
import { SIGNALS_HREF } from '@/lib/inboxTabs'; // v0.10.2481 — geri: Problems › Sinyaller
import { t as tr, useLang, useT } from '@/lib/i18n';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { Spinner } from '@/components/Spinner';
import { useMLEvents } from '@/lib/queries';
import { MLReplayContent } from './MLReplaySheet';
import { wantsAutoPlay } from '@/lib/mlPlay';
import { findGroup } from './mlGroupDetail';

export default function MLReplayPage() {
  const tn = useT(); // v0.10.2486 dilim 2 — Topbar başlığı katalogdan
  const { id = '' } = useParams();
  const { search } = useLocation();
  const lang = useLang();
  const list = useMLEvents('');
  const group = useMemo(() => findGroup(list.data?.items, id), [list.data, id]);
  return (
    <>
      <Topbar title={tn('page.signal')} />
      <PageShell>
        <Link to={navHref(SIGNALS_HREF, search)} className="text-sm">‹ {tr('inbox.tab.signals', lang)}</Link>
        {list.isLoading ? <Spinner />
          : <MLReplayContent id={group?.lead.id ?? id} group={group} autoPlay={wantsAutoPlay(new URLSearchParams(search))} />}
      </PageShell>
    </>
  );
}
