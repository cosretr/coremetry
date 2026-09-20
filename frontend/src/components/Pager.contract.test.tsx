// @vitest-environment jsdom
//
// Pager.contract.test.tsx — sayfalama sözleşmesi (v0.9.1014).
//
// NEDEN BİR SÖZLEŞME: v0.9.1013'e kadar `Pager` atomunun TEK tüketicisi
// /traces'ti. Diğer beş sayfalama yüzeyi elle çizilmişti ve her biri
// kendi cevabını vermişti — konum, vurgu, sayının anlamı, commit anı.
// Bu dosya o cevapları TEKLEŞTİRİP çiviliyor.
//
// Saf yarı (countLabel / derivedLastPage) tablo-güdümlü ve HER `count`
// değerini geziyor. Bu, birim-karışımı dersinin (v0.6.36) sayı-anlamı
// hâli: değer+birim alan her şablon her birimi test etmeli. Burada
// "birim" sayının ANLAMI — 'exact' dalı doğru çalışıp 'capped' dalı
// sessizce son sayfa türetseydi, operatör tam olarak v0.9.638'in
// olayını yeniden yaşardı (ULAŞILAMAYAN sayfaya atlatma).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { Pager, countLabel, cursorProgress, derivedLastPage, type PagerCount } from './Pager';

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(ui: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(ui); });
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const btn = (label: string) =>
  [...document.querySelectorAll('button')].find(b => b.textContent?.includes(label))!;

