// @vitest-environment jsdom
//
// TraceMeasures.render — v0.10.1514 (trace başlığı A). Ölçü kümesi: SÜRE ·
// KRİTİK YOL · SPAN (+servis) · BAŞLANGIÇ bu sırayla <dl> çiftleri; sayı ile
// birim ayrı (birim küçük span), kritik yol yoksa soluk "—", ipuçları eski
// şeridin metinlerini taşır (kritik yol: span sayısı + kök duvar süresi, iç
// içe süreler toplanmaz). Veri yalnız prop'lardan — ek istek yok.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { tsLong } from '@/lib/utils';

vi.mock('@/lib/i18n', async (orig) => {
  const m = await orig<typeof import('@/lib/i18n')>();
  return { ...m, useT: () => (k: string) => m.t(k, 'tr') };
});

import { TraceMeasures, splitValueUnit } from './TraceMeasures';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(p: Partial<Parameters<typeof TraceMeasures>[0]> = {}): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(<TraceMeasures totalNs={24_970_000} criticalNs={5_260_000} criticalSpans={3}
      spanCount={4} svcCount={2} startNs={1_760_000_000_000_000_000} {...p} />);
  });
  return host.querySelector('dl') as HTMLElement;
}
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null; root = null;
});

describe('splitValueUnit', () => {
  const cases: [string, [string, string]][] = [
    ['24.97ms', ['24.97', 'ms']], ['2.00s', ['2.00', 's']], ['350µs', ['350', 'µs']],
    ['12ns', ['12', 'ns']], ['—', ['—', '']], ['4', ['4', '']],
  ];
  for (const [i, want] of cases) it(i, () => expect(splitValueUnit(i)).toEqual(want));
});

describe('TraceMeasures', () => {
  it('dört ölçü bu sırayla, TR etiketler; sayı + küçük birim', () => {
    const dl = mount();
    expect(dl.getAttribute('aria-label')).toBe('Trace ölçüleri');
    expect([...dl.querySelectorAll('dt')].map(d => d.textContent)).toEqual(['SÜRE', 'KRİTİK YOL', 'SPAN', 'BAŞLANGIÇ']);
    const dd = [...dl.querySelectorAll('dd')] as HTMLElement[];
    expect(dd.map(d => d.textContent)).toEqual(['25 ms', '5.26 ms', // v0.10.2484 — 3 anlamlı hane (lib/format/duration)
 '4 · 2 servis', tsLong(1_760_000_000_000_000_000)]);
    expect(dd[0].className).toBe('trace-summary__dur');
    expect(dd[0].querySelector('.trace-measures__unit')!.textContent).toBe(' ms');
    expect(dd[0].title).toBe('Trace toplam süresi: ilk span başlangıcından son span bitişine');
    expect(dd[1].title).toBe('Kritik yol: kökten yaprağa 3 span; kök span duvar süresi 5.26 ms. İç içe span süreleri toplanmaz. Odaklamak için alttaki "Critical path focus" düğmesi.');
    expect(dd[3].title).toBe('Trace başlangıcı (kök span)');
  });

  it('kritik yol yoksa soluk "—"; başlangıç yoksa "—"', () => {
    const dl = mount({ criticalNs: null, criticalSpans: 0, startNs: undefined });
    const dd = [...dl.querySelectorAll('dd')] as HTMLElement[];
    expect(dd[1].textContent).toBe('—');
    expect(dd[1].className).toBe('trace-measures__none');
    expect(dd[3].textContent).toBe('—');
  });

  it('tek servis tekil ad', () => {
    const dl = mount({ svcCount: 1, spanCount: 1 });
    expect(dl.querySelectorAll('dd')[2].textContent).toBe('1 · 1 servis');
  });
});
