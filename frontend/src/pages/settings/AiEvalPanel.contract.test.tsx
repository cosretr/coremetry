// @vitest-environment jsdom
// AiEvalPanel.contract.test.tsx — v0.10.940 (Settings › CoSRE › Değerlendirme).
// Ağ mock'lu sözleşme: başlık varsayılan profili + ayrılan yüzeyi söyler;
// çip seçimi Koş'un gövdesidir; koşu sürerken ilerleme çubuğu (role=
// progressbar), Koş kapalı + sebep, Durdur; sunucunun 409 metni aynen
// görünür; tablolar standartta (Kaldı yalnız >0'da cell-err, sayı hücresi
// mono DEĞİL, boş değer soluk "—", boşken başlık kalır); kalan vaka satırı
// gerçek ?case= linki ve çekmece bölümleri; /ai bağlantısı ?source=evalset.
// v0.10.940 — başlık profili kataloğun defaultProfileId'si (son koşunun
// bayat profili değil); katalog alt sekme dönüşünde ve başlatma hatasında
// tazelenir; kıyas ipucu duruma göre (süren / başarısız).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation, useNavigate, useSearchParams, type NavigateFunction } from 'react-router-dom';
import type { EvalCaseResult, EvalRunSummary, EvalsetCatalog } from '@/lib/types';

const CATALOG: EvalsetCatalog = {
  ready: true, appVersion: 'v0.10.940', promptVersion: '7a1a932c0d3e9f21', total: 29, defaultProfileId: 'yerel',
  surfaces: [
    { surface: 'IntentClassify', cases: 20, profileId: 'kucuk', profileLabel: 'Küçük', provider: 'openai', model: 'qwen3.5-2b', baseUrl: 'http://ollama:11434/v1' },
    { surface: 'Problem', cases: 6, profileId: 'yerel', profileLabel: 'Yerel gemma', provider: 'openai', model: 'gemma4-31b', baseUrl: 'http://ollama:11434/v1' },
    { surface: 'SLOBurn', cases: 3, profileId: 'yerel', profileLabel: 'Yerel gemma', provider: 'openai', model: 'gemma4-31b', baseUrl: 'http://ollama:11434/v1' },
  ],
};

const run = (id: string, over: Partial<EvalRunSummary> = {}): EvalRunSummary => ({
  id, status: 'done', startedAt: '2026-09-26T18:40:00Z', updatedAt: '2026-09-26T18:50:00Z', finishedAt: '2026-09-26T18:50:00Z',
  startedBy: 'admin@example.com', appVersion: 'v0.10.940', promptVersion: '7a1a932c0d3e9f21', model: 'gemma4-31b', profileId: 'yerel',
  surfaces: [], total: 29, done: 29, pass: 26, fail: 3, skipped: 0, rubricMean: 0.91, error: '',
  bySurface: [
    { surface: 'IntentClassify', cases: 20, pass: 19, fail: 1, skipped: 0, unknownEntities: null, avgLatencyMs: 1400 },
    { surface: 'Problem', cases: 6, pass: 6, fail: 0, skipped: 0, unknownEntities: 0, avgLatencyMs: 5230 },
  ],
  ...over,
});

const kase = (id: string, over: Partial<EvalCaseResult> = {}): EvalCaseResult => ({
  id, surface: 'IntentClassify', why: 'küçük model niyeti kaçırmasın', ok: true, skipped: false, skipReason: '', latencyMs: 1400,
  fails: [], unknownEntities: 0, rubricTotal: 1, profileId: 'kucuk', model: 'qwen3.5-2b',
  input: 'checkout servisi nerede?', inputTruncated: false, answer: '{"intent":"none"}', answerTruncated: false,
  error: '', expect: { intent: 'find_entity', intentService: 'checkout' },
  ...over,
});

const IDLE_RUNS = [run('ev-new', { pass: 26 }), run('ev-old', { pass: 27, startedAt: '2026-09-25T10:00:00Z', finishedAt: '2026-09-25T10:10:00Z' })];
const DETAIL = {
  run: IDLE_RUNS[0],
  cases: [
    kase('intent-checkout-find', { ok: false, fails: ['intent: want find_entity got none'], answerTruncated: true }),
    kase('intent-ok'),
  ],
};

