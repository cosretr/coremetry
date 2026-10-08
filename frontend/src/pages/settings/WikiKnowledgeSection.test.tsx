// @vitest-environment jsdom
//
// WikiKnowledgeSection — v0.10.1122 ("karma"). Bilgi (RAG) sekmesinin Azure
// DevOps Wiki bölümü: ayar formu, durum kartı, "Şimdi senkronize et".
// Sözleşme:
//   • kimlik alanı YOK (PAT Kod entegrasyonu'nda) — form şifre kutusu çizmez;
//   • kaydet gövdesi izin listelerini satır/virgülden ayrıştırır, boş sayı = 0 (varsayılan);
//   • durum kartı indeks sayılarını ve hataları gösterir; bağlantı yoksa uyarı;
//   • "Şimdi senkronize et" POST eder ve "istendi" durumunu gösterir.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { WikiConfig, WikiConfigView, WikiPagesPage, WikiSyncStatus, WikiTestSearchResult } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<WikiConfigView>>(),
  put: vi.fn<(c: WikiConfig) => Promise<WikiConfigView>>(),
  sync: vi.fn<() => Promise<{ queued: boolean; status: WikiSyncStatus }>>(),
  status: vi.fn<() => Promise<WikiSyncStatus>>(),
  test: vi.fn<(q: string) => Promise<WikiTestSearchResult>>(),
  pages: vi.fn<(q: string, off: number, lim: number) => Promise<WikiPagesPage>>(),
  purge: vi.fn<() => Promise<{ purged: boolean; status: WikiSyncStatus }>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getWikiConfig: () => h.get(),
    putWikiConfig: (c: WikiConfig) => h.put(c),
    syncWiki: () => h.sync(),
    getWikiStatus: () => h.status(),
    testWikiSearch: (q: string) => h.test(q),
    getWikiPages: (q: string, off: number, lim: number) => h.pages(q, off, lim),
    purgeWikiIndex: () => h.purge(),
  },
}));

import { WikiKnowledgeSection } from './WikiKnowledgeSection';
import { parseList, wikiBody, wikiStatusSummary, syncPending, searchLabel, effectiveMode, pagesEmptyText, liveOutcomeText } from './wikiKnowledge';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

const STATUS: WikiSyncStatus = {
  lastStartedAt: 1_760_000_000_000, lastFinishedAt: 1_760_000_060_000, lastOk: false, durationMs: 60_000,
  projects: 2, wikis: 3, pages: 120, fetched: 4, unchanged: 116, deleted: 1,
  indexedPages: 119, indexedChunks: 812, errors: ['Platform/Platform.wiki/Runbooks: http 500'],
  search: 'unavailable', running: false,
};

const VIEW: WikiConfigView = {
  available: true, devopsConfigured: true, embedding: false,
  config: { enabled: true, projects: ['Platform'], intervalMin: 30 },
  status: STATUS, defaults: { intervalMin: 60, minIntervalMin: 15, maxPages: 5000 },
};

const EMPTY_PAGES: WikiPagesPage = { rows: [], total: 0, offset: 0, limit: 100, preview: false };

async function mount(v: WikiConfigView): Promise<HTMLElement> {
  h.get.mockResolvedValue(v);
  if (!h.pages.getMockImplementation()) h.pages.mockResolvedValue(EMPTY_PAGES);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><ConfirmProvider><WikiKnowledgeSection /></ConfirmProvider></MemoryRouter>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset(); h.put.mockReset(); h.sync.mockReset(); h.status.mockReset();
  h.test.mockReset(); h.pages.mockReset(); h.purge.mockReset();
});

function button(el: HTMLElement, text: string): HTMLButtonElement {
  const b = Array.from(el.querySelectorAll('button')).find(x => x.textContent?.includes(text));
  expect(b, `${text} düğmesi yok`).toBeTruthy();
  return b as HTMLButtonElement;
}

