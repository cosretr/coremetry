// @vitest-environment jsdom
//
// SegmentedControl.contract.test.tsx — v0.10.914 (buton bütünlüğü dilim 1).
// Sözleşme: görünüm sınıfları mevcut `.segmented` kalıbıyla AYNI (görsel
// regresyon yok), seçili düğme `active` + aria-checked, radyo grubu, tek Tab
// durağı, ←/→/Home/End seçer ve odağı taşır, disabled atlanır.
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { SegmentedControl } from './SegmentedControl';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });

function Harness({ initial = 'b', size, activation }: { initial?: string; size?: 'sm' | 'md'; activation?: 'auto' | 'manual' }) {
  const [v, setV] = useState(initial);
  return (
    <>
      <SegmentedControl aria-label="Görünüm" value={v} onChange={setV} size={size} activation={activation} options={[
        { value: 'a', label: 'A' }, { value: 'b', label: 'B' },
        { value: 'c', label: 'C', disabled: true }, { value: 'd', label: 'D', title: 'dee' },
      ]} />
      <output>{v}</output>
    </>
  );
}
function render(size?: 'sm' | 'md', props: { initial?: string; activation?: 'auto' | 'manual' } = {}) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<Harness size={size} {...props} />); });
  return host;
}
const btns = (el: HTMLElement) => [...el.querySelectorAll('button')] as HTMLButtonElement[];
const key = (b: HTMLButtonElement, k: string) => act(() => { b.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });

describe('SegmentedControl', () => {
  it('mevcut .segmented görünümü + radyo grubu', () => {
    const el = render();
    const g = el.querySelector('[role="radiogroup"]')!;
    expect(g.className).toBe('segmented');
    expect(g.getAttribute('aria-label')).toBe('Görünüm');
    const b = btns(el);
    expect(b.map(x => x.getAttribute('role'))).toEqual(['radio', 'radio', 'radio', 'radio']);
    expect(b[1].className).toBe('active');
    expect(b[1].getAttribute('aria-checked')).toBe('true');
    expect(b[0].getAttribute('aria-checked')).toBe('false');
    expect(b.map(x => x.tabIndex)).toEqual([-1, 0, -1, -1]); // tek Tab durağı
    expect(b[3].title).toBe('dee');
    expect(b.every(x => x.type === 'button')).toBe(true);
  });
  it('sm → .sg-sm yoğun rung', () => {
    expect(render('sm').querySelector('[role="radiogroup"]')!.className).toBe('segmented sg-sm');
  });
  it('tıklama ve klavye: → disabled\'ı atlar, ← sarar, Home/End', () => {
    const el = render();
    const out = () => el.querySelector('output')!.textContent;
    act(() => { btns(el)[0].click(); });
    expect(out()).toBe('a');
    key(btns(el)[0], 'ArrowRight');
    expect(out()).toBe('b');
    key(btns(el)[1], 'ArrowRight');
    expect(out()).toBe('d'); // c disabled atlandı
    expect(document.activeElement).toBe(btns(el)[3]);
    key(btns(el)[3], 'ArrowRight');
    expect(out()).toBe('a'); // sarar
    key(btns(el)[0], 'End');
    expect(out()).toBe('d');
    key(btns(el)[3], 'Home');
    expect(out()).toBe('a');
    key(btns(el)[0], 'ArrowLeft');
    expect(out()).toBe('d');
  });

  // v0.10.924 — inceleme bulguları: kaydeden gruplarda oklar seçim YAPMAZ;
  // seçili seçenek devre dışıysa Tab durağı ilk etkin seçeneğe kayar.
  it("activation='manual': ok odağı taşır, seçimi değiştirmez; Enter/tık seçer", () => {
    const el = render(undefined, { activation: 'manual' });
    const out = () => el.querySelector('output')!.textContent;
    key(btns(el)[1], 'ArrowRight');
    expect(out()).toBe('b');
    expect(document.activeElement).toBe(btns(el)[3]);
    act(() => { btns(el)[3].click(); });
    expect(out()).toBe('d');
  });
  it('seçili seçenek disabled → Tab durağı ilk etkin seçenek', () => {
    const el = render(undefined, { initial: 'c' });
    expect(btns(el).map(x => x.tabIndex)).toEqual([0, -1, -1, -1]);
  });
});
