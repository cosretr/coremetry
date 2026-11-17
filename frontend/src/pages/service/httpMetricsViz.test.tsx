// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { HttpDonut } from './httpMetricsViz';
import { donutSlices } from '@/lib/httpMetrics';
// v0.10.2305 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));

// v0.10.1258 — operatör: "pasta dilimleri tıklanamıyor, tooltip yok".
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

function mount(onSelect?: (l: string) => void) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(
    <HttpDonut slices={[{ label: '/a', value: 75, pct: 75 }, { label: '/b', value: 25, pct: 25 }]} fmt={v => String(v)} onSelect={onSelect} />,
  ));
  return host;
}

describe('HttpDonut', () => {
  it('dilim üzerine gelince ad + yüzde görünür; dilimde başlık (tooltip) var', () => {
    const h = mount();
    const slices = h.querySelectorAll('circle');
    expect(slices[1].querySelector('title')?.textContent).toBe('/b: 25 (%25)');
    act(() => { slices[1].dispatchEvent(new MouseEvent('mouseover', { bubbles: true })); });
    expect(h.textContent).toContain('%25');
  });
  it('dilim ve lejant tıkı onSelect çağırır', () => {
    const onSelect = vi.fn();
    const h = mount(onSelect);
    act(() => { h.querySelectorAll('circle')[0].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    act(() => { h.querySelectorAll('button')[1].click(); });
    expect(onSelect.mock.calls.map(c => c[0])).toEqual(['/a', '/b']);
  });
});

// v0.10.1325 — operatör (prod ekran görüntüsü, Status Code Dağılımı):
// 200 · 40,5 Mn · %99; 400 · 134,3 B · %0; 401 · 57,5 B · %0; … 503 · 1 · %0.
// "%0.01 olarak da dağılım gösterebilir, burada 0 yazıyor çoğu."
describe('HttpDonut lejant payı (v0.10.1325)', () => {
  it('200 dışı kodlar %0 değil, uyarlanır hassasiyetle görünür', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const rows = [
      { label: '200', value: 40_500_000 },
      { label: '400', value: 134_300 },
      { label: '401', value: 57_500 },
      { label: '404', value: 4_100 },
      { label: '500', value: 12 },
      { label: '503', value: 1 },
    ];
    act(() => root!.render(<HttpDonut slices={donutSlices(rows)} fmt={v => String(v)} />));
    const legend = [...host.querySelectorAll('svg + div > div')].map(d => d.textContent);
    expect(legend).toEqual([
      '20040500000%99,52',
      '400134300%0,33',
      '40157500%0,14',
      '4044100%0,01',
      '50012<%0,01',
      '5031<%0,01',
    ]);
    // Merkez etiketi de aynı biçimde ve asla "%100" değil.
    expect(host.querySelector('svg > text')?.textContent).toBe('%99,52');
    // Dilim başlığı (tooltip) aynı biçimi kullanır.
    expect(host.querySelectorAll('circle')[1].querySelector('title')?.textContent).toBe('400: 134300 (%0,33)');
  });
});