describe('wikiKnowledge helpers', () => {
  it('parseList satır/virgül ayrıştırır, tekilleştirir', () => {
    expect(parseList('Platform, platform\n Payments/Payments.wiki \n\n/Docs/')).toEqual(['Platform', 'Payments/Payments.wiki', 'Docs']);
  });
  it('wikiBody boş sayıyı varsayılana (0) çevirir ve modu taşır (v0.10.1124)', () => {
    expect(wikiBody(true, '', 'A', '', 'abc', 'sync')).toEqual({
      enabled: true, projects: [], wikis: ['A'], intervalMin: 0, maxPages: 0, mode: 'sync',
    });
    expect(wikiBody(false, 'P', '', '20', '100', 'live').disableLiveSearch).toBeUndefined();
  });
  it('effectiveMode: eski bayrak = sync, boş = hybrid', () => {
    expect(effectiveMode(undefined)).toBe('hybrid');
    expect(effectiveMode({ enabled: true, disableLiveSearch: true })).toBe('sync');
    expect(effectiveMode({ enabled: true, mode: 'live', disableLiveSearch: true })).toBe('live');
  });
  it('arama etiketi son denemeyi (her pod) ve canlı modda Search yokluğunu söyler', () => {
    expect(searchLabel('available', { state: 'available', at: 1, hits: 3, apiVersion: '5.0-preview.1' }))
      .toBe('Azure DevOps Search: kullanılabilir — son deneme: 3 sonuç · api-version 5.0-preview.1');
    expect(searchLabel('unavailable', undefined, 'live')).toContain('senkron modunu kullanın');
    expect(searchLabel('unknown', { state: 'error', at: 1, hits: 0, class: 'bad_request', httpStatus: 400 }))
      .toContain('son deneme hata (bad_request · http 400)');
    expect(searchLabel('available', undefined, 'sync')).toContain('kapalı');
  });
  it('canlı mod kartı ve boş liste metni', () => {
    expect(wikiStatusSummary(STATUS, 'live').text).toContain('Canlı mod — senkron yok');
    expect(pagesEmptyText(STATUS, 'hybrid')).toMatch(/^Henüz indekslenmiş sayfa yok — senkron durumu: /);
  });
  it('durum özeti ve ton', () => {
    expect(wikiStatusSummary(undefined).tone).toBe('idle');
    const s = wikiStatusSummary(STATUS);
    expect(s.text).toContain('119 sayfa · 812 parça');
    expect(s.tone).toBe('err');
    expect(wikiStatusSummary({ ...STATUS, errors: [], lastOk: true }).tone).toBe('ok');
    expect(syncPending({ ...STATUS, requestedAt: STATUS.lastStartedAt! + 1 })).toBe(true);
    expect(syncPending(STATUS)).toBe(false);
  });
});

