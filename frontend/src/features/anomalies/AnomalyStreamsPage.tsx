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
import { useSearchParams } from 'react-router';
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
import type { AnomalyView } from '@/lib/types';

// ?view= / ?attached= lib/urlSchema üzerinden (küme dışı değer → varsayılan);
// görünüm değişince iki tablonun sayfası başa (page / donePage varsayılan 0 →
// param silinir). Rol kontrolü resolveAnomalyView'da.
const SIGNALS_VIEWS = ['', 'load', 'held', 'platform'] as const;
const VIEW_URL_SPEC = urlSpec({
  view: zParam<string>(z.string().check(z.refine(v => (SIGNALS_VIEWS as readonly string[]).includes(v))), ''),
  attached: zParam(zInt({ min: 0, max: 1 }), 0),
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

type ViewKey = AnomalyView | 'unattached';

export default function AnomalyStreamsPage() {
  const [sp, setSp] = useSearchParams();
  const tr = useT();
  const isAdmin = useAuthRole() === 'admin';
  const [url, setUrl] = useUrlParams(VIEW_URL_SPEC);
  const view = resolveAnomalyView(url.view, isAdmin);
  const showAttached = url.attached === 1;
  const setView = (v: AnomalyView) => setUrl({ view: v, page: 0, donePage: 0 });
  const signal = resolveMLSignal(sp.get('signal'));
  const setSignal = (s: MLSignal) => {
    const next = new URLSearchParams(window.location.search);
    if (s) next.set('signal', s); else next.delete('signal');
    next.delete('page'); next.delete('donePage'); // v0.10.2051 — süzgeç değişince sayfa başa
    setSp(next, { replace: true });
  };
  const views = SIGNALS_VIEWS.filter(v => v !== 'held' || isAdmin);
  // SegmentedControl boş değer taşıyamaz (ToggleGroup boşu "seçim kalktı" sayar).
  const toKey = (v: AnomalyView): ViewKey => (v === '' ? 'unattached' : v);
  const fromKey = (k: ViewKey): AnomalyView => (k === 'unattached' ? '' : k);
  const viewSwitch = (
    <SegmentedControl<ViewKey> value={toKey(view)} onChange={k => setView(fromKey(k))} aria-label={tr('anomalies.view.aria')}
      options={views.map(v => ({ value: toKey(v), label: tr(VIEW_LABEL[v]) }))} />
  );
  const attachedToggle = (
    <CheckboxField label={tr('signals.showAttached')} checked={showAttached}
      onCheckedChange={on => setUrl({ attached: on ? 1 : 0, page: 0, donePage: 0 })} />
  );
  return (
    <div className="ib-signals" data-testid="inbox-signals">
      <p className="ib-signals__hint">{tr('signals.hint')}</p>
      <SavedViewsBar page="anomalies" viewQuery={withSignalsTab} />
      {viewSwitch}
      {view === '' ? (
        <UnifiedAnomalies signal={signal} onSignal={setSignal} unattached={!showAttached} controls={attachedToggle}
          detectorStatus={<><MLTab signal={signal} /><AnomalyStreams withHistory={false} /></>} />
      ) : (
        <UnifiedAnomalies key={view} view={view} signal={signal} onSignal={setSignal}
          hint={tr(VIEW_TEXT[view].hint)} emptyText={tr(VIEW_TEXT[view].empty)} />
      )}
    </div>
  );
}
