// @vitest-environment jsdom
//
// v0.10.2482 — etki özeti satırı (Problems Etki sütunu + detay Etki kartı):
// TR sunucu metni aynen, EN sayılardan; renk yalnız sapan parçada.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { ProblemStandard, StdImpactSummary } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => {
  const a = await orig<typeof import('@/lib/i18n')>();
  return { ...a, useLang: () => m.lang, useT: () => (k: string) => a.t(k, m.lang) };
});
import { impactSummaryParts, impactSummaryText, impactSortValue } from './impactSummaryView';
import { ProblemStandardBlock } from '@/features/anomalies/ProblemStandardHeader';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ERR: StdImpactSummary = { kind: 'errors', scope: 'service', perMin: 340, sharePct: 14, rank: 340, text: "~340 istek/dk başarısız · trafiğin %14'ü" };
const ROUTE: StdImpactSummary = { kind: 'errors', scope: 'route', route: 'POST /pay', perMin: 0.2, sharePct: 6.8, rank: 0.2, text: "<1 istek/dk başarısız · uç nokta trafiğinin %6,8'i" };
const LAT: StdImpactSummary = { kind: 'latency', scope: 'service', perMin: 1200, delayMs: 680, rank: 1200, text: '~1.200 istek/dk · +680 ms' };
const LAG: StdImpactSummary = { kind: 'lag', backlog: 18213, growthPerMin: 420, rank: 420, text: '18.213 mesaj birikmiş · +420/dk' };
const OOM: StdImpactSummary = { kind: 'oom', killed: 3, pods: 5, rank: 3, text: '3/5 pod' };

describe('impactSummaryView', () => {
  it('TR: sunucu metni aynen; sapan parça işaretli', () => {
    expect(impactSummaryParts(ERR, 'tr')).toEqual([{ text: '~340 istek/dk başarısız', hot: true }, { text: "trafiğin %14'ü", hot: false }]);
    expect(impactSummaryParts(LAT, 'tr')).toEqual([{ text: '~1.200 istek/dk', hot: false }, { text: '+680 ms', hot: true }]);
    expect(impactSummaryText(LAG, 'tr')).toBe('18.213 mesaj birikmiş · +420/dk');
    expect(impactSummaryText(OOM, 'tr')).toBe('3/5 pod');
    expect(impactSummaryParts(null, 'tr')).toBeNull();
  });

  it('EN: sayılardan', () => {
    expect(impactSummaryText(ERR, 'en')).toBe('~340 req/min failing · 14% of traffic');
    expect(impactSummaryText(ROUTE, 'en')).toBe('<1 req/min failing · 6.8% of endpoint traffic');
    expect(impactSummaryText(LAT, 'en')).toBe('~1,200 req/min · +680 ms');
    expect(impactSummaryText({ ...LAT, delayMs: 0 }, 'en')).toBe('~1,200 req/min');
    expect(impactSummaryText(LAG, 'en')).toBe('18,213 messages backlogged · +420/min');
    expect(impactSummaryText({ ...LAG, growthPerMin: -5 }, 'en')).toBe('18,213 messages backlogged');
    expect(impactSummaryText(OOM, 'en')).toBe('3/5 pods');
  });

  it('sıralama değeri: rank; özetsiz −1', () => {
    expect(impactSortValue({ impactSummary: LAT } as ProblemStandard)).toBe(1200);
    expect(impactSortValue({} as ProblemStandard)).toBe(-1);
    expect(impactSortValue(undefined)).toBe(-1);
  });
});

describe('Problem detayı Etki kartı', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.lang = 'tr'; });

  const std = (over: Partial<ProblemStandard> = {}): ProblemStandard => ({
    family: 'error', kind: 'anomaly.error_rate', subject: 'svc-a', headline: 'svc-a: hata oranı yükseldi',
    impact: { known: true, primary: { text: '%14 hata · normal %1 (14×)', value: 14, normal: 1 } },
    since: { startedAt: 1_700_000_000e9, durationSec: 600, ongoing: true },
    evidence: { sentence: 'eşiğin üstünde', source: 'vm', sampled: false },
    confidence: { level: 'high', gates: [] },
    nextStep: { text: 'x' },
    ...over,
  } as ProblemStandard);

  const render = (s: ProblemStandard) => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => root!.render(<MemoryRouter><ProblemStandardBlock std={s} /></MemoryRouter>));
    return host;
  };

  it('özet varsa kartın değeri özet satırı, standardın cümlesi alt satırda', () => {
    const el = render(std({ impactSummary: ERR }));
    const v = el.querySelector('[data-testid="pd-impact-summary"]')!;
    expect(v.textContent).toBe("~340 istek/dk başarısız · trafiğin %14'ü");
    expect(v.querySelector('.cell-err')?.textContent).toBe('~340 istek/dk başarısız');
    expect(el.querySelector('.std-card__sub')?.textContent).toContain('%14 hata · normal %1 (14×)');
  });

  it('özet yoksa kart önceki gibi', () => {
    const el = render(std());
    expect(el.querySelector('[data-testid="pd-impact-summary"]')).toBeNull();
    expect(el.querySelector('.std-card__value')?.textContent).toBe('%14 hata · normal %1 (14×)');
  });
});
