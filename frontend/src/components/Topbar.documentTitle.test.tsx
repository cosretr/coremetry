// @vitest-environment jsdom
//
// v0.10.1340 — Vercel Web Interface Guidelines, kabuk semantiği (audit R2):
// Topbar'ın sayfa başlığı sekmeye `${title} · ${marka}` olarak yansır.
//   · marka /api/branding'den (browserTitle) — fetch sonradan çözülse de,
//     Settings kaydı (invalidateBranding) sonrası da bileşim korunur;
//   · unmount yalnız KENDİ başlığını temizler (A→B gezintisinde B kalır);
//     Topbar'sız sayfa marka başlığında kalır;
//   · /cosre'nin "CoSRE" geçersiz kılması kazanır — marka fetch'i SONRA
//     çözülse bile (eski brandTitle-bağımlı effect varsayılan markada
//     yeniden koşmuyor, sekme "Coremetry"ye dönüyordu).
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

// Sohbetin kendisi bu testin konusu değil — yalnız CoSRE sayfasının başlık davranışı.
vi.mock('@/components/CopilotChat', () => ({ CopilotChat: () => <main data-testid="chat" /> }));

import { Topbar } from './Topbar';
import CoSRE from '@/pages/CoSRE';
import { composeDocumentTitle, invalidateBranding, setDocumentTitleOverride, setPageTitle } from '@/lib/branding';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let brand: Record<string, unknown> = {};
// Branding "mock": /api/branding cevabı; diğer istekler ağsız.
let releaseBranding: (() => void) | null = null;
let holdBranding = false;

function stubFetch() {
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (String(url).startsWith('/api/branding')) {
      const res = { ok: true, json: () => Promise.resolve(brand) };
      if (!holdBranding) return Promise.resolve(res);
      return new Promise(r => { releaseBranding = () => r(res); });
    }
    return Promise.reject(new Error('no network in test'));
  }));
}

function ui(node: React.ReactNode) {
  return (
    <MemoryRouter initialEntries={['/services']}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        {node}
      </QueryClientProvider>
    </MemoryRouter>
  );
}
const render = (node: React.ReactNode) => act(async () => { root.render(ui(node)); });
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 10)); });

beforeEach(async () => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  brand = { browserTitle: 'Acme APM' };
  holdBranding = false;
  releaseBranding = null;
  stubFetch();
  setDocumentTitleOverride(null);
  setPageTitle('');
  await invalidateBranding();
  document.title = 'Acme APM';
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setDocumentTitleOverride(null);
  setPageTitle('');
  vi.unstubAllGlobals();
});

describe('composeDocumentTitle (v0.10.1340)', () => {
  it.each([
    ['Services', 'Coremetry', 'Services · Coremetry'],
    ['  Logs ', 'Acme APM', 'Logs · Acme APM'],
    ['', 'Acme APM', 'Acme APM'],
    ['Coremetry', 'Coremetry', 'Coremetry'], // sayfa = marka → tekrar yok
    ['Services', '', 'Services · Coremetry'], // boş marka → varsayılan
  ])('%j + %j → %j', (page, b, want) => {
    expect(composeDocumentTitle(page, b)).toBe(want);
  });
});

describe('Topbar → document.title (v0.10.1340)', () => {
  it('sayfa başlığı · marka; başlık değişince güncellenir; unmount marka başlığına döner', async () => {
    await render(<Topbar title="Services" />);
    expect(document.title).toBe('Services · Acme APM');
    await render(<Topbar title="Endpoints" />);
    expect(document.title).toBe('Endpoints · Acme APM');
    await render(<div>Topbar'sız sayfa</div>);
    expect(document.title).toBe('Acme APM');
  });

  it('A→B gezintisi: eski Topbar\'ın temizliği yenisinin başlığını silmez', async () => {
    await render(<div key="a"><Topbar title="Services" /></div>);
    await render(<div key="b"><Topbar title="Traces" /></div>);
    expect(document.title).toBe('Traces · Acme APM');
  });

  it('marka fetch\'i SONRA çözülür ve Settings kaydı (invalidateBranding) bileşimi korur', async () => {
    holdBranding = true;
    brand = { browserTitle: 'Late Brand' };
    const pending = invalidateBranding();
    await render(<Topbar title="Logs" />);
    expect(document.title).toBe('Logs · Coremetry'); // henüz varsayılan marka
    await act(async () => { releaseBranding?.(); await pending; });
    expect(document.title).toBe('Logs · Late Brand');
    holdBranding = false;
    brand = { appName: 'Saved Brand' }; // browserTitle boş → appName'e ayna
    await act(async () => { await invalidateBranding(); });
    expect(document.title).toBe('Logs · Saved Brand');
  });
});

describe('/cosre başlık geçersiz kılması kazanır (v0.10.1340)', () => {
  it('Topbar\'lı sayfadan /cosre\'ye: "CoSRE"; geç çözülen marka ezmez; çıkışta marka', async () => {
    await render(<Topbar title="Services" />);
    expect(document.title).toBe('Services · Acme APM');
    holdBranding = true;
    brand = { browserTitle: 'Coremetry' }; // varsayılan markayla aynı — eski hata tam bu durumdu
    const pending = invalidateBranding();
    await render(<CoSRE />);
    expect(document.title).toBe('CoSRE');
    await act(async () => { releaseBranding?.(); await pending; });
    await flush();
    expect(document.title).toBe('CoSRE');
    await render(<div>Topbar'sız sayfa</div>);
    expect(document.title).toBe('Coremetry');
  });

  it('geçersiz kılma açıkken bir Topbar başlığı yazsa da "CoSRE" kalır', async () => {
    await render(<CoSRE />);
    act(() => setPageTitle('Services'));
    expect(document.title).toBe('CoSRE');
  });
});
