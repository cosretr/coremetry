// @vitest-environment jsdom
//
// OptionRow / TileButton / StatTile onClick — v0.10.927 (buton bütünlüğü,
// Faz 2 artığı). Üç atomun DOM sözleşmesi; görünüm globals.css'te
// (primitiveClasses kapısı sınıfların tanımlı olduğunu ayrıca yoklar).
import { describe, it, expect, afterEach } from 'vitest';
import { act, createRef, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { OptionRow } from './OptionRow';
import { TileButton } from './TileButton';
import { StatTile } from './StatTile';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });
const btn = (el: HTMLElement) => el.querySelector('button')!;

describe('OptionRow', () => {
  it('çocuklar sarmalayıcısız; type=button; seçili → is-sel + aria-current', () => {
    const ref = createRef<HTMLButtonElement>();
    const el = render(
      <OptionRow ref={ref} selected className="stmt-pick-row">
        <span className="a">ad</span><span className="b">birim</span>
      </OptionRow>);
    const b = btn(el);
    expect(ref.current).toBe(b);
    expect(b.type).toBe('button');
    expect(b.className).toBe('opt-row is-sel stmt-pick-row');
    expect(b.getAttribute('aria-current')).toBe('true');
    expect([...b.children].map(c => c.className)).toEqual(['a', 'b']);
  });
  it('seçili değil → aria-current yok; tık iletilir', () => {
    let n = 0;
    const el = render(<OptionRow onClick={() => { n++; }}>x</OptionRow>);
    expect(btn(el).hasAttribute('aria-current')).toBe(false);
    expect(btn(el).className).toBe('opt-row');
    act(() => { btn(el).click(); });
    expect(n).toBe(1);
  });
});

describe('TileButton', () => {
  it('tile-btn + type=button; çocuklar doğrudan; title/disabled geçer', () => {
    const el = render(<TileButton title="Grafiği aç" disabled><span>etiket</span></TileButton>);
    const b = btn(el);
    expect(b.className).toBe('tile-btn');
    expect(b.type).toBe('button');
    expect(b.title).toBe('Grafiği aç');
    expect(b.disabled).toBe(true);
    expect(b.firstElementChild!.tagName).toBe('SPAN');
  });
});

describe('StatTile onClick', () => {
  it('onClick yok → div kök, düğme yok (eski davranış)', () => {
    const el = render(<StatTile label="Pods">3</StatTile>);
    expect(el.querySelector('button')).toBeNull();
    expect(el.firstElementChild!.tagName).toBe('DIV');
  });
  it('onClick → gerçek düğme; parçalar span (düğme içinde blok yok); tık çalışır', () => {
    let n = 0;
    const el = render(<StatTile label="CPU" title="CPU grafiğine git" onClick={() => { n++; }}>42%</StatTile>);
    const b = btn(el);
    expect(b.className).toBe('stat-tile-btn');
    expect(b.type).toBe('button');
    expect(b.title).toBe('CPU grafiğine git');
    expect(b.querySelector('div')).toBeNull();
    expect(b.textContent).toBe('CPU42%');
    act(() => { b.click(); });
    expect(n).toBe(1);
  });
});
