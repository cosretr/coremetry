// @vitest-environment jsdom
// v0.10.1384 — yazma efekti kancası + iki tüketicisi (ChatBubble, ExplainBody).
// Operatör: "Cevabı yazarken yazma efekti olsa hızlı". Çiviler: yalnız canlı
// büyük parça açılır, ≤1,2 sn'de biter, kelime sınırında ilerler, tıkla-bitir,
// reduced-motion'da hiç açılmaz, geri yüklenen tur hemen tam; kartlar/kopya
// tam metinle hemen çizilir.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

let reduce = false;
vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => <div>[grafik]</div> }));

import { useTypewriter } from './useTypewriter';
import { REVEAL_CAP_MS } from './revealSchedule';
import { ChatBubble } from './ChatBubble';
import { ExplainBody } from './ExplainBody';
import type { ChatArrival, ChatTurn } from '@/lib/types';
import { setUserLang } from '@/lib/i18n';
// v0.10.2486 dilim 2 — metinler katalogda; bu test Türkçe arayüzü pinler.
// localStorage Node'da yok/boş olabilir: dil tercihi için bellek içi depo.
beforeEach(() => {
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, String(v)); }, removeItem: (k: string) => { mem.delete(k); } });
  setUserLang('tr');
});
afterEach(() => { setUserLang(null); vi.unstubAllGlobals(); });

let host: HTMLDivElement;
let root: Root;
let now = 1_000_000;
let frames = new Map<number, FrameRequestCallback>();
let nextId = 1;
const realMatchMedia = window.matchMedia;

async function advance(ms: number) {
  now += ms;
  const due = [...frames.values()];
  frames = new Map();
  await act(async () => { for (const cb of due) cb(now); });
}
async function runOut() { for (let i = 0; i < 200 && frames.size > 0; i++) await advance(16); }

function Probe({ text, arrival }: { text: string; arrival?: ChatArrival }) {
  const tw = useTypewriter(text, arrival);
  return <div id="p" data-rev={String(tw.revealing)} onClick={tw.finish}>{tw.text}</div>;
}
const probe = () => host.querySelector<HTMLDivElement>('#p')!;
async function render(node: React.ReactNode) {
  await act(async () => {
    root.render(<MemoryRouter><QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider></MemoryRouter>);
  });
}

