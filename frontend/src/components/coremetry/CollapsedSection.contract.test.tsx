// @vitest-environment jsdom
//
// CollapsedSection.contract.test.tsx — v0.10.2132 (servis sekmeleri S1,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
//
// NE ÇİVİLİYOR: kapalı bölümün gövdesi MONTE EDİLMEZ (okuma da başlamaz —
// planın "kapalı bölüm verisini açılana kadar çekmez" kuralı bu bileşenin
// sözleşmesine bağlı); kapalı satırda başlık + özet, açıkken rozet/meta/eylem
// + gövde + "kaynak" satırı; tetik düğmesi onOpenChange'i çağırır; id başlık
// satırında (derin link kapalıyken de hedef bulur).
import { describe, it, expect, afterEach } from 'vitest';
import { act, useEffect, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { CollapsedSection } from './CollapsedSection';
import { CollapsedSection as Barrel } from './index';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const mounts: string[] = [];

function Body() {
  useEffect(() => { mounts.push('body'); }, []);
  return <div data-testid="body">gövde</div>;
}

function Harness({ initial }: { initial: boolean }) {
  const [open, setOpen] = useState(initial);
  return (
    <CollapsedSection id="dtl-x" title="Bölüm" summary="tek cümle özet" source="spans · x"
      badges={<span data-testid="badge">rozet</span>} meta="ilk 8" actions={<a href="#y">Tümü</a>}
      open={open} onOpenChange={setOpen}>
      <Body />
    </CollapsedSection>
  );
}

function render(initial: boolean): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<Harness initial={initial} />); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null; mounts.length = 0;
});

describe('CollapsedSection', () => {
  it('barrel aynı bileşeni verir', () => { expect(Barrel).toBe(CollapsedSection); });
  it('kapalı: gövde monte edilmez, başlık + özet görünür, rozet/kaynak yok', () => {
    const el = render(false);
    expect(mounts).toEqual([]);
    expect(el.querySelector('[data-testid="body"]')).toBeNull();
    expect(el.querySelector('#dtl-x')).not.toBeNull();
    expect(el.textContent).toContain('Bölüm');
    expect(el.querySelector('.csec-summary')?.textContent).toBe('tek cümle özet');
    expect(el.querySelector('[data-testid="badge"]')).toBeNull();
    expect(el.querySelector('.csec-source')).toBeNull();
    expect(el.querySelector('button')?.getAttribute('aria-expanded')).toBe('false');
  });
  it('tetik açar: gövde monte, rozet/meta/eylem, özet gizli, kaynak satırı', () => {
    const el = render(false);
    act(() => { el.querySelector('button')!.click(); });
    expect(mounts).toEqual(['body']);
    expect(el.querySelector('[data-testid="body"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="badge"]')).not.toBeNull();
    expect(el.textContent).toContain('ilk 8');
    expect(el.textContent).toContain('Tümü');
    expect(el.querySelector('.csec-summary')).toBeNull();
    expect(el.querySelector('.csec-source')?.textContent).toBe('kaynak: spans · x');
    expect(el.querySelector('button')?.getAttribute('aria-expanded')).toBe('true');
  });
  it('açık → kapat: gövde söküldü', () => {
    const el = render(true);
    act(() => { el.querySelector('button')!.click(); });
    expect(el.querySelector('[data-testid="body"]')).toBeNull();
  });
});
