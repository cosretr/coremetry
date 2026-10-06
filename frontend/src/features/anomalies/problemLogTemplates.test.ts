// problemLogTemplates — v0.10.1113: "Başlangıçta doğan log şablonları"nın saf
// çekirdeği — göreli zaman etiketi, satır pivotu ve yoklama kararı.
import { describe, it, expect } from 'vitest';
import { newTemplateLogsHref, startOffsetLabel, startOffsetTitle } from './problemLogTemplates';
import { problemLogTemplatesInterval, PROBLEM_LOG_TEMPLATES_POLL_MS } from '@/lib/queries/problems';

describe('startOffsetLabel / startOffsetTitle', () => {
  it.each([
    [-40, '−40 sn', 'başlangıçtan 40 sn önce doğdu'],
    [40, '+40 sn', 'başlangıçtan 40 sn sonra doğdu'],
    [-120, '−2 dk', 'başlangıçtan 2 dk önce doğdu'],
    [-150, '−2 dk 30 sn', 'başlangıçtan 2 dk 30 sn önce doğdu'],
    [299, '+4 dk 59 sn', 'başlangıçtan 4 dk 59 sn sonra doğdu'],
    [0, 'başlangıçta', 'başlangıç anında doğdu'],
  ])('%d sn → %s', (sec, label, title) => {
    expect(startOffsetLabel(sec)).toBe(label);
    expect(startOffsetTitle(sec)).toBe(title);
  });
  it('eksi işareti tire değil (U+2212)', () => {
    expect(startOffsetLabel(-5).charCodeAt(0)).toBe(0x2212);
  });
  it('bozuk değer', () => {
    expect(startOffsetLabel(Number.NaN)).toBe('—');
    expect(startOffsetTitle(Number.POSITIVE_INFINITY)).toBe('');
  });
});

describe('newTemplateLogsHref', () => {
  const T = 1_759_399_980 * 1e9;
  it('servis + sunucunun arama metni, pencere doğumdan 1 dk önce → problem penceresinin sonu', () => {
    const href = newTemplateLogsHref(
      { service: 'payments-api', query: '"ledger write rejected" AND "account"', firstSeen: T },
      { fromNs: T - 3600e9, toNs: T + 1200e9 });
    const u = new URL(href, 'http://x');
    expect(u.pathname).toBe('/logs');
    expect(u.searchParams.get('service')).toBe('payments-api');
    expect(u.searchParams.get('q')).toBe('"ledger write rejected" AND "account"');
    expect(u.searchParams.get('range')).toBe(`custom:${(T - 60e9) / 1e6}-${(T + 1200e9) / 1e6}`);
  });
  it('arama metni yoksa q yazılmaz; ters pencerede doğumdan 15 dk', () => {
    const u = new URL(newTemplateLogsHref({ service: 'svc-orders', query: '', firstSeen: T }, { fromNs: 0, toNs: T - 600e9 }), 'http://x');
    expect(u.searchParams.has('q')).toBe(false);
    expect(u.searchParams.get('range')).toBe(`custom:${(T - 60e9) / 1e6}-${(T + 900e9) / 1e6}`);
  });
});

describe('problemLogTemplatesInterval', () => {
  const startMs = 1_759_399_980_000;
  it('açık Problem, pencere dolarken 60 s; sonra yok', () => {
    expect(problemLogTemplatesInterval(startMs * 1e6, true, startMs + 2 * 60_000)).toBe(PROBLEM_LOG_TEMPLATES_POLL_MS);
    expect(PROBLEM_LOG_TEMPLATES_POLL_MS).toBeGreaterThanOrEqual(60_000);
    expect(problemLogTemplatesInterval(startMs * 1e6, true, startMs + 16 * 60_000)).toBe(false);
  });
  it('çözülmüş Problem ya da bozuk başlangıç: hiç yoklama', () => {
    expect(problemLogTemplatesInterval(startMs * 1e6, false, startMs + 60_000)).toBe(false);
    expect(problemLogTemplatesInterval(0, true, startMs)).toBe(false);
  });
});
