// @vitest-environment jsdom
//
// v0.10.1402 — operatör: "Alttaki takımımın servisleri exceptionları yukarıda
// olsun. Takım seçilebilsin. Son deploy etkisini kaldırabiliriz." + "Takımı da
// seçilebilir yapabiliriz, oradan devam eder."
//
// Pinler:
//   (1) saf: teamCardQuestion (takımlı / takımsız) ve teamPickQuestion — Go
//       copilot_team_card_test.go aynı cümlelerin LLM'siz yönlenmesini pinler;
//       son seçilen takım depolaması erişilemezken sessiz;
//   (2) başlangıç: Takımım kartı İLK; içinde sunucu-aramalı takım seçici + 3
//       çip; deploy kartı yok; alttaki gevşek satırda takım çipleri yok (diğer
//       gevşek çipler duruyor);
//   (3) çip: takım yokken "Takımımın …", depodaki / sunucunun çözdüğü takımla
//       "<takım> takımının …" gönderir;
//   (4) seçicide takım seçmek TEK tur gönderir: "<takım> takımının servisleri
//       nasıl?" (team_services) ve seçimi bu tarayıcıda hatırlar.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import { setUserLang } from '@/lib/i18n';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import {
  COSRE_TEAM_STORAGE_KEY, readStoredTeam, teamCardQuestion, teamPickQuestion, writeStoredTeam,
} from './capabilityHints';

