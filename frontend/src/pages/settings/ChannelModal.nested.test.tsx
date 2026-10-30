// @vitest-environment jsdom
// ChannelModal.nested.test.tsx — Faz 2 dilim 1 R9 (plan R-7, D8).
//
// NE ÇİVİLİYOR: ChannelModal ve içinde açılan ZoomChannelPicker artık iki
// portal edilmiş coremetry/overlay Dialog. Eskiden ikisi elle yazılmış sabit
// perdeydi ve "iç üstte" sözleşmesi zLayers.test'te z-index rung'larıyla
// (`--z-modal-nested` > `--z-modal`) çivileniyordu; o çivi buraya taşındı:
//   • PORTAL SIRASI: iç diyalog body'de dıştakinden SONRA (üst üste binme
//     sırası; ikisi aynı rung'da, sonra gelen üstte);
//   • Esc LIFO (lib/escLayer yığını): ilk Esc yalnız seçiciyi kapatır, kanal
//     formu açık kalır (onClose çağrılmaz); ikinci Esc formu kapatır.
// ÇİVİLENMEYEN: Combobox listesine (`.pick-pop`) basmanın diyaloğu
// kapatmaması — jsdom'da Radix'in dışarı-pointerdown yolu düz bir div'e
// basınca da tetiklenmedi (ölçüldü), yani böyle bir test ısırmazdı.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { useShortcuts } from '@/lib/keyboard';
import { __resetEscLayers } from '@/lib/escLayer';
import type { NotificationChannel } from '@/lib/types';

vi.mock('@/lib/api', () => ({ api: { updateChannel: vi.fn(), createChannel: vi.fn() } }));
vi.mock('@/lib/queries', () => ({ useServicesMetadata: () => ({ data: {} }) }));

import { ChannelModal } from './ChannelModal';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ZOOM: NotificationChannel = {
  id: 'ch-zoom', name: 'zoom-ops', type: 'zoomchat', enabled: true, minSeverity: 'warning',
  config: { accountId: 'acc-1', clientId: 'cli-1' },
} as NotificationChannel;

function KeyboardHost() { useShortcuts([]); return null; } // keyboard.ts dinleyicisi
let host: HTMLDivElement | null = null; let root: Root | null = null;
const realFetch = globalThis.fetch;
const onClose = vi.fn();

async function render() {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => {
    root!.render(<><KeyboardHost /><ChannelModal initial={ZOOM} onClose={onClose} onSaved={() => {}} /></>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}
async function esc() {
  const target = document.activeElement ?? document.body;
  await act(async () => {
    target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
  });
  await act(async () => { await Promise.resolve(); });
}
const dialogs = () => [...document.body.querySelectorAll<HTMLElement>('[role="dialog"]')];

beforeEach(() => {
  __resetEscLayers();
  globalThis.fetch = vi.fn(() => new Promise<Response>(() => {})) as unknown as typeof fetch;
});
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  globalThis.fetch = realFetch;
  onClose.mockReset();
});

describe('ChannelModal + ZoomChannelPicker — iç içe diyalog', () => {
  it('kanal formu role=dialog + başlıkla adlandırılmış', async () => {
    await render();
    const [d] = dialogs();
    expect(d).toBeTruthy();
    expect(d.getAttribute('aria-labelledby')).toBeTruthy();
    expect(document.getElementById(d.getAttribute('aria-labelledby')!)?.textContent).toBe('Edit channel — zoom-ops');
  });

  it('iç diyalog portal sırasında SONRA; Esc önce içtekini kapatır, dıştaki açık kalır', async () => {
    await render();
    const list = [...document.body.querySelectorAll('button')].find(b => /List my channels/.test(b.textContent ?? ''))!;
    await act(async () => { list.click(); });
    const ds = dialogs();
    expect(ds).toHaveLength(2);
    expect(ds[0].textContent).toContain('Edit channel — zoom-ops');
    expect(ds[1].textContent).toContain('Pick a Zoom channel');
    // Portal sırası = üst üste binme sırası.
    expect(ds[0].compareDocumentPosition(ds[1]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    await esc();
    expect(dialogs()).toHaveLength(1);
    expect(dialogs()[0].textContent).toContain('Edit channel — zoom-ops');
    expect(onClose).not.toHaveBeenCalled();

    await esc();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
