// @vitest-environment jsdom
// CosreConversationLine.test.tsx — v0.10.2324 (conversation-audit karar 5):
// konuşma durumu aç/kapa; v0.10.2339 doğrulayıcı üç seçenek. Pinler: sunucu değeri (varsayılan açık) görünür,
// tık PUT {state} gönderir, viewer salt okunur, okuma hatası boş değil.
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({
  role: 'admin' as string | undefined,
  get: (() => Promise.resolve({ state: true, style: false, verification: 'shadow' })) as () => Promise<unknown>,
  put: vi.fn((state: boolean) => Promise.resolve({ state, style: false, verification: 'shadow' })),
  putVerify: vi.fn((verification: string) => Promise.resolve({ state: true, style: false, verification })),
  putStyle: vi.fn((style: boolean) => Promise.resolve({ state: true, style, verification: 'shadow' })),
  putBanned: vi.fn((bannedPhrases: string[]) => Promise.resolve({ state: true, style: true, verification: 'shadow', bannedPhrases })),
  putInteraction: vi.fn((interaction: string) => Promise.resolve({ state: true, style: false, verification: 'shadow', interaction })),
  putEvidence: vi.fn((evidence: string) => Promise.resolve({ state: true, style: false, verification: 'shadow', evidence })),
  putStatusLine: vi.fn((statusLine: boolean) => Promise.resolve({ state: true, style: false, verification: 'shadow', statusLine })),
}));
vi.mock('@/lib/api', () => ({
  api: {
    cosreConversationSettings: () => m.get(),
    putCosreConversationState: (s: boolean) => m.put(s),
    putCosreConversationVerification: (v: string) => m.putVerify(v),
    putCosreConversationStyle: (s: boolean) => m.putStyle(s),
    putCosreConversationBannedPhrases: (p: string[]) => m.putBanned(p),
    putCosreConversationInteraction: (v: string) => m.putInteraction(v),
    putCosreConversationEvidence: (e: string) => m.putEvidence(e),
    putCosreConversationStatusLine: (v: boolean) => m.putStatusLine(v),
  },
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));

import { CosreConversationLine, CONVERSATION_STYLE_HINT, CONVERSATION_STATUS_LINE_HINT, parseBannedPhrases, bannedPhrasesError } from './CosreConversationLine';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeAll(() => {
  (globalThis as unknown as Record<string, unknown>).ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
});
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.put.mockClear(); m.putVerify.mockClear(); m.putStyle.mockClear(); m.putBanned.mockClear(); m.putInteraction.mockClear(); m.role = 'admin';
  m.put.mockClear(); m.putVerify.mockClear(); m.putStyle.mockClear(); m.putBanned.mockClear(); m.putEvidence.mockClear(); m.role = 'admin';
  m.get = () => Promise.resolve({ state: true, style: false, verification: 'shadow' });
});
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreConversationLine /></QueryClientProvider>); });
  await tick();
  return host;
}
const box = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[role="checkbox"]');
const verifyRadio = (el: HTMLElement, label: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('[data-testid="cosre-verify-mode"] [role="radio"]')].find(b => b.textContent === label)!;

const evidenceRadio = (el: HTMLElement, label: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('[data-testid="cosre-evidence-mode"] [role="radio"]')].find(b => b.textContent === label)!;

