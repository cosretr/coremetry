// @vitest-environment jsdom
//
// v0.10.1125 — /cosre bağımsız CoSRE sayfası, render ile:
//   (1) rota sohbeti SIDEBAR'SIZ çizer (AppShell kromsuz dalı) — başlıkta
//       CoSRE + model çipi + Geçmiş + "Coremetry'yi aç" + tema düğmesi; FAB yok;
//       sekme başlığı "CoSRE";
//   (2) custom-rol kısıtlı kullanıcı /cosre'den ilk izinli sayfaya ışınlanmaz;
//   (3) kimliksiz açılış /login'e gider ve derin bağlantı olarak /cosre saklanır
//       (postLoginRedirect; OIDC ?next= de aynı süzgeçten geçer);
//   (4) copilot kapalıysa sayfa boş değil, kapalı durumunu söyler;
//   (5) cevap içindeki iç linkler /cosre'de yeni sekmede (target=_blank
//       rel=noopener), çekmecede (bağlamsız) aynı sekmede.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { AppShell, isPathAllowed } from '@/components/AppShell';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { ChatBubble } from '@/components/ai/ChatBubble';
import { ChatLinkNewTabContext } from '@/components/ai/chatLinkTarget';
import { __resetCopilotEnabledCache } from '@/components/ai/useCopilotEnabled';
import { api } from '@/lib/api';
import { isCosrePage } from '@/lib/cosrePage';
import { isPublicPath } from '@/lib/auth-paths';
import { oidcStartHref, peekPostLoginRedirect, sanitizeRedirect } from '@/lib/postLoginRedirect';
import type { ChatTurn } from '@/lib/types';
import CoSRE from './CoSRE';

let host: HTMLDivElement;
let root: Root;
let here = '';
function Probe() { const l = useLocation(); here = l.pathname + l.search; return null; }

const tick = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

function renderAt(path: string) {
  return act(async () => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <AuthProvider>
              <Probe />
              <Routes>
                <Route element={<AppShell />}>
                  <Route path="/cosre" element={<CoSRE />} />
                  <Route path="/login" element={<div data-testid="login">login</div>} />
                  <Route path="/services" element={<div data-testid="services">services</div>} />
                </Route>
              </Routes>
            </AuthProvider>
          </ConfirmProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
}

beforeEach(() => {
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  sessionStorage.clear();
  document.title = 'Coremetry';
  vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no network in test'))));
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'model-x' } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0 } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue([] as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ starters: [] } as never);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('/cosre bağımsız sohbet sayfası', () => {
  it('saf yardımcılar: /cosre kromsuz ama PUBLIC değil; custom rol için hep izinli', () => {
    expect(isCosrePage('/cosre')).toBe(true);
    expect(isCosrePage('/cosre/')).toBe(true);
    expect(isCosrePage('/cosrex')).toBe(false);
    expect(isPublicPath('/cosre')).toBe(false);
    expect(isPathAllowed('/cosre', ['/services'])).toBe(true);
    expect(isPathAllowed('/cosre', [])).toBe(true);
  });

  it('sohbeti sidebar olmadan tam sayfa çizer: başlık eylemleri + tema + uygulamaya dönüş', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
    await renderAt('/cosre');
    await tick();
    expect(here.startsWith('/cosre')).toBe(true);
    expect(document.getElementById('sidebar')).toBeNull();
    expect(document.querySelector('.cosre-bare')).not.toBeNull();
    expect(document.querySelector('.cm-ai-fab'), 'FAB yok').toBeNull();
    expect(document.querySelector('[role="dialog"]'), 'çekmece değil, sayfa').toBeNull();
    const head = document.querySelector('.cosre-page__head');
    expect(head?.textContent).toContain('CoSRE');
    expect(head?.textContent).toContain('model-x');
    expect(head?.textContent).toContain('Geçmiş');
    expect(head?.textContent).not.toContain('⤢');
    const home = Array.from(head?.querySelectorAll('a') ?? []).find(a => a.textContent === "Coremetry'yi aç");
    expect(home?.getAttribute('href')).toBe('/');
    expect(head?.querySelector('[aria-label*="heme"], [title*="heme"]'), 'tema düğmesi').not.toBeNull();
    expect(document.querySelector('.cosre-page__body textarea')).not.toBeNull();
    expect(document.title).toBe('CoSRE');
  });

  it('custom-rol kısıtlı kullanıcı /cosre\'de kalır (ilk izinli sayfaya ışınlanmaz)', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u2', email: 'r@example.test', role: 'viewer', customRolePages: ['/services'] } as never);
    await renderAt('/cosre');
    await tick();
    expect(here.startsWith('/cosre')).toBe(true);
  });

  it('kimliksiz: /login\'e gider, /cosre derin bağlantı olarak saklanır (OIDC ?next= dahil)', async () => {
    vi.spyOn(api, 'me').mockRejectedValue(new Error('401'));
    await renderAt('/cosre');
    await tick();
    expect(here).toBe('/login');
    expect(peekPostLoginRedirect()).toBe('/cosre');
    expect(oidcStartHref()).toBe('/api/auth/oidc/start?next=%2Fcosre');
    expect(sanitizeRedirect('/cosre?chat=c1')).toBe('/cosre?chat=c1');
  });

  it('copilot kapalıysa kapalı durumunu gösterir (boş sayfa değil)', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: false } as never);
    await renderAt('/cosre');
    await tick();
    expect(document.getElementById('sidebar')).toBeNull();
    expect(document.body.textContent).toContain('CoSRE bu kurulumda kapalı');
    expect(document.querySelector('textarea')).toBeNull();
  });
});

describe('cevap linkleri: /cosre\'de yeni sekme', () => {
  const tid = 'a'.repeat(32);
  const turn: ChatTurn = {
    role: 'assistant',
    text: `trace ${tid} yavaş`,
    links: [{ label: 'payment servisi', href: '/service?service=payment' }, { label: 'wiki', href: 'https://wiki.example.test/x' }],
  } as ChatTurn;

  async function renderBubble(newTab: boolean) {
    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={['/cosre']}>
          <ChatLinkNewTabContext.Provider value={newTab}>
            <ChatBubble turn={turn} />
          </ChatLinkNewTabContext.Provider>
        </MemoryRouter>,
      );
    });
  }

  it('iç çip + mdLite trace linki target=_blank rel=noopener; href aynı-köken yol kalır', async () => {
    await renderBubble(true);
    const chip = Array.from(host.querySelectorAll('a.ai-link')).find(a => a.textContent?.includes('payment'));
    expect(chip?.getAttribute('href')).toBe('/service?service=payment');
    expect(chip?.getAttribute('target')).toBe('_blank');
    expect(chip?.getAttribute('rel')).toBe('noopener');
    const traceA = host.querySelector('a[data-nav]');
    expect(traceA?.getAttribute('href')?.startsWith('/trace')).toBe(true);
    expect(traceA?.getAttribute('target')).toBe('_blank');
    expect(traceA?.getAttribute('rel')).toBe('noopener');
  });

  it('çekmecede (bağlam yok) iç linkler aynı sekmede kalır', async () => {
    await renderBubble(false);
    const chip = Array.from(host.querySelectorAll('a.ai-link')).find(a => a.textContent?.includes('payment'));
    expect(chip?.getAttribute('target')).toBeNull();
    expect(host.querySelector('a[data-nav]')?.getAttribute('target')).toBeNull();
  });
});
