// @vitest-environment jsdom
//
// v0.10.2310 — Davis özneleri ↔ ML karşılaştırma bölümü (EngineCompare.tsx):
// kapalı <details> açılınca tek istek; örneklenmiş (yanlı) satır rozeti,
// caveat cümleleri, tavan notu, boş durum; TR ve EN metin.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { AnomalyEngineCompare, EngineCompareRow } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr', data: null as unknown, enabled: [] as boolean[] }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/queries', () => ({
  useAnomalyEngineCompare: (enabled: boolean) => { m.enabled.push(enabled); return { data: m.data, isLoading: false, error: null }; },
}));

import { EngineCompareSection } from './EngineCompare';
import { engineCaveatText } from './engineCompareText';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const subj = (o: Partial<EngineCompareRow['subject']> = {}): EngineCompareRow['subject'] => ({
  series: 0, episodes: 0, episodesExact: true, matched: 0, only: 0, noCounterpart: 0, wouldOpen: 0, withProblem: 0, medianSec: 0, short: 0, perDay: 0, ...o,
});
const mls = (o: Partial<EngineCompareRow['ml']> = {}): EngineCompareRow['ml'] => ({
  events: 0, matched: 0, only: 0, indeterminate: 0, wouldOpen: 0, liveOpened: 0, withProblem: 0, medianSec: 0, short: 0, perDay: 0, ...o,
});
function fixture(rows: EngineCompareRow[], truncated = false): AnomalyEngineCompare {
  return {
    days: 7, fromNs: 1, toNs: 2, toleranceSec: 900, shortSec: 600,
    config: { subjects: { endpoint: true, queue: true, promotionMode: 'shadow' }, ml: { enabled: true, groupProblems: true, detectorsOff: ['throughput_drop'] } },
    refProblems: 0,
    totals: { subject: subj({ series: 3, only: 1, matched: 2 }), ml: mls({ events: 4, only: 1, indeterminate: 1, matched: 2 }) },
    rows,
    caveats: ['subject_last_episode_only', 'ml_group_problems_live', 'ml_detector_off:throughput_drop', 'no_reference_problems'],
    truncated: { subject: false, ml: truncated, problems: false, rows: false },
  };
}
const ROWS: EngineCompareRow[] = [
  { service: 'checkout', family: 'latency', source: 'metric', biased: false,
    subject: subj({ series: 3, matched: 2, only: 1, wouldOpen: 1, medianSec: 900, short: 1 }),
    ml: mls({ events: 4, matched: 2, only: 1, indeterminate: 1, wouldOpen: 2, medianSec: 1200 }),
    examples: { onlySubject: ['s-1'], onlyMl: ['m-1'] } },
  { service: 'queue:kafka@orders', family: 'errors', source: 'sampled_trace', biased: true,
    subject: subj({ series: 1, matched: 1 }), ml: mls({ events: 1, matched: 1 }), examples: { onlySubject: [], onlyMl: [] } },
];

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'tr'; m.data = null; m.enabled = []; });

function render(): HTMLDivElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><EngineCompareSection /></MemoryRouter>); });
  return host;
}

describe('EngineCompareSection (v0.10.2310)', () => {
  it('kapalıyken istek yok; açılınca etkin', () => {
    const el = render();
    expect(m.enabled.every(e => !e)).toBe(true);
    const d = el.querySelector('details')!;
    act(() => { d.open = true; d.dispatchEvent(new Event('toggle')); });
    expect(m.enabled.at(-1)).toBe(true);
  });

  it('yanlı satır rozeti, caveat, özet, örnek bağlantıları, "yalnız" rengi', () => {
    m.data = fixture(ROWS);
    const el = render();
    const badges = el.querySelectorAll('[data-testid="engine-compare-sampled"]');
    expect(badges.length).toBe(1);
    expect(badges[0].textContent).toBe('Örneklenmiş');
    expect(badges[0].getAttribute('title')).toBe('Örneklenmiş trace: hata oranı yaklaşık 20 kat şişik');
    const cav = el.querySelector('[data-testid="engine-compare-caveats"]')!.textContent!;
    expect(cav).toContain('Davis satırı yalnız son bölümü tutar; önceki bölümler eşlenemez.');
    expect(cav).toContain('ML bu dönemde gerçek Problem açıyor; Davis gölgede.');
    expect(cav).toContain('ML dedektörü kapalı: ');
    expect(el.querySelector('[data-testid="engine-compare-summary"]')!.textContent)
      .toBe('Son 7 gün (örneklenmiş satırlar hariç): Davis 3 olay, 1 yalnız Davis; ML 4 olay, 1 yalnız ML, 1 belirsiz; örtüşen 2 Davis / 2 ML.');
    const hrefs = [...el.querySelectorAll('a')].map(a => a.getAttribute('href'));
    expect(hrefs).toEqual(['/anomalies/s-1', '/anomalies/ml/m-1']);
    expect(el.querySelectorAll('.text-\\[color\\:var\\(--warn\\)\\]').length).toBe(2 + 1); // iki "yalnız" hücresi + rozet
    expect(el.textContent).not.toContain('Okuma ya da satır tavanına');
  });

  it('tavan notu ve boş durum', () => {
    m.data = fixture([], true);
    const el = render();
    expect(el.textContent).toContain('Okuma ya da satır tavanına değdi; bazı satırlar eksik olabilir.');
    expect(el.textContent).toContain('Bu dönemde iki motor da olay üretmedi');
  });

  it('İngilizce arayüz: başlık, caveat, özet, kolonlar, rozet İngilizce; Türkçe yok', () => {
    m.lang = 'en';
    m.data = fixture(ROWS, true);
    const el = render();
    const text = el.textContent ?? '';
    for (const en of ['Davis subjects ↔ ML: shadow comparison', 'Events of both engines for the same service and metric family, within ±15 min.',
      'A Davis row keeps only its last episode; earlier episodes cannot be matched.', 'ML opens real Problems in this period; Davis is in shadow.',
      'Last 7 days (sampled rows excluded): Davis 3 events, 1 Davis only; ML 4 events, 1 ML only, 1 indeterminate; overlap 2 Davis / 2 ML.',
      'Hit a read or row cap; some rows may be missing.', 'Davis only', 'ML only', 'Indeterminate', 'Latency', 'Errors', 'Sampled', 'Metric']) {
      expect(text, en).toContain(en);
    }
    expect(el.querySelector('[data-testid="engine-compare-sampled"]')!.getAttribute('title')).toBe('Sampled traces: error rate inflated about 20×');
    for (const trWord of ['Yalnız', 'Belirsiz', 'Örneklenmiş', 'Gecikme', 'olay', 'gölge', 'Son 7 gün']) expect(text, trWord).not.toContain(trWord);
  });

  it('caveat kodları: bilinmeyen aynen, tür / kip / referans', () => {
    expect(engineCaveatText('subject_kind_off:queue', 'tr')).toBe('Davis özne türü kapalı: kuyruk.');
    expect(engineCaveatText('subject_kind_off:queue', 'en')).toBe('Davis subject kind off: queue.');
    expect(engineCaveatText('subject_promotion_off', 'en')).toContain('Davis promotion mode is off');
    expect(engineCaveatText('no_reference_problems', 'tr')).toContain('Bu dönemde bağımsız Problem yok');
    expect(engineCaveatText('something_new', 'tr')).toBe('something_new');
  });
});
