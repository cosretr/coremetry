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
import type { WikiConfig, WikiConfigView, WikiSyncStatus } from '@/lib/types';

const h = vi.hoisted(() => ({
  get: vi.fn<() => Promise<WikiConfigView>>(),
  put: vi.fn<(c: WikiConfig) => Promise<WikiConfigView>>(),
  sync: vi.fn<() => Promise<{ queued: boolean; status: WikiSyncStatus }>>(),
  status: vi.fn<() => Promise<WikiSyncStatus>>(),
}));
vi.mock('@/lib/api', () => ({
  api: {
    getWikiConfig: () => h.get(),
    putWikiConfig: (c: WikiConfig) => h.put(c),
    syncWiki: () => h.sync(),
    getWikiStatus: () => h.status(),
  },
}));

import { WikiKnowledgeSection } from './WikiKnowledgeSection';
import { parseList, wikiBody, wikiStatusSummary, syncPending } from './wikiKnowledge';

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

async function mount(v: WikiConfigView): Promise<HTMLElement> {
  h.get.mockResolvedValue(v);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<WikiKnowledgeSection />); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.get.mockReset(); h.put.mockReset(); h.sync.mockReset(); h.status.mockReset();
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
  it('wikiBody boş sayıyı varsayılana (0) çevirir, canlı arama kapalıysa bayrak', () => {
    expect(wikiBody(true, '', 'A', '', 'abc', false)).toEqual({
      enabled: true, projects: [], wikis: ['A'], intervalMin: 0, maxPages: 0, disableLiveSearch: true,
    });
    expect(wikiBody(false, 'P', '', '20', '100', true).disableLiveSearch).toBeUndefined();
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
});
