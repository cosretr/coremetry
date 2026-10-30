// @vitest-environment jsdom
// ChannelModal.secret.test.tsx — Faz 2 dilim 1 R9 (plan R-1, secret
// sözleşmesi; ClustersTab.secret.test emsali). Kanal formunun iki secret'ı
// (Zoom Client Secret, Twilio Auth Token) shadcn + Radix Dialog'a geçerken
// sözleşme AYNEN kalmalı; işaretlemeden bağımsız ölçülür (tip/etiket/değer,
// sorgular document.body üstünde — diyalog portal olabilir):
//   - sunucu GET'te secret'ı silerek döner; alan type=password ve BOŞ başlar;
//   - düzenlemede Client Secret etiketi "leave empty to keep saved value",
//     yer tutucu "•••••••• (unchanged)"; yeni kanalda "never echoed back";
//   - dokunulmamış Client Secret PUT gövdesinde YOK (sunucu saklıyı taşır);
//   - yazılan secret gider (bugünkü kural: kırpılmış).
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { NotificationChannel } from '@/lib/types';

const { updateChannel, createChannel } = vi.hoisted(() => ({
  updateChannel: vi.fn(async () => ({})),
  createChannel: vi.fn(async () => ({})),
}));
vi.mock('@/lib/api', () => ({ api: { updateChannel, createChannel } }));
vi.mock('@/lib/queries', () => ({ useServicesMetadata: () => ({ data: {} }) }));

import { ChannelModal } from './ChannelModal';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ZOOM: NotificationChannel = {
  id: 'ch-zoom', name: 'zoom-ops', type: 'zoomchat', enabled: true, minSeverity: 'warning',
  config: { accountId: 'acc-1', clientId: 'cli-1', channelId: 'ops@conference.xmpp.zoom.us' },
} as NotificationChannel;
const WA: NotificationChannel = {
  id: 'ch-wa', name: 'wa-ops', type: 'whatsapp', enabled: true, minSeverity: 'warning',
  config: { accountSid: 'AC123', from: 'whatsapp:+10000000000', to: ['+10000000001'] },
} as NotificationChannel;

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(initial: NotificationChannel | null) {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<ChannelModal initial={initial} onClose={() => {}} onSaved={() => {}} />); });
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  updateChannel.mockClear(); createChannel.mockClear();
});
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const pw = () => document.body.querySelector<HTMLInputElement>('input[type="password"]')!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function submit(label: string) {
  const btn = [...document.body.querySelectorAll('button')].find(b => b.textContent?.trim() === label)!;
  await act(async () => { btn.closest('form')!.requestSubmit(btn); });
  await tick();
}
type Payload = { config: NotificationChannel['config'] };
const sentConfig = () => (updateChannel.mock.calls[0] as unknown as [string, Payload])[1].config;

describe('ChannelModal — Zoom Client Secret', () => {
  it('düzenleme: alan boş, type=password, saklı etiketi + yer tutucusu', () => {
    render(ZOOM);
    const p = pw();
    expect(p.value).toBe('');
    expect(p.placeholder).toBe('•••••••• (unchanged)');
    expect(p.labels?.[0]?.textContent).toContain('Client Secret (leave empty to keep saved value)');
    expect(p.required).toBe(false);
  });

  it('dokunulmamış secret PUT gövdesinde yok (saklı korunur)', async () => {
    render(ZOOM);
    await submit('Update');
    expect(updateChannel).toHaveBeenCalledTimes(1);
    const cfg = sentConfig();
    expect('clientSecret' in cfg).toBe(false);
    expect(cfg.accountId).toBe('acc-1');
  });

  it('yazılan secret gider', async () => {
    render(ZOOM);
    act(() => { setValue(pw(), ' s3cret '); });
    await submit('Update');
    expect(sentConfig().clientSecret).toBe('s3cret');
  });

  it('yeni kanal: secret zorunlu, yer tutucu "never echoed back after save"', () => {
    render(null);
    const sel = document.body.querySelector<HTMLSelectElement>('select')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(sel, 'zoomchat');
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    });
    const p = pw();
    expect(p.value).toBe('');
    expect(p.placeholder).toBe('never echoed back after save');
    expect(p.required).toBe(true);
    expect(p.labels?.[0]?.textContent?.trim()).toBe('Client Secret');
  });
});

describe('ChannelModal — Twilio Auth Token', () => {
  it('düzenleme: sunucu token göndermez → alan boş, type=password', () => {
    render(WA);
    const p = pw();
    expect(p.value).toBe('');
    expect(p.labels?.[0]?.textContent).toContain('Auth Token');
  });

  it('yazılan token gider', async () => {
    render(WA);
    act(() => { setValue(pw(), 'tok-123'); });
    await submit('Update');
    expect(sentConfig().authToken).toBe('tok-123');
  });
});
