// @vitest-environment jsdom
//
// v0.10.948 (CoSRE Faz B) — "CoSRE'ye sor" (kind=trace) incelemesinin render
// sözleşmesi. Sunucu cevaptan ÖNCE gerçek okumalar yürütür ve bunları
// step / step-result olaylarıyla bildirir; cevap çerçevesi `sources` (her
// kaynağın durumu) ve `id`siz kanıt linkleri taşır.
//
// NEDEN GERÇEK MOUNT: hepsi çalışma zamanı dalı —
//   (1) ilerleme listesi YALNIZ olaylardan doğar (olay yoksa satır yok;
//       sabit "loglar taranıyor…" metni yok) ve sonuç gelene dek «çalışıyor…»,
//   (2) cevap akmaya başlayınca liste tek satırlık özete iner, tıklanınca açılır,
//   (3) önbellek isabetinde liste YOK, isabet etiketi var,
//   (4) dipnot ok dahil her kaynağı rozetler, eksik veriyi sayar; sunucunun
//       metin dipnotu yapısal kopya varken ikinci kez basılmaz,
//   (5) kanıt linkleri gerçek <a href> (SPA Link), kimlik köprüleri satır içi kalır,
//   (6) "Yeniden sor" listeyi sıfırlar; öteki Explain türleri onStep ALMAZ.
// Adlar sentetik: checkout / payments, env prod.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { CopilotExplain } from './CopilotExplain';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { ExplainStreamOpts } from '@/lib/api';
import type { ExplainStepEvent, ExplainTraceAnswer } from '@/lib/types';

const TRACE = '0af7651916cd43dd8448eb211c80319c';

let host: HTMLDivElement;
let root: Root;

async function mount(node: React.ReactNode) {
  await act(async () => { root.render(<MemoryRouter>{node}</MemoryRouter>); });
}
const text = () => host.textContent ?? '';
const stepRows = () => Array.from(host.querySelectorAll('li.cx-step')).map(li => li.textContent ?? '');
const btn = (re: RegExp) => Array.from(host.querySelectorAll('button')).find(b => re.test(b.textContent ?? ''));

interface Call {
  opts?: ExplainStreamOpts;
  step: (ev: ExplainStepEvent) => Promise<void>;
  delta: (t: string) => Promise<void>;
  finish: (a: Omit<ExplainTraceAnswer, 'explanation'> & { explanation: string }) => Promise<void>;
  fail: (e: unknown) => Promise<void>;
}

function fakeTrace() {
  const calls: Call[] = [];
  vi.spyOn(api, 'copilotExplainTrace').mockImplementation(
    (_id: string, _code?: boolean, opts?: ExplainStreamOpts) => {
      let resolve!: (v: ExplainTraceAnswer) => void;
      let reject!: (e: unknown) => void;
      const p = new Promise<ExplainTraceAnswer>((res, rej) => { resolve = res; reject = rej; });
      calls.push({
        opts,
        step: async ev => { await act(async () => { opts?.onStep?.(ev); }); },
        delta: async t => { await act(async () => { opts?.onDelta?.(t); }); },
        finish: async a => { await act(async () => { resolve(a); }); },
        fail: async e => { reject(e); await act(async () => { await p.catch(() => {}); }); },
      });
      return p;
    });
  return { call: (i = 0) => calls[i], count: () => calls.length };
}

