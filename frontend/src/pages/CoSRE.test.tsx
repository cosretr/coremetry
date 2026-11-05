// @vitest-environment jsdom
//
// v0.10.1125 — /cosre bağımsız CoSRE sayfası, render ile:
//   (1) rota sohbeti SIDEBAR'SIZ çizer (AppShell kromsuz dalı) — başlıkta
//       CoSRE + Geçmiş + "Coremetry'yi aç" + tema düğmesi (model çipi v0.10.1141'ten beri composer'da); FAB yok;
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
import '@/test/radixJsdom'; // v0.10.1788 — v2 karşılaması (cmdk) ResizeObserver ister

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
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
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
    // v0.10.1141 — model etiketi başlıkta DEĞİL, composer'ın içinde.
    expect(head?.textContent).not.toContain('model-x');
    // 2026-10-10 — kompakt etiket: tam model kimliği tooltip'te.
    expect(document.querySelector('form.cm-composer .cm-model-label')?.getAttribute('title')).toBe('Model: model-x');
    // v0.10.1137 — geçmiş başlık düğmesi değil, sol kenar çubuğu (Claude gibi).
    expect(head?.textContent).not.toContain('Geçmiş');
    expect(document.getElementById('cosre-side')?.textContent).toContain('Yeni sohbet');
    expect(head?.textContent).not.toContain('⤢');
    const home = Array.from(head?.querySelectorAll('a') ?? []).find(a => a.textContent === "Coremetry'yi aç");
    expect(home?.getAttribute('href')).toBe('/');
    expect(head?.querySelector('[aria-label*="heme"], [title*="heme"]'), 'tema düğmesi').not.toBeNull();
    expect(document.querySelector('.cosre-page__body textarea')).not.toBeNull();
    expect(document.title).toBe('CoSRE');
  });

  it('v0.10.1128 — boş sohbette "neler yapabilirim" ipucu (wiki bayrağıyla wiki satırı + çipi)', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'model-x', wiki: true } as never);
    await renderAt('/cosre');
    await tick();
    const caps = Array.from(document.querySelectorAll<HTMLButtonElement>('.cosre-page__body button[data-cap]'));
    // v0.10.1402 — Takımım kartı en başta (tam sayfada da aynı bileşen).
    expect(caps.map(b => b.dataset.cap)).toEqual(['team', 'wiki', 'service', 'operation', 'exceptions']);
    expect(document.querySelector('.cosre-page__body [data-chip="wiki"]')).not.toBeNull();
    // Prod hatası (v0.10.1130): UI dili burada EN (kullanıcı seçimi yok, marka
    // isteği ağa çıkmaz → İngilizce varsayılan) ama ipuçları karşılamayla aynı
    // dilde, TÜRKÇE çizilir — karışık dilli karşılama yok.
    const body = document.querySelector('.cosre-page__body')!.textContent ?? '';
    expect(body).toContain('Sana nasıl yardımcı olabilirim?');
    expect(body).toContain('Kurum wiki’sinde arayabilirim');
    expect(body).toContain('Wikide ara');
    expect(body).not.toContain('I can search the company wiki');
    expect(body).not.toContain('Search the wiki');
    expect(caps[0].title).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
  });

  // v0.10.1404 — P1 kartı yerine exceptions kartı. v0.10.1515 (operatör
  // 2026-10-24) — navigate satırı yok; exceptions kartında filo çipleri yok
  // (eski şekilli yanıtta bile), yerinde takım seçici.
  it('v0.10.1515 — exceptions kartı takım seçicili, çipsiz; navigate satırı yok', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ team: 'orders-team', groups: [{ kind: 'exceptions', items: [
      { chip: 'ORDERS_GET_X · orders-api', question: 'q', kind: 'exception', explainAsk: { kind: 'exception', id: 'ora:00000000000000a1', src: 'starter' } },
    ] }] } as never);
    await renderAt('/cosre');
    await tick();
    const caps = Array.from(document.querySelectorAll<HTMLButtonElement>('.cosre-page__body button[data-cap]'));
    expect(caps.map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'exceptions']);
    const body = document.querySelector('.cosre-page__body')!.textContent ?? '';
    expect(body).toContain('Takımının son exception\'larına bakabilirim');
    expect(body).not.toContain('P1 problemlerinin');
    expect(body).not.toContain('ORDERS_GET_X');
    expect(body).not.toContain('Nereye bakman gerektiğini');
    expect(document.querySelector('.cosre-page__body [data-cap-choices="exceptions"]')).toBeNull();
    const picker = document.querySelector<HTMLInputElement>('.cosre-page__body [data-cap-search="exceptions"] input');
    expect(picker?.placeholder).toBe('Takım ara…');
    expect(picker?.value).toBe('orders-team');
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