describe('WikiKnowledgeSection', () => {
  it('formu ve durum kartını çizer; PAT alanı yok', async () => {
    const el = await mount(VIEW);
    expect(el.textContent).toContain('Azure DevOps Wiki');
    expect(el.querySelector('input[type="password"]')).toBeNull();
    const card = el.querySelector('[data-testid="wiki-status"]')!;
    expect(card.textContent).toContain('119 sayfa · 812 parça');
    expect(card.textContent).toContain('http 500');
    expect(card.textContent).toContain('sunucuda yok');
    const ta = el.querySelectorAll('textarea');
    expect((ta[0] as HTMLTextAreaElement).value).toBe('Platform');
  });

  it('bağlantı yoksa uyarı gösterir ve senkron düğmesi kapalı', async () => {
    const el = await mount({ ...VIEW, devopsConfigured: false });
    expect(el.textContent).toContain('Kod entegrasyonu');
    expect(button(el, 'Şimdi senkronize et').disabled).toBe(true);
  });

  it('kaydet ayrıştırılmış gövdeyi PUT eder', async () => {
    h.put.mockImplementation(async c => ({ ...VIEW, config: c }));
    const el = await mount(VIEW);
    await act(async () => { button(el, 'Kaydet').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.put).toHaveBeenCalledTimes(1);
    const body = h.put.mock.calls[0][0];
    expect(body.enabled).toBe(true);
    expect(body.projects).toEqual(['Platform']);
    expect(body.intervalMin).toBe(30);
    expect(el.textContent).toContain('Kaydedildi');
  });

  it('Şimdi senkronize et POST eder ve isteği gösterir', async () => {
    h.sync.mockResolvedValue({ queued: true, status: { ...STATUS, requestedAt: STATUS.lastStartedAt! + 5, requestedBy: 'ops@example.test' } });
    const el = await mount(VIEW);
    await act(async () => { button(el, 'Şimdi senkronize et').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.sync).toHaveBeenCalledTimes(1);
    expect(el.textContent).toContain('Senkron istendi (ops@example.test)');
  });

  it('servis yoksa bölüm çizilmez', async () => {
    const el = await mount({ available: false });
    expect(el.textContent).toBe('');
  });

  it('mod seçimi PUT gövdesine girer; canlı modda senkron düğmesi kapalı ve uyarı görünür', async () => {
    h.put.mockImplementation(async c => ({ ...VIEW, config: c, mode: c.mode, modeWarning: 'Azure DevOps Search henüz doğrulanmadı' }));
    const el = await mount(VIEW);
    expect(el.querySelector('[data-testid="wiki-mode-help"]')!.textContent).toContain('senkron');
    await act(async () => { button(el, 'Yalnız canlı arama').click(); });
    expect(el.querySelector('[data-testid="wiki-mode-help"]')!.textContent).toContain('1–3 sn');
    await act(async () => { button(el, 'Kaydet').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.put.mock.calls[0][0].mode).toBe('live');
    expect(el.textContent).toContain('uyarı: Azure DevOps Search henüz doğrulanmadı');
    expect(el.querySelector('[data-testid="wiki-mode-warning"]')).toBeTruthy();
    expect(button(el, 'Şimdi senkronize et').disabled).toBe(true);
    expect(el.querySelector('[data-testid="wiki-status"]')!.textContent).toContain('Canlı mod — senkron yok');
  });

  it('Aramayı test et: canlı sonucu, sürümü ve taban kararlarını gösterir', async () => {
    const res: WikiTestSearchResult = {
      mode: 'hybrid', terms: ['svc-orders', 'restart'], liveQueries: ['svc-orders restart', 'svc-orders OR restart'], stale: true,
      local: [],
      live: { attempted: true, queryMode: 'or', info: { class: 'ok', httpStatus: 200, apiVersion: '5.0-preview.1', hits: 1, tried: 4 },
        results: 1, read: 1, hits: 2 },
      final: [{ project: 'Platform', wiki: 'Platform.wiki', path: '/Runbooks/Restart svc-orders', title: 'Restart svc-orders',
        score: 0.66, live: true, snippet: 'kubectl rollout restart', passesRag: true, passesWikiTier: true }],
      floors: { rag: 0.5, wikiTier: 0.3 }, verdict: 'Sonuç cevaba girer (her iki taban da geçiliyor).',
    };
    h.test.mockResolvedValue(res);
    expect(liveOutcomeText(res)).toBe('Canlı arama: 1 sonuç (OR sorgusu), 1 sayfa okundu · api-version 5.0-preview.1');
    const el = await mount(VIEW);
    const box = el.querySelector('[data-testid="wiki-test-search"]') as HTMLElement;
    const input = box.querySelector('input') as HTMLInputElement;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'svc-orders nasıl restart edilir');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { button(box, 'Aramayı test et').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.test).toHaveBeenCalledWith('svc-orders nasıl restart edilir');
    expect(box.textContent).toContain('Sonuç cevaba girer');
    expect(box.querySelector('[data-testid="wiki-test-live"]')!.textContent).toContain('api-version 5.0-preview.1');
    expect(box.textContent).toContain('Restart svc-orders');
  });

  it('indeksteki sayfalar: bağlantılı başlık, sunucu sayfalama, yönetici önizlemesi', async () => {
    h.pages.mockResolvedValue({
      rows: [{ project: 'Platform', wiki: 'Platform.wiki', wikiId: 'w1', path: '/Runbooks/Restart svc-orders', title: 'Restart svc-orders',
        url: 'https://devops.example.test/x', chunks: 3, updatedAt: '2026-10-08T10:00:00Z', preview: '# Yeniden başlatma\nkubectl rollout restart' }],
      total: 250, offset: 0, limit: 100, preview: true,
    });
    const el = await mount(VIEW);
    await act(async () => { await new Promise(r => setTimeout(r, 400)); });
    const tbl = el.querySelector('[data-testid="wiki-pages"]') as HTMLElement;
    expect(h.pages).toHaveBeenCalledWith('', 0, 100);
    const a = tbl.querySelector('a[href="https://devops.example.test/x"]') as HTMLAnchorElement;
    expect(a.textContent).toBe('Restart svc-orders');
    expect(a.getAttribute('rel')).toContain('noopener');
    expect(tbl.textContent).toContain('250 sayfa');
    expect(tbl.querySelector('[data-testid="wiki-page-preview"]')).toBeNull();
    const disc = tbl.querySelector('button[aria-label="Restart svc-orders önizlemesi"]') as HTMLButtonElement;
    await act(async () => { disc.click(); });
    expect(tbl.querySelector('[data-testid="wiki-page-preview"]')!.textContent).toContain('kubectl rollout restart');
    await act(async () => { button(tbl, 'Sonraki').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.pages).toHaveBeenLastCalledWith('', 100, 100);
  });

  it('önizlemesiz (yönetici değil) satırda açılım yok; boş liste senkron durumunu söyler', async () => {
    h.pages.mockResolvedValue(EMPTY_PAGES);
    const el = await mount(VIEW);
    await act(async () => { await new Promise(r => setTimeout(r, 400)); });
    const tbl = el.querySelector('[data-testid="wiki-pages"]') as HTMLElement;
    expect(tbl.textContent).toContain('Henüz indekslenmiş sayfa yok — senkron durumu:');
  });

  it('İndeksi temizle: onay ister, POST eder; canlı modda eski indeks notu', async () => {
    h.purge.mockResolvedValue({ purged: true, status: { ...STATUS, indexedPages: 0, indexedChunks: 0 } });
    const el = await mount({ ...VIEW, mode: 'live', config: { ...VIEW.config!, mode: 'live' } });
    expect(el.querySelector('[data-testid="wiki-live-leftover"]')!.textContent).toContain('119 sayfa');
    await act(async () => { button(el, 'İndeksi temizle').click(); });
    expect(h.purge).not.toHaveBeenCalled();
    const ok = Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Temizle');
    expect(ok, 'onay diyaloğu').toBeTruthy();
    await act(async () => { ok!.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(h.purge).toHaveBeenCalledTimes(1);
    expect(el.textContent).toContain('İndeks temizlendi');
    expect(el.querySelector('[data-testid="wiki-live-leftover"]')).toBeNull();
  });
});
