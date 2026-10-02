// @vitest-environment jsdom
//
// v0.10.1041 (operatör: "'Kodu da incele → Evet' sonrası ilk kart da kaynak
// satırlarını basıyor; düzeltilsin.") — her kart YALNIZ kendi isteğinin kod
// künyesini çizer (depo/branş satırı, dosya + "hata satırı N", bütçe notu,
// "Kod okunamadı" uyarısı).
//
// Kök neden: iki geçiş tek `code` state'ini paylaşıyordu ve iki kart da onu
// çiziyordu; Evet'in kod geçişi yazınca kodsuz İLK kart da okumadığı dosyaları
// kaynak gösteriyordu. Artık ilk kartınki `code` (yalnız run() yazar), kod
// kartınınki `codeCtx` (yalnız runCode() yazar).
//
// Pinlenenler (gerçek mount, iki kart kabı ayrı ayrı ölçülür): Evet → dosyalar
// yalnız kod kartında; okunamayan kod → uyarı yalnız kod kartında; çip ve
// ?aicode yolunda ilk kart kendi künyesini çizer (değişmedi); "Yeniden sor",
// Durdur, özne değişimi (çekmece `key` ile yeniden mount) ikisini de temizler;
// önbellek isabeti sahipliği bozmaz. Adlar sentetik: checkout-svc / payments-svc.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { CopilotExplain } from './CopilotExplain';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { ExplainStreamOpts } from '@/lib/api';
import type { AICodeContext, ExplainTraceAnswer } from '@/lib/types';
import { writeAiCodeParam } from '@/lib/aiSubject';

type ExceptionAnswer = Awaited<ReturnType<typeof api.copilotExplainException>>;

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const TRACE2 = '1bf7651916cd43dd8448eb211c80319d';

const CODE_OK: AICodeContext = {
  repo: 'checkout-svc', branch: 'main', source: 'convention',
  browseUrl: 'https://devops.example.test/demo/_git/checkout-svc',
  files: [{ path: 'src/main/java/demo/cart/CartService.java', fromLine: 10, toLine: 60, line: 42 }],
  reason: 'bütçe: 2 pencereden 1\'i kısaltıldı',
};
const CODE_OTHER: AICodeContext = {
  repo: 'payments-svc', branch: 'main', source: 'pin',
  files: [{ path: 'src/main/java/demo/pay/PaymentService.java', fromLine: 5, toLine: 30, line: 12 }],
};
const CODE_MISS: AICodeContext = { files: [], reason: 'depo bulunamadı: checkout-svc' };

let host: HTMLDivElement;
let root: Root;

async function mount(node: React.ReactNode) {
  await act(async () => { root.render(<MemoryRouter>{node}</MemoryRouter>); });
}
const text = () => host.textContent ?? '';
const cards = () => Array.from(host.querySelectorAll<HTMLElement>('.ai-answer-card'));
const btn = (label: string) =>
  Array.from(host.querySelectorAll('button')).find(b => b.textContent?.trim() === label);
const rerun = () => Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Yeniden sor'));
const chip = () => host.querySelector<HTMLButtonElement>('button.btn-chip');
const repoLink = () => host.querySelector('a[title="Depoyu tarayıcıda aç"]');

/** Bir kartta hiçbir kod künyesi izi yok. */
function expectNoCodeInfo(el: HTMLElement) {
  const t = el.textContent ?? '';
  for (const s of ['📄 Kaynak', 'CartService.java', 'PaymentService.java', 'hata satırı', 'Kod okunamadı', 'depo bulunamadı', 'bütçe:']) {
    expect(t).not.toContain(s);
  }
}

/** İki kart: [ilk cevap, "Kod incelemesi"] — kod kartı başlığıyla doğrulanır. */
function twoCards() {
  const cs = cards();
  expect(cs).toHaveLength(2);
  expect(cs[0].textContent).not.toContain('Kod incelemesi');
  expect(cs[1].textContent).toContain('Kod incelemesi');
  return cs;
}

interface Call<A> {
  includeCode?: boolean;
  emit: (t: string) => Promise<void>;
  finish: (a: A) => Promise<void>;
  fail: (e: unknown) => Promise<void>;
}

