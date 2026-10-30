// @vitest-environment jsdom
// AiTab.secret.test.tsx — Faz 2 dilim 1 R11 (plan R-1, secret sözleşmesi;
// ClustersTab.secret.test deseni). Settings › CoSRE shadcn'e geçerken API
// anahtarı sözleşmesi AYNEN kalmalı; işaretlemeden bağımsız ölçülür (yalnız
// tip/rol/metin):
//   - Sağlayıcı formu: saklı anahtar YANKILANMAZ (password boş başlar, sunucu
//     bir gün yanlışlıkla `apiKey` gönderse bile); saklıyken etiket satırında
//     "(saved — leave empty to keep current)" ve maskeli yer tutucu; dokunulmamış
//     alan PUT'a '' gider (sunucu: boş = koru); yazılan AYNEN gider.
//   - Profil düzenleme diyaloğu: anahtar alanı boş başlar, ipucu "Boş = mevcut
//     anahtar korunur"; dokunulmamışsa gövdede apiKey YOK (undefined), yazılan aynen.
// Göçten ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AIModelProfileInput, AISettingsInput } from '@/lib/types';
import { ConfirmGate } from '@/components/coremetry/Confirm';

const api = vi.hoisted(() => {
  const profiles = [
    { id: 'main', label: 'Ana model', provider: 'anthropic', model: 'claude', hasKey: true, default: true, effectiveToolset: 'full', apiKey: 'LEAKED-PROFILE-KEY' },
  ];
  const settings = {
    provider: 'anthropic', model: '', baseUrl: '', hasKey: true, enabled: true, apiKey: 'LEAKED-SECRET',
    profiles, defaultProfile: 'main', surfaceMap: {},
  };
  return {
    getAISettings: vi.fn(async () => settings),
    putAISettings: vi.fn(async (s: AISettingsInput) => ({ ...settings, provider: s.provider, hasKey: true })),
    putAIProfile: vi.fn(async (..._a: unknown[]) => ({ profiles, defaultProfile: 'main', surfaceMap: {} })),
    aiBudget: vi.fn(async () => ({ budget: {} })),
    aiChatRetention: vi.fn(async () => ({ days: 90 })),
    aiGeneralQuestions: vi.fn(async () => ({ allow: true })),
    cosreV2Flag: vi.fn(async () => ({ enabled: false })),
  };
});
vi.mock('@/lib/api', () => ({ api }));

import { AITab } from './AiTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(
      <MemoryRouter initialEntries={['/settings/ai']}>
        <QueryClientProvider client={qc}><ConfirmGate><AITab /></ConfirmGate></QueryClientProvider>
      </MemoryRouter>,
    );
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  api.putAISettings.mockClear(); api.putAIProfile.mockClear();
});
const tick = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
};
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
// Sağlayıcı formu = "Save" düğmesini taşıyan form (profil diyaloğu portalda).
const providerForm = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('form')).find(f => Array.from(f.querySelectorAll('button')).some(b => b.textContent?.trim() === 'Save'))!;
const keyInput = (el: HTMLElement) => providerForm(el).querySelector<HTMLInputElement>('input[type="password"]')!;
async function save(el: HTMLElement) {
  await act(async () => { providerForm(el).requestSubmit(); });
  await tick();
  expect(api.putAISettings).toHaveBeenCalledTimes(1);
  return api.putAISettings.mock.calls[0][0] as AISettingsInput;
}
const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]')!;
async function openEdit(el: HTMLElement) {
  const row = Array.from(el.querySelectorAll('tbody tr')).find(r => r.textContent?.includes('Ana model'))!;
  const btn = Array.from(row.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Düzenle')!;
  act(() => { btn.click(); });
  await tick();
  return dialog().querySelector<HTMLInputElement>('input[type="password"]')!;
}
async function saveProfile() {
  await act(async () => { (document.getElementById('ai-profile-form') as HTMLFormElement).requestSubmit(); });
  await tick();
  expect(api.putAIProfile).toHaveBeenCalledTimes(1);
  return api.putAIProfile.mock.calls[0][1] as AIModelProfileInput;
}

describe('AiTab — API anahtarı secret sözleşmesi', () => {
  it('saklı anahtar yankılanmaz; saklı notu etiket satırında + maskeli yer tutucu', async () => {
    const el = render();
    await tick();
    const k = keyInput(el);
    expect(k.value).toBe('');
    expect(document.body.innerHTML).not.toContain('LEAKED-SECRET');
    expect(k.placeholder).toBe('••••••••••••••••');
    // input.labels: sarmalayan <label> da label[for] da sayılır (işaretlemeden bağımsız).
    // Eski işaretleme: sarmalayan <label>; yeni: coremetry Field kabı (etiket + aside).
    const lab = k.labels![0];
    const labelRow = (lab.closest('[data-cm-field]') ?? lab).textContent ?? '';
    expect(k.labels?.[0]?.textContent?.startsWith('API key')).toBe(true);
    expect(labelRow).toContain('(saved — leave empty to keep current)');
  });

  it('dokunulmamış anahtar PUT\'a boş gider (saklı korunur)', async () => {
    const el = render();
    await tick();
    const body = await save(el);
    expect(body.apiKey).toBe('');
    expect(body.clearKey).toBeUndefined();
  });

  it('yazılan anahtar AYNEN gider (kırpma yok)', async () => {
    const el = render();
    await tick();
    act(() => { setValue(keyInput(el), '  sk-ant-NEW  '); });
    const body = await save(el);
    expect(body.apiKey).toBe('  sk-ant-NEW  ');
  });

  it('profil diyaloğu: anahtar boş başlar, dokunulmamışsa gövdede yok; yazılan aynen', async () => {
    const el = render();
    await tick();
    let k = await openEdit(el);
    expect(k.value).toBe('');
    expect(document.body.innerHTML).not.toContain('LEAKED-PROFILE-KEY');
    expect(dialog().textContent).toContain('Boş = mevcut anahtar korunur');
    expect((await saveProfile()).apiKey).toBeUndefined();
    api.putAIProfile.mockClear();
    k = await openEdit(el);
    act(() => { setValue(k, 'gh-NEW-key '); });
    expect((await saveProfile()).apiKey).toBe('gh-NEW-key ');
  });
});
