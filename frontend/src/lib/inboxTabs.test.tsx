// @vitest-environment jsdom
//
// v0.10.2481 — Problems tek sayfa: sekme URL kodeği, eski /anomalies
// yönlendirmesi (paramlar korunur), sekme şeridi yazımı, kayıtlı görünüm
// sorgusu ve menü girişleri. Servis adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router';
import { readUrlParams } from '@/lib/urlSchema';
import {
  INBOX_TAB_SPEC, SIGNALS_HREF, anomaliesListRedirect, inboxTabSearch, resolveInboxTab, withSignalsTab,
} from './inboxTabs';

vi.mock('@/lib/i18n', async (orig) => {
  const mod = await orig<typeof import('@/lib/i18n')>();
  return { ...mod, useLang: () => 'tr', useT: () => (k: string) => mod.t(k, 'tr') };
});

import { AnomaliesListRedirect } from '@/components/AnomaliesListRedirect';
import { InboxTabStrip } from '@/pages/inbox/InboxTabs';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('sekme kodeği', () => {
  it('yalnız signals geçerli; varsayılan yazılmaz', () => {
    const tab = (qs: string) => resolveInboxTab(readUrlParams(new URLSearchParams(qs), INBOX_TAB_SPEC).tab);
    expect(tab('')).toBe('problems');
    expect(tab('tab=signals')).toBe('signals');
    expect(tab('tab=ml')).toBe('problems'); // eski /anomalies ?tab= değerleri
    expect(tab('tab=groups')).toBe('problems');
    expect(SIGNALS_HREF).toBe('/inbox?tab=signals');
  });
  it('sekme değişimi: Problemler paramları korunur; Sinyaller paramları dönüşte silinir', () => {
    expect(inboxTabSearch('status=closed&prio=P1', 'signals')).toBe('status=closed&prio=P1&tab=signals');
    expect(inboxTabSearch('status=closed&tab=signals&view=load&page=2&donePage=1&sort=x&doneSort=y&signal=log&attached=1', 'problems'))
      .toBe('status=closed');
  });
  it('kayıtlı görünüm: eski Anomalies sorgusuna sekme eklenir', () => {
    expect(withSignalsTab('view=load&signal=log')).toBe('tab=signals&view=load&signal=log');
    expect(withSignalsTab('')).toBe('tab=signals');
    expect(withSignalsTab('tab=signals&view=held')).toBe('tab=signals&view=held');
    expect(withSignalsTab('tab=ml&signal=log')).toBe('tab=signals&signal=log');
  });
});

describe('eski /anomalies → Sinyaller', () => {
  it('saf: paramlar korunur, eski tab düşer, ?event= / ?replay= tam sayfa', () => {
    expect(anomaliesListRedirect('')).toBe('/inbox?tab=signals');
    expect(anomaliesListRedirect('?view=load&signal=trace&page=2&sort=started.asc')).toBe('/inbox?tab=signals&view=load&signal=trace&page=2&sort=started.asc');
    expect(anomaliesListRedirect('?tab=ml&signal=log')).toBe('/inbox?tab=signals&signal=log');
    expect(anomaliesListRedirect('?tab=groups')).toBe('/inbox?tab=signals');
    expect(anomaliesListRedirect('?service=checkout-api')).toBe('/inbox?tab=signals&service=checkout-api');
    expect(anomaliesListRedirect('?event=e1')).toBe('/anomalies/e1');
    expect(anomaliesListRedirect('?replay=m1')).toBe('/anomalies/ml/m1');
  });

  let root: Root | null = null;
  let host: HTMLDivElement | null = null;
  afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

  it('rota: /anomalies?view=held&signal=log → /inbox?tab=signals&view=held&signal=log', () => {
    let at = '';
    function Probe() { const l = useLocation(); at = l.pathname + l.search; return null; }
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => {
      root!.render(
        <MemoryRouter initialEntries={['/anomalies?view=held&signal=log']}>
          <Routes>
            <Route path="/anomalies" element={<AnomaliesListRedirect />} />
            <Route path="/inbox" element={<Probe />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    expect(at).toBe('/inbox?tab=signals&view=held&signal=log');
  });

  it('App: /anomalies listesi yönlendirir; detay rotaları yerinde', () => {
    const app = read('../App.tsx');
    expect(app).toContain('<Route path="/anomalies"      element={<AnomaliesListRedirect />} />');
    expect(app).toContain('<Route path="/anomalies/ml/:id" element={<MLReplayPage />} />');
    expect(app).toContain('<Route path="/anomalies/:id"  element={<AnomalyEventPage />} />');
    expect(app).not.toMatch(/import\('\.\/features\/anomalies\/AnomalyStreamsPage'\)/);
  });
});

describe('sekme şeridi', () => {
  let root: Root | null = null;
  let host: HTMLDivElement | null = null;
  afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

  it('Sinyaller tıkı ?tab=signals yazar, Problemler paramları korunur; geri dönüş sekme + görünüm paramlarını siler', () => {
    let search = '';
    function Probe() { search = useLocation().search; return null; }
    window.history.replaceState(null, '', '/inbox?status=closed');
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const mount = (tab: 'problems' | 'signals') => act(() => {
      root!.render(<MemoryRouter initialEntries={[`/inbox${window.location.search}`]}><InboxTabStrip tab={tab} /><Probe /></MemoryRouter>);
    });
    mount('problems');
    const tabs = () => Array.from(host!.querySelectorAll('[role="tab"]')) as HTMLElement[];
    expect(tabs().map(t => t.textContent)).toEqual(['Problemler', 'Sinyaller']);
    expect(tabs()[0].getAttribute('aria-selected')).toBe('true');
    const press = (t: HTMLElement) => act(() => {
      t.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }));
      t.click();
    });
    press(tabs()[1]);
    expect(search).toBe('?status=closed&tab=signals');
    window.history.replaceState(null, '', `/inbox?status=closed&tab=signals&view=load&page=1`);
    act(() => root!.unmount());
    root = createRoot(host);
    mount('signals');
    press(tabs()[0]);
    expect(search).toBe('?status=closed');
  });
});

describe('menü', () => {
  it('TRIAGE: Problems · Exceptions · Deployments; Anomalies / Incidents girişi yok', () => {
    const src = read('../components/Sidebar.tsx');
    const triage = src.slice(src.indexOf("titleKey: 'navGroup.triage'"), src.indexOf("titleKey: 'navGroup.services'"));
    expect([...triage.matchAll(/href: '([^']+)'/g)].map(m => m[1])).toEqual(['/inbox', '/problems', '/rollouts']);
  });
});
