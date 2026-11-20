// @vitest-environment jsdom
//
// v0.10.1515 (operatör 2026-10-24: "Alttaki chipler çok çıkıyor, gerek yok
// servis servis yazmasına. Sadece 'sağlığı analiz et' ve 'exception'ları' —
// bir servisin diyerek genel başlıklarda servis seçmesi sağlanabilir.")
// Pinler: (1) saf: şablon doldurma, yük doğrulama, gönderim kapsamı, aday
// birleştirme; (2) 40 servisli takım cevabında servis başına çip YOK, iki
// genel çip var; genel çip mesaj göndermeden satır içi servis seçicisi açar,
// adaylar listenin başında; seçim eski çipin cümlesini kapsamla gönderir.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ChatFollowup } from '@/lib/types';

const aiSignal = vi.fn();
const serviceNames = vi.fn();
vi.mock('@/lib/api', () => ({ api: {
  aiSignal: (...a: unknown[]) => aiSignal(...a),
  serviceNames: (...a: unknown[]) => serviceNames(...a),
} }));

import { FollowupChips } from './FollowupChips';
import { fillPickTemplate, isServicePick, pickSendOpts, PICK_SERVICE_PLACEHOLDER } from './followupPick';
import { mergePreferred } from '@/lib/pickerPreferred';

// 40 servisli takım: sunucu adayları tablonun tavanında (15) keser, hata oranı azalan.
const TEAM = Array.from({ length: 40 }, (_, i) => `svc-${String(i).padStart(2, '0')}`);
const CANDS = ['svc-17', 'svc-03', ...TEAM.filter(n => n !== 'svc-17' && n !== 'svc-03')].slice(0, 15);
const PICKS: ChatFollowup[] = [
  { text: 'Bir servisin sağlığını analiz et', label: 'Bir servisin sağlığını analiz et', kind: 'team_pick_health',
    scope: { env: 'prod' }, pick: { kind: 'service', template: '{service} sağlığı nasıl?', services: CANDS } },
  { text: 'Bir servisin exception\'ları', label: 'Bir servisin exception\'ları', kind: 'team_pick_exceptions',
    scope: { env: 'prod' }, pick: { kind: 'service', template: '{service} teknik hataları?', services: CANDS } },
];

describe('followupPick — saf', () => {
  it('şablon, yük doğrulama, kapsam', () => {
    expect(PICK_SERVICE_PLACEHOLDER).toBe('{service}');
    expect(fillPickTemplate('{service} sağlığı nasıl?', ' orders-api ')).toBe('orders-api sağlığı nasıl?');
    expect(isServicePick(PICKS[0])).toBe(true);
    expect(isServicePick({ text: 'x', kind: 'k', pick: { kind: 'service', template: 'yer tutucusuz' } })).toBe(false);
    expect(isServicePick({ text: 'x', kind: 'k', pick: { kind: 'pod', template: '{service}' } })).toBe(false);
    expect(isServicePick({ text: 'x', kind: 'k' })).toBe(false);
    expect(pickSendOpts(PICKS[0], 'svc-03')).toEqual({ parsed: { scope: { env: 'prod', services: ['svc-03'] } } });
    expect(pickSendOpts({ text: 'x', kind: 'k' }, 'a')).toEqual({ parsed: { scope: { services: ['a'] } } });
  });

  it('mergePreferred: uyan adaylar önce (sıra korunur), sonra sunucu, tekrar yok; joker sorguda aday yok', () => {
    expect(mergePreferred(['b-api', 'a-api'], '', ['a-api', 'c-api'])).toEqual(['b-api', 'a-api', 'c-api']);
    expect(mergePreferred(['b-api', 'a-worker'], 'API', ['x-api', 'b-api'])).toEqual(['b-api', 'x-api']);
    expect(mergePreferred(['b-api'], 'b*', ['b-api', 'bb'])).toEqual(['b-api', 'bb']);
    expect(mergePreferred(undefined, 'x', ['x1'])).toEqual(['x1']);
  });
});

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  aiSignal.mockClear();
  serviceNames.mockReset();
  serviceNames.mockImplementation(async (q: string) => {
    const names = ['other-api', ...TEAM].filter(n => !q || n.includes(q));
    return { names, total: names.length, hasMore: false };
  });
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

