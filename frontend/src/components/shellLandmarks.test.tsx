// @vitest-environment jsdom
//
// v0.10.1340 — Vercel Web Interface Guidelines, kabuk semantiği (audit R2):
//   (1) her kabuk dalı TAM BİR <main> landmark'ı çizer: kiosk-çıplak ve
//       tam-kromlu dalda `<main id="main">`; /cosre dalında #main bilerek
//       <div> — tek <main> sayfanın kendisi (CopilotChat variant="page"),
//       iç içe iki <main> olmasın;
//   (2) "İçeriğe atla" tam-kromlu kabuğun İLK odaklanabilir öğesi ve var
//       olan bir kimliği (#main) hedefler; tıkta odak #main'e geçer, geçici
//       tabindex blur'da geri alınır, URL'ye hash yazılmaz;
//   (3) bağlantı metni iki katalogda da var (en + tr).
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { AppShell, SKIP_TARGET_ID } from './AppShell';
import { AuthProvider } from './AuthProvider';
import { ConfirmProvider } from './ui/ConfirmDialog';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import { t } from '@/lib/i18n';
import CoSRE from '@/pages/CoSRE';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SHELL_SRC = readFileSync(resolve(__dirname, 'AppShell.tsx'), 'utf8');
// Yorumlar düşülür — bu dosyadaki ve AppShell'deki açıklamalar "<main>" yazıyor.
const code = (s: string) => s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
const count = (s: string, re: RegExp) => (code(s).match(re) ?? []).length;

function branch(from: string, to: string): string {
  const i = SHELL_SRC.indexOf(from);
  const j = SHELL_SRC.indexOf(to, i + from.length);
  expect(i, from).toBeGreaterThan(-1);
  expect(j, to).toBeGreaterThan(i);
  return SHELL_SRC.slice(i, j);
}

describe('AppShell kaynak pini: dal başına tek <main> (v0.10.1340)', () => {
  it('kiosk-çıplak dal: tek <main id="main">', () => {
    const b = branch('if (kioskBare) {', 'if (cosreBare) {');
    expect(count(b, /<main\b/g)).toBe(1);
    expect(count(b, /<main id="main">/g)).toBe(1);
    expect(count(b, /<\/main>/g)).toBe(1);
  });

  it('/cosre dalı: #main <div> kalır (tek <main> sayfanın kendisi)', () => {
    const b = branch('if (cosreBare) {', '<div id="app">\n');
    expect(count(b, /<main\b/g)).toBe(0);
    expect(count(b, /<div id="main">/g)).toBe(1);
    const chat = readFileSync(resolve(__dirname, 'CopilotChat.tsx'), 'utf8');
    expect(count(chat, /<main\b/g), 'CopilotChat page varyantı tek <main> çizer').toBe(1);
  });

  it('tam-kromlu dal: tek <main id="main">, SkipToContent Sidebar\'dan ÖNCE', () => {
    const i = SHELL_SRC.lastIndexOf('<div id="app">');
    const b = code(SHELL_SRC.slice(i));
    expect((b.match(/<main\b/g) ?? []).length).toBe(1);
    expect((b.match(/<main id="main">/g) ?? []).length).toBe(1);
    expect(b.indexOf('<SkipToContent />')).toBeGreaterThan(-1);
    expect(b.indexOf('<SkipToContent />')).toBeLessThan(b.indexOf('<Sidebar />'));
    expect(code(SHELL_SRC).match(/<div id="main">/g) ?? [], 'yalnız /cosre dalında div').toHaveLength(1);
  });
});

describe('İçeriğe atla metni (v0.10.1340)', () => {
  it('iki katalogda da tanımlı', () => {
    expect(t('shell.skipToContent', 'en')).toBe('Skip to content');
    expect(t('shell.skipToContent', 'tr')).toBe('İçeriğe atla');
  });
});

let host: HTMLDivElement;
let root: Root;
let here = '';
function Probe() { const l = useLocation(); here = l.pathname + l.search + l.hash; return null; }
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function renderAt(path: string) {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ConfirmProvider>
            <AuthProvider>
              <Probe />
              <Routes>
                <Route element={<AppShell />}>
                  <Route path="/services" element={<div data-testid="page"><a href="#x">ilk sayfa linki</a></div>} />
                  <Route path="/trace" element={<div data-testid="kiosk">kiosk</div>} />
                  <Route path="/cosre" element={<CoSRE />} />
                </Route>
              </Routes>
            </AuthProvider>
          </ConfirmProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
  await tick();
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

beforeEach(() => {
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  Element.prototype.scrollIntoView = function noop() {};
  class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  sessionStorage.clear();
  vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no network in test'))));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'admin' } as never);
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

describe('AppShell render: landmark + atlama bağlantısı (v0.10.1340)', () => {
  it('tam-kromlu kabuk: tek <main id="main">; ilk odak durağı "Skip to content" → #main', async () => {
    await renderAt('/services');
    expect(document.querySelector('[data-testid="page"]')).not.toBeNull();
    const mains = document.querySelectorAll('main');
    expect(mains).toHaveLength(1);
    expect(mains[0].id).toBe(SKIP_TARGET_ID);
    const app = document.getElementById('app')!;
    const first = app.querySelector<HTMLAnchorElement>(FOCUSABLE);
    expect(first?.className).toBe('skip-link');
    expect(first?.textContent).toBe('Skip to content');
    const href = first!.getAttribute('href')!;
    expect(href).toBe(`#${SKIP_TARGET_ID}`);
    expect(document.getElementById(href.slice(1)), 'hedef kimlik DOM\'da').toBe(mains[0]);
    // Sidebar'ın bağlantıları atlama bağlantısından SONRA gelir.
    const sidebar = document.getElementById('sidebar');
    expect(sidebar).not.toBeNull();
    expect(first!.compareDocumentPosition(sidebar!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    const before = here;
    await act(async () => { first!.click(); });
    expect(document.activeElement).toBe(mains[0]);
    expect(here, 'hash URL\'ye yazılmaz').toBe(before);
    // Geçici tabindex: odak çıkınca geri alınır (kalıcı odak durağı değil).
    expect(mains[0].getAttribute('tabindex')).toBe('-1');
    await act(async () => { mains[0].blur(); });
    expect(mains[0].hasAttribute('tabindex')).toBe(false);
  });

  it('kiosk-çıplak kabuk: tek <main id="main">, atlama bağlantısı yok', async () => {
    await renderAt('/trace?kiosk=1');
    expect(document.querySelector('.kiosk-bare')).not.toBeNull();
    const mains = document.querySelectorAll('main');
    expect(mains).toHaveLength(1);
    expect(mains[0].id).toBe('main');
    expect(document.querySelector('.skip-link')).toBeNull();
  });

  it('/cosre kabuğu: tek <main> (sayfanınki), #main div', async () => {
    await renderAt('/cosre');
    expect(document.querySelector('.cosre-bare')).not.toBeNull();
    const mains = document.querySelectorAll('main');
    expect(mains).toHaveLength(1);
    expect(mains[0].className).toContain('cosre-page__body');
    expect(document.getElementById('main')?.tagName).toBe('DIV');
  });
});
