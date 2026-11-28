// AnomalyStreamsPage — v0.10.2481: eski /anomalies sayfasının GÖVDESİ, artık
// Problems sayfasının "Sinyaller" sekmesi (/inbox?tab=signals; sekmeyi
// pages/Inbox.tsx tembel yükler). /anomalies adresi buraya yönlenir
// (App.tsx AnomaliesListRedirect, parametreler korunur).
//
// Geçmiş: v0.10.2027 tek liste (UnifiedAnomalies: ML grupları + eski dedektör
// olayları aynı satır modelinde, süren olaylar üstte, bitenler kapalı
// bölümde); en altta KAPALI "Teknik ayrıntı / Dedektör durumu" (ML dedektör
// karnesi + eski dedektörlerin canlı kartları ve susturmaları — silinmedi,
// yer değiştirmedi; ML teknik görünümlerin tek, göze batmayan yeri).
// v0.10.2243 görünümler ?view= (varsayılan yazılmaz); v0.10.2473 Elenenler
// (yalnız admin); v0.10.2476 Yük ve yapı (view=load).
//
// v0.10.2481 — görünümler sekme değil ÇİP (üstte sayfa sekmeleri var):
//   Bağlanmamış anomaliler (view='', varsayılan) · Yük ve yapı · Elenenler
//   (yalnız admin) · Telemetri hacmi. Bağlanmamış = açık bir Problem'in üyesi
//   OLMAYAN satırlar (sunucu süzgeci ?unattached=1, internal/api/
//   anomalies_unified_attached.go); "Problem'e bağlı olanları da göster"
//   anahtarı ?attached=1 yazar ve süzgeci kaldırır.
// v0.10.2482 — ?service= (servis sayfası "N sinyal" bağlantısı, servis başlığı
// çipi) listeyi sunucuda o servise süzer; süzgeç görünür satırda adıyla ve
// tek tıkla kaldırılır (sessiz süzgeç yok). Bağlanmamış listede "Neden
// Problem değil" sütunu (kapının kararı; lib/whyNotProblem.ts).
// v0.10.2484 — precision programı B.3/2: "Bu hafta etiketle (10)" çipi
// (view=label; yalnız editor+ — etiket düğmesi editör ister): haftalık
// deterministik örnek (MLLabelQueue). Liste ve sayaçları etkilemez.
import { useSearchParams } from 'react-router';
import { X } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { useAuthRole } from '@/components/AuthProvider';
import { useT } from '@/lib/i18n';
import { resolveAnomalyView } from '@/lib/anomalyView';
import { urlSpec, useUrlParams, z, zInt, zParam } from '@/lib/urlSchema';
import { withSignalsTab } from '@/lib/inboxTabs';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { AnomalyStreams } from './streams';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { CheckboxField } from '@/components/coremetry/Field';
import { MLTab, resolveMLSignal, type MLSignal } from '@/components/coremetry/anomalies/MLTab';
import { UnifiedAnomalies } from '@/components/coremetry/anomalies/UnifiedAnomalies';
import { MLLabelQueue } from '@/components/coremetry/anomalies/MLLabelQueue';
import type { AnomalyView } from '@/lib/types';

// ?view= / ?attached= lib/urlSchema üzerinden (küme dışı değer → varsayılan);
// görünüm değişince iki tablonun sayfası başa (page / donePage varsayılan 0 →
// param silinir). Rol kontrolü resolveAnomalyView'da.
const SIGNALS_VIEWS = ['', 'load', 'held', 'platform', 'label'] as const;
/** v0.10.2484 — etiket kuyruğu çipi (AnomalyView DEĞİL: sunucu listesine gitmez). */
const LABEL_VIEW = 'label';
const VIEW_URL_SPEC = urlSpec({
  view: zParam<string>(z.string().check(z.refine(v => (SIGNALS_VIEWS as readonly string[]).includes(v))), ''),
  attached: zParam(zInt({ min: 0, max: 1 }), 0),
  service: zParam(z.string(), ''),
  page: zParam(zInt({ min: 0 }), 0),
  donePage: zParam(zInt({ min: 0 }), 0),
});

