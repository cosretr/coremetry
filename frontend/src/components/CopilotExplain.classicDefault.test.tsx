// @vitest-environment jsdom
//
// v0.10.1036 (operatör: "Aslında CoSRE'nin eski explain trace'teki yapısı daha
// iyiydi … Eski kanıt toplayıcı güzeldi."; varsayılan için: "dönsün") —
// "CoSRE'ye sor" (kind=trace, kodsuz) yine KLASİK tek atış: sunucu adım olayı
// (step / step-result), `sources` ve `id`siz kanıt linki GÖNDERMEZ; cevap
// klasik üç başlık (İşlem Akışı ve Veri Özeti / Stacktrace Detayı / Kök Neden
// ve Sonraki Adım) + evidenceSpanIds + oracleRows.
//
// Pinlenenler (gerçek mount): bekleme ve cevap boyunca adım bloğu YOK (boş
// liste alanı da), "inceleniyor"/"okuma" aşama metni YOK, "Kaynak durumu"
// dipnotu ve "Kanıt →" satırı YOK; kanıt span'leri onEvidence'a gider
// (waterfall kutulaması) ve Kanıt satırı sayar; Kök Neden'in ilk cümlesi Karar
// şeridi; önbellek isabetinde de aynısı; Durdur metni incelemeden söz etmez.
// Adlar sentetik: checkout / payments.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { CopilotExplain } from './CopilotExplain';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { ExplainStreamOpts } from '@/lib/api';
import type { ExplainTraceAnswer } from '@/lib/types';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const ROOT_SPAN = 'aaaaaaaaaaaaaaa1';
const PAY_SPAN = 'bbbbbbbbbbbbbbb2';

let host: HTMLDivElement;
let root: Root;

async function mount(node: React.ReactNode) {
  await act(async () => { root.render(<MemoryRouter>{node}</MemoryRouter>); });
}
const text = () => host.textContent ?? '';
const btn = (re: RegExp) => Array.from(host.querySelectorAll('button')).find(b => re.test(b.textContent ?? ''));

interface Call {
  opts?: ExplainStreamOpts;
  delta: (t: string) => Promise<void>;
  finish: (a: ExplainTraceAnswer) => Promise<void>;
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
        delta: async t => { await act(async () => { opts?.onDelta?.(t); }); },
        finish: async a => { await act(async () => { resolve(a); }); },
        fail: async e => { reject(e); await act(async () => { await p.catch(() => {}); }); },
      });
      return p;
    });
  return { call: (i = 0) => calls[i], count: () => calls.length };
}

// Klasik (SystemPromptTrace) cevap biçimi — sunucu metne künye EKLEMEZ.
const CLASSIC = [
  '**İşlem Akışı ve Veri Özeti**',
  '- checkout GET /cart → payments POST /charge; payments "card declined" döndü.',
  '- En yavaş bileşen payments POST /charge (900 ms).',
  '',
  '**Kök Neden ve Sonraki Adım**',
  '- Kart reddi payments servisinde. Sonraki adım: payments loglarında reddin kodunu kontrol edin.',
].join('\n');

// Klasik çerçeve: sources YOK, id'siz link YOK (yalnız kimlik köprüsü gelebilir).
const CLASSIC_FRAME: ExplainTraceAnswer = {
  explanation: CLASSIC, exchangeId: 'x1',
  evidenceSpanIds: [ROOT_SPAN, PAY_SPAN], oracleRows: 0,
};

function expectNoInvestigationChrome() {
  expect(host.querySelector('.cx-steps')).toBeNull();       // adım bloğu (boş alan dahil) yok
  expect(host.querySelector('li.cx-step')).toBeNull();
  expect(host.querySelector('.cx-sources')).toBeNull();     // "Kaynak durumu" dipnotu yok
  expect(host.querySelector('[aria-label="Kanıt linkleri"]')).toBeNull();
  expect(text()).not.toMatch(/inceleniyor|taranıyor|Kaynak durumu|⚙ \d+ okuma|Yürütülen okumalar/);
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

describe('CoSRE’ye sor — klasik varsayılan (v0.10.1036)', () => {
  it('bekleme → akış → cevap: adım bloğu, aşama metni, Kaynak durumu dipnotu YOK; kanıt span’leri onEvidence’a gider', async () => {
    const f = fakeTrace();
    const onEvidence = vi.fn();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto onEvidence={onEvidence} />);
    // bekleme: nötr yükleniyor metni, adım listesi yok
    expect(text()).toContain('CoSRE düşünüyor');
    expectNoInvestigationChrome();

    await f.call().delta('**İşlem Akışı ve Veri Özeti**\n- checkout');
    expect(text()).not.toContain('CoSRE düşünüyor');
    expectNoInvestigationChrome();

    await f.call().finish(CLASSIC_FRAME);
    expectNoInvestigationChrome();
    for (const h of ['İşlem Akışı ve Veri Özeti', 'Kök Neden ve Sonraki Adım', 'card declined']) expect(text()).toContain(h);
    // waterfall kutulaması: sunucunun kanıt span'leri sayfaya gider, Kanıt satırı sayar
    expect(onEvidence).toHaveBeenCalledWith([ROOT_SPAN, PAY_SPAN]);
    const ev = host.querySelector('.cx-evidence')?.textContent ?? '';
    expect(ev).toContain('2 span');
    expect(ev).toContain("waterfall'da kutulu");
    // klasik davranış: Kök Neden'in ilk cümlesi Karar şeridi
    expect(host.querySelector('.cx-verdict')?.textContent).toContain('Kart reddi payments servisinde.');
    expect(btn(/Yeniden sor/)).toBeTruthy();
    expect(btn(/^Durdur$/)).toBeUndefined();
  });

  it('önbellek isabeti (klasik satır): etiket var, kutulama yine, liste/dipnot yok', async () => {
    const f = fakeTrace();
    const onEvidence = vi.fn();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto onEvidence={onEvidence} />);
    await f.call().finish({ ...CLASSIC_FRAME, cached: true, cachedAtMs: Date.now() - 120_000 });
    expect(text()).toContain('♻ önbellekten');
    expect(onEvidence).toHaveBeenCalledWith([ROOT_SPAN, PAY_SPAN]);
    expectNoInvestigationChrome();
  });

  it('Durdur metni incelemeden ya da okumalardan söz etmez', async () => {
    const f = fakeTrace();
    await mount(<CopilotExplain kind="trace" id={TRACE} auto />);
    const stop = btn(/^Durdur$/);
    expect(stop).toBeTruthy();
    expect(stop?.getAttribute('title') ?? '').not.toMatch(/incele|okuma/i);
    await act(async () => { stop!.click(); });
    expect(f.call().opts?.signal?.aborted).toBe(true);
    await f.call().fail(new DOMException('aborted', 'AbortError'));
    expect(text()).toContain('Durduruldu — açıklama yarıda kesildi');
    expect(text()).not.toMatch(/inceleme yarıda/);
    expectNoInvestigationChrome();
  });
});
