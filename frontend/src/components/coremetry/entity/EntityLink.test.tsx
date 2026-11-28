// @vitest-environment jsdom
//
// EntityLink.test.tsx — v0.10.2488 (entity tutarlılık denetimi dilim 4).
//   • hedef rota DEĞİŞMEDİ: kayıt, yedi üreticinin bugünkü çıktısını birebir
//     verir (range / env / tab / params / scope aynen taşınır);
//   • eski SubjectLink dalı: servis olmayan özne bağlantısız + rozet;
//   • hover kartı 400 ms sonra, tembel parçadan; hover boyunca ağ isteği 0
//     (fetch + XMLHttpRequest sayacı) — Karar A5 çıkış ölçütü;
//   • gecikme dolmadan ayrılan fare kartı açmaz;
//   • tür kaydının her girdisi TR + EN katalogda adlı.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { EntityLink, HOVER_DELAY_MS } from './EntityLink';
import { ENTITY_KINDS, entityTargetHref, entityPivots } from '@/lib/entityKinds';
import { serviceHref } from '@/lib/serviceHref';
import { entityHref } from '@/lib/entityHref';
import { podDetailPath } from '@/pages/service/podDetailPath';
import { endpointDetailHref } from '@/pages/endpoints/endpointParam';
import { databaseDetailHref } from '@/pages/databases/databaseParam';
import { messagingTopicHref } from '@/pages/messaging/topicHref';
import { EN } from '@/lib/i18n.en';
import { TR } from '@/lib/i18n.tr';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); document.body.innerHTML = ''; vi.restoreAllMocks(); });

const wait = (ms: number) => new Promise(r => setTimeout(r, ms));

describe('entityTargetHref — rotalar bu dilimde değişmez', () => {
  const range = 'custom:1700000000000-1700000600000';
  it('yedi üreticinin çıktısı birebir', () => {
    expect(entityTargetHref({ kind: 'service', name: 'orders', opts: { range, env: 'uat', tab: 'logs', params: { op: 'GET /x' } } }))
      .toBe(serviceHref('orders', { range, env: 'uat', tab: 'logs', params: { op: 'GET /x' } }));
    const rec = { type: 'pod' as const, id: 'pod:c1/ns/p-1', name: 'p-1', clusterId: 'c1', namespace: 'ns' };
    expect(entityTargetHref({ kind: 'record', rec, opts: { range, service: 'orders' } })).toBe(entityHref(rec, { range, service: 'orders' }));
    const node = { type: 'node' as const, id: 'node:c1/n1', name: 'n1', clusterId: 'c1' };
    expect(entityTargetHref({ kind: 'record', rec: node, opts: { at: 5 } })).toBe('/entity?id=node%3Ac1%2Fn1&at=5');
    const pod = { pod: 'p-1', cluster: 'c1', namespace: 'ns', service: 'orders', range, from: 'infra' as const };
    expect(entityTargetHref({ kind: 'pod', ref: pod })).toBe(podDetailPath(pod));
    const ep = { service: 'orders', path: '/api/x', sig: false, method: 'GET' };
    expect(entityTargetHref({ kind: 'endpoint', ref: ep, scope: { range, env: 'uat', cluster: 'c1' } }))
      .toBe(endpointDetailHref(ep, { range, env: 'uat', cluster: 'c1' }));
    const db = { system: 'postgresql', instance: 'db-1', dbName: 'app', source: 'spans' as const };
    expect(entityTargetHref({ kind: 'database', ref: db, scope: { range } })).toBe(databaseDetailHref(db, { range }));
    const topic = { system: 'kafka', cluster: 'k1', destination: 'events', range };
    expect(entityTargetHref({ kind: 'queue', ref: topic })).toBe(messagingTopicHref(topic));
  });

  it('pivotlar yalnız tab değiştirir, kapsamı taşır', () => {
    const p = entityPivots({ kind: 'service', name: 'orders', opts: { range, env: 'uat', hash: 'deploys' } });
    expect(p.map(x => x.href)).toEqual([
      serviceHref('orders', { range, env: 'uat', tab: 'logs' }),
      serviceHref('orders', { range, env: 'uat', tab: 'details' }),
    ]);
  });

  it('tür kaydının her girdisi TR + EN katalogda', () => {
    for (const spec of Object.values(ENTITY_KINDS)) {
      expect(EN[spec.labelKey], spec.labelKey).toBeTruthy();
      expect(TR[spec.labelKey], spec.labelKey).toBeTruthy();
    }
  });
});