const api = vi.hoisted(() => ({
  aiEvalsetCatalog: vi.fn(async (..._a: unknown[]) => ({}) as unknown),
  aiEvalsetRuns: vi.fn(async (..._a: unknown[]) => ({ runs: [] as unknown[] })),
  aiEvalsetRun: vi.fn(async (..._a: unknown[]) => ({}) as unknown),
  aiEvalsetStartRun: vi.fn(async (..._a: unknown[]) => ({}) as unknown),
  aiEvalsetCancelRun: vi.fn(async (..._a: unknown[]) => ({ ok: true })),
  aiEvalsetCompare: vi.fn(async (..._a: unknown[]) => ({}) as unknown),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiEvalPanel } from './AiEvalPanel';

let host: HTMLDivElement | null = null; let root: Root | null = null;
let where = '';
let nav: NavigateFunction | null = null;
function Where() { const l = useLocation(); where = l.pathname + l.search; nav = useNavigate(); return null; }
// Alt sekme kabuğu: `?tab=eval` dışında panel bağlı değil (AiTab gibi).
function Tabbed() { const [sp] = useSearchParams(); return sp.get('tab') === 'eval' ? <AiEvalPanel /> : null; }
function render(node: ReactNode, entry = '/settings/ai?tab=eval'): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(<MemoryRouter initialEntries={[entry]}><QueryClientProvider client={qc}>{node}<Where /></QueryClientProvider></MemoryRouter>);
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  for (const f of Object.values(api)) f.mockReset();
  try { localStorage.clear(); } catch { /* jsdom */ }
});
// Katalog + liste paralel, detay listeye bağlı (seçili koşu kimliği listeden):
// tek tur yetmez — birkaç tur bekle.
const tick = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); });
};
const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;
const tables = (el: HTMLElement) => Array.from(el.querySelectorAll('table'));
const cellsOf = (tr: Element) => Array.from(tr.querySelectorAll('td'));

function idle() {
  api.aiEvalsetCatalog.mockResolvedValue(CATALOG);
  api.aiEvalsetRuns.mockResolvedValue({ runs: IDLE_RUNS });
  api.aiEvalsetRun.mockResolvedValue(DETAIL);
}