const ANSWER = [
  '**Bulgu**', '- checkout POST /orders 1840 ms; payments hata döndü.', '',
  '**Kanıt**', '- [T1] payments öz süre 1620 ms', '',
  '**Olası neden**', '- payments bağlantı havuzu dolu görünüyor. Aynı pencerede deploy var (ilişki, neden değil).', '',
  '**Eksik veri**', '- logs: erişilemedi — log kanıtı yok.', '',
  '**Sonraki kontrol**', '- payments havuz metriği.', '',
  '---', '**Kaynak durumu**', '- traces/clickhouse: başarılı', '- logs/elasticsearch: kaynağa erişilemedi (eksik veri)',
].join('\n');

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('CoSRE’ye sor — canlı ilerleme (yalnız gerçek adımlar)', () => {
  it('olay yokken liste YOK; step → «çalışıyor…», step-result → süre + durum rozeti', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    expect(f.call().opts?.onStep).toBeTypeOf('function');
    expect(text()).toContain('CoSRE düşünüyor');
    expect(stepRows()).toEqual([]);                  // sunucu henüz bir şey başlatmadı
    expect(text()).not.toMatch(/taranıyor|inceleniyor/);

    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace', args: `{"trace_id":"${TRACE}"}` });
    expect(stepRows()).toEqual(['get_traceçalışıyor…']);

    await f.call().step({ kind: 'step-result', i: 0, tool: 'get_trace', ok: true, preview: '{}', truncated: false, bytes: 2, durationMs: 84,
      sources: [{ source: 'traces', state: 'ok' }] });
    await f.call().step({ kind: 'step', i: 1, tool: 'get_logs_for_trace' });
    await f.call().step({ kind: 'step', i: 2, tool: 'compare_periods' });
    await f.call().step({ kind: 'step-result', i: 1, tool: 'get_logs_for_trace', ok: true, preview: '{}', truncated: false, bytes: 2, durationMs: 8000,
      sources: [{ source: 'logs', state: 'unreachable', detail: 'dial tcp: connection refused' }] });

    const rows = stepRows();
    expect(rows[0]).toBe('get_traceok84 ms');
    expect(rows[1]).toBe('get_logs_for_traceerişilemedi8.0 s');
    expect(rows[2]).toBe('compare_periodsçalışıyor…');
    expect(text()).toContain('⚙ 3 okuma · 1 sürüyor · 1 eksik kapsam');
    // kırmızı rozet: kanıt YOK sınıfı
    expect(host.querySelector('li.cx-step .badge.b-err')?.textContent).toBe('erişilemedi');
  });

  it('cevap akmaya başlayınca liste özete iner; tık → açılır (aria-expanded)', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace' });
    await f.call().step({ kind: 'step-result', i: 0, tool: 'get_trace', ok: true, preview: '{}', truncated: false, bytes: 2, durationMs: 90 });
    await f.call().step({ kind: 'step', i: 1, tool: 'list_deploys' });
    await f.call().step({ kind: 'step-result', i: 1, tool: 'list_deploys', ok: false, preview: '{"error":"unauthorized"}', truncated: false, bytes: 24, durationMs: 10 });
    await f.call().delta('**Bulgu**');
    expect(text()).not.toContain('CoSRE düşünüyor');
    expect(stepRows()).toEqual([]);                  // kapalı özet
    const toggle = btn(/⚙ 2 okuma/);
    expect(toggle?.textContent).toContain('1 hata');
    // v0.10.948 — okumalar paralel: Σ değil, en uzun tek okuma
    expect(toggle?.textContent).toContain('en uzun 90 ms');
    expect(toggle?.textContent).not.toContain('Σ');
    expect(toggle?.getAttribute('aria-expanded')).toBe('false');
    await act(async () => { toggle!.click(); });
    expect(stepRows()).toEqual(['get_traceok90 ms', 'list_deploys⚠ yetki yok10 ms']);
  });

  it('inceleme cevapsız düşerse koşan okumalar hata kutusunun yanında kalır', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace' });
    await f.call().fail(new Error('HTTP 502: model erişilemedi'));
    expect(text()).toContain('model erişilemedi');
    expect(btn(/⚙ 1 okuma/)).toBeTruthy();
    await act(async () => { btn(/⚙ 1 okuma/)!.click(); });
    expect(stepRows()).toEqual(['get_tracesonuç gelmedi']); // biten incelemede «çalışıyor» yalan olurdu
  });
});

