// @vitest-environment jsdom
//
// v0.10.1912 (operatör-raporlu) — Wordmark TEK kök öğe döner: flex / grid
// ebeveynde "Core" ile "metry" ayrı öğe olup araya `gap` girmesin.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { Wordmark } from './Wordmark';

function roots(html: string): Element[] {
  const host = document.createElement('div');
  host.innerHTML = html;
  expect([...host.childNodes].every(n => n.nodeType === Node.ELEMENT_NODE)).toBe(true);
  return [...host.children];
}

describe('Wordmark', () => {
  it('stok ad: tek kök, metin bitişik, "Core" marka renginde', () => {
    const r = roots(renderToStaticMarkup(<Wordmark />));
    expect(r).toHaveLength(1);
    expect(r[0].textContent).toBe('Coremetry');
    expect(r[0].querySelector('span')?.textContent).toBe('Core');
  });

  it('özel ad: tek kök, aynen', () => {
    const r = roots(renderToStaticMarkup(<Wordmark name="Acme APM" />));
    expect(r).toHaveLength(1);
    expect(r[0].textContent).toBe('Acme APM');
  });
});
