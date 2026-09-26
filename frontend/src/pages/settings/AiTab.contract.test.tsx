// @vitest-environment jsdom
// AiTab.contract.test.tsx — v0.10.940 (Settings › CoSRE sekmeleri).
// Sözleşme: sekme şeridi ayar-okuma kapısının ÖNÜNDE — GET /api/settings/ai
// başarısızken ilk sekme SettingsLoadError çizer ama şerit yerinde ve
// Değerlendirme sekmesi açılır (panel kendi uçlarına dayanıyor). Sekme URL'de
// (?tab=eval, replace); ilk sekmeye dönüş tab + koşu/vaka/kıyas
// parametrelerini siler, yabancı parametreye dokunmaz.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom';

const api = vi.hoisted(() => ({
  getAISettings: vi.fn(async () => { throw new Error('HTTP 500: settings store down'); }),
  aiEvalsetCatalog: vi.fn(async (..._a: unknown[]) => ({ ready: true, appVersion: 'v0.10.940', promptVersion: 'p', total: 0, surfaces: [] })),
  aiEvalsetRuns: vi.fn(async (..._a: unknown[]) => ({ runs: [] })),
  aiEvalsetRun: vi.fn(async (..._a: unknown[]) => ({})),
  aiEvalsetCompare: vi.fn(async (..._a: unknown[]) => ({})),
  aiEvalsetStartRun: vi.fn(async (..._a: unknown[]) => ({})),
  aiEvalsetCancelRun: vi.fn(async (..._a: unknown[]) => ({ ok: true })),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AITab } from './AiTab';
import { ConfirmProvider } from '@/components/ui';

let host: HTMLDivElement | null = null; let root: Root | null = null;
let where = ''; let navType = '';
function Where() { const l = useLocation(); where = l.pathname + l.search; navType = useNavigationType(); return null; }
function render(entry: string): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(
      <MemoryRouter initialEntries={[entry]}>
        <QueryClientProvider client={qc}><ConfirmProvider><AITab /></ConfirmProvider></QueryClientProvider>
        <Where />
      </MemoryRouter>,
    );
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  try { localStorage.clear(); } catch { /* jsdom */ }
});
const tick = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
};
const tab = (el: HTMLElement, label: string) =>
  Array.from(el.querySelectorAll('[role="tab"]')).find(t => t.textContent === label) as HTMLButtonElement;

describe('AITab — sekmeler', () => {
  it('ayar okuması düşse de şerit çizili; Değerlendirme açılır (replace)', async () => {
    const el = render('/settings/ai');
    await tick();
    expect(el.textContent).toContain('Ayarlar okunamadı');
    expect(el.querySelector('[role="tablist"]')).not.toBeNull();
    expect(tab(el, 'Sağlayıcı ve profiller').getAttribute('aria-selected')).toBe('true');
    act(() => { tab(el, 'Değerlendirme').click(); });
    await tick();
    expect(where).toBe('/settings/ai?tab=eval');
    expect(navType).toBe('REPLACE');
    expect(tab(el, 'Değerlendirme').getAttribute('aria-selected')).toBe('true');
    expect(el.textContent).toContain('Değerlendirme (evalset)');
    expect(el.textContent).not.toContain('Ayarlar okunamadı');
  });

  it('ilk sekmeye dönüş tab + run/case/cmp siler, yabancı parametre kalır', async () => {
    const el = render('/settings/ai?tab=eval&run=ev-1&case=c1&cmp=ev-0&keep=1');
    await tick();
    act(() => { tab(el, 'Sağlayıcı ve profiller').click(); });
    await tick();
    expect(where).toBe('/settings/ai?keep=1');
  });
});
