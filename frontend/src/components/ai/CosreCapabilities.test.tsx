// @vitest-environment jsdom
//
// v0.10.1128 (operatör) — boş CoSRE sohbetinin "neler yapabilirim" ipucu:
//   (1) saf: wiki satırı yalnız `wiki` bayrağıyla ve en başta; `|` imleç işareti;
//   (2) EN/TR kataloglarında her anahtar var, metinler dile göre (EN gelecekteki
//       sohbet-geneli i18n için katalogda durur);
//   (3) çekmecede: ipucu boş sohbette görünür; wiki kapalıyken wiki satırı ve
//       wiki çipi YOK, açıkken VAR; satıra tıklamak composer'ı doldurur ama
//       GÖNDERMEZ (copilotChat çağrılmaz); wiki çipi "wikide " doldurur;
//   (4) prod hatası (v0.10.1130): sohbet yüzeyi Türkçe-öncelikli — UI dili EN
//       iken de ipuçları, title'ı, wiki çipi ve doldurulan örnekler TÜRKÇE.
//   /cosre sayfası aynı bileşeni çizer — pages/CoSRE.test.tsx.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import { setUserLang, t } from '@/lib/i18n';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { capabilityItems, splitCaret, tCosre, COSRE_LANG } from './capabilityHints';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  // Dil seçimi localStorage'da; Node'un yerleşik localStorage'ı jsdom'unkini
  // gölgeliyor (traceAskCosre.contract.test.tsx emsali). Marka isteği ağa çıkmaz.
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue([] as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ starters: [] } as never);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('capabilityHints — saf', () => {
  it('wiki satırı yalnız bayrakla ve en başta', () => {
    expect(capabilityItems(false).map(i => i.kind)).toEqual(['service', 'operation', 'navigate']);
    expect(capabilityItems(true).map(i => i.kind)).toEqual(['wiki', 'service', 'operation', 'navigate']);
  });

  it('splitCaret: | imleç yeri, işaretsiz metinde imleç sonda', () => {
    expect(splitCaret('@| svc')).toEqual({ text: '@ svc', caret: 1 });
    expect(splitCaret('| op')).toEqual({ text: ' op', caret: 0 });
    expect(splitCaret('wikide ')).toEqual({ text: 'wikide ', caret: 7 });
  });

  it('EN/TR: her anahtar iki katalogda da var ve dile göre farklı', () => {
    const keys = capabilityItems(true).flatMap(i => [i.labelKey, i.promptKey])
      .concat(['cosre.cap.aria', 'cosre.cap.tryHint', 'cosre.chip.wiki', 'cosre.chip.wiki.prompt']);
    for (const k of keys) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(t('cosre.cap.wiki', 'tr')).toContain('wiki');
    expect(t('cosre.cap.service', 'tr')).toContain('servis adını yaz');
    expect(t('cosre.cap.service', 'en')).toContain('service name');
    expect(t('cosre.chip.wiki.prompt', 'tr')).toBe('wikide ');
  });

  it('tCosre: UI dilinden bağımsız, karşılamanın dili (TR)', () => {
    expect(COSRE_LANG).toBe('tr');
    setUserLang('en');
    expect(tCosre('cosre.cap.wiki')).toBe(t('cosre.cap.wiki', 'tr'));
    expect(tCosre('cosre.cap.tryHint')).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(tCosre('cosre.chip.wiki.prompt')).toBe('wikide ');
  });
});

function shell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <ConfirmProvider>
            <CopilotChat />
          </ConfirmProvider>
        </AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

async function openDrawer() {
  await act(async () => { root.render(shell()); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

const rows = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('button[data-cap]'));
const textarea = () => document.body.querySelector<HTMLTextAreaElement>('textarea');

describe('CopilotChat çekmecesi — yetenek ipucu', () => {
  it('wiki kapalı: üç satır, wiki satırı/çipi yok', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['service', 'operation', 'navigate']);
    expect(document.body.querySelector('[data-chip="wiki"]')).toBeNull();
    expect(document.body.textContent).toContain('Hata veren teknik operasyonları inceleyebilirim');
  });

  it('wiki açık: wiki satırı + çip; tıklama doldurur, GÖNDERMEZ', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['wiki', 'service', 'operation', 'navigate']);
    expect(document.body.textContent).toContain('Kurum wiki’sinde arayabilirim');

    const wikiRow = rows().find(b => b.dataset.cap === 'wiki')!;
    await act(async () => { wikiRow.click(); });
    expect(textarea()?.value).toBe('wikide cache refresh nasıl yapılır');

    const svcRow = rows().find(b => b.dataset.cap === 'service')!;
    await act(async () => { svcRow.click(); });
    expect(textarea()?.value.startsWith('@ servisinin')).toBe(true);

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('wikide ');
    expect(send).not.toHaveBeenCalled();
    // Hâlâ boş sohbet: ipucu yerinde (gönderilmedi).
    expect(rows().length).toBe(4);
  });

  it('UI dili EN: ipuçları, title, wiki çipi ve örnekler yine TÜRKÇE (karşılamayla aynı dil)', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('en');
    await openDrawer();
    const text = document.body.textContent ?? '';
    expect(text).toContain('Sana nasıl yardımcı olabilirim?');
    expect(text).toContain('Kurum wiki’sinde arayabilirim');
    expect(text).toContain('Hata veren teknik operasyonları inceleyebilirim');
    expect(text).not.toContain('I can search the company wiki');
    expect(text).not.toContain('type the operation name');
    for (const b of rows()) expect(b.title).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(document.body.querySelector('[role="list"].cosre-cap')?.getAttribute('aria-label')).toBe('CoSRE neler yapabilir');

    await act(async () => { rows().find(b => b.dataset.cap === 'wiki')!.click(); });
    expect(textarea()?.value).toBe('wikide cache refresh nasıl yapılır');
    await act(async () => { rows().find(b => b.dataset.cap === 'operation')!.click(); });
    expect(textarea()?.value).toBe(' operasyonu neden hata veriyor?');
    await act(async () => { rows().find(b => b.dataset.cap === 'navigate')!.click(); });
    expect(textarea()?.value).toBe(' sorununu incelemek için nereye bakmalıyım?');

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    expect(chip?.textContent).not.toContain('Search the wiki');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('wikide ');
    expect(send).not.toHaveBeenCalled();
  });
});
