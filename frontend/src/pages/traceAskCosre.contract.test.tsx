// @vitest-environment jsdom
//
// v0.10.944 (operatör) — trace'in ✨ düğmesi "CoSRE'ye sor" / "Ask CoSRE"
// adını aldı; ipucu "Bu trace hakkında CoSRE'ye soru sor." / "Ask CoSRE
// about this trace.". Değişiklik YALNIZ arayüz metni: özne (`?ai=trace`),
// sunucu yüzeyi (explain-trace) ve AIExplainButton davranışı aynı kalmalı.
//
// Neden gerçek mount + kaynak pini birlikte: ad/ipucu dil seçimine göre
// çalışma zamanında çözülür (useT), akış (adrese yazma; v0.10.1405'ten beri
// ikinci tık özneyi korur, aria-expanded yok) da çalışma zamanı dalıdır; kaynak
// pini ise iki çağrı yerinin (/trace ve kiosk) bu anahtarları GERÇEKTEN
// kullandığını ve eski "Explain this trace" metninin geri gelmediğini
// çiviler — test kablosu çağrı yerinin kopyası olduğu için tek başına yetmez.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter, useLocation } from 'react-router';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { __resetCopilotEnabledCache } from '@/components/ai/useCopilotEnabled';
import { IconSparkles } from '@/components/icons';
import { api } from '@/lib/api';
import { parseAiParam } from '@/lib/aiSubject';
import { setUserLang, t, useT } from '@/lib/i18n';

const TRACE_ID = 'abc123def456';

let host: HTMLDivElement;
let root: Root;
let lastSearch = '';

function Probe() {
  lastSearch = useLocation().search;
  return null;
}

// Çağrı yerinin (Trace.tsx) JSX'i — kaynak pini aşağıda ikisinin aynı
// anahtarları kullandığını ayrıca doğrular.
function TraceAsk({ id }: { id: string }) {
  const tr = useT();
  return (
    <AIExplainButton subject={{ kind: 'trace', id }} emphasis="strong"
      title={tr('ai.askCosreTraceHint')}
      label={<><IconSparkles /> <span>{tr('ai.askCosre')}</span></>} />
  );
}

async function mount(initial: string) {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[initial]}>
        <Probe />
        <TraceAsk id={TRACE_ID} />
      </MemoryRouter>,
    );
  });
}

const button = () => host.querySelector('button');

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  lastSearch = '';
  __resetCopilotEnabledCache();
  // Marka isteği (useLang → useBranding) ağa çıkmasın; hata dalı
  // varsayılan markaya düşer, dil kullanıcı seçiminden gelir.
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  // Dil seçimi localStorage'da; Node'un yerleşik localStorage'ı jsdom'unkini
  // gölgeliyor ve metotları çalışmıyor (CopilotExplain.stream.test.tsx emsali).
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('katalog', () => {
  it('Türkçe ve İngilizce ad + ipucu operatörün metniyle birebir', () => {
    expect(t('ai.askCosre', 'tr')).toBe('CoSRE’ye sor');
    expect(t('ai.askCosre', 'en')).toBe('Ask CoSRE');
    expect(t('ai.askCosreTraceHint', 'tr')).toBe('Bu trace hakkında CoSRE’ye soru sor.');
    expect(t('ai.askCosreTraceHint', 'en')).toBe('Ask CoSRE about this trace.');
  });
});

describe('trace düğmesi — ad, ipucu, erişilebilirlik', () => {
  it('Türkçe: görünen/erişilebilir ad "CoSRE’ye sor", ipucu title\'da', async () => {
    setUserLang('tr');
    await mount(`/trace?id=${TRACE_ID}`);
    const b = button()!;
    // İkon aria-hidden → erişilebilir ad yalnız metinden gelir.
    expect(b.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
    expect(b.textContent?.trim()).toBe('CoSRE’ye sor');
    expect(b.getAttribute('title')).toBe('Bu trace hakkında CoSRE’ye soru sor.');
    // v0.10.1405 — düğme çekmece açıp kapatmıyor (sohbet turu): aria-expanded yok.
    expect(b.hasAttribute('aria-expanded')).toBe(false);
  });

  it('İngilizce: "Ask CoSRE" + "Ask CoSRE about this trace."', async () => {
    setUserLang('en');
    await mount(`/trace?id=${TRACE_ID}`);
    const b = button()!;
    expect(b.textContent?.trim()).toBe('Ask CoSRE');
    expect(b.getAttribute('title')).toBe('Ask CoSRE about this trace.');
  });
});

describe('trace düğmesi — akış değişmedi', () => {
  // v0.10.1405 (operatör: "Trace ve exception üzerinden çalışan Ask CoSRE
  // açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
  // uymuyor.") — ikinci tık artık KAPATMAZ: özne yeniden yazılır (CoSRE onu
  // tüketip yeni tur gönderir — önbellek isabeti); aria-expanded yok.
  it('tık seçili trace\'i CoSRE niyeti olarak yazar; ikinci tık özneyi korur', async () => {
    setUserLang('tr');
    await mount(`/trace?id=${TRACE_ID}&span=s1`);

    await act(async () => { button()!.click(); });
    let p = new URLSearchParams(lastSearch);
    // Sayfanın kendi trace'i → kısa biçim `?ai=trace`, kimlik ?id='den.
    expect(p.get('ai')).toBe('trace');
    expect(p.get('id')).toBe(TRACE_ID);
    expect(p.get('span')).toBe('s1'); // yabancı param korunur
    expect(parseAiParam(p.get('ai'), p)).toEqual({ kind: 'trace', id: TRACE_ID });
    expect(button()!.hasAttribute('aria-expanded')).toBe(false);

    await act(async () => { button()!.click(); });
    p = new URLSearchParams(lastSearch);
    expect(p.get('ai')).toBe('trace');
    expect(p.get('id')).toBe(TRACE_ID);
    expect(p.get('span')).toBe('s1');
  });

  it('copilot kapalıyken düğme yine hiç çizilmez', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: false });
    await mount(`/trace?id=${TRACE_ID}`);
    expect(button()).toBeNull();
  });
});

describe('çağrı yerleri', () => {
  const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');
  for (const file of ['Trace.tsx', 'TraceKiosk.tsx']) {
    it(`${file} i18n anahtarlarını kullanır, eski metin yok`, () => {
      const src = read(file);
      expect(src).toContain("import { useT } from '@/lib/i18n';");
      expect(src).toContain("<AIExplainButton subject={{ kind: 'trace', id }}");
      expect(src).toContain("title={tr('ai.askCosreTraceHint')}");
      expect(src).toContain("{tr('ai.askCosre')}");
      expect(src).not.toContain('>Explain this trace<');
    });
  }
});