const VIEW_TEXT: Record<Exclude<AnomalyView, ''>, { hint: string; empty: string }> = {
  platform: { hint: 'anomalies.view.platformHint', empty: 'anomalies.view.platformEmpty' },
  load: { hint: 'anomalies.view.loadHint', empty: 'anomalies.view.loadEmpty' },
  held: { hint: 'anomalies.view.heldHint', empty: 'anomalies.view.heldEmpty' },
};

const VIEW_LABEL: Record<AnomalyView, string> = {
  '': 'signals.view.unattached', load: 'anomalies.view.load', held: 'anomalies.view.held', platform: 'anomalies.view.platform',
};

type ViewKey = AnomalyView | 'unattached' | typeof LABEL_VIEW;

export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tr = useT();
  const role = useAuthRole();
  const isAdmin = role === 'admin';
  const canLabel = isAdmin || role === 'editor';
  const [url, setUrl] = useUrlParams(VIEW_URL_SPEC);
  const view = resolveAnomalyView(url.view, isAdmin);
  const labelQueue = canLabel && url.view === LABEL_VIEW;
  const showAttached = url.attached === 1;
  const service = url.service.trim();
  const setView = (v: AnomalyView) => setUrl({ view: v, page: 0, donePage: 0 });
  const signal = resolveMLSignal(sp.get('signal'));
  const setSignal = (s: MLSignal) => {
    const next = new URLSearchParams(window.location.search);
    if (s) next.set('signal', s); else next.delete('signal');
    next.delete('page'); next.delete('donePage'); // v0.10.2051 — süzgeç değişince sayfa başa
    setSp(next, { replace: true });
  };
  const views = SIGNALS_VIEWS.filter(v => (v !== 'held' || isAdmin) && (v !== LABEL_VIEW || canLabel));
  // SegmentedControl boş değer taşıyamaz (ToggleGroup boşu "seçim kalktı" sayar).
  const toKey = (v: AnomalyView | typeof LABEL_VIEW): ViewKey => (v === '' ? 'unattached' : v);
  const viewSwitch = (
    <SegmentedControl<ViewKey> value={labelQueue ? LABEL_VIEW : toKey(view)} aria-label={tr('anomalies.view.aria')}
      onChange={k => (k === LABEL_VIEW ? setUrl({ view: LABEL_VIEW, page: 0, donePage: 0 }) : setView(k === 'unattached' ? '' : k))}
      options={views.map(v => ({ value: toKey(v), label: tr(v === LABEL_VIEW ? 'signals.view.labelQueue' : VIEW_LABEL[v]) }))} />
  );
  const attachedToggle = (
    <CheckboxField label={tr('signals.showAttached')} checked={showAttached}
      onCheckedChange={on => setUrl({ attached: on ? 1 : 0, page: 0, donePage: 0 })} />
  );
  const serviceFilter = service && (
    <p className="ib-signals__hint" data-testid="signals-service-filter">
      <span className="mono">{tr('signals.service').replace('{s}', service)}</span>
      <Button variant="ghost" size="icon-xs" aria-label={tr('signals.serviceClear')} title={tr('signals.serviceClear')}
        onClick={() => setUrl({ service: '', page: 0, donePage: 0 })}><X /></Button>
    </p>
  );
  return (
    <div className="ib-signals" data-testid="inbox-signals">
      <p className="ib-signals__hint">{tr('signals.hint')}</p>
      <SavedViewsBar page="anomalies" viewQuery={withSignalsTab} />
      {viewSwitch}
      {serviceFilter}
      {labelQueue ? (
        <MLLabelQueue />
      ) : view === '' ? (
        <UnifiedAnomalies signal={signal} onSignal={setSignal} unattached={!showAttached} controls={attachedToggle} service={service}
          detectorStatus={<><MLTab signal={signal} /><AnomalyStreams withHistory={false} /></>} />
      ) : (
        <UnifiedAnomalies key={view} view={view} signal={signal} onSignal={setSignal} service={service}
          hint={tr(VIEW_TEXT[view].hint)} emptyText={tr(VIEW_TEXT[view].empty)} />
      )}
    </div>
  );
}