// React KONTROLLÜ input'a yazmak: düz `input.value = x` React'in değer
// izleyicisini atlar ve onChange HİÇ kaçmaz (test yeşil görünür ama
// bileşen hiç uyarılmamıştır). Prototip setter'ı üzerinden yazmak
// izleyiciyi bozar ve sentetik onChange gerçekten koşar.
function type(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype, 'value')!.set!;
  act(() => {
    setter.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

// React `onBlur`u NATIVE `focusout`a bağlar (blur kabarmaz, focusout
// kabarır). `new FocusEvent('blur')` dispatch etmek hiçbir handler
// çalıştırmaz — iddia sessizce anlamsızlaşır.
function blur(el: HTMLElement) {
  act(() => { el.dispatchEvent(new FocusEvent('focusout', { bubbles: true })); });
}

function submitForm() {
  act(() => {
    document.querySelector('form')!.dispatchEvent(
      new Event('submit', { bubbles: true, cancelable: true }));
  });
}

describe('Pager — sayının ANLAMI (saf)', () => {
  const CASES: Array<{ count: PagerCount; total?: number; label: string | null; last: number | null }> = [
    // count      total   görünen etiket   türetilen son sayfa (pageSize=50)
    { count: 'exact',  total: 500,  label: '500',   last: 9 },
    { count: 'exact',  total: 1,    label: '1',     last: 0 },
    { count: 'exact',  total: 0,    label: '0',     last: 0 },
    { count: 'exact',  total: 51,   label: '51',    last: 1 },
    { count: 'capped', total: 10000, label: '10,000+', last: null },
    { count: 'approx', total: 1234,  label: '~1,234', last: null },
    { count: 'skip',   total: undefined, label: null, last: null },
    // Sayı verilmemişse hiçbir dal etiket üretmez.
    { count: 'exact',  total: undefined, label: null, last: null },
    { count: 'capped', total: undefined, label: null, last: null },
    { count: 'approx', total: undefined, label: null, last: null },
  ];

  for (const c of CASES) {
    it(`${c.count} / total=${String(c.total)} → etiket ${String(c.label)}, son sayfa ${String(c.last)}`, () => {
      expect(countLabel(c.count, c.total)).toBe(c.label);
      expect(derivedLastPage(c.count, c.total, 50)).toBe(c.last);
    });
  }

  it('YALNIZ exact son sayfa türetir — v0.9.638 olayının çivisi', () => {
    // Tavanlı bir sayıdan son sayfa türetilirse operatör listenin
    // sunulamayan bölgesine atlar ve boş tablo görür.
    for (const count of ['capped', 'approx', 'skip'] as PagerCount[]) {
      expect(derivedLastPage(count, 10000, 50), `${count} son sayfa TÜRETTİ`).toBeNull();
    }
    expect(derivedLastPage('exact', 10000, 50)).toBe(199);
  });

  it('pageSize 0 bölme hatası üretmiyor', () => {
    expect(derivedLastPage('exact', 100, 0)).toBeNull();
  });

  const PROGRESS: Array<{ loaded?: number; count: PagerCount; total?: number; out: string | null }> = [
    { loaded: 200,  count: 'capped', total: 10000, out: 'showing 200 of 10,000+' },
    { loaded: 200,  count: 'exact',  total: 10000, out: 'showing 200 of 10,000' },
    { loaded: 200,  count: 'approx', total: 10000, out: 'showing 200 of ~10,000' },
    { loaded: 200,  count: 'skip',   total: undefined, out: 'showing 200' },
    { loaded: undefined, count: 'exact', total: 500, out: '500' },
    { loaded: undefined, count: 'skip', total: undefined, out: null },
    { loaded: 0,    count: 'skip',   total: undefined, out: 'showing 0' },
  ];
  for (const c of PROGRESS) {
    it(`ilerleme: loaded=${String(c.loaded)} ${c.count}/${String(c.total)} → ${String(c.out)}`, () => {
      expect(cursorProgress(c.loaded, c.count, c.total)).toBe(c.out);
    });
  }
});

describe('Pager — offset kipi', () => {
  it('Next SAĞDA ve şeritteki TEK vurgulu kontrol (Gutenberg)', () => {
    render(<Pager mode="offset" count="exact" total={500} page={1} pageSize={50}
      onPage={() => {}} lastReachablePage={9} />);
    const buttons = [...document.querySelectorAll('button')];
    // Vurgu = birincil = `Button` atomunda SINIFSIZ (variantClass.primary '').
    const primaries = buttons.filter(b => !b.className.split(/\s+/).includes('sec'));
    expect(primaries).toHaveLength(1);
    expect(primaries[0].textContent).toContain('Next');
    // Sağda: Next'ten sonra yalnız "Last" gelebilir, Prev ondan ÖNCE.
    expect(buttons.indexOf(btn('← Prev'))).toBeLessThan(buttons.indexOf(btn('Next')));
  });

  it('Enter commit eder, blur GERİ ALIR (Ö5)', () => {
    const seen: number[] = [];
    render(<Pager mode="offset" count="exact" total={500} page={0} pageSize={50}
      onPage={n => seen.push(n)} />);
    const input = document.querySelector('input')! as HTMLInputElement;

    // Yarım yazılmış bir sayı blur'da fetch TETİKLEMEZ.
    type(input, '7');
    blur(input);
    expect(seen, 'blur commit etti — Ö5 ihlali').toEqual([]);
    expect(input.value, 'blur taslağı geri almadı').toBe('1');

    // Enter (form submit) commit EDER.
    type(input, '7');
    submitForm();
    expect(seen).toEqual([6]);
  });

  it('kesin sayıda girdi son sayfaya KENETLENİR', () => {
    const seen: number[] = [];
    render(<Pager mode="offset" count="exact" total={100} page={0} pageSize={50}
      onPage={n => seen.push(n)} />);
    const input = document.querySelector('input')! as HTMLInputElement;
    type(input, '999');
    submitForm();
    expect(seen).toEqual([1]); // 100/50 → son sayfa index 1
  });

  it('son sayfada Next KAPALI, ilk sayfada Prev KAPALI', () => {
    render(<Pager mode="offset" count="exact" total={100} page={1} pageSize={50} onPage={() => {}} />);
    expect(btn('Next →').disabled).toBe(true);
    act(() => { root!.render(<Pager mode="offset" count="exact" total={100} page={0} pageSize={50} onPage={() => {}} />); });
    expect(btn('← Prev').disabled).toBe(true);
    expect(btn('Next →').disabled).toBe(false);
  });

  it('skip sayımda gezinme hasMore üzerinde', () => {
    render(<Pager mode="offset" count="skip" page={3} pageSize={50} hasMore={false} onPage={() => {}} />);
    expect(btn('Next →').disabled).toBe(true);
    // Denominatör YOK — sayılmamış bir toplamı ima etmez.
    expect(document.body.textContent).not.toMatch(/\/\s*\d/);
  });

  it('Last YALNIZ ulaşılabilir ve ileridEyse çizilir', () => {
    render(<Pager mode="offset" count="skip" page={3} pageSize={50} hasMore onPage={() => {}} />);
    expect(document.body.textContent).not.toContain('Last');
    act(() => {
      root!.render(<Pager mode="offset" count="skip" page={3} pageSize={50} hasMore
        onPage={() => {}} lastReachablePage={3} />);
    });
    expect(document.body.textContent, 'geçerli sayfa = son sayfa iken Last çizildi')
      .not.toContain('Last');
    act(() => {
      root!.render(<Pager mode="offset" count="skip" page={3} pageSize={50} hasMore
        onPage={() => {}} lastReachablePage={9} />);
    });
    expect(document.body.textContent).toContain('Last');
  });
});

// v0.10.727 → v0.10.831 (operator-reported, İKİ kez: "Last diyince sayfa
// numarası hâlâ 1 gözüküyor").
//
// 727 girdinin yanına bir ETİKET koymuştu ("Sondan sayfa") ama kutu yine
// "1" yazıyordu ve operatör aynı şikâyeti tekrarladı. 831'de kutu ters
// kipte HİÇ çizilmiyor: konum SONDAN yazılıyor.
//
// Saf yarı (pagePositionLabel) tablo-güdümlü ve İKİ kipi de geziyor —
// v0.6.36 birim-karışımı dersinin yön hâli: aynı `page` iki kipte iki ayrı
// şey demek, o yüzden ileri dal da, ters dal da, kipin döndüğü sınır da
// test ediliyor.
describe('Pager — sayfa göstergesi (v0.10.831)', () => {
  it("ileri kip: 'Page' + numara kutusu (değişmedi)", () => {
    render(<Pager mode="offset" count="skip" page={0} pageSize={50} hasMore onPage={() => {}} />);
    expect(host!.textContent).toContain('Page');
    expect(host!.querySelector('input')!.value).toBe('1');
    // İleri kipte Prev/Next başlıksız — ters kip açıklamaları sızmasın.
    expect(btn('Prev').getAttribute('title')).toBeNull();
    expect(btn('Next').getAttribute('title')).toBeNull();
  });

  it('ters kip: numara kutusu YOK, konum sondan yazılır', () => {
    render(<Pager mode="offset" count="skip" page={0} pageSize={50} hasMore onPage={() => {}}
      reverse reverseTitle="ters sıra" />);
    expect(host!.querySelector('input')).toBeNull();
    expect(host!.textContent).toContain('Son sayfa');
    expect(host!.textContent).not.toMatch(/\bPage\b/);
    expect(host!.querySelector('[title="ters sıra"]')).not.toBeNull();
  });

  // v0.10.831 (inceleme, 2026-09-20) — ters kipte SAYISAL SIÇRAMA dalı hiç
  // çizilmez. Ölçülen tuzak: `onPage(lastReachablePage)` tıkı sayfayı 5'e
  // götürüyor, gösterge "Sondan 6." diyor, sıra HÂLÂ ters ve
  // `lastReachablePage > page` yanlışa döndüğü için İKİ bitiş düğmesi birden
  // kayboluyordu — şeritten geri dönüş yolu kalmıyordu. Ters kipte "başa dön"
  // bir sayfa sıçraması değil bir SIRA işlemi: yalnız `onEnd` yapabilir.
  it('ters kipte bitiş yuvası onEnd e bağlı; sayısal sıçrama ÇİZİLMEZ', () => {
    const pages: number[] = [];
    let ends = 0;
    render(<Pager mode="offset" count="exact" total={300} page={1} pageSize={50}
      onPage={p => pages.push(p)} lastReachablePage={5}
      onEnd={() => { ends++; }} endLabel="⇤ First" endTitle="başa dön" reverse />);
    const ends_buttons = [...host!.querySelectorAll('button')].map(b => b.textContent ?? '');
    expect(ends_buttons.filter(l => l.includes('⇤ First') || l.includes('Last ⇥'))).toHaveLength(1);
    const back = btn('⇤ First');
    expect(back.getAttribute('title')).toBe('başa dön');
    act(() => { back.click(); });
    expect(ends).toBe(1);
    expect(pages).toEqual([]);   // sayfa sıçraması YOK — sırayı çağıran düzeltir
  });

  it('ileri kipte sayısal sıçrama bayt bayt eskisi', () => {
    const pages: number[] = [];
    render(<Pager mode="offset" count="exact" total={500} page={1} pageSize={50}
      onPage={p => pages.push(p)} lastReachablePage={9} />);
    const jump = btn('Last ⇥');
    expect(jump.getAttribute('title')).toBe('Son sayfaya git (10)');
    act(() => { jump.click(); });
    expect(pages).toEqual([9]);
  });

  // v0.10.831 (inceleme) — KESİN toplam ters kipte de görünür; "türetilemez"
  // gerekçesi yalnız TAVANLI sayımda geçerliydi.
  it('kesin sayımda ters kip toplamı da yazar: "Sondan 2. / 7"', () => {
    render(<Pager mode="offset" count="exact" total={340} page={1} pageSize={50}
      onPage={() => {}} reverse />);
    expect(host!.textContent).toContain('Sondan 2.');
    expect(host!.textContent).toContain('/ 7');
  });

  it('ters kipte Prev/Next başlıkları YÖNÜ söyler (davranış aynı)', () => {
    const pages: number[] = [];
    render(<Pager mode="offset" count="skip" page={1} pageSize={50} hasMore
      onPage={p => pages.push(p)} reverse />);
    expect(host!.textContent).toContain('Sondan 2.');
    expect(btn('Prev').getAttribute('title')).toContain('"Son sayfa" yönünde');
    expect(btn('Next').getAttribute('title')).toContain('Sondan');
    act(() => { btn('Next').click(); });
    act(() => { btn('Prev').click(); });
    // Ters kip SAYFA MATEMATİĞİNİ değiştirmiyor: Next ileri, Prev geri.
    expect(pages).toEqual([2, 0]);
  });
});

describe('Pager — cursor kipi', () => {
  it('sayfa girdisi YOK — keyset imleçte "sayfa 7" ifade edilemez', () => {
    render(<Pager mode="cursor" count="skip" hasMore onMore={() => {}} />);
    expect(document.querySelector('input')).toBeNull();
    expect(document.body.textContent).not.toContain('Prev');
  });

  it('daha fazla yükle birincil; biterken DÜRÜST son', () => {
    let calls = 0;
    render(<Pager mode="cursor" count="skip" hasMore onMore={() => { calls++; }} />);
    const more = document.querySelector('button')!;
    expect(more.className.split(/\s+/)).not.toContain('sec');
    act(() => { more.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(calls).toBe(1);

    act(() => {
      root!.render(<Pager mode="cursor" count="skip" hasMore={false} onMore={() => {}} loaded={1234} />);
    });
    expect(document.querySelector('button')).toBeNull();
    expect(document.body.textContent).toContain('1,234');
  });

  it('loading iken buton tıklama YUTUYOR (Button.loading sözleşmesi)', () => {
    let calls = 0;
    render(<Pager mode="cursor" count="skip" hasMore loading onMore={() => { calls++; }} />);
    const more = document.querySelector('button')!;
    expect(more.disabled).toBe(true);
    act(() => { more.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    expect(calls).toBe(0);
  });

  it('tavanlı sayı "+" ile beyan ediliyor', () => {
    render(<Pager mode="cursor" count="capped" total={10000} hasMore onMore={() => {}} />);
    expect(document.body.textContent).toContain('10,000+');
  });

  // v0.9.1016 — ilerleme cümlesi. v0.9.288 olayının çivisi: /logs
  // "showing 200 of 10,000" basıyordu ve o 10.000 ES'in
  // track_total_hits TAVANIYDI, gerçek sayı değil.
  it('ilerleme cümlesi tavanı GİZLEMİYOR', () => {
    render(<Pager mode="cursor" count="capped" total={10000} loaded={200}
      hasMore onMore={() => {}} />);
    expect(document.body.textContent).toContain('showing 200 of 10,000+');
  });

  it('biten listede ilerleme cümlesi TEKRARLANMIYOR', () => {
    render(<Pager mode="cursor" count="exact" total={1234} loaded={1234}
      hasMore={false} onMore={() => {}} />);
    expect(document.body.textContent).not.toContain('showing');
    expect(document.body.textContent).toContain('1,234');
  });
});

describe('Pager — konum', () => {
  // v0.9.1078 — OPERATÖR KARARI (2026-08-16): yüzen şeritler kalktı.
  // v0.9.645'in yapışkan alt şeridi ve `stickyBottom` prop'u söküldü;
  // şerit her zaman akışta. Bu test birinin "geçici olarak" geri
  // açmasına karşı: sınıf asla basılmamalı.
  it('şerit AKIŞTA — is-sticky-bottom asla basılmıyor', () => {
    render(<Pager mode="cursor" count="skip" hasMore onMore={() => {}} />);
    expect(document.querySelector('.pager')!.className).not.toContain('is-sticky-bottom');
  });
});

// v0.10.711 (operatör: "Next yanında last page butonu olsun") — kesin son
// sayfa yoksa `onEnd` ile "sona git"; kesin sayfa varsa o kazanır, iki
// düğme birden çizilmez; etiket/başlık çağıranın.
describe('Pager — onEnd (sona git)', () => {
  it('lastReachablePage yokken onEnd düğmesi çizilir ve çağrılır', () => {
    const onEnd = vi.fn();
    render(<Pager mode="offset" count="skip" page={0} pageSize={50} hasMore onPage={() => {}} onEnd={onEnd} />);
    const btn = Array.from(host!.querySelectorAll('button')).find(b => b.textContent?.includes('Last ⇥'))!;
    expect(btn).toBeDefined();
    act(() => { btn.click(); });
    expect(onEnd).toHaveBeenCalledTimes(1);
  });
  it('lastReachablePage varken onEnd ÇİZİLMEZ (kesin sayfa kazanır)', () => {
    render(<Pager mode="offset" count="exact" total={500} page={0} pageSize={50}
      onPage={() => {}} lastReachablePage={9} onEnd={() => {}} endLabel="SONA" />);
    const labels = Array.from(host!.querySelectorAll('button')).map(b => b.textContent ?? '');
    expect(labels.some(l => l.includes('SONA'))).toBe(false);
    expect(labels.some(l => l.includes('Last ⇥'))).toBe(true);
  });
  it('etiket ve başlık çağıranın', () => {
    render(<Pager mode="offset" count="skip" page={0} pageSize={50} hasMore onPage={() => {}}
      onEnd={() => {}} endLabel="⇤ First" endTitle="başa dön" />);
    const btn = Array.from(host!.querySelectorAll('button')).find(b => b.textContent?.includes('⇤ First'))!;
    expect(btn.getAttribute('title')).toBe('başa dön');
  });
});
