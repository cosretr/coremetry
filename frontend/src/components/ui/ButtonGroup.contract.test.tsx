// @vitest-environment jsdom
//
// ButtonGroup.contract.test.tsx — v0.10.919 (buton bütünlüğü, Seçenek B).
// Sınırlar ui/ButtonGroup.tsx başlığında; burada çivilenen: rol/ad, sınıf,
// boyut devri (açık prop kazanır), her butonun KENDİ Tab durağı olması
// (SegmentedControl ile sınır), CSS'in odak halkasını kırpmaması ve
// bitişik grupta yalnız kenarlı/yıkıcı-olmayan varyant.

import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { ButtonGroup } from './ButtonGroup';
import { Button } from './Button';
import { IconButton } from './IconButton';
import { stripTsComments } from '../../styles/zLayers.test';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const group = (el: HTMLElement) => el.querySelector<HTMLElement>('[role="group"]')!;
const buttons = (el: HTMLElement) => [...el.querySelectorAll('button')];

describe('ButtonGroup — rol ve sınıf', () => {
  it('role="group" + zorunlu aria-label', () => {
    const g = group(render(<ButtonGroup aria-label="Dışa aktar"><Button variant="ghost">CSV</Button></ButtonGroup>));
    expect(g.getAttribute('aria-label')).toBe('Dışa aktar');
    expect(g.className).toBe('btn-group');
  });

  it('attached → is-attached; çağıranın className\'i korunur', () => {
    const g = group(render(
      <ButtonGroup aria-label="x" attached className="cell-actions end">
        <Button variant="secondary">a</Button>
      </ButtonGroup>));
    expect(g.className).toBe('btn-group is-attached cell-actions end');
  });

  it('aria-label tip düzeyinde zorunlu', () => {
    // @ts-expect-error — aria-label eksik
    const node = <ButtonGroup><Button variant="ghost">a</Button></ButtonGroup>;
    expect(node).toBeTruthy();
  });

  it('her buton KENDİ Tab durağı (roving tabindex yok — SegmentedControl ile sınır)', () => {
    const el = render(
      <ButtonGroup aria-label="x">
        <Button variant="ghost">a</Button><Button variant="ghost">b</Button><IconButton aria-label="c" icon="c" />
      </ButtonGroup>);
    for (const b of buttons(el)) expect(b.hasAttribute('tabindex')).toBe(false);
  });
});

describe('ButtonGroup — boyut devri', () => {
  it('grup size="sm" → Button sm, IconButton ib-sm', () => {
    const el = render(
      <ButtonGroup aria-label="x" size="sm">
        <Button variant="ghost">a</Button><IconButton aria-label="i" icon="i" />
      </ButtonGroup>);
    const [b, ib] = buttons(el);
    expect(b.classList.contains('sm')).toBe(true);
    expect(ib.classList.contains('ib-sm')).toBe(true);
  });

  it('grup size="xs" → Button xs, IconButton ib-xs', () => {
    const el = render(
      <ButtonGroup aria-label="x" size="xs">
        <Button variant="ghost">a</Button><IconButton aria-label="i" icon="i" />
      </ButtonGroup>);
    const [b, ib] = buttons(el);
    expect(b.classList.contains('xs')).toBe(true);
    expect(ib.classList.contains('ib-xs')).toBe(true);
  });

  it('çocuğun açık size\'ı kazanır', () => {
    const el = render(
      <ButtonGroup aria-label="x" size="xs">
        <Button variant="ghost" size="lg">a</Button><IconButton aria-label="i" icon="i" size="md" />
      </ButtonGroup>);
    const [b, ib] = buttons(el);
    expect(b.classList.contains('lg')).toBe(true);
    expect(b.classList.contains('xs')).toBe(false);
    expect(ib.classList.contains('ib-md')).toBe(true);
  });

  it('grup dışında varsayılanlar değişmedi (Button md sınıfsız, IconButton ib-sm)', () => {
    const el = render(<><Button variant="ghost">a</Button><IconButton aria-label="i" icon="i" /></>);
    const [b, ib] = buttons(el);
    expect(b.className).toBe('ghost');
    expect(ib.classList.contains('ib-sm')).toBe(true);
  });

  it('size vermeyen grup çocukların varsayılanını ezmez', () => {
    const el = render(
      <ButtonGroup aria-label="x"><Button variant="ghost">a</Button><IconButton aria-label="i" icon="i" /></ButtonGroup>);
    const [b, ib] = buttons(el);
    expect(b.className).toBe('ghost');
    expect(ib.classList.contains('ib-sm')).toBe(true);
  });
});

describe('ButtonGroup — CSS', () => {
  const css = readFileSync(resolve(__dirname, '..', '..', 'styles', 'globals.css'), 'utf8');
  const rules = [...css.matchAll(/\n(\.btn-group[^{]*)\{([^}]*)\}/g)];

  it('sınıflar tanımlı, aralık token\'dan', () => {
    expect(css).toMatch(/\n\.btn-group \{[^}]*gap: var\(--sp-3\)/);
    expect(css).toMatch(/\n\.btn-group\.is-attached \{/);
  });

  it('hiçbir .btn-group kuralı overflow kırpmaz (odak halkası görünür kalır)', () => {
    expect(rules.length).toBeGreaterThan(2);
    for (const [, sel, body] of rules) expect(body, sel).not.toMatch(/overflow/);
  });

  it('bitişik köşeler :first/:last-of-type ile (kardeş ipucu kutusu :last-child\'ı çalmasın)', () => {
    const sels = rules.map(r => r[1]).join('\n');
    expect(sels).toMatch(/:not\(:first-of-type\)/);
    expect(sels).toMatch(/:not\(:last-of-type\)/);
    expect(sels).not.toMatch(/:(first|last)-child/);
  });
});

// Kaynak taraması: bitişik grupta yalnız kenarlı ve yıkıcı-olmayan varyant.
// SINIR: literal `variant="…"` görür; `variant={x ? … : …}` görünmez.
describe('ButtonGroup — bitişik grup varyant kuralı (kaynak taraması)', () => {
  const SRC = resolve(__dirname, '..', '..');
  const files: string[] = [];
  (function walk(dir: string) {
    for (const e of readdirSync(dir)) {
      const p = join(dir, e);
      if (statSync(p).isDirectory()) { if (e !== 'node_modules') walk(p); }
      else if (p.endsWith('.tsx') && !p.includes('.' + 'test' + '.')) files.push(p);
    }
  })(SRC);

  it('attached ButtonGroup içinde primary/danger/ghost/ghost-danger/accent yok', () => {
    const offenders: string[] = [];
    for (const p of files) {
      const src = stripTsComments(readFileSync(p, 'utf8'));
      for (const m of src.matchAll(/<ButtonGroup\b[^>]*\battached\b[^>]*>([\s\S]*?)<\/ButtonGroup>/g)) {
        for (const v of m[1].matchAll(/variant="([\w-]+)"/g)) {
          if (v[1] !== 'secondary') offenders.push(`${p.slice(SRC.length + 1)}: variant="${v[1]}"`);
        }
      }
    }
    expect(offenders, `Bitişik grupta kenarsız ya da yıkıcı varyant:\n${offenders.join('\n')}`).toEqual([]);
  });
});
