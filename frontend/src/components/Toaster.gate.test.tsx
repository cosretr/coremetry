// @vitest-environment jsdom
//
// Toaster kapısı — v0.10.1619 (shadcn Faz 1 R10, plan §1.4 A). İlk toast'a
// kadar sonner yüklenmez ve DOM boş; ilk toast sıraya alınır, çizici
// mount olunca yeniden oynatılır; sonrakiler doğrudan. lib/toast API'si aynı.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Toaster } from './Toaster';
import { toast } from '@/lib/toast';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  document.body.innerHTML = '';
});
// Tembel modülün ilk dönüşümü vitest'te yavaş olabilir: koşula kadar bekle.
async function until(pred: () => boolean) {
  for (let i = 0; i < 200 && !pred(); i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

describe('Toaster kapısı', () => {
  it('kabuk dosyası sonner içe aktarmaz; çiziciyi tembel import eder', () => {
    const s = readFileSync(resolve(__dirname, 'Toaster.tsx'), 'utf8');
    expect(s).not.toMatch(/from ['"]sonner['"]/);
    expect(s).toContain("lazy(() => import('./coremetry/Toaster'))");
  });

  it('ilk toast sıradan yeniden oynatılır, sonraki doğrudan çizilir', async () => {
    (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<Toaster />); });
    expect(document.body.querySelector('[data-sonner-toaster]')).toBeNull();
    await act(async () => { toast.success('ilk girdi'); });
    await until(() => !!document.body.textContent?.includes('ilk girdi'));
    expect(document.body.querySelector('[data-sonner-toaster]')).not.toBeNull();
    expect(document.body.textContent).toContain('ilk girdi');
    await act(async () => { toast.error('ikinci girdi'); });
    await until(() => !!document.body.textContent?.includes('ikinci girdi'));
    expect(document.body.textContent).toContain('ikinci girdi');
    // Aynı girdi iki kez çizilmez (kapı canlıyken sıraya almaz).
    expect(document.body.querySelectorAll('[data-sonner-toast]')).toHaveLength(2);
  });
});