// v0.10.948 — gereksinim 7: inceleme fazında (cevap akmadan) durdurma. İptal
// hata değildir (kırmızı kutu yok) ama görünür bir "durduruldu" hâli olmalı:
// auto kipte "Yeniden sor" yalnız text/error/durduruldu varken çıkar.
describe('CoSRE’ye sor — inceleme fazında Durdur', () => {
  it('Durdur akışı keser; nötr not + koşan okumalar + "Yeniden sor" görünür', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace' });
    const stop = btn(/^Durdur$/);
    expect(stop).toBeTruthy();
    expect(btn(/Yeniden sor/)).toBeUndefined();
    await act(async () => { stop!.click(); });
    expect(f.call().opts?.signal?.aborted).toBe(true);
    await f.call().fail(new DOMException('aborted', 'AbortError'));
    expect(btn(/^Durdur$/)).toBeUndefined();
    expect(text()).toContain('Durduruldu');
    expect(text()).not.toContain('CoSRE düşünüyor');
    expect(host.querySelector('[style*="--err"]')).toBeNull();   // iptal HATA DEĞİL
    expect(btn(/⚙ 1 okuma/)).toBeTruthy();
    expect(btn(/Yeniden sor/)).toBeTruthy();
    await act(async () => { btn(/Yeniden sor/)!.click(); });
    expect(f.count()).toBe(2);
    expect(text()).not.toContain('Durduruldu');
  });

  it('cevap akmaya başlayınca Durdur kalkar; öteki Explain türlerinde hiç yok', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().delta('**Bulgu**');
    expect(btn(/^Durdur$/)).toBeUndefined();
    act(() => root.unmount());
    root = createRoot(host);
    vi.spyOn(api, 'copilotExplainProblem').mockImplementation(() => new Promise(() => {}));
    await mount(<CopilotExplain kind="problem" id="p1" auto />);
    await act(async () => { await Promise.resolve(); });
    expect(text()).toContain('CoSRE düşünüyor');
    expect(btn(/^Durdur$/)).toBeUndefined();
  });
});

describe('CoSRE’ye sor — cevap kartı', () => {
  it('beş bölüm + kaynak dipnotu (ok dahil) + eksik veri; metin dipnotu ikinci kez basılmaz', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().finish({
      explanation: ANSWER, exchangeId: 'x1', evidenceSpanIds: ['b7ad6b7169203331'],
      sources: [
        { source: 'traces', backend: 'clickhouse', state: 'ok', returned: 12 },
        { source: 'logs', backend: 'elasticsearch', state: 'unreachable' },
        { source: 'metrics', backend: 'victoriametrics', state: 'not_configured' },
      ],
    });
    for (const h of ['Bulgu', 'Kanıt', 'Eksik veri', 'Sonraki kontrol']) expect(text()).toContain(h);
    // v0.10.948 — «Olası neden» hipotez: Karar şeridine çıkmaz, bölüm bütün ve yerinde kalır
    expect(host.querySelector('.cx-verdict')).toBeNull();
    expect(text()).toContain('payments bağlantı havuzu dolu görünüyor. Aynı pencerede deploy var (ilişki, neden değil).');
    expect(text().indexOf('Bulgu')).toBeLessThan(text().indexOf('payments bağlantı havuzu'));
    const footer = host.querySelector('.cx-sources');
    expect(footer?.getAttribute('aria-label')).toBe('Kaynak durumu');
    const badges = Array.from(footer!.querySelectorAll('.badge')).map(b => `${b.textContent}|${b.className}`);
    expect(badges).toEqual([
      'traces/clickhouse · ok|badge b-gray',
      'logs/elasticsearch · erişilemedi|badge b-err',
      'metrics/victoriametrics · yapılandırılmamış|badge b-err',
    ]);
    expect(footer?.textContent).toContain('eksik veri: logs/elasticsearch, metrics/victoriametrics');
    // sunucunun metin bloğu yapısal kopya varken gövdeden ayrıldı
    expect(text()).not.toContain('kaynağa erişilemedi (eksik veri)');
    expect(text().split('Kaynak durumu').length - 1).toBe(1);
  });

  it('sources YOKSA (eski sunucu / eski önbellek metni) metin aynen, dipnot yok', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().finish({ explanation: ANSWER, exchangeId: 'x1' });
    expect(host.querySelector('.cx-sources')).toBeNull();
    expect(text()).toContain('kaynağa erişilemedi (eksik veri)');
    // v0.10.948 — meta ıskalı önbellek isabeti de `sources`suz gelir: Karar yine YOK
    expect(host.querySelector('.cx-verdict')).toBeNull();
  });

  it('kanıt linkleri gerçek göreli href (SPA Link); kimlik köprüsü satıra girmez', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().finish({
      explanation: 'request_id r-123 görüldü.\n\n**Bulgu**\n- checkout yavaş.', exchangeId: 'x1',
      links: [
        { label: 'Trace', href: `/trace?id=${TRACE}&span=b7ad6b7169203331` },
        { label: 'Loglar', href: `/logs?traceId=${TRACE}&range=custom:1000-2000` },
        { label: 'Servis', href: '/service?name=checkout&env=prod' },
        { label: 'request_id', href: 'https://kibana.example/app?q=r-123', id: 'r-123' },
      ],
    });
    const row = host.querySelector('[aria-label="Kanıt linkleri"]');
    const hrefs = Array.from(row!.querySelectorAll('a')).map(a => a.getAttribute('href'));
    expect(hrefs).toEqual([
      `/trace?id=${TRACE}&span=b7ad6b7169203331`,
      `/logs?traceId=${TRACE}&range=custom:1000-2000`,
      '/service?name=checkout&env=prod',
    ]);
    // id'li köprü metnin İÇİNDE (v0.10.35), satırda değil
    const inline = Array.from(host.querySelectorAll('a')).find(a => a.textContent === 'r-123');
    expect(inline?.getAttribute('href')).toBe('https://kibana.example/app?q=r-123');
  });

  it('önbellek isabeti: isabet etiketi var, adım listesi YOK', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    // Sunucu isabette adım yayınlamaz; yayınlasa bile liste çizilmez (spec: cached → liste yok).
    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace' });
    await f.call().finish({ explanation: '**Bulgu**\n- eski cevap', exchangeId: 'x1', cached: true, cachedAtMs: Date.now() - 120_000 });
    expect(text()).toContain('♻ önbellekten');
    expect(host.querySelector('.cx-steps')).toBeNull();
  });

  it('"Yeniden sor" listeyi sıfırlar; yeni koşunun adımları baştan', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call().step({ kind: 'step', i: 0, tool: 'get_trace' });
    await f.call().step({ kind: 'step-result', i: 0, tool: 'get_trace', ok: true, preview: '{}', truncated: false, bytes: 2, durationMs: 5 });
    await f.call().finish({ explanation: '**Bulgu**\n- ilk', exchangeId: 'x1' });
    expect(btn(/⚙ 1 okuma/)).toBeTruthy();
    await act(async () => { btn(/Yeniden sor/)!.click(); });
    expect(f.count()).toBe(2);
    expect(host.querySelector('.cx-steps')).toBeNull();
    // eski akışın geç gelen adımı yeni listeye KARIŞMAZ
    await f.call(0).step({ kind: 'step', i: 9, tool: 'geç_kalan' });
    expect(text()).not.toContain('geç_kalan');
    await f.call(1).step({ kind: 'step', i: 0, tool: 'get_trace' });
    expect(stepRows()).toEqual(['get_traceçalışıyor…']);
  });
});