function render(node: React.ReactNode) {
  act(() => root.render(<MemoryRouter>{node}</MemoryRouter>));
}

describe('EntityLink', () => {
  it('bağlantı bugünkü href ile, çağıranın sınıfı / title korunur', () => {
    render(<EntityLink to={{ kind: 'service', name: 'orders', opts: { range: '6h' } }} className="mono" title="t">orders</EntityLink>);
    const a = host.querySelector('a')!;
    expect(a.getAttribute('href')).toBe(serviceHref('orders', { range: '6h' }));
    expect(a.className).toBe('mono');
    expect(a.title).toBe('t');
    expect(a.textContent).toBe('orders');
  });

  it('subject: servis olmayan özne bağlantısız, rozetli (eski SubjectLink dalı)', () => {
    render(<EntityLink to={{ kind: 'subject', service: 'db:oracle@db-1' }} />);
    expect(host.querySelector('a')).toBeNull();
    expect(host.textContent).toContain('DB');
    render(<EntityLink to={{ kind: 'subject', service: 'orders', opts: { range: '1h' } }} />);
    expect(host.querySelector('a')!.getAttribute('href')).toBe(serviceHref('orders', { range: '1h' }));
    render(<EntityLink to={{ kind: 'subject', service: '' }} emptyFallback="—" />);
    expect(host.textContent).toBe('—');
  });

  it('hover kartı 400 ms sonra açılır; hover boyunca ağ isteği 0', async () => {
    const fetchSpy = vi.fn(() => Promise.reject(new Error('no network in hover')));
    vi.stubGlobal('fetch', fetchSpy);
    const xhrOpen = vi.spyOn(XMLHttpRequest.prototype, 'open');
    render(
      <EntityLink to={{ kind: 'service', name: 'orders', opts: { range: '6h' } }}
        card={{ health: 'critical', metrics: [{ label: 'p95', value: '120 ms' }, { label: 'err', value: '2,1 %' }] }}>
        orders
      </EntityLink>,
    );
    const a = host.querySelector('a')!;
    act(() => { a.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })); });
    await act(async () => { await wait(HOVER_DELAY_MS - 150); });
    expect(document.querySelector('[data-testid="entity-hover-card"]')).toBeNull();
    await act(async () => { await wait(300); });
    for (let i = 0; i < 20 && !document.querySelector('[data-testid="entity-hover-card"]'); i++) {
      await act(async () => { await wait(25); });
    }
    const card = document.querySelector('[data-testid="entity-hover-card"]')!;
    expect(card).not.toBeNull();
    expect(card.textContent).toContain('Service');
    expect(card.textContent).toContain('orders');
    expect(card.textContent).toContain('120 ms');
    expect(card.querySelector('[data-health="critical"]')).not.toBeNull();
    const hrefs = [...card.querySelectorAll('a')].map(x => x.getAttribute('href'));
    expect(hrefs).toEqual([serviceHref('orders', { range: '6h', tab: 'logs' }), serviceHref('orders', { range: '6h', tab: 'details' })]);
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(xhrOpen).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it('gecikme dolmadan ayrılan fare kartı açmaz; card={false} hiç açmaz', async () => {
    render(<EntityLink to={{ kind: 'service', name: 'orders' }}>orders</EntityLink>);
    const a = host.querySelector('a')!;
    act(() => { a.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })); });
    await act(async () => { await wait(100); });
    act(() => { a.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })); });
    await act(async () => { await wait(HOVER_DELAY_MS + 100); });
    expect(document.querySelector('[data-testid="entity-hover-card"]')).toBeNull();

    render(<EntityLink to={{ kind: 'service', name: 'orders' }} card={false}>orders</EntityLink>);
    const b = host.querySelector('a')!;
    act(() => { b.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })); });
    await act(async () => { await wait(HOVER_DELAY_MS + 100); });
    expect(document.querySelector('[data-testid="entity-hover-card"]')).toBeNull();
  });
});