describe('CosreConversationLine', () => {
  it('v0.10.2355 — kanıt alanı üç seçenek; alan yokken kapalı; seçim evidence yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Kanıt (kapsam kararı)');
    expect(el.textContent).toContain('scope_would_refuse');
    expect(evidenceRadio(el, 'kapalı').getAttribute('aria-checked')).toBe('true');
    await act(async () => { evidenceRadio(el, 'gölge').click(); });
    await tick();
    expect(m.putEvidence).toHaveBeenCalledWith('shadow');
    expect(m.putVerify).not.toHaveBeenCalled();
  });
  it('sunucu değeri görünür (varsayılan açık); tık state:false yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Konuşma durumu');
    expect(box(el)?.getAttribute('aria-checked')).toBe('true');
    await act(async () => { box(el)!.click(); });
    await tick();
    expect(m.put).toHaveBeenCalledWith(false);
  });
  it('v0.10.2339 — doğrulayıcı üç seçenek (kapalı / gölge / zorla); sunucunun kipi seçili; seçim yazar', async () => {
    const el = await render();
    expect(el.textContent).toContain('Cevap doğrulayıcısı');
    expect(el.textContent).toContain('kısa “†” işareti'); // v0.10.2480 — satır içi cümle yerine işaret + tek dipnot
    expect(verifyRadio(el, 'gölge').getAttribute('aria-checked')).toBe('true');
    await act(async () => { verifyRadio(el, 'zorla').click(); });
    await tick();
    expect(m.putVerify).toHaveBeenCalledWith('enforce');
    await act(async () => { verifyRadio(el, 'kapalı').click(); });
    await tick();
    expect(m.putVerify).toHaveBeenLastCalledWith('off');
    expect(m.put).not.toHaveBeenCalled();
  });
  it('v0.10.2439 — zorla varsayılan: açıklama notu ve yeniden kontrol önerisini anlatır; gölgeye dönüş yazar', async () => {
    m.get = () => Promise.resolve({ state: true, style: false, verification: 'enforce' });
    const el = await render();
    expect(verifyRadio(el, 'zorla').getAttribute('aria-checked')).toBe('true');
    expect(el.textContent).toContain('Zorla (varsayılan)');
    expect(el.textContent).toContain('Değerleri veriden yeniden kontrol et');
    expect(el.textContent).toContain('yerine değer uydurulmaz');
    await act(async () => { verifyRadio(el, 'gölge').click(); });
    await tick();
    expect(m.putVerify).toHaveBeenCalledWith('shadow');
  });
  it('seçili kipe tık yazmaz', async () => {
    m.get = () => Promise.resolve({ state: true, style: false, verification: 'enforce' });
    const el = await render();
    expect(verifyRadio(el, 'zorla').getAttribute('aria-checked')).toBe('true');
    await act(async () => { verifyRadio(el, 'zorla').click(); });
    await tick();
    expect(m.putVerify).not.toHaveBeenCalled();
  });
  it('üslup (v0.10.2331): varsayılan kapalı; tık style:true yazar', async () => {
    const el = await render();
    const boxes = el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]');
    expect(el.textContent).toContain('Üslup');
    expect(boxes[1]?.getAttribute('aria-checked')).toBe('false');
    await act(async () => { boxes[1]!.click(); });
    await tick();
    expect(m.putStyle).toHaveBeenCalledWith(true);
    expect(m.put).not.toHaveBeenCalled();
    expect(m.putVerify).not.toHaveBeenCalled();
  });
  it('etkileşim (v0.10.2353): varsayılan kapalı; tık interaction:on yazar, diğerlerini yazmaz', async () => {
    const el = await render();
    const boxes = el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]');
    expect(el.textContent).toContain('Etkileşim');
    expect(boxes[2]?.getAttribute('aria-checked')).toBe('false');
    await act(async () => { boxes[2]!.click(); });
    await tick();
    expect(m.putInteraction).toHaveBeenCalledWith('on');
    expect(m.put).not.toHaveBeenCalled();
    expect(m.putStyle).not.toHaveBeenCalled();
  });
  it('üslup metni (v0.10.2337): ölçü ve örnekle üslubu söyler; ara cümle v0.10.2473 ayrı', () => {
    for (const w of ['uzunluğu', 'örnek diyalog', 'Raporlar']) expect(CONVERSATION_STYLE_HINT).toContain(w);
    expect(CONVERSATION_STYLE_HINT).not.toContain('ara durum cümlesi');
    expect(CONVERSATION_STATUS_LINE_HINT).toContain('model çağrısı yapmaz');
  });
  it('ara durum cümlesi (v0.10.2473): alanı yoksa açık; tık statusLine:false yazar, style yazmaz', async () => {
    const el = await render();
    const boxes = el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]');
    expect(el.textContent).toContain('Ara durum cümlesi');
    expect(boxes[3]?.getAttribute('aria-checked')).toBe('true');
    await act(async () => { boxes[3]!.click(); });
    await tick();
    expect(m.putStatusLine).toHaveBeenCalledWith(false);
    expect(m.putStyle).not.toHaveBeenCalled();
    m.putStatusLine.mockClear();
  });
  it('viewer salt okunur', async () => {
    m.role = 'viewer';
    const el = await render();
    expect(box(el)?.disabled).toBe(true);
    expect(verifyRadio(el, 'zorla').disabled).toBe(true);
    expect(el.querySelectorAll<HTMLButtonElement>('[role="checkbox"]')[1]?.disabled).toBe(true);
    expect(el.textContent).toContain('Yalnız admin');
  });
  it('okuma hatası boş değil', async () => {
    m.get = () => Promise.reject(new Error('503'));
    const el = await render();
    expect(el.textContent).toContain('ayar okunamadı');
  });
  // v0.10.2338 (D) — ek kalıp ifadeleri: düzenlenebilir liste, tam liste yazılır, tavanlar istemcide de.
  it('ek kalıp ifadeleri: sunucu listesi görünür; düzenleyip Kaydet tam listeyi yazar', async () => {
    m.get = () => Promise.resolve({ state: true, style: true, verification: 'shadow', bannedPhrases: ['Sorunuzu inceledim'] });
    const el = await render();
    const ta = el.querySelector<HTMLTextAreaElement>('[data-testid="cosre-banned-phrases"] textarea')!;
    expect(ta.value).toBe('Sorunuzu inceledim');
    const save = [...el.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent?.includes('Kaydet'))!;
    expect(save.disabled).toBe(true);
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    await act(async () => {
      setter.call(ta, 'Sorunuzu inceledim\n  kolay   gelsin \n\nsorunuzu inceledim');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(save.disabled).toBe(false);
    await act(async () => { save.click(); });
    await tick();
    expect(m.putBanned).toHaveBeenCalledWith(['Sorunuzu inceledim', 'kolay gelsin']);
  });
  it('ek kalıp ifadeleri: tavanlar ve viewer', async () => {
    expect(parseBannedPhrases(' a \n\nA\nb  c')).toEqual(['a', 'b c']);
    expect(bannedPhrasesError(Array.from({ length: 41 }, (_, i) => `ifade ${i}`))).toContain('40');
    expect(bannedPhrasesError(['ş'.repeat(41)])).toContain('80 bayt');
    expect(bannedPhrasesError(['ş'.repeat(40)])).toBeUndefined();
    m.role = 'viewer';
    const el = await render();
    expect(el.querySelector<HTMLTextAreaElement>('[data-testid="cosre-banned-phrases"] textarea')?.disabled).toBe(true);
    expect([...el.querySelectorAll('button')].some(b => b.textContent?.includes('Kaydet'))).toBe(false);
  });
});
