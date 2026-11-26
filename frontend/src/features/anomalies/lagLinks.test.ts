// v0.10.2475 — consumer lag Problem'inin "Ne yapılabilir" bağlantıları.
import { describe, it, expect } from 'vitest';
import { lagProblemLinks, LAG_METRIC } from './lagLinks';

describe('lagProblemLinks', () => {
  const win = { fromNs: 1_700_000_000_000_000_000, toNs: 1_700_000_600_000_000_000 };
  it('Messaging sekmesi topic süzgeciyle; lag grafiği VM, topic süzgeçli', () => {
    const l = lagProblemLinks('checkout', { topic: 'orders-topic', observed: 26, baseline: 5 }, win);
    const m = new URL(l.messagingHref, 'http://x');
    expect(m.pathname).toBe('/service');
    expect(m.searchParams.get('name')).toBe('checkout');
    expect(m.searchParams.get('tab')).toBe('messaging');
    expect(m.searchParams.get('ktopic')).toBe('orders-topic');
    expect(m.searchParams.get('range')).toBeTruthy();
    expect(l.chartHref).toMatch(/^\/metrics\?mx=/);
    expect(l.chartHref).toContain('range=');
  });
  it('topic yoksa süzgeç yok: Messaging sekmesi düz, grafik tek sorgu (katalog linki)', () => {
    const l = lagProblemLinks('checkout', { observed: 1200, baseline: 0 }, win);
    expect(new URL(l.messagingHref, 'http://x').searchParams.has('ktopic')).toBe(false);
    expect(l.chartHref).toContain(`metric=${LAG_METRIC}`);
    expect(l.chartHref).toContain('by=topic');
  });
});