/** Akan sahte uç — her çağrıyı includeCode bayrağıyla AYRI tutar (ilk geçiş / kod geçişi). */
function streamCalls<A>() {
  const seen: Call<A>[] = [];
  const impl = (_id: string, includeCode?: boolean, opts?: ExplainStreamOpts): Promise<A> => {
    let resolve!: (v: A) => void;
    let reject!: (e: unknown) => void;
    const p = new Promise<A>((res, rej) => { resolve = res; reject = rej; });
    seen.push({
      includeCode,
      emit: async t => { await act(async () => { opts?.onDelta?.(t); }); },
      finish: async a => { await act(async () => { resolve(a); }); },
      fail: async e => { reject(e); await act(async () => { await p.catch(() => {}); }); },
    });
    return p;
  };
  return { impl, call: (i = 0) => seen[i], count: () => seen.length };
}

function fakeTrace() {
  const s = streamCalls<ExplainTraceAnswer>();
  vi.spyOn(api, 'copilotExplainTrace').mockImplementation(s.impl);
  return s;
}

function fakeException() {
  const s = streamCalls<ExceptionAnswer>();
  vi.spyOn(api, 'copilotExplainException').mockImplementation(s.impl);
  return s;
}

/** Kodsuz ilk cevap → Evet → kod geçişi `code` künyesiyle biter. */
async function answerThenYes(f: ReturnType<typeof fakeTrace>, code: AICodeContext, extra: Partial<ExplainTraceAnswer> = {}) {
  expect(f.call(0).includeCode).toBe(false);
  await f.call(0).finish({ explanation: 'ilk cevap: havuz doldu', exchangeId: 'x1', ...extra });
  await act(async () => { btn('Evet')!.click(); });
  expect(f.call(1).includeCode).toBe(true);
  await f.call(1).finish({ explanation: 'kodda: maxPoolSize=5', exchangeId: 'x2', code, ...extra });
}

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

