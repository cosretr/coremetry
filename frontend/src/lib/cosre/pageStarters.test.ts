import { describe, it, expect } from 'vitest';
import { pageContext } from '@/lib/pageContext';
import { pageStarters, PAGE_STARTER_MAX } from './pageStarters';
import { nextPanelOpen } from '@/components/ai/aiEvents';
import { welcomeTabs } from '@/components/cosre/conversation/welcomeSuggestions';

const t = (k: string) => `[${k}] {svc}{op}{pod}`;
const at = (url: string) => {
  const [p, q = ''] = url.split('?');
  return pageStarters(pageContext(p, q), t);
};

describe('pageStarters — sayfa türüne göre 2–4 hazır soru (wave 3)', () => {
  it('servis sayfası: varlık adı bağlamdan, 3 soru', () => {
    const s = at('/service?name=checkout');
    expect(s.map(x => x.id)).toEqual(['service.slow', 'service.errors', 'service.deploy']);
    expect(s[0].question).toContain('checkout');
  });
  it('endpoint detayı: servis + route (pageContext operation)', () => {
    const s = at('/endpoint?service=api&path=/pay');
    expect(s.map(x => x.id)).toEqual(['endpoint.latency', 'endpoint.errors']);
    expect(s[0].question).toContain('api /pay');
  });
  it('pod ve entity pod kimliği', () => {
    expect(at('/pod?pod=web-1&cluster=c1').map(x => x.id)).toEqual(['pod.health', 'pod.restarts']);
    expect(at('/entity?id=pod:c1/ns/web-2')[0].question).toContain('web-2');
  });
  it('varlıksız ayrıntı ve bağlamsız sayfa → boş', () => {
    expect(at('/service')).toEqual([]);
    expect(at('/settings')).toEqual([]);
  });
  it('liste sayfaları ve tavan', () => {
    expect(at('/logs').length).toBe(2);
    for (const u of ['/service?name=a', '/logs', '/endpoints', '/trace?id=' + 'a'.repeat(32), '/problems?problem=P1']) {
      expect(at(u).length).toBeLessThanOrEqual(PAGE_STARTER_MAX);
    }
  });
  it('karşılamanın Araştır sekmesine düşer (≤4)', () => {
    const tabs = welcomeTabs({ team: '', wiki: false, ctxStarter: null, pageStarters: at('/service?name=a') });
    const inv = tabs.find(x => x.id === 'investigate')!;
    expect(inv.items.slice(0, 3).every(i => i.mode === 'send')).toBe(true);
    expect(inv.items.length).toBe(4);
  });
});

describe('nextPanelOpen', () => {
  it('toggle tersine çevirir, open açar', () => {
    expect(nextPanelOpen(false, { mode: 'toggle' })).toBe(true);
    expect(nextPanelOpen(true, { mode: 'toggle' })).toBe(false);
    expect(nextPanelOpen(true, { mode: 'open' })).toBe(true);
    expect(nextPanelOpen(false, undefined)).toBe(true);
  });
});
