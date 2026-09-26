// @vitest-environment jsdom
//
// Card.contract.test.tsx — v0.10.928 (Y1, kartlar statik).
//
// `.card` artık statik; tıklanabilir kart YALNIZ `CardLink` — gerçek bir
// `<a class="card card-link">`. Bu dosya iki sözleşmeyi tutar:
//   1. CardLink bir link basar (Tab/Enter/⌘-tık tarayıcıdan) ve Card ile
//      AYNI başlık/altlık yuvalarını geçirir.
//   2. Card `card-tight` BASMAZ (seçenek A: o sınıf hiç uygulanmamıştı;
//      yeniden basmak 11 siteyi sessizce bg2/10px'e çevirirdi) ve etkisiz

import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { Card, CardLink } from './Card';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter initialEntries={['/clusters?env=prod']}>{node}</MemoryRouter>); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('CardLink sözleşmesi', () => {
  it('<a class="card card-link"> basar, hedef href\'e yazılır', () => {
    const el = render(<CardLink to={{ search: '?cluster=a' }}>gövde</CardLink>);
    const a = el.querySelector('a')!;
    expect(a).not.toBeNull();
    expect(a.className).toBe('card card-link');
    expect(a.getAttribute('href')).toBe('/clusters?cluster=a');
    expect(a.textContent).toBe('gövde');
  });

  it('çağıranın className\'i eklenir, taban korunur', () => {
    const el = render(<CardLink to="/x" className="extra">g</CardLink>);
    expect(el.querySelector('a')!.className).toBe('card card-link extra');
  });

  it('header/footer yuvaları Card ile aynı işaretleme', () => {
    const link = render(<CardLink to="/x" header="Başlık" footer="Alt">g</CardLink>);
    const a = link.querySelector('a')!;
    const linkKids = Array.from(a.children) as HTMLElement[];
    expect(linkKids[0].textContent).toBe('Başlık');
    expect(linkKids[linkKids.length - 1].textContent).toBe('Alt');
    const linkHead = linkKids[0].getAttribute('style');
    const linkFoot = linkKids[linkKids.length - 1].getAttribute('style');
    act(() => { root?.unmount(); });
    host?.remove();

    const card = render(<Card header="Başlık" footer="Alt">g</Card>);
    const div = card.querySelector('.card')!;
    const cardKids = Array.from(div.children) as HTMLElement[];
    expect(cardKids[0].getAttribute('style')).toBe(linkHead);
    expect(cardKids[cardKids.length - 1].getAttribute('style')).toBe(linkFoot);
    // v0.10.928 (Y2) — iç ayraç --divider, dış çerçeve değil.
    expect(linkHead).toContain('var(--divider)');
    expect(linkFoot).toContain('var(--divider)');
  });
});

describe('Card — statik taban', () => {
  it('yalnız `card` sınıfı; tıklanabilirlik yok', () => {
    const el = render(<Card>g</Card>);
    const div = el.querySelector('div.card')!;
    expect(div.className).toBe('card');
    expect(el.querySelector('a')).toBeNull();
  });
});
