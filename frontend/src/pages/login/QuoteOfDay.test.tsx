// @vitest-environment jsdom
//
// QuoteOfDay.test.tsx — v0.10.1911. loginQuote: "" → çizilmez, "off" → çizilmez,
// "on" → çizilir (operatör kararı: varsayılan KAPALI). Kapalıyken söz verisi
// için dinamik import da yapılmaz (sahte modülün `default` okuma sayacı).
//
// v0.10.2164 — kararsızlık (CI, v0.10.2148 koşusu). İki belirti, TEK neden:
//   1. "on" testinde blockquote null: mount'tan sonra sabit 20 ms uyunuyordu;
//      bileşenin `import('@/data/quotes.json')`'u (vite'a bir fetch turu) yük
//      altında 20 ms'de bitmiyordu.
//   2. Sıradaki testte fixture yerine GERÇEK quotes.json ("Hope is not a
//      strategy."): önceki testin import'u hâlâ uçuştayken yeni bileşen aynı
//      modülü ikinci kez import etti. vitest, fabrikalı bir mock çözülürken
//      mock kimliğini importer modülün PAYLAŞILAN callstack dizisine koyar;
//      aynı importer'dan gelen ikinci eşzamanlı istek bunu "fabrika kendi
//      modülünü import ediyor" sanıp mock'u atlar ve gerçek modülü döndürür
//      (vitest kaynağındaki not: "this will not work if user does
//      Promise.all(import(), import())"). Ürün hatası değil — ürün yolunda
//      mock yok. Aynı anda iki bileşen mount edilerek 3/3 yeniden üretildi.
// Düzeltme: (a) koşula bekle — söz çizilene kadar, üst sınırlı (`settleUntil`);
// (b) her testten sonra uçuştaki import'u bitir (`drainQuoteImport`), böylece
// başarısız bir test bile sıradakine uçuşta import bırakmaz ve yüklenen her
// zaman fixture'dır; (c) "yüklenmedi" kanıtı fabrika çağrı sayısı değil
// `default` OKUMA sayısı: fabrika dosya başına bir kez koşar, okuma her
// yüklemede olur — (b)'deki test-içi import sayacı OYNATMAZ (default'u okumaz).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { settleUntil } from '@/test/actWait';

const m = vi.hoisted(() => ({ reads: 0, loginQuote: '' as string }));
vi.mock('@/data/quotes.json', () => {
  const quotes = [
    { text: 'Zero.', author: 'Ann Zero', source: 'Src zero', attributed: false },
    { text: 'One.', author: 'Bob One', source: 'Src one', attributed: false },
    { text: 'Two.', author: 'Cy Two', source: 'Attributed to Cy Two, unverified', attributed: true },
  ];
  // Bileşen veriyi `import().then(m => m.default)` ile okur: her yükleme bir okuma.
  return { get default() { m.reads++; return quotes; } };
});
vi.mock('@/lib/branding', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/lib/branding')>();
  return { ...mod, useBranding: () => ({ ...mod.DEFAULT_BRANDING, loginQuote: m.loginQuote }) };
});

import { QuoteOfDay } from './QuoteOfDay';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<QuoteOfDay />); });
  return host;
}
/** Söz çizilene kadar bekler (üst sınırlı; aşılırsa koşulun adıyla düşer). */
const quoteShown = (h: HTMLElement) =>
  settleUntil(() => h.querySelector('blockquote') !== null, 'söz çizildi (blockquote)');
/**
 * Uçuştaki söz import'unu bitirir: aynı modülü test dosyasından import eder
 * (bileşeninki daha önce başladıysa ondan SONRA çözülür), ardından bir makro
 * tur bekler ki bileşenin `.then`'i ve setState'i de koşmuş olsun. Test
 * dosyası ayrı bir importer'dır — bileşenin import'uyla yukarıdaki callstack
 * çakışmasına girmez.
 */
const drainQuoteImport = () => act(async () => {
  await import('@/data/quotes.json');
  await new Promise(r => setTimeout(r, 0));
});
beforeEach(() => { m.loginQuote = ''; });
afterEach(async () => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null; vi.useRealTimers();
  await drainQuoteImport();
});

describe('QuoteOfDay', () => {
  it.each(['', 'off'])('loginQuote=%j → çizilmez ve veri yüklenmez', async (v) => {
    m.loginQuote = v;
    const before = m.reads;
    const h = await mount();
    // Bileşen (yanlışlıkla) import ettiyse o import burada biter ve `default` okunur.
    await drainQuoteImport();
    expect(h.querySelector('figure')).toBeNull();
    expect(h.textContent).toBe('');
    expect(m.reads).toBe(before);
  });

  it('loginQuote=on → bugünün sözü (yılın günü % uzunluk) "— yazar" satırıyla', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 0, 1, 12)); // gün 1 → 1 % 3 = 1
    const before = m.reads;
    const h = await mount();
    await quoteShown(h);
    // Karşı kanıt: açıkken sayaç yüklemeyi GÖRÜR (kapalı testindeki "değişmedi" boş bir iddia değil).
    expect(m.reads).toBe(before + 1);
    expect(h.querySelector('blockquote')!.textContent).toBe('“One.”');
    expect(h.querySelector('figcaption')!.textContent).toBe('— Bob One');
  });

  it('attributed:true → alt satır kaynak metni (yazar satırı değil)', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 0, 2, 12)); // gün 2 → 2 % 3 = 2
    const h = await mount();
    await quoteShown(h);
    expect(h.querySelector('blockquote')!.textContent).toBe('“Two.”');
    expect(h.querySelector('figcaption')!.textContent).toBe('Attributed to Cy Two, unverified');
  });

  it('aynı gün aynı söz', async () => {
    m.loginQuote = 'on';
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 5, 10, 0, 1));
    const h1 = await mount();
    await quoteShown(h1);
    const a = h1.querySelector('blockquote')!.textContent;
    act(() => root?.unmount()); host?.remove();
    vi.setSystemTime(new Date(2026, 5, 10, 23, 58));
    const h2 = await mount();
    await quoteShown(h2);
    const b = h2.querySelector('blockquote')!.textContent;
    expect(a).toBe(b);
    expect(a).toMatch(/^“(Zero|One|Two)\.”$/); // fixture'dan (gerçek quotes.json'dan değil)
  });
});
