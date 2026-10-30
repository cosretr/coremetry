// @vitest-environment jsdom
//
// v0.10.1533 (Problem standardı R5, D8) — enforce'a geçiş açık bir onay ister;
// replay çalıştırılmadıysa onay metni bunu söyler ve kaydetme onaysız gitmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ConfirmGate } from '@/components/coremetry/Confirm'; // R9: sekme coremetry useConfirm
import { MemoryRouter } from 'react-router-dom';
import type { NotifyPolicy } from '@/lib/types';

const m = vi.hoisted(() => ({ put: vi.fn() }));
const POL: NotifyPolicy = {
  mode: 'shadow', p3: 'digest', digestTimes: ['09:00', '16:00'], noiseDedupeHours: 12, noiseMuteHours: 168, template: 'tr',
  impactFloors: { errorFailed5m: 30, errorPct: 1, minCalls5m: 30, latencyP99DeltaMs: 300, trafficDropPct: 40, trafficMinBaselineCalls5m: 100 },
};
vi.mock('@/lib/api', () => ({
  api: {
    notifyPolicy: async () => POL,
    notifyDigest: async () => ({ items: [], lastSlot: '' }),
    putNotifyPolicy: (b: unknown) => { m.put(b); return Promise.resolve(b); },
    notifyPolicyReplay: async () => null,
  },
}));

import { NotifyPolicyTab } from './NotifyPolicyTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopRO { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopRO;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.put.mockReset(); });
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

describe('NotifyPolicyTab', () => {
  it('form yüklenir; enforce seçimi replay’siz uyarılı onay ister', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<MemoryRouter><ConfirmGate><NotifyPolicyTab /></ConfirmGate></MemoryRouter>); });
    await settle();
    const mode = host.querySelector('select') as HTMLSelectElement;
    expect(mode.value).toBe('shadow');
    const digest = [...host.querySelectorAll('input')].find(i => i.value === '09:00, 16:00');
    expect(digest).toBeTruthy();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(mode, 'enforce');
      mode.dispatchEvent(new Event('change', { bubbles: true }));
    });
    const save = [...host.querySelectorAll('button')].find(b => b.textContent === 'Kaydet')!;
    await act(async () => { save.click(); });
    // R9: coremetry ConfirmGate onay diyaloğunu tembel yükler — görünene dek bekle.
    for (let i = 0; i < 50 && !document.querySelector('[role="dialog"], [role="alertdialog"]'); i++) await settle();
    const dlg = document.querySelector('[role="dialog"], [role="alertdialog"]');
    expect(dlg?.textContent).toContain('Replay sonucu yok');
    expect(m.put).not.toHaveBeenCalled();
  });
});
