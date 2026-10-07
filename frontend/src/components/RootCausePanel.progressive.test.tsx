// @vitest-environment jsdom
//
// RootCausePanel.progressive — v0.10.1119 regresyon testi (operatör: kök-neden
// paneli soğuk açılışta 30–45 sn boş bekliyordu).
//
// NE ÇİVİLİYOR: panel /rootcause/core'u gelir gelmez çizer (bubbleUp hâlâ
// hesaplanırken blast radius / exemplar görünür); manşet bubbleUp'a bağlıysa
// "inceleniyor" der, bubbleUp gelince tam demetle AYNI manşete döner;
// bubbleUp düşerse ekran, alt-okuması düşmüş tam demetle aynıdır. Servis
// adları sentetik.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { RootCause, RootCauseBubbleUp, BubbleUpResult } from '@/lib/types';

type Deferred<T> = { promise: Promise<T>; resolve: (v: T) => void; reject: (e: unknown) => void };
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

const h = vi.hoisted(() => ({
  core: null as unknown as Promise<unknown>,
  bubble: null as unknown as Promise<unknown>,
}));
vi.mock('@/lib/api', () => ({
  api: {
    problemRootCauseCore: () => h.core,
    problemRootCauseBubbleUp: () => h.bubble,
  },
}));

import { RootCausePanel } from './RootCausePanel';
import { withBubbleUp, headlineNeedsBubble } from '@/lib/rootCauseProgressive';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const core = (over: Partial<RootCause> = {}): RootCause => ({
  problemId: 'p1', service: 'svc-checkout', metric: 'error_rate',
  startedAt: 1.7e18, fromNs: 1.7e18, toNs: 1.7e18 + 6e11,
  correlations: [],
  blastRadius: {
    service: 'svc-checkout', windowSec: 600, totalCallers: 1, cascadingCallers: 0,
    totalRps: 3, totalErrorsPerSec: 0.1,
    callers: [{ service: 'svc-gateway', calls: 50, errors: 1, rps: 3, errorRate: 2, hasOpenProblem: false }],
  },
  ...over,
});

const bubbleUp: BubbleUpResult = {
  selectionTotal: 40, baselineTotal: 900,
  attributes: [{ key: 'k8s.pod.name', values: [{
    value: 'svc-checkout-7d9f', selectionCount: 30, baselineCount: 90,
    selectionPct: 0.75, baselinePct: 0.1, score: 0.65,
  }] }],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><RootCausePanel problemId="p1" service="svc-checkout" /></MemoryRouter>);
  });
  return host;
}
const flush = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('RootCausePanel — aşamalı çizim', () => {
  it('çekirdek gelince çizer; bubbleUp beklerken manşet "inceleniyor", sonra tam demetin manşeti', async () => {
    const c = deferred<RootCause>();
    const b = deferred<RootCauseBubbleUp>();
    h.core = c.promise; h.bubble = b.promise;
    const el = await mount();

    await act(async () => { c.resolve(core()); });
    await flush();
    expect(el.textContent).toContain('Blast radius');
    expect(el.textContent).toContain('svc-gateway');
    expect(el.textContent).toContain('Comparing span attributes');
    expect(el.textContent).not.toContain('localized');

    await act(async () => { b.resolve({ fromNs: 1.7e18, toNs: 1.7e18 + 6e11, bubbleUp }); });
    await flush();
    expect(el.textContent).not.toContain('Comparing');
    expect(el.textContent).toContain('Errors concentrate in');
    expect(el.textContent).toContain('k8s.pod.name');
  });

  it('deploy varken manşet bubbleUp beklemez', async () => {
    h.core = Promise.resolve(core({ recentDeploy: { version: 'v2.4.1', timeUnixNs: 1.7e18, ageSeconds: 420 } }));
    h.bubble = new Promise(() => {});
    const el = await mount();
    await flush();
    expect(el.textContent).toContain('Coincides with a deploy');
    expect(el.textContent).not.toContain('Comparing span attributes');
    expect(el.textContent).toContain('Comparing attribute values'); // bölüm yerinde bekliyor
  });

  it('bubbleUp düşerse: manşet "Comparing"de takılmaz, bubbleUp\'sız tam demetin manşeti; bölüm "okunamadı" der', async () => {
    const b = deferred<RootCauseBubbleUp>();
    h.core = Promise.resolve(core());
    h.bubble = b.promise;
    const el = await mount();
    await flush();
    expect(el.textContent).toContain('Comparing span attributes');
    await act(async () => { b.reject(new Error('Request timed out after 95s')); });
    await flush();
    expect(el.textContent).not.toContain('Comparing');
    expect(el.textContent).toContain('localized to');
    const note = el.querySelector('[data-bubble-unavailable]');
    expect(note?.textContent).toContain('Attribute comparison unavailable');
    expect(el.textContent).not.toContain('No correlating signals');
  });

  it('bubbleUp boş döndüyse "okunamadı" DEMEZ (yokluk ≠ hata)', async () => {
    h.core = Promise.resolve(core());
    h.bubble = Promise.resolve({ fromNs: 0, toNs: 1, bubbleUp: { selectionTotal: 0, baselineTotal: 10, attributes: [] } });
    const el = await mount();
    await flush();
    expect(el.querySelector('[data-bubble-unavailable]')).toBeNull();
    expect(el.textContent).toContain('localized to');
  });

  it('çekirdek düşerse hata satırı (eskisi gibi)', async () => {
    h.core = Promise.reject(new Error('500'));
    h.bubble = new Promise(() => {});
    const el = await mount();
    await flush();
    expect(el.textContent).toContain('Root-cause analysis failed to load');
  });
});

describe('withBubbleUp / headlineNeedsBubble — saf', () => {
  it('çekirdek + bubbleUp = tam demet', () => {
    const full: RootCause = { ...core(), bubbleUp };
    expect(withBubbleUp(core(), { fromNs: 0, toNs: 1, bubbleUp })).toEqual(full);
  });
  it('bubbleUp yok / okunamadı → alan yok (tam demette düşen alt-okuma gibi)', () => {
    expect(withBubbleUp(core(), null)).toEqual(core());
    expect(withBubbleUp(core(), { fromNs: 0, toNs: 1 })).toEqual(core());
    expect('bubbleUp' in withBubbleUp(core(), undefined)).toBe(false);
  });
  it('manşet sırası: deploy ya da sıcak rollout varsa bubbleUp beklenmez', () => {
    expect(headlineNeedsBubble(core(), [])).toBe(true);
    expect(headlineNeedsBubble(core({ recentDeploy: { version: 'v1', timeUnixNs: 0, ageSeconds: 1 } }), [])).toBe(false);
  });
});
