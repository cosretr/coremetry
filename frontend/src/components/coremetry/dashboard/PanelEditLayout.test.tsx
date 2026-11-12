// @vitest-environment jsdom
//
// v0.10.2004 (Dashboards Faz 3 dilim 3.7) — panel düzenleyici kabuğu:
// açılışta odak başlıkta, Esc = Vazgeç, Tamam / Sil çağrıları; eski 520 px
// sabit kutu (position:fixed) PanelEditor'de kalmadı.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { PanelEditLayout } from './PanelEditLayout';
import { topEscLayer } from '@/lib/escLayer';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
afterEach(() => { if (root) act(() => root!.unmount()); root = null; document.body.innerHTML = ''; });

function mount(h: { onCancel(): void; onDone(): void; onDelete(): void; onTitle(v: string): void }) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host);
    root.render(<PanelEditLayout title="İstek hızı" dashboardName="Service RED" preview={<i data-preview="" />}
      options={<i data-options="" />} {...h} />);
  });
  return host;
}

describe('PanelEditLayout', () => {
  it('odak başlıkta; önizleme ve seçenekler yan yana bölgelerde', () => {
    const h = { onCancel: vi.fn(), onDone: vi.fn(), onDelete: vi.fn(), onTitle: vi.fn() };
    const el = mount(h);
    expect(document.activeElement).toBe(el.querySelector('input[aria-label="Panel başlığı"]'));
    expect(el.querySelector('[aria-label="Önizleme"] [data-preview]')).not.toBeNull();
    expect(el.querySelector('[aria-label="Seçenekler"] [data-options]')).not.toBeNull();
  });

  it('Esc = Vazgeç; Tamam ve Sil kendi çağrıları', () => {
    const h = { onCancel: vi.fn(), onDone: vi.fn(), onDelete: vi.fn(), onTitle: vi.fn() };
    const el = mount(h);
    // Global Esc dinleyicisi kabukta; katman yığınının tepesi bu düzenleyici.
    act(() => { topEscLayer()?.(); });
    expect(h.onCancel).toHaveBeenCalledTimes(1);
    const btn = (t: string) => [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === t)!;
    act(() => btn('Tamam').click());
    act(() => btn('Paneli sil').click());
    expect(h.onDone).toHaveBeenCalledTimes(1);
    expect(h.onDelete).toHaveBeenCalledTimes(1);
  });

  it('PanelEditor eski sabit kutuyu taşımaz', () => {
    const src = readFileSync(resolve(__dirname, '../../dashboard/PanelEditor.tsx'), 'utf8').replace(/^\s*\/\/.*$/gm, '');
    expect(src).not.toMatch(/position:\s*'fixed'/);
    expect(src).toContain('<PanelEditLayout');
  });
});