let host: HTMLDivElement;
let root: Root;
let mem: Map<string, string>;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  mem = new Map<string, string>();
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
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
  setUserLang('tr');
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Takımım kartı — saf', () => {
  it('takımsız: iyelik cümleleri; takımlı: adı geçen takımın cümleleri', () => {
    expect(teamCardQuestion('services', '')).toBe('Takımımın servisleri nasıl?');
    expect(teamCardQuestion('exceptions', '  ')).toBe("Takımımın exception'ları?");
    expect(teamCardQuestion('topErrors', '')).toBe('Takımımın en hatalı servisleri?');
    expect(teamCardQuestion('services', 'platform')).toBe('platform takımının servisleri nasıl?');
    expect(teamCardQuestion('exceptions', ' platform ')).toBe("platform takımının exception'ları?");
    expect(teamCardQuestion('topErrors', 'platform')).toBe('platform takımının en hatalı servisleri?');
    expect(teamPickQuestion('platform')).toBe('platform takımının servisleri nasıl?');
  });

  it('son seçilen takım: yaz / oku / temizle; depolama atarsa sessiz', () => {
    expect(readStoredTeam()).toBeNull();
    writeStoredTeam('platform');
    expect(mem.get(COSRE_TEAM_STORAGE_KEY)).toBe('platform');
    expect(readStoredTeam()).toBe('platform');
    writeStoredTeam('');
    expect(readStoredTeam()).toBeNull();
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('blocked'); },
      removeItem: () => { throw new Error('blocked'); },
    });
    expect(readStoredTeam()).toBeNull();
    expect(() => writeStoredTeam('platform')).not.toThrow();
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
const teamCard = () => rows()[0]?.closest('.cosre-cap__item') ?? null;
const teamChips = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('[data-cap-choices="team"] button'));
const teamInput = () => document.body.querySelector<HTMLInputElement>('[data-cap-search="team"] input');
const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
const type = async (input: HTMLInputElement, v: string) => {
  await act(async () => {
    setVal.call(input, v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
};
const key = async (input: HTMLInputElement, k: string) => {
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
};
const settle = async (ms = 260) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };
const lastSent = (send: ReturnType<typeof vi.spyOn>) => {
  const msgs = (send.mock.calls[send.mock.calls.length - 1] as unknown[])[0] as { role: string; text: string }[];
  return msgs[msgs.length - 1].text;
};

describe('CoSRE başlangıcı — v0.10.1402 Takımım kartı', () => {
  it('Takımım kartı ilk: seçici + 3 çip; deploy kartı yok; gevşek satırda takım çipi yok', async () => {
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: [] });
    await openDrawer();
    expect(rows()[0]?.dataset.cap).toBe('team');
    expect(rows().map(b => b.dataset.cap)).not.toContain('deploy');
    const card = teamCard()!;
    expect(card.classList.contains('cosre-cap__item--team')).toBe(true);
    expect(card.querySelector('[data-cap-search="team"] input')).not.toBeNull();
    expect(teamInput()?.placeholder).toBe('Takım ara…');
    expect(teamChips().map(b => b.dataset.teamAsk)).toEqual(['services', 'exceptions', 'topErrors']);
    expect(teamChips().map(b => b.textContent)).toEqual(['Servisleri nasıl?', "Exception'ları?", 'En hatalı servisleri?']);
    for (const b of teamChips()) expect(card.contains(b)).toBe(true);

    const body = document.body.textContent ?? '';
    expect(body).not.toContain('Son deploy’ların neyi değiştirdiğini');
    expect(body).not.toContain('son deploy etkisi');

    const loose = document.body.querySelector('.cm-greet__chips')!;
    expect(loose.textContent).not.toContain('Takımımın');
    expect(loose.textContent).toContain("…/yol hatalı trace'leri");
    expect(loose.querySelector('[data-chip="wiki"]')).not.toBeNull();
  });

  it('takım seçilmemiş: çip "Takımımın …" gönderir', async () => {
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: [] });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(teamInput()?.value).toBe('');
    expect(teamChips()[1].title).toBe("Takımımın exception'ları?");
    await act(async () => { teamChips()[1].click(); });
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe("Takımımın exception'ları?");
  });

  it('bu tarayıcıda seçilmiş takım: çip "<takım> takımının …" gönderir (sunucu takımını ezer)', async () => {
    mem.set(COSRE_TEAM_STORAGE_KEY, 'platform');
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [], team: 'orders-team' } as never);
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['platform'] });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(teamInput()?.value).toBe('platform');
    await act(async () => { teamChips()[2].click(); });
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('platform takımının en hatalı servisleri?');
  });

  it('seçim yokken varsayılan sunucunun çözdüğü takım', async () => {
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [], team: 'platform' } as never);
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['platform'] });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(teamInput()?.value).toBe('platform');
    await act(async () => { teamChips()[0].click(); });
    await settle(20);
    expect(lastSent(send)).toBe('platform takımının servisleri nasıl?');
  });

  it('seçicide takım seçmek TEK tur gönderir ("<takım> takımının servisleri nasıl?") ve seçimi hatırlar', async () => {
    const names = vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['platform', 'platform-data'] });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = teamInput()!;
    await type(input, 'plat');
    await settle();
    expect(names).toHaveBeenCalledWith('team', 'plat');
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('platform takımının servisleri nasıl?');
    expect(mem.get(COSRE_TEAM_STORAGE_KEY)).toBe('platform');
  });

  it('Enter: yazılan ad sunucuda birebir doğrulanınca gönderir, değilse cümleyi DOLDURUR', async () => {
    vi.spyOn(api, 'copilotScopeNames').mockResolvedValue({ names: ['platform', 'platform-data'] });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = teamInput()!;
    await type(input, 'plat');
    await key(input, 'Enter');
    await settle(20);
    expect(send).not.toHaveBeenCalled();
    expect(document.body.querySelector('textarea')?.value).toBe('plat takımının servisleri nasıl?');
    expect(mem.has(COSRE_TEAM_STORAGE_KEY)).toBe(false);
  });

  it('Enter: birebir ad sunucuda doğrulanınca TEK tur gönderir', async () => {
    vi.spyOn(api, 'copilotScopeNames').mockImplementation(async (_k, q) => ({ names: q === 'platform' ? ['platform'] : [] }));
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = teamInput()!;
    await type(input, 'platform');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('platform takımının servisleri nasıl?');
    expect(mem.get(COSRE_TEAM_STORAGE_KEY)).toBe('platform');
  });
});
