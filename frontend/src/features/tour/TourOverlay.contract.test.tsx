// @vitest-environment jsdom
//
// TourOverlay.contract.test.tsx — v0.10.1334 rehberli tur örtüsü sözleşmesi.
// Kaynak taraması YETMEZ: odak hapsi, odak iadesi ve canlı duyuru ancak
// gerçek render + odak hareketiyle görülür (Modal.contract.test emsali).
//   • role=dialog + aria-modal + aria-labelledby → başlık
//   • ilk odak birincil (İleri/Bitir); Tab kartın içinde döner
//   • kapanınca odak açılıştaki elemana döner
//   • aria-live=polite duyuru her adımda güncellenir
//   • ←/→ kart kökünde; Esc → useEscLayer → atla
//   • waitForTarget: sahte zamanlayıcıyla 8 s zaman aşımı, yedek hedef payı,
//     MutationObserver ile geç beliren hedef, AbortSignal
//   • useTour: ResizeObserver YOKKEN (jsdom) çökmeden hedefi bulur; varken gözler
//   • v0.10.1336: restoreFocus=false → kapanışta iade YOK (CoSRE composer
//     karesi); hedef belirince kart dışına kaçan odak bir kare sonra birincile
//     döner; Esc sırası: sonradan açılan katman (çekmece) önce, tur sonra.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useEffect } from 'react';
import { MemoryRouter } from 'react-router';
import { TourOverlay, type TourOverlayProps } from './TourOverlay';
import { topEscLayer, pushEscLayer, __resetEscLayers } from '@/lib/escLayer';
import { waitForTarget, FALLBACK_GRACE_MS } from './waitForTarget';
import { TARGET_TIMEOUT_MS, type TourCtx } from './steps';
import { useTour, type UseTour } from './useTour';

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(ui: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(ui); });
}
function rerender(ui: React.ReactElement) {
  act(() => { root!.render(ui); });
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
  __resetEscLayers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function props(over: Partial<TourOverlayProps> = {}): TourOverlayProps {
  return {
    stepKey: 'services', stepLabel: 'Step 2 of 5', progressLabel: 'Tour progress',
    segments: [
      { id: 'welcome', kind: 'done' }, { id: 'services', kind: 'current' },
      { id: 'operations', kind: 'skipped', reason: 'not_yet' },
      { id: 'traces', kind: 'skipped', reason: 'not_yet' }, { id: 'cosre', kind: 'skipped', reason: 'not_yet' },
    ],
    segmentTitle: s => s.id,
    title: 'Services', body: 'Body', notes: [], rect: null, placement: 'bottom',
    dialogLabel: 'Guided tour',
    labels: { back: 'Back', next: 'Next', finish: 'Finish', skip: 'Skip tour' },
    canBack: true, isLast: false,
    onBack: () => {}, onNext: () => {}, onSkip: () => {},
    ...over,
  };
}

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const btn = (label: string) => [...document.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label)!;
function tab(shift = false) {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: shift, bubbles: true }));
}
function key(el: HTMLElement, k: string) {
  act(() => { el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
}

describe('TourOverlay — diyalog anatomisi', () => {
  it('role=dialog, aria-modal, aria-labelledby başlığa; body\'ye portallı', () => {
    render(<TourOverlay {...props()} />);
    const d = dialog();
    expect(d.getAttribute('aria-modal')).toBe('true');
    const title = document.getElementById(d.getAttribute('aria-labelledby')!);
    expect(title?.textContent).toBe('Services');
    expect(d.parentElement).toBe(document.body);
    expect(d.textContent).toContain('Step 2 of 5');
  });

  it('gösterge 5 segment; kapalılar is-skipped (kesik)', () => {
    render(<TourOverlay {...props()} />);
    const segs = document.querySelectorAll('.tour-progress__seg');
    expect(segs).toHaveLength(5);
    expect(document.querySelectorAll('.tour-progress__seg.is-skipped')).toHaveLength(3);
  });

  it('tek birincil düğme; son adımda "Finish"', () => {
    render(<TourOverlay {...props({ isLast: true })} />);
    // Button atomunda birincil = varyant sınıfı YOK; ikincil 'sec', hayalet 'ghost'.
    const foot = document.querySelector('.tour-card__foot')!;
    const primaries = [...foot.querySelectorAll('button')].filter(b => !/\b(sec|ghost|danger|accent)\b/.test(b.className));
    expect(primaries).toHaveLength(1);
    expect(btn('Back').classList.contains('sec')).toBe(true);
    expect(btn('Skip tour').classList.contains('ghost')).toBe(true);
    expect(btn('Finish')).toBeTruthy();
    expect(btn('Next')).toBeUndefined();
  });

  it('hedef yokken perde karartır, halka yok; hedef varken halka', () => {
    render(<TourOverlay {...props({ rect: null })} />);
    expect(document.querySelector('.tour-backdrop.is-dim')).not.toBeNull();
    expect(document.querySelector('[data-testid="tour-ring"]')).toBeNull();
    rerender(<TourOverlay {...props({ rect: { left: 100, top: 100, width: 200, height: 40 } })} />);
    const ring = document.querySelector<HTMLElement>('[data-testid="tour-ring"]')!;
    expect(ring.style.left).toBe('94px');
    expect(document.querySelector('.tour-backdrop.is-dim')).toBeNull();
  });
});

describe('TourOverlay — odak', () => {
  it('ilk odak birincil düğmede', () => {
    render(<TourOverlay {...props()} />);
    expect(document.activeElement).toBe(btn('Next'));
  });

  it('Tab sondan başa, Shift+Tab baştan sona döner', () => {
    render(<TourOverlay {...props()} />);
    btn('Next').focus();
    act(() => { tab(); });
    expect(document.activeElement).toBe(btn('Back'));
    act(() => { tab(true); });
    expect(document.activeElement).toBe(btn('Next'));
  });

  it('kapanınca odak açılıştaki elemana döner', () => {
    const trigger = document.createElement('button');
    trigger.textContent = 'trigger';
    document.body.appendChild(trigger);
    trigger.focus();
    render(<TourOverlay {...props()} />);
    expect(document.activeElement).not.toBe(trigger);
    act(() => { root!.unmount(); });
    root = null;
    expect(document.activeElement).toBe(trigger);
  });
});

describe('TourOverlay — v0.10.1336 odak dalları', () => {
  it('restoreFocus=false → kapanışta açılıştaki elemana iade YOK', () => {
    const trigger = document.createElement('button');
    trigger.textContent = 'trigger';
    document.body.appendChild(trigger);
    trigger.focus();
    render(<TourOverlay {...props()} />);
    rerender(<TourOverlay {...props({ restoreFocus: false })} />);
    act(() => { root!.unmount(); });
    root = null;
    expect(document.activeElement).not.toBe(trigger);
  });

  it('hedef belirince kart dışına kaçan odak (prefill/autoFocus) bir kare sonra birincile döner', async () => {
    const outside = document.createElement('textarea');
    document.body.appendChild(outside);
    render(<TourOverlay {...props({ stepKey: 'cosre.composer', rect: null })} />);
    outside.focus();
    expect(document.activeElement).toBe(outside);
    rerender(<TourOverlay {...props({ stepKey: 'cosre.composer', rect: { left: 10, top: 10, width: 100, height: 30 } })} />);
    await act(async () => { await new Promise(r => requestAnimationFrame(() => r(null))); });
    expect(document.activeElement).toBe(btn('Next'));
  });

  it('Esc sırası: tur sonra açılan katmanın ALTINDA — ilk Esc katmanı, ikinci Esc turu', () => {
    const onSkip = vi.fn();
    render(<TourOverlay {...props({ onSkip })} />);
    const drawerClose = vi.fn();
    const pop = pushEscLayer(drawerClose); // CoSRE çekmecesi (Drawer useEscLayer) turdan SONRA açılır
    act(() => { topEscLayer()!(); });
    expect(drawerClose).toHaveBeenCalledTimes(1);
    expect(onSkip).not.toHaveBeenCalled();
    pop(); // çekmece kapandı → katmanı düştü
    act(() => { topEscLayer()!(); });
    expect(onSkip).toHaveBeenCalledTimes(1);
  });
});

describe('TourOverlay — duyuru ve klavye', () => {
  it('aria-live=polite duyuru her adımda güncellenir', () => {
    render(<TourOverlay {...props({ stepKey: 'welcome', stepLabel: 'Step 1 of 5', title: 'Welcome' })} />);
    const live = document.querySelector<HTMLElement>('[role="status"][aria-live="polite"]')!;
    expect(dialog().contains(live)).toBe(true);
    expect(live.textContent).toBe('Step 1 of 5: Welcome');
    rerender(<TourOverlay {...props()} />);
    expect(live.textContent).toBe('Step 2 of 5: Services');
  });

  it('→ İleri, ← Geri (kart kökünde); Geri kapalıyken ← no-op', () => {
    const onNext = vi.fn(); const onBack = vi.fn();
    render(<TourOverlay {...props({ onNext, onBack })} />);
    key(dialog(), 'ArrowRight');
    key(dialog(), 'ArrowLeft');
    expect(onNext).toHaveBeenCalledTimes(1);
    expect(onBack).toHaveBeenCalledTimes(1);
    rerender(<TourOverlay {...props({ onNext, onBack, canBack: false })} />);
    key(dialog(), 'ArrowLeft');
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it('Esc katmanı = atla (useEscLayer); kapanınca katman düşer', () => {
    const onSkip = vi.fn();
    render(<TourOverlay {...props({ onSkip })} />);
    const layer = topEscLayer();
    expect(layer).not.toBeNull();
    act(() => { layer!(); });
    expect(onSkip).toHaveBeenCalledTimes(1);
    act(() => { root!.unmount(); });
    root = null;
    expect(topEscLayer()).toBeNull();
  });
});

describe('waitForTarget — sahte zamanlayıcı', () => {
  const visible = () => true;

  it('hedef zaten varsa anında bulunur', async () => {
    document.body.innerHTML = '<div data-tour="x"></div>';
    const r = await waitForTarget('[data-tour="x"]', { isVisible: visible });
    expect(r.status).toBe('found');
  });

  it('8 s içinde belirmezse timeout', async () => {
    vi.useFakeTimers();
    const p = waitForTarget('[data-tour="nope"]', { isVisible: visible });
    await vi.advanceTimersByTimeAsync(TARGET_TIMEOUT_MS - 1);
    let settled = false;
    void p.then(() => { settled = true; });
    await Promise.resolve();
    expect(settled).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect((await p).status).toBe('timeout');
  });

  it('geç beliren hedef MutationObserver ile yakalanır', async () => {
    vi.useFakeTimers();
    const p = waitForTarget('[data-tour="late"]', { isVisible: visible });
    const el = document.createElement('div');
    el.setAttribute('data-tour', 'late');
    document.body.appendChild(el);
    await vi.advanceTimersByTimeAsync(0);
    const r = await p;
    expect(r).toMatchObject({ status: 'found', via: 'target' });
  });

  it('görünmez hedef bulunmuş sayılmaz', async () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div data-tour="hidden"></div>';
    const p = waitForTarget('[data-tour="hidden"]', { isVisible: () => false });
    await vi.advanceTimersByTimeAsync(TARGET_TIMEOUT_MS);
    expect((await p).status).toBe('timeout');
  });

  it('asıl hedef yoksa pay sonunda yedeğe düşer', async () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div data-tour="table"></div>';
    const p = waitForTarget('[data-tour="table"] tr', { fallback: '[data-tour="table"]', isVisible: visible });
    await vi.advanceTimersByTimeAsync(FALLBACK_GRACE_MS);
    expect(await p).toMatchObject({ status: 'found', via: 'fallback' });
  });

  it('AbortSignal → aborted', async () => {
    vi.useFakeTimers();
    const ac = new AbortController();
    const p = waitForTarget('[data-tour="nope"]', { signal: ac.signal, isVisible: visible });
    ac.abort();
    expect((await p).status).toBe('aborted');
  });

  it('geçersiz seçici fırlatmaz', async () => {
    vi.useFakeTimers();
    const p = waitForTarget('[[[', { isVisible: visible, timeoutMs: 10 });
    await vi.advanceTimersByTimeAsync(10);
    expect((await p).status).toBe('timeout');
  });
});

describe('useTour — ResizeObserver bekçisi', () => {
  const CTX: TourCtx = {
    service: 'svc', range: null, serviceCount: 3, spans5m: 10, environments: 1, copilotEnabled: false, returnTo: '/inbox',
  };
  let api: UseTour | null = null;
  function Harness() {
    const t = useTour({ onDone: () => {} });
    api = t;
    useEffect(() => { t.start(CTX); }, []); // eslint-disable-line react-hooks/exhaustive-deps -- yalnız mount
    return null;
  }
  function plantTable() {
    const wrap = document.createElement('div');
    wrap.setAttribute('data-tour', 'services-table');
    wrap.innerHTML = '<table><tbody><tr data-row-idx="0"><td>a</td></tr></tbody></table>';
    document.body.appendChild(wrap);
    const tr = wrap.querySelector('tr')!;
    tr.getBoundingClientRect = () => ({ left: 10, top: 20, width: 300, height: 30, right: 310, bottom: 50, x: 10, y: 20, toJSON: () => ({}) });
    return tr;
  }

  it('ResizeObserver tanımsızken (jsdom) hedef bulunur, çökmez', async () => {
    expect(typeof (globalThis as { ResizeObserver?: unknown }).ResizeObserver).toBe('undefined');
    plantTable();
    render(<MemoryRouter initialEntries={['/inbox']}><Harness /></MemoryRouter>);
    expect(api!.state.status).toBe('running');
    act(() => { api!.next(); });
    await act(async () => { await Promise.resolve(); });
    expect(api!.target.status).toBe('found');
    expect(api!.target.rect).toEqual({ left: 10, top: 20, width: 300, height: 30 });
  });

  it('ResizeObserver varsa hedef gözlenir ve sökülürken bağlantı kesilir', async () => {
    const observe = vi.fn(); const disconnect = vi.fn();
    vi.stubGlobal('ResizeObserver', class { observe = observe; disconnect = disconnect; unobserve() {} });
    plantTable();
    render(<MemoryRouter initialEntries={['/inbox']}><Harness /></MemoryRouter>);
    act(() => { api!.next(); });
    await act(async () => { await Promise.resolve(); });
    expect(observe).toHaveBeenCalledTimes(1);
    act(() => { api!.skip(); });
    expect(disconnect).toHaveBeenCalled();
  });
});
