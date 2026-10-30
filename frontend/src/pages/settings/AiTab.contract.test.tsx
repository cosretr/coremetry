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
  cosreRules: vi.fn(async (..._a: unknown[]) => ({ rules: [], rev: 0, limits: { maxRules: 200, maxPerScope: 20, minText: 8, maxText: 200 } })),
  aiToolsets: vi.fn(async (..._a: unknown[]) => ({ toolsets: [], disabled: [], rev: 0 })), // v0.10.1434
}));
vi.mock('@/lib/api', () => ({ api }));

import { AITab } from './AiTab';
import { ConfirmGate } from '@/components/coremetry/Confirm';
// Kurallar sekmesi (components/ai/CosreRulesPanel, bu dilimin dışında) hâlâ ui/useConfirm'de:
// iki kapı App.tsx'teki gibi yan yana.
import { ConfirmProvider } from '@/components/ui';
import '@/test/radixJsdom';

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
        <QueryClientProvider client={qc}><ConfirmProvider><ConfirmGate><AITab /></ConfirmGate></ConfirmProvider></QueryClientProvider>
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
// R11: shadcn/Radix Tabs sekmeyi `mousedown`da (sol tuş) seçer — `.click()` jsdom'da
// mousedown üretmez; kullanıcının tıklamasıyla aynı olay sırası.
const select = (t: HTMLElement) => {
  t.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 }));
  t.click();
};

describe('AITab — sekmeler', () => {
  // v0.10.1434 — dördüncü sekme Kaynak araç setleri (?tab=toolsets); ayar okuması düşse de açılır.
  it('Kaynak araç setleri sekmesi ?tab=toolsets ile açılır, panel kendi ucuna dayanır', async () => {
    const el = render('/settings/ai?tab=toolsets');
    await tick();
    const ts = Array.from(el.querySelectorAll('[role="tab"]')).find(t => /Source toolsets|Kaynak araç setleri/.test(t.textContent ?? '')) as HTMLButtonElement;
    expect(ts.getAttribute('aria-selected')).toBe('true');
    expect(el.querySelector('[data-testid="ai-toolsets-panel"]')).not.toBeNull();
    expect(api.aiToolsets).toHaveBeenCalled();
    expect(el.textContent).not.toContain('Ayarlar okunamadı');
  });

  it('ayar okuması düşse de şerit çizili; Değerlendirme açılır (replace)', async () => {
    const el = render('/settings/ai');
    await tick();
    expect(el.textContent).toContain('Ayarlar okunamadı');
    expect(el.querySelector('[role="tablist"]')).not.toBeNull();
    expect(tab(el, 'Sağlayıcı ve profiller').getAttribute('aria-selected')).toBe('true');
    act(() => { select(tab(el, 'Değerlendirme')); });
    await tick();
    expect(where).toBe('/settings/ai?tab=eval');
    expect(navType).toBe('REPLACE');
    expect(tab(el, 'Değerlendirme').getAttribute('aria-selected')).toBe('true');
    expect(el.textContent).toContain('Değerlendirme (evalset)');
    expect(el.textContent).not.toContain('Ayarlar okunamadı');
  });

  // v0.10.1289 — üçüncü sekme Kurallar (?tab=rules); ayar okuması düşse de açılır.
  it('Kurallar sekmesi ?tab=rules ile açılır, panel kendi ucuna dayanır', async () => {
    const el = render('/settings/ai?tab=rules&run=ev-1');
    await tick();
    expect(tab(el, 'Kurallar').getAttribute('aria-selected')).toBe('true');
    expect(el.querySelector('[data-testid="cosre-rules-panel"]')).not.toBeNull();
    expect(el.textContent).toContain('Henüz kural yok');
    act(() => { select(tab(el, 'Değerlendirme')); });
    await tick();
    expect(where).toBe('/settings/ai?tab=eval&run=ev-1');
    act(() => { select(tab(el, 'Kurallar')); });
    await tick();
    expect(where).toBe('/settings/ai?tab=rules');
  });

  it('ilk sekmeye dönüş tab + run/case/cmp siler, yabancı parametre kalır', async () => {
    const el = render('/settings/ai?tab=eval&run=ev-1&case=c1&cmp=ev-0&keep=1');
    await tick();
    act(() => { select(tab(el, 'Sağlayıcı ve profiller')); });
    await tick();
    expect(where).toBe('/settings/ai?keep=1');
  });
});