describe('AiEvalPanel', () => {
  it('başlık: varsayılan profil kataloğun defaultProfileId\'sinden; ayrılan yüzey tek satırda; /ai linki kaynaklı', async () => {
    idle();
    const el = render(<AiEvalPanel />);
    await tick();
    const kv = el.querySelector('dl.keyval')!;
    expect(kv.textContent).toContain('Yerel gemma (openai)');
    expect(kv.textContent).toContain('gemma4-31b');
    expect(kv.textContent).toContain('7a1a932c0d3e9f21');
    expect(el.textContent).toContain('Farklı profile yönlenen: IntentClassify → Küçük (qwen3.5-2b)');
    const link = Array.from(el.querySelectorAll('a')).find(a => a.textContent?.includes("/ai'da, Kaynak: Değerlendirme"))!;
    expect(link.getAttribute('href')).toBe('/ai?source=evalset');
  });

  it('başlık: son koşunun bayat profili değil, kataloğun ŞİMDİKİ varsayılanı', async () => {
    // v0.10.940 — son koşular "yerel" ile yapıldı, operatör sonra kardeş
    // sekmede varsayılanı "kucuk"a çevirdi. Başlık bir sonraki koşunun
    // profilini söyler: Küçük; artık ayrılan yüzeyler Problem + SLOBurn.
    idle();
    api.aiEvalsetCatalog.mockResolvedValue({ ...CATALOG, defaultProfileId: 'kucuk' });
    const el = render(<AiEvalPanel />);
    await tick();
    expect(IDLE_RUNS[0].profileId).toBe('yerel');
    const kv = el.querySelector('dl.keyval')!;
    expect(kv.textContent).toContain('Küçük (openai)');
    expect(kv.textContent).toContain('qwen3.5-2b');
    expect(kv.textContent).not.toContain('Yerel gemma');
    expect(el.textContent).toContain('Farklı profile yönlenen: Problem → Yerel gemma (gemma4-31b) · SLOBurn → Yerel gemma (gemma4-31b)');
  });

  it('katalog alt sekme dönüşünde (yeniden bağlanınca) staleTime içinde de tazelenir', async () => {
    // v0.10.940 — refetchOnMount 'always': kardeş sekmede kaydedilen ayar
    // (ready / varsayılan profil) 60 s beklemeden başlığa düşsün. Liste
    // (staleTime 8 s) aynı dönüşte yeniden okunmaz — fark bilerek.
    idle();
    render(<Tabbed />);
    await tick();
    expect(api.aiEvalsetCatalog).toHaveBeenCalledTimes(1);
    expect(api.aiEvalsetRuns).toHaveBeenCalledTimes(1);
    act(() => { void nav!('/settings/ai?tab=providers', { replace: true }); });
    await tick();
    act(() => { void nav!('/settings/ai?tab=eval', { replace: true }); });
    await tick();
    expect(api.aiEvalsetCatalog).toHaveBeenCalledTimes(2);
    expect(api.aiEvalsetRuns).toHaveBeenCalledTimes(1);
  });

  it('çipler aria-pressed; seçim Koş gövdesi; başarıda koşu parametreleri silinir', async () => {
    idle();
    api.aiEvalsetStartRun.mockResolvedValue({ run: run('ev-3', { status: 'running', done: 0, finishedAt: '', surfaces: ['Problem'], total: 6 }) });
    const el = render(<AiEvalPanel />, '/settings/ai?tab=eval&run=ev-old&case=x');
    await tick();
    const all = button(el, 'Tümü 29')!;
    const problem = button(el, 'Problem 6')!;
    expect(all.getAttribute('aria-pressed')).toBe('true');
    expect(problem.getAttribute('aria-pressed')).toBe('false');
    act(() => { problem.click(); });
    expect(button(el, 'Tümü 29')!.getAttribute('aria-pressed')).toBe('false');
    expect(button(el, 'Problem 6')!.getAttribute('aria-pressed')).toBe('true');
    expect(el.textContent).toContain('6 vaka · sırayla koşar');
    act(() => { button(el, 'Koş')!.click(); });
    await tick();
    expect(api.aiEvalsetStartRun).toHaveBeenCalledWith(['Problem']);
    expect(where).toBe('/settings/ai?tab=eval');
  });

  it('başlatma 409 → sunucunun metni aynen', async () => {
    idle();
    api.aiEvalsetStartRun.mockRejectedValue(new Error('HTTP 409: {"error":"bir değerlendirme koşusu zaten sürüyor","run":{}}'));
    const el = render(<AiEvalPanel />);
    await tick();
    act(() => { button(el, 'Koş')!.click(); });
    await tick();
    expect(el.querySelector('[role="alert"]')!.textContent).toBe('bir değerlendirme koşusu zaten sürüyor');
    // v0.10.940 — başlatma hatası kataloğu da tazeler (503 = ready değişti).
    expect(api.aiEvalsetCatalog).toHaveBeenCalledTimes(2);
  });

  it('koşu sürerken: ilerleme çubuğu, Koş kapalı + sebep, Durdur; iptal 409 metni', async () => {
    const live = run('ev-live', { status: 'running', done: 12, pass: 11, fail: 1, finishedAt: '', startedAt: new Date(Date.now() - 250_000).toISOString() });
    api.aiEvalsetCatalog.mockResolvedValue(CATALOG);
    api.aiEvalsetRuns.mockResolvedValue({ runs: [live, ...IDLE_RUNS] });
    api.aiEvalsetRun.mockResolvedValue({ run: live, cases: [] });
    api.aiEvalsetCancelRun.mockRejectedValue(new Error('HTTP 409: {"error":"koşu bu sunucuda sürmüyor ya da bitti"}'));
    const el = render(<AiEvalPanel />);
    await tick();
    const bar = el.querySelector('[role="progressbar"]')!;
    expect(bar.getAttribute('aria-valuenow')).toBe('12');
    expect(bar.getAttribute('aria-valuemin')).toBe('0');
    expect(bar.getAttribute('aria-valuemax')).toBe('29');
    expect(el.textContent).toMatch(/Koşuyor · 12 \/ 29 vaka · 4 dk \d+ sn · sayfadan ayrılabilirsin, koşu sunucuda sürer/);
    expect(button(el, 'Koş')!.disabled).toBe(true);
    expect(el.textContent).toContain('Bir koşu sürüyor');
    // Başka sunucuda süren koşu: ara sonuç yok — "kalan vaka yok" DENMEZ.
    expect(el.textContent).toContain('Ara sonuçlar koşuyu yürüten sunucuda');
    // Karşılaştırma: süren koşu bitince kıyaslanır (seçici kapalı).
    expect(el.textContent).toContain('Seçili koşu bitince karşılaştırılabilir.');
    act(() => { button(el, 'Durdur')!.click(); });
    await tick();
    expect(api.aiEvalsetCancelRun).toHaveBeenCalledWith('ev-live');
    expect(el.querySelector('[role="alert"]')!.textContent).toBe('koşu bu sunucuda sürmüyor ya da bitti');
  });

  it('AI hazır değilse Koş kapalı ve sebebi yazılı; ilerleme yok', async () => {
    idle();
    api.aiEvalsetCatalog.mockResolvedValue({ ...CATALOG, ready: false });
    const el = render(<AiEvalPanel />);
    await tick();
    expect(button(el, 'Koş')!.disabled).toBe(true);
    expect(el.textContent).toContain('CoSRE yapılandırılmamış ya da kapalı');
    expect(el.querySelector('[role="progressbar"]')).toBeNull();
    expect(button(el, 'Durdur')).toBeUndefined();
  });

  it('yüzey özeti: Kaldı yalnız >0\'da cell-err, null → soluk —, sayı mono değil, tr-TR ondalık', async () => {
    idle();
    const el = render(<AiEvalPanel />);
    await tick();
    const [summary] = tables(el);
    const rows = Array.from(summary.querySelectorAll('tbody tr'));
    expect(rows.length).toBe(2);
    const intent = cellsOf(rows.find(r => r.textContent?.startsWith('IntentClassify'))!);
    const problem = cellsOf(rows.find(r => r.textContent?.startsWith('Problem'))!);
    expect(intent[2].className).toContain('cell-err');
    expect(problem[2].className).not.toContain('cell-err');
    expect(intent[3].querySelector('.cell-empty')!.textContent).toBe('—');
    expect(problem[3].textContent).toBe('0');
    expect(intent[4].textContent).toBe('1,4');
    for (const td of intent.slice(1)) {
      expect(td.className).toContain('num');
      expect(td.className).not.toMatch(/\bmono\b/);
    }
  });

  it('kalan vaka satırı gerçek ?case= linki; çekmece bölümleri + kırpma notu', async () => {
    idle();
    const el = render(<AiEvalPanel />);
    await tick();
    const failing = tables(el)[1];
    const rows = Array.from(failing.querySelectorAll('tbody tr'));
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toContain('intent: want find_entity got none');
    const a = rows[0].querySelector('a.row-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('/settings/ai?tab=eval&run=ev-new&case=intent-checkout-find');
    act(() => { a.click(); });
    await tick();
    expect(where).toBe('/settings/ai?tab=eval&run=ev-new&case=intent-checkout-find');
    const drawer = document.body.textContent ?? '';
    for (const s of ['Özet', 'Neden', 'Beklenti', 'Girdi', 'Model cevabı', 'Niyet: find_entity · servis: checkout', 'kırpıldı — ilk 16 KiB']) {
      expect(drawer, s).toContain(s);
    }
    expect(document.body.querySelector('ul.evs-fails')!.textContent).toBe('intent: want find_entity got none');
  });

  it('koşu geçmişi: Fark eksi → cell-err; durum nokta + metin; satır ?run= linki', async () => {
    idle();
    const el = render(<AiEvalPanel />);
    await tick();
    const history = tables(el)[2];
    const rows = Array.from(history.querySelectorAll('tbody tr'));
    expect(rows.length).toBe(2);
    const newest = cellsOf(rows[0]);
    expect(newest[3].querySelector('.status-dot')).not.toBeNull();
    expect(newest[3].textContent).toBe('Bitti');
    expect(newest[3].className).not.toContain('cell-err');
    expect(newest[4].textContent).toBe('26 / 29');
    expect(newest[5].textContent).toBe('−1');
    expect(newest[5].className).toContain('cell-err');
    expect(cellsOf(rows[1])[5].querySelector('.cell-empty')!.textContent).toBe('—');
    expect(rows[0].className).toContain('row-selected'); // varsayılan seçili koşu
    const a = rows[1].querySelector('a.row-link') as HTMLAnchorElement;
    expect(a.getAttribute('href')).toBe('/settings/ai?tab=eval&run=ev-old');
  });

  it('başarısız koşu seçiliyken karşılaştırma ipucu "bitince" DEMEZ', async () => {
    // v0.10.940 — failed / abandoned hiç bitmeyecek: "bitince
    // karşılaştırılabilir" operatörü gelmeyecek bir anı bekletirdi.
    const failed = run('ev-fail', { status: 'failed', error: 'sağlayıcı 500', done: 3, pass: 2, fail: 1 });
    api.aiEvalsetCatalog.mockResolvedValue(CATALOG);
    api.aiEvalsetRuns.mockResolvedValue({ runs: [failed, ...IDLE_RUNS] });
    api.aiEvalsetRun.mockResolvedValue({ run: failed, cases: [] });
    const el = render(<AiEvalPanel />, '/settings/ai?tab=eval&run=ev-fail');
    await tick();
    const pick = el.querySelector('.evs-cmp-pick select') as HTMLSelectElement;
    expect(pick.disabled).toBe(true);
    expect(el.querySelector('.evs-cmp-pick')!.textContent).toContain('Başarısız ya da yarım kalan koşu karşılaştırılamaz — bitmiş bir koşu seç.');
    expect(el.textContent).not.toContain('Seçili koşu bitince karşılaştırılabilir.');
  });

  it('koşu yokken: başlık kalır, Türkçe boş mesaj', async () => {
    api.aiEvalsetCatalog.mockResolvedValue(CATALOG);
    api.aiEvalsetRuns.mockResolvedValue({ runs: [] });
    const el = render(<AiEvalPanel />);
    await tick();
    const history = tables(el)[2];
    expect(history.querySelectorAll('thead th').length).toBeGreaterThanOrEqual(6);
    expect(history.querySelector('[data-dt-state="empty"]')!.textContent).toContain('Henüz koşu yok — Koş ile başlat');
    expect(api.aiEvalsetRun).not.toHaveBeenCalled();
  });
});
