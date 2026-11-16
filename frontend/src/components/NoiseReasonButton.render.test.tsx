// @vitest-environment jsdom
//
// v0.10.1531 (Problem standardı R3, D5) — "Problem değil ▾" gerekçe diyaloğu:
// gerekçe seçilmeden işaretlenmez; "Diğer" not ister; "Kalıcı gizle" yalnız
// yöneticiye görünür; seçim onConfirm'e aynen gider.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2283 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { NoiseReasonButton } from './NoiseReasonButton';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

function mount(isAdmin: boolean, onConfirm = vi.fn()) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<NoiseReasonButton label="Problem değil" isAdmin={isAdmin} onConfirm={onConfirm} />); });
  const trigger = host.querySelector('button')!;
  act(() => { trigger.click(); });
  return { onConfirm };
}
const dialog = () => document.querySelector('[role="dialog"]') as HTMLElement;
const submit = () => [...dialog().querySelectorAll('button')].find(b => b.textContent?.includes('işaretle'))!;
const radio = (id: string) => dialog().querySelector(`input[value="${id}"]`) as HTMLInputElement;

describe('NoiseReasonButton', () => {
  it('gerekçesiz gönderilemez; seçince onConfirm', () => {
    const { onConfirm } = mount(false);
    expect(submit().disabled).toBe(true);
    act(() => { radio('too_sensitive').click(); });
    expect(submit().disabled).toBe(false);
    act(() => { submit().click(); });
    expect(onConfirm).toHaveBeenCalledWith({ reason: 'too_sensitive', note: '', permanent: false });
  });
  it('"Diğer" not ister', () => {
    mount(false);
    act(() => { radio('other').click(); });
    expect(submit().disabled).toBe(true);
    expect(dialog().textContent).toContain('kısa bir not');
  });
  it('kalıcı gizle yalnız yöneticide', () => {
    mount(false);
    expect(dialog().textContent).not.toContain('Kalıcı gizle');
    act(() => root?.unmount()); host?.remove();
    const { onConfirm } = mount(true);
    expect(dialog().textContent).toContain('Kalıcı gizle');
    act(() => { radio('expected').click(); });
    act(() => { (dialog().querySelector('input[type="checkbox"]') as HTMLInputElement).click(); });
    expect(dialog().textContent).toContain('süresiz');
    act(() => { submit().click(); });
    expect(onConfirm).toHaveBeenCalledWith({ reason: 'expected', note: '', permanent: true });
  });
});
