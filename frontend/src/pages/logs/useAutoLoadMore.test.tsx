// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { useAutoLoadMore, AUTO_LOAD_MAX_ROWS, type AutoLoadArgs } from './useAutoLoadMore';

// v0.10.2238 — Logs otomatik sonraki sayfa: nöbetçi tek istek, gizli sekme,
// tavan ve hata kuralları.
type IOCb = (entries: Array<{ isIntersecting: boolean }>) => void;
let observers: Array<{ cb: IOCb; disconnected: boolean }> = [];
class FakeIO {
  rec: { cb: IOCb; disconnected: boolean };
  constructor(cb: IOCb) { this.rec = { cb, disconnected: false }; observers.push(this.rec); }
  observe() {}
  disconnect() { this.rec.disconnected = true; }
}
const fire = () => act(() => { observers.filter(o => !o.disconnected).forEach(o => o.cb([{ isIntersecting: true }])); });
let hidden = false;

function Harness(p: AutoLoadArgs) {
  const a = useAutoLoadMore(p);
  return (<>
    {a.active && <div ref={a.sentinelRef} data-testid="s" />}
    {a.capped && <button>more</button>}
  </>);
}
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null; let host: HTMLElement;
function render(el: React.ReactElement) {
  host = document.createElement('div'); document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(el));
  return {
    rerender: (e: React.ReactElement) => act(() => root!.render(e)),
    queryByTestId: (id: string) => host.querySelector(`[data-testid="${id}"]`),
    getByText: (t: string) => { const b = [...host.querySelectorAll('button')].find(x => x.textContent === t); if (!b) throw new Error(t); return b; },
  };
}
const base = (over: Partial<AutoLoadArgs> = {}): AutoLoadArgs =>
  ({ enabled: true, busy: false, failed: false, loaded: 200, onLoad: vi.fn(), ...over });

describe('useAutoLoadMore', () => {
  beforeEach(() => {
    observers = []; hidden = false;
    vi.stubGlobal('IntersectionObserver', FakeIO);
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  });
  afterEach(() => { act(() => root?.unmount()); root = null; host?.remove(); vi.unstubAllGlobals(); });

  it('sentinel triggers exactly one fetch until the request settles', () => {
    const p = base();
    const r = render(<Harness {...p} />);
    fire(); fire();
    expect(p.onLoad).toHaveBeenCalledTimes(1);
    r.rerender(<Harness {...p} busy />);
    fire();
    expect(p.onLoad).toHaveBeenCalledTimes(1);
    r.rerender(<Harness {...p} busy={false} loaded={400} />);
    fire();
    expect(p.onLoad).toHaveBeenCalledTimes(2);
  });

  it('does not fetch while the tab is hidden', () => {
    hidden = true;
    const p = base();
    render(<Harness {...p} />);
    fire();
    expect(p.onLoad).not.toHaveBeenCalled();
  });

  it('cap reverts to the button', () => {
    const p = base({ loaded: AUTO_LOAD_MAX_ROWS });
    const r = render(<Harness {...p} />);
    fire();
    expect(p.onLoad).not.toHaveBeenCalled();
    expect(r.queryByTestId('s')).toBeNull();
    expect(r.getByText('more')).toBeTruthy();
  });

  it('an error stops auto-load', () => {
    const p = base({ failed: true });
    const r = render(<Harness {...p} />);
    fire();
    expect(p.onLoad).not.toHaveBeenCalled();
    expect(r.queryByTestId('s')).toBeNull();
  });
});