describe('AI paneli: kod künyesi yalnız kendi kartında (v0.10.1041)', () => {
  it('ilk kart kodsuz → Evet: dosya satırları YALNIZ kod kartında (trace)', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await answerThenYes(f, CODE_OK);

    const [first, codeCard] = twoCards();
    const c = codeCard.textContent ?? '';
    expect(c).toContain('📄 Kaynak');
    expect(c).toContain('checkout-svc');
    expect(c).toContain('CartService.java:10-60');
    expect(c).toContain('hata satırı 42');
    expect(c).toContain('bütçe: 2 pencereden 1');
    expect(first.textContent).toContain('ilk cevap: havuz doldu'); // ilk cevap korunur…
    expectNoCodeInfo(first);                                        // …ama okumadığı kodu göstermez
    // Kutunun altındaki depo linki kartlara ait değil: Evet sonrası eskisi gibi kod geçişinin deposu.
    expect(repoLink()?.getAttribute('href')).toBe(CODE_OK.browseUrl);
  });

  it('kod geçişi kodu okuyamazsa "Kod okunamadı" YALNIZ kod kartında (exception)', async () => {
    const f = fakeException();
    await mount(<CopilotExplain kind="exception" id="fp-checkout-1" auto />);
    expect(f.call(0).includeCode).toBe(false);
    await f.call(0).finish({ explanation: 'ilk cevap: null referans', exchangeId: 'x1' });
    await act(async () => { btn('Evet')!.click(); });
    expect(f.call(1).includeCode).toBe(true);
    await f.call(1).finish({ explanation: 'kodsuz değerlendirme', exchangeId: 'x2', code: CODE_MISS });

    const [first, codeCard] = twoCards();
    expect(codeCard.textContent).toContain('Kod okunamadı');
    expect(codeCard.textContent).toContain('depo bulunamadı: checkout-svc');
    expect(first.textContent).toContain('ilk cevap: null referans');
    expectNoCodeInfo(first);
  });

  it('çip yolu: ilk kart kodla yeniden koşar → dosya satırları İLK kartta (değişmedi)', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await f.call(0).finish({ explanation: 'ilk cevap', exchangeId: 'x1' });
    await act(async () => { chip()!.click(); });
    expect(f.call(1).includeCode).toBe(true);
    await f.call(1).finish({ explanation: 'kodlu cevap', exchangeId: 'x2', code: CODE_OK });

    const cs = cards();
    expect(cs).toHaveLength(1);
    expect(text()).not.toContain('Kod incelemesi');
    const t = cs[0].textContent ?? '';
    expect(t).toContain('📄 Kaynak');
    expect(t).toContain('CartService.java:10-60');
    expect(t).toContain('hata satırı 42');
    expect(repoLink()?.getAttribute('href')).toBe(CODE_OK.browseUrl);
  });

  it('?aicode ile açılış: ilk kart kodlu, "Kod okunamadı" İLK kartta (değişmedi)', async () => {
    writeAiCodeParam(true);
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    expect(f.call(0).includeCode).toBe(true);
    await f.call(0).finish({ explanation: 'kodlu cevap', exchangeId: 'x1', code: CODE_MISS });

    const cs = cards();
    expect(cs).toHaveLength(1);
    expect(cs[0].textContent).toContain('Kod okunamadı');
    expect(cs[0].textContent).toContain('depo bulunamadı: checkout-svc');
  });

  it('"Yeniden sor" iki kartın künyesini de temizler', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await answerThenYes(f, CODE_OK);
    twoCards();

    // Evet kutuyu işaretledi: "Yeniden sor" tek turda KODLU gider, kod kartı kalkar.
    await act(async () => { rerun()!.click(); });
    expect(f.call(2).includeCode).toBe(true);
    expect(text()).not.toContain('Kod incelemesi');
    expect(text()).not.toContain('CartService.java');
    expect(repoLink()).toBeNull();
    await f.call(2).emit('yeni ');
    expectNoCodeInfo(cards()[0]);          // akarken: kod kartının künyesi ilk karta sızmaz
    await f.call(2).finish({ explanation: 'yeni cevap', exchangeId: 'x3', code: CODE_OTHER });
    expect(cards()).toHaveLength(1);
    expect(cards()[0].textContent).toContain('PaymentService.java:5-30');
    expect(cards()[0].textContent).not.toContain('CartService.java');

    // İlk kartın kendi künyesi de: ikinci "Yeniden sor" akarken eskisi görünmez.
    await act(async () => { rerun()!.click(); });
    await f.call(3).emit('tekrar ');
    expectNoCodeInfo(cards()[0]);
  });

  it('Durdur: kesilen koşu eski künyeleri geri getirmez', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await answerThenYes(f, CODE_OK);
    await act(async () => { rerun()!.click(); });
    await act(async () => { btn('Durdur')!.click(); });
    await f.call(2).fail(new DOMException('aborted', 'AbortError'));
    expect(text()).toContain('Durduruldu');
    expect(cards()).toHaveLength(0);
    expect(text()).not.toContain('CartService.java');
    expect(text()).not.toContain('📄 Kaynak');
    expect(repoLink()).toBeNull();
  });

  it('özne değişimi (çekmece `key` ile yeniden mount) iki künyeyi de bırakır', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain key={TRACE} kind="trace" id={TRACE} auto />);
    await answerThenYes(f, CODE_OK);
    twoCards();
    // AIDrawerBody özneyle key'li (CopilotChat); useAiSubject özne değişiminde ?aicode'u siler (v0.10.81).
    writeAiCodeParam(false);
    await mount(<CopilotExplain key={TRACE2} kind="trace" id={TRACE2} auto />);
    expect(f.count()).toBe(3);
    expect(f.call(2).includeCode).toBe(false);
    expect(text()).not.toContain('Kod incelemesi');
    expect(text()).not.toContain('CartService.java');
    await f.call(2).finish({ explanation: 'ikinci özne cevabı', exchangeId: 'x3' });
    expect(cards()).toHaveLength(1);
    expectNoCodeInfo(cards()[0]);
    expect(repoLink()).toBeNull();
    expect(text()).toContain('Kodu da inceleyeyim mi?');
  });

  it('önbellek isabeti sahipliği bozmaz: ♻ etiketi ilk kartta, künye kod kartında', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    await answerThenYes(f, CODE_OK, { cached: true, cachedAtMs: Date.now() - 120_000 });

    const [first, codeCard] = twoCards();
    expect(first.textContent).toContain('♻ önbellekten');
    expectNoCodeInfo(first);
    expect(codeCard.textContent).toContain('CartService.java:10-60');
  });
});
