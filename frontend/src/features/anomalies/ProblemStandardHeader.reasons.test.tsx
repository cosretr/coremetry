// @vitest-environment jsdom
//
// v0.10.2383 (triage mockup dilim 2) — "Neden böyle değerlendiriyoruz" bloğu:
// sunucu etki kuralı gerçeklerini satır satır verdiyse (evidence.reasons) her
// biri AYRI satır çizilir; vermediyse bugünkü tek cümle aynen. Metin tek Go
// çekirdeğinden: TR sunucu metni bayt bayt, EN anahtardan (std.m.why.*).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { ProblemStandard, StdMsg } from '@/lib/types';
import { stdReasonsView } from '@/lib/problemStandardView';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
import { ProblemStandardBlock } from './ProblemStandardHeader';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SENTENCE = "Dedektör sürdürme koşulunu geçti; ölçü VictoriaMetrics'ten (örneklenmemiş).";
const REASONS = [
  "Son 5 dakikanın 5'inde eşik aşıldı (en az 3 gerekir)",
  'Eşik %1,4: en az %1, normalin 1,5 katı ve 1 puan üstü',
  'Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)',
  'Kaynak örneklenmemiş sunucu metriği',
];
const REASON_MSGS: StdMsg[] = [
  { k: 'why.viol', a: { n: '5', w: '5', min: '3' } },
  { k: 'why.threshold', a: { t: '1.4%', floor: '1%', rel: '1.5', abs: '1' } },
  { k: 'why.load', a: { load: '3,100', min: '10' } },
  { k: 'why.srcVM' },
];
const REASONS_EN = [
  'Threshold exceeded in 5 of the last 5 minutes (at least 3 required)',
  'Threshold 1.4%: at least 1%, 1.5× normal and 1 pt above normal',
  'Enough load: about 3,100 requests per minute (at least 10 required)',
  'Source: unsampled server metric',
];

function std(withReasons: boolean): ProblemStandard {
  return {
    family: 'error', kind: 'anomaly_auto', subject: 'orders',
    headline: 'orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat).',
    headlineMsg: { k: 'h.mlErr', a: { svc: 'orders', n: '0.4%', v: '6.8%', x: '17' } },
    impact: { primary: { text: 'x', value: 0.068, normal: 0.005 }, known: true },
    since: { startedAt: 0, durationSec: 840, ongoing: true },
    evidence: {
      sentence: SENTENCE, source: 'vm', sampled: false, msg: { k: 'ev.sentence', a: { baseK: 'ev.base.dwell', srcK: 'ev.src.vm' } },
      ...(withReasons ? { reasons: REASONS, reasonMsgs: REASON_MSGS } : {}),
    },
    confidence: { level: 'high', gates: [{ id: 'G2', name: 'sürdürme', status: 'pass' }, { id: 'G3', name: 'VM karar', status: 'pass' }] },
    nextStep: { text: 'Servisin hata kırılımına bak' },
  };
}

describe('stdReasonsView', () => {
  it('satır yoksa boş dizi (çağıran tek cümleyi çizer)', () => {
    expect(stdReasonsView(std(false))).toEqual([]);
    expect(stdReasonsView(std(false), 'en')).toEqual([]);
  });
  it('TR: sunucu satırları bayt bayt', () => {
    expect(stdReasonsView(std(true), 'tr')).toEqual(REASONS.map(text => ({ text, orig: false })));
  });
  it('EN: her satır anahtarından', () => {
    expect(stdReasonsView(std(true), 'en')).toEqual(REASONS_EN.map(text => ({ text, orig: false })));
  });
  it('EN: diğer dallar (açık kalan olay, yalnız eşik)', () => {
    const s = std(true);
    s.evidence.reasons = ['a', 'b'];
    s.evidence.reasonMsgs = [{ k: 'why.violOpen', a: { n: '2', w: '5', min: '3' } }, { k: 'why.thrOnly', a: { t: '3%' } }];
    expect(stdReasonsView(s, 'en').map(r => r.text)).toEqual([
      'Threshold exceeded in 2 of the last 5 minutes (at least 3 required to open; still open while violations continue)',
      'Threshold 3%',
    ]);
  });
  it('EN: anahtar dizisi eşleşmezse / bilinmiyorsa sunucu metni + özgün dil işareti', () => {
    const s = std(true);
    s.evidence.reasonMsgs = REASON_MSGS.slice(0, 2);
    expect(stdReasonsView(s, 'en')).toEqual(REASONS.map(text => ({ text, orig: true })));
    s.evidence.reasonMsgs = [{ k: 'why.nope' }, ...REASON_MSGS.slice(1)];
    expect(stdReasonsView(s, 'en')[0]).toEqual({ text: REASONS[0], orig: true });
    expect(stdReasonsView(s, 'en')[1]).toEqual({ text: REASONS_EN[1], orig: false });
  });
});

describe('ProblemStandardBlock — neden bloğu', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; m.lang = 'tr'; });
  const render = async (s: ProblemStandard, factsOnly = false) => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<MemoryRouter><ProblemStandardBlock std={s} factsOnly={factsOnly} /></MemoryRouter>); });
    return host;
  };
  const whyDd = (h: HTMLElement) => h.querySelector('.std-why dd') as HTMLElement;
  // satırlar dd'nin doğrudan çocuğu düz <div> (yeni CSS sınıfı yok)
  const lineEls = (h: HTMLElement) => [...whyDd(h).children].filter(e => e.tagName === 'DIV');

  it('satırlar varsa her biri ayrı satır; tek cümle çizilmez', async () => {
    const h = await render(std(true));
    expect(lineEls(h).map(e => e.textContent)).toEqual(REASONS);
    expect(h.textContent).not.toContain(SENTENCE);
  });
  it('satır yoksa bugünkü tek cümle', async () => {
    const h = await render(std(false));
    expect(lineEls(h).length).toBe(0);
    expect(whyDd(h).textContent).toBe(SENTENCE);
  });
  it('EN: satırlar ve manşet anahtardan, Türkçe işareti yok', async () => {
    m.lang = 'en';
    const h = await render(std(true));
    expect(lineEls(h).map(e => e.textContent)).toEqual(REASONS_EN);
    expect(h.querySelector('h1')?.textContent).toBe('orders — error rate normally 0.4%, now 6.8% (17×).');
    expect(whyDd(h).querySelector('[lang="tr"]')).toBeNull();
    expect(h.querySelector('h1 [lang="tr"]')).toBeNull();
  });
  it('yalnız dört kart kipinde blok yok', async () => {
    const h = await render(std(true), true);
    expect(h.querySelector('.std-why')).toBeNull();
  });
});
