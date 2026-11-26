// InboxTabs — v0.10.2481 (operatör: "yapalım, sade ve güzel"): Problems
// sayfasının iki sekmesi. Problemler (varsayılan, URL'e yazılmaz) bugünkü
// liste; Sinyaller (?tab=signals) eski /anomalies listesinin görünümleri
// (features/anomalies/AnomalyStreamsPage). Yalnız ETKİN sekme mount edilir:
// öbürünün sorguları çalışmaz ve yoklamaz; Sinyaller kodu ilk açılışta
// tembel yüklenir. Sekme değişimi replace ile yazılır; Problemler'in
// paramlarına dokunulmaz, Sinyaller'inkiler Problemler'e dönüşte silinir
// (lib/inboxTabs inboxTabSearch).
import { lazy, Suspense, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { Tabs, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { Topbar } from '@/components/Topbar';
import { Spinner } from '@/components/Spinner';
import { useT } from '@/lib/i18n';
import { inboxTabSearch, type InboxTab } from '@/lib/inboxTabs';

const SignalsPanel = lazy(() => import('@/features/anomalies/AnomalyStreamsPage'));

export function InboxTabStrip({ tab }: { tab: InboxTab }) {
  const tr = useT();
  const [, setSp] = useSearchParams();
  const onTab = (k: string) => {
    const next = k === 'signals' ? 'signals' : 'problems';
    if (next === tab) return;
    setSp(() => new URLSearchParams(inboxTabSearch(window.location.search, next)), { replace: true });
  };
  return (
    <Tabs value={tab} onValueChange={onTab} className="ib-tabs">
      <TabsList variant="line" aria-label={tr('inbox.tab.aria')}>
        <TabsTrigger value="problems">{tr('inbox.tab.problems')}</TabsTrigger>
        <TabsTrigger value="signals">{tr('inbox.tab.signals')}</TabsTrigger>
      </TabsList>
    </Tabs>
  );
}

/** Sinyaller sekmesi: başlık Problems'te kalır (tek sayfa). */
export function SignalsTab({ strip }: { strip: ReactNode }) {
  return (
    <>
      <Topbar title="Problems" />
      <div className="page-body">
        {strip}
        <Suspense fallback={<Spinner />}>
          <SignalsPanel />
        </Suspense>
      </div>
    </>
  );
}