const mount = async (onAsk: (t: string, o?: unknown) => void) => {
  const qc = new QueryClient();
  await act(async () => {
    root.render(<QueryClientProvider client={qc}><MemoryRouter>
      <FollowupChips followups={PICKS} fallback={['statik']} exchangeId="x1" onAsk={onAsk} />
    </MemoryRouter></QueryClientProvider>);
  });
};
const settle = async (ms = 260) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };
const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
const type = async (input: HTMLInputElement, v: string) => {
  await act(async () => { setVal.call(input, v); input.dispatchEvent(new Event('input', { bubbles: true })); });
};
const key = async (input: HTMLInputElement, k: string) => {
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
};

describe('FollowupChips — seçici çipi', () => {
  it('40 servisli takım: servis başına çip yok, iki genel çip; tık göndermez, seçici açar', async () => {
    const onAsk = vi.fn();
    await mount(onAsk);
    const labels = Array.from(host.querySelectorAll('button')).map(b => b.textContent?.trim());
    expect(labels).toEqual(['↳ Bir servisin sağlığını analiz et', '↳ Bir servisin exception\'ları']);
    for (const svc of TEAM) expect(host.textContent).not.toContain(svc);
    expect(host.textContent).not.toContain('detayı');
    expect(host.textContent).not.toContain('deploy etkisi');
    expect(host.querySelector('[data-fu-pick]')).toBeNull();

    const health = host.querySelector<HTMLButtonElement>('[data-fu-pick-chip="team_pick_health"]')!;
    await act(async () => { health.click(); });
    expect(onAsk).not.toHaveBeenCalled();
    const input = host.querySelector<HTMLInputElement>('[data-fu-pick="team_pick_health"] input');
    expect(input?.placeholder).toBe('Servis ara…');
    expect(health.getAttribute('aria-expanded')).toBe('true');
    // Tekrar tık kapatır.
    await act(async () => { health.click(); });
    expect(host.querySelector('[data-fu-pick]')).toBeNull();
  });

  it('seçim: adaylar listenin başında; seçilen servisle eski çipin cümlesi + kapsam gönderilir', async () => {
    const onAsk = vi.fn();
    await mount(onAsk);
    await act(async () => { host.querySelector<HTMLButtonElement>('[data-fu-pick-chip="team_pick_exceptions"]')!.click(); });
    const input = host.querySelector<HTMLInputElement>('[data-fu-pick="team_pick_exceptions"] input')!;
    await type(input, 'svc-');
    await settle();
    expect(serviceNames).toHaveBeenCalledWith('svc-', 200);
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(onAsk).toHaveBeenCalledTimes(1);
    // İlk aday (hata oranı en yüksek) — sunucunun alfabetik listesinin ilki değil.
    expect(onAsk).toHaveBeenCalledWith('svc-17 teknik hataları?', { parsed: { scope: { env: 'prod', services: ['svc-17'] } } });
    expect(aiSignal).toHaveBeenCalledWith('x1', 'followup_click', 'team_pick_exceptions');
    expect(host.querySelector('[data-fu-pick]')).toBeNull(); // seçimden sonra kapanır
  });

  it('Enter: adaylarda olan ad doğrudan, olmayan ad sunucuda birebirse gönderilir; değilse gönderilmez', async () => {
    const onAsk = vi.fn();
    await mount(onAsk);
    await act(async () => { host.querySelector<HTMLButtonElement>('[data-fu-pick-chip="team_pick_health"]')!.click(); });
    let input = host.querySelector<HTMLInputElement>('[data-fu-pick="team_pick_health"] input')!;
    await type(input, 'svc');
    await key(input, 'Enter');
    await settle(40);
    expect(onAsk).not.toHaveBeenCalled(); // birebir değil
    input = host.querySelector<HTMLInputElement>('[data-fu-pick="team_pick_health"] input')!;
    await type(input, 'other-api');
    await key(input, 'Enter');
    await settle(40);
    expect(onAsk).toHaveBeenCalledWith('other-api sağlığı nasıl?', { parsed: { scope: { env: 'prod', services: ['other-api'] } } });
  });
});
