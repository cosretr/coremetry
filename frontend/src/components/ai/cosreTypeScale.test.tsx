// @vitest-environment jsdom
//
// v0.10.1142 (operatör: "/cosre sol geçmiş yazısı çok küçük; gerçek bir sohbet
// asistanı gibi tipografi tutarlı olsun") — CoSRE tip ölçeği sözleşmesi:
//  1. .cosre-root tip ölçeği değişkenlerini (--cosre-fs-*, satır yüksekliği,
//     ağırlık, harf aralığı, 4/8 boşluk) tanımlıyor; sayfa kipi gövdeyi 15px'e
//     çıkarıyor, çekmece 14px.
//  2. Kenar çubuğu kuralları bu değişkenleri okuyor (satır 14px, grup başlığı
//     12px meta + yapışkan, satır yüksekliği --cosre-row-h).
//  3. CopilotChat sayfa + çekmece kökleri .cosre-root taşıyor; kenar çubuğu
//     satır başlığını ellipsis span'ında + title ipucuyla çiziyor.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, describe, expect, it } from 'vitest';
import { CosreSidebar } from './CosreSidebar';
import type { AiConversationSummary } from '@/lib/types';

const CSS = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '');
const CHAT = readFileSync(resolve(__dirname, '../CopilotChat.tsx'), 'utf8');

/** İlk `selector {` bloğunun gövdesi (yorumlar ayıklanmış). */
function block(selector: string): string {
  const i = CSS.indexOf(`${selector} {`);
  expect(i, `${selector} kuralı yok`).toBeGreaterThanOrEqual(0);
  return CSS.slice(i, CSS.indexOf('}', i));
}

describe('CoSRE tip ölçeği — CSS', () => {
  it('.cosre-root ölçeği tanımlıyor', () => {
    const b = block('.cosre-root, .cosre-head');
    for (const v of [
      '--cosre-fs-body', '--cosre-fs-ui', '--cosre-fs-small', '--cosre-fs-meta',
      '--cosre-fs-h1', '--cosre-fs-h2', '--cosre-fs-h3', '--cosre-fs-h4',
      '--cosre-lh-body', '--cosre-lh-heading', '--cosre-fw-semibold', '--cosre-ls-meta',
      '--cosre-font-mono', '--cosre-s1', '--cosre-s2', '--cosre-row-h',
    ]) expect(b, v).toContain(`${v}:`);
    expect(b).toMatch(/--cosre-fs-body:\s*14px/);
    expect(b).toMatch(/--cosre-fs-ui:\s*14px/);
    expect(b).toMatch(/--cosre-fs-small:\s*13px/);
    expect(b).toMatch(/--cosre-fs-meta:\s*12px/);
    expect(b).toMatch(/--cosre-lh-body:\s*1\.6/);
    // yeni yazı tipi yok: mono evin yığınından
    expect(b).toMatch(/--cosre-font-mono:\s*var\(--font-mono\)/);
  });

  it('sayfa kipi gövdeyi 15px yapıyor', () => {
    expect(block('.cosre-root--page, .cosre-root--page .cosre-head')).toMatch(/--cosre-fs-body:\s*15px/);
  });

  it('kenar çubuğu ölçeği okuyor (satır 14px, grup başlığı 12px + yapışkan)', () => {
    const open = block('.cosre-side .cosre-side__open');
    expect(open).toContain('font-size: var(--cosre-fs-ui)');
    expect(open).toContain('min-height: var(--cosre-row-h)');
    const gh = block('.cosre-side__gh');
    expect(gh).toContain('font-size: var(--cosre-fs-meta)');
    expect(gh).toContain('position: sticky');
    expect(block('.cosre-side__search .sf-input')).toContain('font-size: var(--cosre-fs-ui)');
    expect(block('.cosre-side .cosre-side__new')).toContain('font-size: var(--cosre-fs-ui)');
    expect(block('.cosre-side__title')).toContain('text-overflow: ellipsis');
  });

  it('composer gövde boyutunda, kod 13px mono', () => {
    expect(block('.cosre-root .cm-composer__input')).toContain('font-size: var(--cosre-fs-body)');
    expect(block('.cosre-root .cm-md-code pre')).toContain('var(--cosre-fs-small)/1.55 var(--cosre-font-mono)');
  });
});

describe('CoSRE tip ölçeği — kökler', () => {
  it('CopilotChat sayfa + çekmece gövdesi .cosre-root taşıyor', () => {
    expect(CHAT).toContain('className="cosre-page cosre-root cosre-root--page"');
    expect(CHAT).toContain('className="cosre-root cosre-root--drawer"');
    expect(CHAT).toContain('className="cosre-head"');
  });

  let root: Root | null = null;
  let host: HTMLDivElement | null = null;
  afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

  it('kenar çubuğu: grup başlığı + ellipsis başlık + tam başlık ipucu + etkin satır', () => {
    const NOW = new Date(2026, 9, 9, 15, 0, 0).getTime();
    const long = 'Çok uzun bir konuşma başlığı '.repeat(4).trim();
    const threads: AiConversationSummary[] = [
      { id: 'a', title: long, updatedAt: (NOW - 3_600_000) * 1e6, messages: 2 },
      { id: 'b', title: 'ikinci', updatedAt: (NOW - 7_200_000) * 1e6, messages: 4 },
    ];
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const noop = () => {};
    act(() => root!.render(
      <CosreSidebar threads={threads} activeId="a" collapsed={false} mobileOpen={false}
        onNew={noop} onOpen={noop} onDelete={noop} onCloseMobile={noop} nowMs={NOW} />,
    ));
    expect(host.querySelector('nav.cosre-side')).toBeTruthy();
    expect(host.querySelectorAll('.cosre-side__gh').length).toBeGreaterThan(0);
    const titles = host.querySelectorAll('.cosre-side__open .cosre-side__title');
    expect(titles.length).toBe(2);
    const first = host.querySelector<HTMLButtonElement>('.cosre-side__item.is-active .cosre-side__open')!;
    expect(first.getAttribute('title')).toBe(long);
    expect(first.getAttribute('aria-current')).toBe('true');
  });
});