// v0.10.1785 — CoSRE v2 tam sayfası (mockup A): bayrak açık → v2 kabuğu
// (CosreChatV2 variant="page"), kapalı → v1 CopilotChat birebir.
describe('/cosre bayrak: v1 / v2 sayfası', () => {
  it('cosreV2 kapalı → v1 sayfası (data-cosre-v2 yok, v1 kenar çubuğu)', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    await renderAt('/cosre');
    await tick();
    expect(document.querySelector('[data-cosre-v2]')).toBeNull();
    expect(document.querySelector('.cosre-root--page .cosre-page__body textarea')).not.toBeNull();
    expect(document.getElementById('cosre-side')?.textContent).toContain('Yeni sohbet');
  });

  it('cosreV2 açık → v2 sayfası: kenar çubuğu + Sheet\'siz gövde, ?chat= konuşmayı açar', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'model-x', cosreV2: true } as never);
    const load = vi.spyOn(api, 'aiConversation').mockResolvedValue({
      id: 'c1', title: 't', messages: [{ role: 'user', text: 'eski soru' }, { role: 'assistant', text: 'eski cevap', exchangeId: 'x0' }],
    } as never);
    await renderAt('/cosre?chat=c1');
    await vi.waitFor(() => expect(document.querySelector('[data-cosre-v2].cosre-root--page')).not.toBeNull());
    expect(document.querySelector('[role="dialog"]'), 'Sheet değil, sayfa').toBeNull();
    expect(document.querySelector('.cm-ai-fab'), 'FAB yok').toBeNull();
    expect(document.getElementById('cosre-side')?.textContent).toContain('Yeni sohbet');
    const head = document.querySelector('[data-cosre-v2] .cosre-page__head');
    expect(head?.textContent).toContain('CoSRE');
    expect(head?.textContent).not.toContain('Tam sayfada aç');
    await vi.waitFor(() => expect(document.querySelector('.cosre-page__body')?.textContent).toContain('eski cevap'));
    expect(load).toHaveBeenCalledWith('c1');
    expect(here).toBe('/cosre?chat=c1');
    expect(document.title).toBe('CoSRE');
  });

  // v0.10.1788 — v2 kenar çubuğu gün gruplu (Bugün / Dün / tarih); v1 kovaları ("Son 7 gün") değil.
  it('cosreV2 açık → kenar çubuğu gün grupları', async () => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer' } as never);
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'model-x', cosreV2: true } as never);
    vi.spyOn(api, 'aiConversations').mockResolvedValue([
      { id: 'c3', title: 'üç gün önce', updatedAt: (Date.now() - 3 * 86_400_000) * 1e6, messages: 2 },
    ] as never);
    await renderAt('/cosre');
    await vi.waitFor(() => expect(document.querySelector('[data-cosre-v2] #cosre-side h2')).not.toBeNull());
    const h = document.querySelector('[data-cosre-v2] #cosre-side h2')!.textContent ?? '';
    expect(h).not.toBe('Son 7 gün');
    expect(h).toMatch(/^\d{1,2} \S+/);
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
    expect(chip?.getAttribute('rel')).toBe('noopener noreferrer'); // v0.10.1137 — Referer yok
    const traceA = host.querySelector('a[data-nav]');
    expect(traceA?.getAttribute('href')?.startsWith('/trace')).toBe(true);
    expect(traceA?.getAttribute('target')).toBe('_blank');
    expect(traceA?.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('çekmecede (bağlam yok) iç linkler aynı sekmede kalır', async () => {
    await renderBubble(false);
    const chip = Array.from(host.querySelectorAll('a.ai-link')).find(a => a.textContent?.includes('payment'));
    expect(chip?.getAttribute('target')).toBeNull();
    expect(host.querySelector('a[data-nav]')?.getAttribute('target')).toBeNull();
  });
});