// v0.10.948 — seçili span incelemenin odak servisini belirler (sunucu ?span=);
// kind=trace'te spanId prop'u 4. argüman olarak gider, "Kodu da incele" de taşır.
describe('CoSRE’ye sor — odak span', () => {
  it('spanId prop\'u copilotExplainTrace\'e 4. argüman; yoksa undefined', async () => {
    const spans: (string | undefined)[] = [];
    vi.spyOn(api, 'copilotExplainTrace').mockImplementation(
      (_id: string, _code?: boolean, _opts?: ExplainStreamOpts, spanId?: string) => {
        spans.push(spanId);
        return new Promise<ExplainTraceAnswer>(() => {});
      });
    await mount(<CopilotExplain kind="trace" id={TRACE} spanId="b7ad6b7169203331" auto />);
    expect(spans).toEqual(['b7ad6b7169203331']);
    act(() => root.unmount());
    root = createRoot(host);
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    expect(spans).toEqual(['b7ad6b7169203331', undefined]);
  });
});

describe('öteki Explain türleri DEĞİŞMEDİ', () => {
  it('problem explain onStep almaz, dipnot/link satırı çizmez', async () => {
    let seen: ExplainStreamOpts | undefined;
    vi.spyOn(api, 'copilotExplainProblem').mockImplementation(async (_id: string, opts?: ExplainStreamOpts) => {
      seen = opts;
      return { explanation: 'Olası neden: redis. Kontrol et.', exchangeId: 'x1' };
    });
    await mount(<CopilotExplain kind="problem" id="p1" auto />);
    await act(async () => { await Promise.resolve(); });
    expect(seen?.onDelta).toBeTypeOf('function');
    expect(seen?.onStep).toBeUndefined();
    expect(host.querySelector('.cx-sources')).toBeNull();
    expect(host.querySelector('.cx-steps')).toBeNull();
  });
});
