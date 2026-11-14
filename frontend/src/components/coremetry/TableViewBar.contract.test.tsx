// @vitest-environment jsdom
//
// TableViewBar.contract.test.tsx — v0.10.2141 (servis sekmeleri S3,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
// NE ÇİVİLİYOR: katlanan satır cümlesi + "hepsini göster" / gizle geçişi,
// "Tüm kolonlar" düğmesi aria-pressed ile seçim taşır, kaynak satırı,
// gösterecek bir şey yokken hiçbir şey çizilmez.
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { TableViewBar } from './TableViewBar';
import { TableViewBar as Barrel } from './index';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function Harness({ foldable, cols = true, source }: { foldable: number; cols?: boolean; source?: string }) {
  const [expanded, setExpanded] = useState(false);
  const [all, setAll] = useState(false);
  return (
    <TableViewBar foldable={foldable} expanded={expanded} onExpandedChange={setExpanded}
      allColumns={cols ? all : undefined} onAllColumnsChange={cols ? setAll : undefined} source={source} />
  );
}

function render(node: React.ReactElement): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
const button = (el: HTMLElement, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('TableViewBar', () => {
  it('barrel aynı bileşeni verir', () => expect(Barrel).toBe(TableViewBar));

  it('katlanan satır cümlesi ve "hepsini göster" → gizle geçişi', () => {
    const el = render(<Harness foldable={3} />);
    expect(el.textContent).toContain('3 daha, hepsi normal');
    act(() => { button(el, 'hepsini göster')!.click(); });
    expect(el.textContent).not.toContain('hepsi normal');
    act(() => { button(el, '3 normal satırı gizle')!.click(); });
    expect(el.textContent).toContain('3 daha, hepsi normal');
  });

  it('"Tüm kolonlar" seçimi aria-pressed ile taşır', () => {
    const el = render(<Harness foldable={0} />);
    const b = button(el, 'Tüm kolonlar')!;
    expect(b.getAttribute('aria-pressed')).toBe('false');
    act(() => { b.click(); });
    expect(button(el, 'Tüm kolonlar')!.getAttribute('aria-pressed')).toBe('true');
    expect(el.textContent).not.toContain('hepsi normal');
  });

  it('kaynak satırı', () => {
    const el = render(<Harness foldable={0} cols={false} source="trace'ler" />);
    expect(el.textContent).toBe("kaynak: trace'ler");
    expect(el.querySelector('button')).toBeNull();
  });

  it('gösterecek bir şey yoksa çizilmez', () => {
    expect(render(<Harness foldable={0} cols={false} />).innerHTML).toBe('');
  });
});
