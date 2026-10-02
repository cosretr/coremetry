// @vitest-environment jsdom
//
// DevOpsTab.codeBudget — v0.10.1038. Operatör: "Kod bütçesi daha fazla
// karakter olabilir bence, default 10k gibi, performans sorunu
// olmayacaksa." Kod bütçesi DevOps bağlantı blob'unda bir ayar oldu; bu
// dosya kutunun sözleşmesini çiviliyor:
//   • kayıtlı değer kutuda, yürürlükteki değer ipucunda;
//   • yazılan sayı mevcut PUT gövdesinde gider (yeni uç yok), PAT alanı
//     gövdeye girmez (boş PAT = saklı değer korunur);
//   • boş kutu 0 gönderir = varsayılan (10000);
//   • sunucu sıkıştırdıysa (500 → 2000) kayıttan sonra kutu kaydedileni gösterir.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { DevOpsSnapshot } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<DevOpsSnapshot>>(),
  put: vi.fn<(s: Record<string, unknown>) => Promise<DevOpsSnapshot>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getDevOpsSettings: () => h.get(),
    putDevOpsSettings: (s: Record<string, unknown>) => h.put(s),
    testDevOpsSettings: () => Promise.resolve({ ok: true, projectCount: 1 }),
    resolveDevOpsDryRun: () => Promise.resolve(null),
    getSchemaCatalog: () => Promise.resolve({ tables: 0, columns: 0, importedAt: 0, snapshotSql: {} }),
  },
}));

import { ConfirmProvider } from '@/components/ui';
import { DevOpsTab } from './DevOpsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

const SNAP: DevOpsSnapshot = {
  baseUrl: 'https://devops.example.local/tfs', collection: 'DefaultCollection',
  hasPat: true, flavor: 'auto', effectiveLookupLimit: 6,
};

async function mount(snap: DevOpsSnapshot): Promise<HTMLElement> {
  h.get.mockResolvedValue(snap);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<ConfirmProvider><DevOpsTab /></ConfirmProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset(); h.put.mockReset();
});

function budgetInput(el: HTMLElement): HTMLInputElement {
  const label = Array.from(el.querySelectorAll('label')).find(l => l.textContent === 'Kod bütçesi (karakter)');
  expect(label, 'kod bütçesi alanı yok').toBeTruthy();
  return document.getElementById(label!.htmlFor) as HTMLInputElement;
}
function hintOf(input: HTMLInputElement): string {
  return document.getElementById(input.getAttribute('aria-describedby') || '')?.textContent || '';
}
async function setInput(input: HTMLInputElement, text: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  await act(async () => {
    setter.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
async function save(input: HTMLInputElement) {
  await act(async () => { input.form!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

describe('DevOpsTab — kod bütçesi (v0.10.1038)', () => {
  it('kayıtlı değer kutuda; sınırlar, varsayılan yer tutucu ve ipucu', async () => {
    const el = await mount({ ...SNAP, codeBudgetRunes: 6000, effectiveCodeBudgetRunes: 6000 });
    const input = budgetInput(el);
    expect(input.value).toBe('6000');
    expect(input.type).toBe('number');
    expect(input.min).toBe('2000');
    expect(input.max).toBe('20000');
    expect(input.placeholder).toBe('10000');
    const hint = hintOf(input);
    expect(hint).toContain('Modele gönderilen kaynak kod miktarı. Küçük bağlamlı modellerde cevap kesiliyorsa düşürün.');
    expect(hint).toContain('yürürlükte: 6000');
  });

  it('yazılan sayı PUT gövdesinde gider; PAT alanı gövdeye girmez', async () => {
    h.put.mockImplementation(s => Promise.resolve({ ...SNAP, codeBudgetRunes: s.codeBudgetRunes as number, effectiveCodeBudgetRunes: s.codeBudgetRunes as number }));
    const el = await mount({ ...SNAP, codeBudgetRunes: 6000, effectiveCodeBudgetRunes: 6000 });
    const input = budgetInput(el);
    await setInput(input, '12000');
    await save(input);
    expect(h.put).toHaveBeenCalledTimes(1);
    const body = h.put.mock.calls[0][0];
    expect(body.codeBudgetRunes).toBe(12000);
    expect(body).not.toHaveProperty('pat');
    expect(body.codeLookupLimit).toBe(0); // komşu tavanlar dokunulmadan gider
    expect(hintOf(budgetInput(el))).toContain('yürürlükte: 12000');
  });

  it('ayar yoksa kutu boş, ipucu varsayılanı söyler; boş kutu 0 gönderir (= varsayılan)', async () => {
    h.put.mockResolvedValue({ ...SNAP, effectiveCodeBudgetRunes: 10000 });
    const el = await mount(SNAP);
    const input = budgetInput(el);
    expect(input.value).toBe('');
    expect(hintOf(input)).toContain('yürürlükte: 10000');
    await save(input);
    expect(h.put.mock.calls[0][0].codeBudgetRunes).toBe(0);
  });

  it('sunucu sıkıştırırsa kutu kaydedileni gösterir (500 → 2000)', async () => {
    h.put.mockResolvedValue({ ...SNAP, codeBudgetRunes: 2000, effectiveCodeBudgetRunes: 2000 });
    const el = await mount(SNAP);
    const input = budgetInput(el);
    await setInput(input, '500');
    await save(input);
    expect(h.put.mock.calls[0][0].codeBudgetRunes).toBe(500);
    expect(budgetInput(el).value).toBe('2000');
    expect(hintOf(budgetInput(el))).toContain('yürürlükte: 2000');
  });

  // v0.10.1038 — elle düzenlenmiş blob (kayıtlı 500): kutu YÜRÜRLÜKTEKİ değerle
  // (2000) dolar; tarayıcının min/max doğrulaması formu kilitlemez, Kaydet çalışır.
  it('aralık dışı kayıtlı değer (500) kutuya yürürlükteki 2000 olarak gelir; Kaydet kilitlenmez', async () => {
    h.put.mockImplementation(s => Promise.resolve({ ...SNAP, codeBudgetRunes: s.codeBudgetRunes as number, effectiveCodeBudgetRunes: 2000 }));
    const el = await mount({ ...SNAP, codeBudgetRunes: 500, effectiveCodeBudgetRunes: 2000 });
    const input = budgetInput(el);
    expect(input.value).toBe('2000');
    expect(input.checkValidity()).toBe(true);
    expect(input.form!.checkValidity()).toBe(true);
    // Doğrulama jsdom'da gerçekten çalışıyor: 500 yazılsaydı form geçersizdi.
    await setInput(input, '500');
    expect(input.checkValidity()).toBe(false);
    await setInput(input, '2000');
    // requestSubmit = tarayıcının Kaydet yolu (doğrulama dahil).
    await act(async () => { input.form!.requestSubmit(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.put).toHaveBeenCalledTimes(1);
    expect(h.put.mock.calls[0][0].codeBudgetRunes).toBe(2000);
    expect(h.put.mock.calls[0][0]).not.toHaveProperty('pat');
  });
});
