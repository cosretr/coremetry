// @vitest-environment jsdom
// KnowledgeTab.secret.test.tsx — Faz 2 dilim 1 R12 (plan R-1, secret
// sözleşmesi; SMTP deseni v0.8.451). Bilgi (RAG) formu shadcn'e geçerken
// sözleşme AYNEN kalmalı ve işaretlemeden bağımsız ölçülür (tip/yer tutucu/metin):
//   - Embedding API key: saklıysa alan BOŞ başlar (sunucu yanlışlıkla değer
//     gönderse bile yankılanmaz), yer tutucu "********", ipucu "kayıtlı — boş
//     bırakırsan korunur"; dokunulmamış → PUT'ta apiKey YOK (undefined);
//     yazılan AYNEN gider;
//   - kaynak şifresi / ham header: saklıysa alan boş, "(kayıtlı)" yer
//     tutucusu; dokunulmamış → '********' sentineli gider (sunucu URL
//     eşleşmesinden devralır); yazılan şifre aynen, header kırpılarak gider.
// Kayıt geçişinden ÖNCE eski işaretlemeye karşı yeşil yazıldı, sonra da yeşil.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { ConfirmGate } from '@/components/coremetry/Confirm';
import type { RagConfigView } from '@/lib/types';

type PutBody = { endpoint: string; model: string; enabled: boolean; topK?: number; apiKey?: string; insecureSkipVerify?: boolean;
  sources?: { url: string; username?: string; password?: string; authHeader?: string }[] };

const { getRagConfig, putRagConfig } = vi.hoisted(() => {
  const view = {
    endpoint: 'http://emb.example.invalid/v1', model: 'bge-m3', enabled: true, topK: 5, hasKey: true,
    apiKey: 'LEAKED-KEY',
    sources: [{ url: 'https://wiki.example.invalid/a', username: 'svc', password: '********', authHeader: '********' }],
  } as unknown as RagConfigView;
  return {
    getRagConfig: vi.fn(async () => view),
    putRagConfig: vi.fn(async (b: PutBody) => ({ ...view, endpoint: b.endpoint }) as RagConfigView),
  };
});
vi.mock('@/lib/api', () => ({
  api: {
    getRagConfig, putRagConfig,
    listRagDocuments: async () => ({ documents: [], pending: [] }),
    listKBCandidates: async () => ({ rows: [] }),
  },
}));
vi.mock('./WikiKnowledgeSection', () => ({ WikiKnowledgeSection: () => null }));

import { KnowledgeTab } from './KnowledgeTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null; let root: Root | null = null;
async function render(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><ConfirmProvider><ConfirmGate><KnowledgeTab /></ConfirmGate></ConfirmProvider></MemoryRouter>); });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  putRagConfig.mockClear();
});
const pws = (el: HTMLElement) => [...el.querySelectorAll<HTMLInputElement>('input[type="password"]')];
const header = (el: HTMLElement) => [...el.querySelectorAll<HTMLInputElement>('input')].find(i => i.placeholder.includes('kayıtlı header'))!;
function setValue(ctl: HTMLInputElement, text: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(ctl, text);
  ctl.dispatchEvent(new Event('input', { bubbles: true }));
}
async function save(el: HTMLElement): Promise<PutBody> {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Kaydet')!;
  await act(async () => { btn.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  expect(putRagConfig).toHaveBeenCalledTimes(1);
  return putRagConfig.mock.calls[0][0];
}

describe('KnowledgeTab — secret sözleşmesi', () => {
  it('saklı API key ve kaynak sırları yankılanmaz; saklı yer tutucuları ve ipucu görünür', async () => {
    const el = await render();
    const [key, pw] = pws(el);
    expect(pws(el)).toHaveLength(2);
    expect(key.value).toBe('');
    expect(key.placeholder).toBe('********');
    expect(el.textContent).toContain('kayıtlı — boş bırakırsan korunur');
    expect(pw.value).toBe('');
    expect(pw.placeholder).toBe('******** (kayıtlı)');
    expect(header(el).value).toBe('');
    expect(el.innerHTML).not.toContain('LEAKED-KEY');
  });

  it('dokunulmamış: apiKey gitmez, kaynak sırları sentinel olarak gider', async () => {
    const el = await render();
    const body = await save(el);
    expect(body.apiKey).toBeUndefined();
    expect(body.sources).toEqual([{ url: 'https://wiki.example.invalid/a', username: 'svc', password: '********', authHeader: '********' }]);
  });

  it('yazılan değerler gider: key ve şifre aynen, header kırpılarak', async () => {
    const el = await render();
    const [key, pw] = pws(el);
    await act(async () => { setValue(key, ' new-key '); });
    await act(async () => { setValue(pw, ' p@ss '); });
    await act(async () => { setValue(header(el), '  Authorization: Bearer x  '); });
    const body = await save(el);
    expect(body.apiKey).toBe(' new-key ');
    expect(body.sources?.[0]).toMatchObject({ password: ' p@ss ', authHeader: 'Authorization: Bearer x' });
    // Kayıttan sonra alanlar yeniden boş (yankı yok).
    expect(pws(el)[0].value).toBe('');
    expect(pws(el)[1].value).toBe('');
  });
});
