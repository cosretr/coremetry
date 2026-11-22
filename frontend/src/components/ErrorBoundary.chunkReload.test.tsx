// @vitest-environment jsdom
// v0.10.2447 — bayat chunk oto-yenilemesi her deploy'da bir kez çalışır
// (eski oturum boyu bayrak ikinci deploy'u kaçırıyordu), 30 sn içinde
// ikinci kez yenilemez (döngü korkuluğu).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { ErrorBoundary } from './ErrorBoundary';

function Boom(): never { throw new Error('Failed to fetch dynamically imported module: https://x/assets/Trace-abc.js'); }

async function renderOnce() {
  const div = document.createElement('div');
  const root = createRoot(div);
  await act(async () => { root.render(<ErrorBoundary><Boom /></ErrorBoundary>); });
  root.unmount();
}

describe('ErrorBoundary chunk reload guard', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); sessionStorage.clear(); });
  it('reloads again after 30 s (second deploy in the same session)', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    const reload = vi.fn();
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, reload } });
    vi.setSystemTime(1_000_000);
    await renderOnce();
    expect(reload).toHaveBeenCalledTimes(1);
    vi.setSystemTime(1_010_000); // 10 s later: loop guard
    await renderOnce();
    expect(reload).toHaveBeenCalledTimes(1);
    vi.setSystemTime(1_000_000 + 3_600_000); // next deploy, an hour later
    await renderOnce();
    expect(reload).toHaveBeenCalledTimes(2);
  });
});
