// @vitest-environment jsdom
//
// IdentityChip.contract — v0.10.1514 (trace başlığı A, operatör onayı 2026-10-24).
// Kimlik çipinin DOM sözleşmesi: etiket · değer · (ipucu) · kopyala hücreleri bu
// sırayla; bağlantı varsa değer gerçek <a>, yoksa düz metin (+ sr-only not);
// ipucu tonu sınıfla (is-warn), kopyala düğmesinin adı kendi başlığı; kök
// varsayılan listitem ve çağıranın title/data-* niteliklerini taşır. Görünüm
// globals.css trace başlığı bloğunda (pages/traceHeaderMeasures.pin.test.ts).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { IdentityChip } from './IdentityChip';
import { IdentityChip as BarrelIdentityChip } from './index';

const copySpy = vi.fn(async (_v: string) => true);
vi.mock('@/lib/clipboard', () => ({ copyToClipboard: (v: string) => copySpy(v) }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host.firstElementChild as HTMLElement;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  copySpy.mockClear();
});

const cells = (el: HTMLElement) => [...el.children].map(c => c.className);

describe('IdentityChip', () => {
  it('barrel ihracı aynı bileşen', () => {
    expect(BarrelIdentityChip).toBe(IdentityChip);
  });

  it('etiket · bağlantılı değer · warn ipucu · kopyala — bu sırayla', () => {
    const el = render(
      <IdentityChip label="OPERASYON" value="ORDERS_APPROVE_REST" to="/traces?fc=OA1"
        valueLabel="ORDERS_APPROVE_REST — tahmin" hint="tahmin · 3 aday" hintTone="warn" hintTitle="kesin DEĞİL"
        copyTitle="Operasyon adını kopyala" title="kök ipucu" data-op-source="dictionary" />);
    expect(el.className).toBe('idchip');
    expect(el.getAttribute('role')).toBe('listitem');
    expect(el.title).toBe('kök ipucu');
    expect(el.dataset.opSource).toBe('dictionary');
    expect(cells(el)).toEqual(['idchip__label', 'idchip__val', 'idchip__hint is-warn', 'ghost xs copy-btn idchip__copy']);
    expect(el.textContent).toBe('OPERASYONORDERS_APPROVE_RESTtahmin · 3 aday⧉');
    const a = el.querySelector('a')!;
    expect(a.getAttribute('href')).toBe('/traces?fc=OA1');
    expect(a.getAttribute('aria-label')).toBe('ORDERS_APPROVE_REST — tahmin');
    expect((el.querySelector('.idchip__hint') as HTMLElement).title).toBe('kesin DEĞİL');
  });

  it('bağlantısız değer düz metin; sr-only not; ipucu yoksa hücre yok; nötr ton varsayılan', () => {
    const el = render(<IdentityChip label="CHANNEL" value="060203" srNote="kanal kodu" copyTitle="Kanal kodunu kopyala" />);
    expect(el.querySelector('a')).toBeNull();
    expect(cells(el)).toEqual(['idchip__label', 'idchip__val', 'ghost xs copy-btn idchip__copy']);
    expect(el.querySelector('.idchip__val .sr-only')!.textContent).toBe(' — kanal kodu');
    const n = render(<IdentityChip label="OPERASYON" value="X" hint="+2" copyTitle="c" />);
    expect(n.querySelector('.idchip__hint')!.className).toBe('idchip__hint');
  });

  it('kopyala hücresi: ad = başlık, değeri kopyalar, sonra ✓', async () => {
    const el = render(<IdentityChip label="FUNCTION" value="SXQ0112" copyTitle="Fonksiyon kodunu kopyala" />);
    const btn = el.querySelector('button')!;
    expect(btn.getAttribute('aria-label')).toBe('Fonksiyon kodunu kopyala');
    expect(btn.title).toBe('Fonksiyon kodunu kopyala');
    expect(btn.getAttribute('style')).toBeNull(); // hücre ölçüsü CSS'te, satır-içi değil
    await act(async () => { btn.click(); });
    expect(copySpy).toHaveBeenCalledWith('SXQ0112');
    expect(btn.textContent).toBe('✓');
  });

  it('role ve ek sınıf çağırandan geçersiz kılınabilir', () => {
    const el = render(<IdentityChip label="L" value="V" copyTitle="c" role="group" className="extra" />);
    expect(el.getAttribute('role')).toBe('group');
    expect(el.className).toBe('idchip extra');
  });
});