const LONG = Array.from({ length: 120 }, (_, i) => `kelime${i}`).join(' ');

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  now = 1_000_000;
  frames = new Map();
  reduce = false;
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  window.requestAnimationFrame = (cb: FrameRequestCallback) => { const id = nextId++; frames.set(id, cb); return id; };
  window.cancelAnimationFrame = (id: number) => { frames.delete(id); };
  window.matchMedia = ((q: string) => ({
    matches: reduce && q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.restoreAllMocks(); window.matchMedia = realMatchMedia; });

describe('useTypewriter', () => {
  it('varış yoksa (token akışı / geri yüklenen tur) metin olduğu gibi, döngü yok', async () => {
    await render(<Probe text={LONG} />);
    expect(probe().textContent).toBe(LONG);
    expect(probe().dataset.rev).toBe('false');
    expect(frames.size).toBe(0);
  });

  it('canlı büyük parça: boyamadan önce öneke iner, kelime sınırında ilerler, ≤1,2 sn\'de tam', async () => {
    await render(<Probe text={LONG} arrival={{ at: now, from: 0 }} />);
    expect(probe().dataset.rev).toBe('true');
    expect(probe().textContent!.length).toBeLessThan(LONG.length * 0.1); // tam metin bir kare bile görünmedi
    await advance(REVEAL_CAP_MS / 2);
    const mid = probe().textContent!;
    expect(mid.length).toBeGreaterThan(LONG.length * 0.3);
    expect(mid.length).toBeLessThan(LONG.length);
    expect(mid.endsWith(' ')).toBe(true); // kelime ortasında kesilmez
    await advance(REVEAL_CAP_MS / 2 + 1);
    expect(probe().textContent).toBe(LONG);
    expect(probe().dataset.rev).toBe('false');
    expect(frames.size).toBe(0); // döngü kapandı
  });

  it('tıkla-bitir: anında tam, kuyruktaki kare yeniden kesmez', async () => {
    await render(<Probe text={LONG} arrival={{ at: now, from: 0 }} />);
    await advance(100);
    await act(async () => { probe().click(); });
    expect(probe().textContent).toBe(LONG);
    expect(probe().dataset.rev).toBe('false');
    await runOut();
    expect(probe().textContent).toBe(LONG);
  });

  it('prefers-reduced-motion → açılma yok', async () => {
    reduce = true;
    await render(<Probe text={LONG} arrival={{ at: now, from: 0 }} />);
    expect(probe().textContent).toBe(LONG);
    expect(frames.size).toBe(0);
  });

  it('süresi geçmiş varış (çekmece kapatılıp açıldı) yeniden yazdırmaz', async () => {
    await render(<Probe text={LONG} arrival={{ at: now - REVEAL_CAP_MS - 50, from: 0 }} />);
    expect(probe().textContent).toBe(LONG);
    expect(frames.size).toBe(0);
  });

  it('from: o ana dek görünen önek hemen görünür, yalnız yeni parça açılır', async () => {
    const pre = 'Önce akan kısa önsöz. ';
    await render(<Probe text={pre + LONG} arrival={{ at: now, from: pre.length }} />);
    expect(probe().textContent!.startsWith(pre)).toBe(true);
    expect(probe().textContent!.length).toBeLessThan((pre + LONG).length);
    await runOut();
    expect(probe().textContent).toBe(pre + LONG);
  });
});

describe('ChatBubble — yazma efekti', () => {
  const text = `**Özet:** checkout servisi son bir saatte hata veriyor. ${LONG}`;
  const turn = (over: Partial<ChatTurn> = {}): ChatTurn => ({ role: 'assistant', text, exchangeId: 'x1', ...over });
  const body = () => host.querySelector('.cm-msg-ai')!;

  it('canlı tur: gövde açılır + imleç; kopya/eylem satırı TAM metinle hemen; tıkla-bitir', async () => {
    await render(<ChatBubble turn={turn({ arrivedAt: { at: now, from: 0 } })} />);
    await advance(300);
    expect(body().textContent!.length).toBeLessThan(text.length);
    expect(host.querySelector('.cm-msg-ai .cm-ai-cursor')).toBeTruthy();
    expect(host.querySelector('[aria-label="Cevabı kopyala"]')).toBeTruthy();
    await act(async () => { (body() as HTMLElement).click(); });
    expect(body().textContent).toContain('kelime119');
    expect(host.querySelector('.cm-msg-ai .cm-ai-cursor')).toBeNull();
  });

  it('geri yüklenen / geçmiş tur (arrivedAt yok): hemen tam, imleç yok', async () => {
    await render(<ChatBubble turn={turn()} />);
    expect(body().textContent).toContain('kelime119');
    expect(host.querySelector('.cm-msg-ai .cm-ai-cursor')).toBeNull();
    expect(frames.size).toBe(0);
  });

  it('akan tur (pending, token akışı) efektsiz: metin olduğu gibi', async () => {
    await render(<ChatBubble turn={turn({ pending: true })} />);
    expect(body().textContent).toContain('kelime119');
    expect(frames.size).toBe(0);
  });
});

describe('ExplainBody — tek seferde gelen açıklama', () => {
  const answer = `Olası neden: ledger-writer v2.14.0 deploy sonrası hata oranı arttı. Kanıt: deploy 13:52 sonrası 5xx.\n\n${LONG}`;
  it('Karar kartı TAM metinden hemen; gövde açılır; tıkla-bitir', async () => {
    await render(<div><ExplainBody text={answer} busy={false} revealAt={now} /></div>);
    expect(host.querySelector('.cx-verdict')?.textContent).toContain('ledger-writer v2.14.0');
    expect(host.textContent).not.toContain('kelime119');
    expect(host.querySelector('.ai-reveal .cm-ai-cursor')).toBeTruthy();
    await act(async () => { host.querySelector<HTMLElement>('.ai-reveal')!.click(); });
    expect(host.textContent).toContain('kelime119');
    expect(host.querySelector('.ai-reveal .cm-ai-cursor')).toBeNull();
  });
  it('revealAt yoksa (akışla gelmiş ya da önbellek) hemen tam', async () => {
    await render(<ExplainBody text={answer} busy={false} />);
    expect(host.textContent).toContain('kelime119');
    expect(frames.size).toBe(0);
  });
});
