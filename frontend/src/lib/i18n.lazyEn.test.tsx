// @vitest-environment jsdom
// v0.10.2307 — İngilizce katalog tembel: yüklenene dek etkin dil 'tr'
// (İngilizce istense bile), yüklenince abonelere haber verilir; Türkçe yol
// import()'u hiç tetiklemez. Kurulum (src/test/i18nEnSetup.ts) kataloğu
// kaydettiği için her test önce registerEnCatalog(null) ile boşaltır.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { EN } from './i18n.en';
import { currentLang, loadInitialCatalog, registerEnCatalog, setUserLang, t, useLang } from './i18n';
import { settleUntil } from '../test/actWait';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let renders: string[] = [];
function Probe() {
  const lang = useLang();
  renders.push(lang);
  return <span>{lang}</span>;
}

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  renders = [];
  registerEnCatalog(null);
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ language: 'tr' }), { status: 200 })));
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  registerEnCatalog(EN); // diğer test dosyalarıyla aynı başlangıç
});

describe('lazy English catalog', () => {
  it('English requested → tr until loaded, then en and subscribers re-render', async () => {
    localStorage.setItem('coremetry.lang', 'en');
    expect(currentLang()).toBe('tr');
    expect(t('nav.services', 'tr')).not.toBe(EN['nav.services']);
    act(() => root.render(<Probe />));
    expect(renders[0]).toBe('tr');
    await settleUntil(() => host.textContent === 'en', 'Probe re-renders in English');
    expect(renders).toContain('tr');
    expect(renders[renders.length - 1]).toBe('en');
    expect(currentLang()).toBe('en');
  });

  it('setUserLang("en") starts the import and notifies subscribers', async () => {
    localStorage.setItem('coremetry.lang', 'tr');
    act(() => root.render(<Probe />));
    expect(host.textContent).toBe('tr');
    act(() => setUserLang('en'));
    expect(currentLang()).toBe('tr');
    await settleUntil(() => host.textContent === 'en', 'setUserLang(en) loads the catalog');
    expect(currentLang()).toBe('en');
  });

  it('Turkish path never triggers the import', async () => {
    localStorage.setItem('coremetry.lang', 'tr');
    act(() => root.render(<Probe />));
    // Branding fetch (tr) + effects settle; an import would register EN.
    await act(async () => { await new Promise(r => setTimeout(r, 50)); });
    expect(host.textContent).toBe('tr');
    expect(renders.every(l => l === 'tr')).toBe(true);
    // EN not registered: an explicit English lookup of a TR-covered key falls back.
    expect(t('nav.services', 'en')).not.toBe(EN['nav.services']);
    expect(currentLang()).toBe('tr');
  });

  // v0.10.2400 — açılış: kullanıcı seçimi yoksa son görülen marka dili
  // (branding fetch'inin yazdığı ipucu) ilk çizimden önce yüklenir.
  it('loadInitialCatalog: brand hint en → English before first render', async () => {
    localStorage.setItem('coremetry.brandLang', 'en');
    expect(t('nav.services', 'en')).not.toBe(EN['nav.services']);
    await loadInitialCatalog();
    expect(t('nav.services', 'en')).toBe(EN['nav.services']);
  });
});

// v0.10.2400 — TR ve EN katalogları ilk yükte değil: i18n.ts onları yalnız
// dinamik import() ile alır; main.tsx ilk çizimden önce loadInitialCatalog'u bekler.
describe('catalogs are lazy (pin)', () => {
  it('i18n.ts has no static catalog import; main.tsx awaits loadInitialCatalog', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const src = readFileSync(resolve(__dirname, 'i18n.ts'), 'utf8');
    expect(src).not.toMatch(/from\s+'\.\/i18n\.(tr|en|common)'/);
    expect(src).toContain("import('./i18n.tr')");
    expect(src).toContain("import('./i18n.en')");
    const main = readFileSync(resolve(__dirname, '..', 'main.tsx'), 'utf8');
    expect(main).toContain('void loadInitialCatalog().then(mount);');
  });
});
