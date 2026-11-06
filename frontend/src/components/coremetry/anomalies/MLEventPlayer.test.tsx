// @vitest-environment jsdom
//
// MLEventPlayer — v0.10.1846 (audit §15, §17 "olayı oynat"). Çiviler:
//   • durağan hâlde imleç son noktada ("şimdi / bitiş"), cümle görünür;
//   • ▶ ilk sapmadan 6 kova önce başlar, kova başına ilerler, sonda durur;
//   • hareket azaltma: yalnız adımlar arası atlar;
//   • sekme gizlenince duraklar; adım düğmesi imleci oraya taşır;
//   • ?play=1 (autoPlay) veri gelince kendiliğinden başlar.
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ExplainPlay, MLSnapshot } from '@/lib/types';

const m = vi.hoisted(() => ({ reduced: false }));
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: (p: { playhead?: { cursorSec: number } | null }) => <div data-testid="chart" data-cursor={p.playhead?.cursorSec ?? ''} />,
}));

import { MLEventPlayer } from './MLEventPlayer';
import { PLAY_REDUCED_STEP_MS, PLAY_TICK_MS } from '@/lib/mlPlay';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_800_000_000;
const times = Array.from({ length: 20 }, (_, i) => T0 + i * 300);
const snap: MLSnapshot = {
  eventId: 'e1', detector: 'op_latency', signal: 'trace', service: 'notify-gateway', part: '/x|slow', detectedAt: times[11] * 1e9,
  lastSeen: times[19] * 1e9, bucketSec: 300, series: JSON.stringify(times.map(t => ({ t, v: 0.04 }))), baselineMed: 0.04, baselineMad: 0.004,
  thresholds: '{"criticalZ":4}', configHash: '', dayClass: 'weekday', dwell: '[]', confirmIds: null, evidence: '{}', samples: '[]', deploys: '[]', complete: false,
};
const play: ExplainPlay = {
  from: times[10],
  steps: [{ key: 'first', label: 'İlk sapma', t: times[10] }, { key: 'detect', label: 'Tespit', t: times[11] }, { key: 'peak', label: 'Tepe', t: times[14] }, { key: 'open', label: 'Sürüyor', t: times[19] }],
  frames: times.map((t, i) => ({ t, clock: `c${i}`, text: `kare ${i}` })),
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeEach(() => {
  vi.useFakeTimers();
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia = ((q: string) => ({ matches: q.includes('reduce') ? m.reduced : false, media: q, addEventListener: () => {}, removeEventListener: () => {} })) as unknown as typeof window.matchMedia;
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  vi.useRealTimers();
  m.reduced = false;
});

function render(autoPlay = false): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MLEventPlayer snap={snap} play={play} autoPlay={autoPlay} />); });
  return host;
}
const cursor = (el: HTMLElement) => Number(el.querySelector('[data-testid="chart"]')!.getAttribute('data-cursor'));
const say = (el: HTMLElement) => el.querySelector('[data-testid="ml-play-say"]')!.textContent;
const tick = (ms: number) => act(() => { vi.advanceTimersByTime(ms); });
const click = (el: Element) => act(() => { (el as HTMLElement).click(); });

describe('MLEventPlayer', () => {
  it('at rest: cursor on the last point, sentence readable without playing', () => {
    const el = render();
    expect(cursor(el)).toBe(times[19]);
    expect(say(el)).toBe('c19 — kare 19');
    expect(el.querySelector('[data-testid="ml-play-say"]')!.getAttribute('aria-live')).toBe('polite');
    expect(el.querySelector('[data-testid="ml-step-open"]')!.getAttribute('aria-pressed')).toBe('true');
  });
  it('▶ starts 6 buckets before the first deviation, steps per bucket, stops at the end', () => {
    const el = render();
    click(el.querySelector('[data-testid="ml-play-toggle"]')!);
    expect(cursor(el)).toBe(times[4]);
    tick(PLAY_TICK_MS);
    expect(cursor(el)).toBe(times[5]);
    for (let k = 0; k < 30; k++) tick(PLAY_TICK_MS); // zincir: her kare bir sonraki zamanlayıcıyı kurar
    expect(cursor(el)).toBe(times[19]);
    expect(el.querySelector('[data-testid="ml-play-toggle"]')!.textContent).toContain('Oynat');
  });
  it('reduced motion jumps between steps only', () => {
    m.reduced = true;
    const el = render();
    click(el.querySelector('[data-testid="ml-play-toggle"]')!);
    expect(cursor(el)).toBe(times[10]);
    tick(PLAY_REDUCED_STEP_MS);
    expect(cursor(el)).toBe(times[11]);
    tick(PLAY_REDUCED_STEP_MS);
    expect(cursor(el)).toBe(times[14]);
  });
  it('pauses when the tab is hidden; step buttons move the cursor', () => {
    const el = render();
    click(el.querySelector('[data-testid="ml-play-toggle"]')!);
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    const at = cursor(el);
    for (let k = 0; k < 5; k++) tick(PLAY_TICK_MS);
    expect(cursor(el)).toBe(at);
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false });
    click(el.querySelector('[data-testid="ml-step-peak"]')!);
    expect(cursor(el)).toBe(times[14]);
    expect(say(el)).toBe('c14 — kare 14');
  });
  it('autoPlay (?play=1) starts once', () => {
    const el = render(true);
    expect(cursor(el)).toBe(times[4]);
    tick(PLAY_TICK_MS);
    expect(cursor(el)).toBe(times[5]);
  });
});
