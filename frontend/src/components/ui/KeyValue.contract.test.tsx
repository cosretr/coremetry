// @vitest-environment jsdom
//
// KeyValue.contract.test.tsx — v0.10.939 (tablo standardı T1, dilim 2).
//
// NEDEN BU TEST VAR: atomun bugün HİÇ çağrı yeri yok (21 yer dilim 3'te
// göçer). Çağrı yeri olmayan bir atomun sözleşmesi ancak burada yaşar —
// PanelTitle'ın `right` yuvası tam böyle, çağıran olmadığı için sessizce
// kaybolmuştu (v0.9.1365). Göç sırasında "şu satır mono olsun" diye atoma
// dokunan biri, etiketi de mono yapar ya da boş değerde `<dd>`yi boş
// bırakırsa tsc/eslint susar; burası kırmızıya döner.
//
// KAPSAM DÜRÜSTÇE: DOM sözleşmesi (yapı, sınıf kancaları, boş glif, eylem
// yuvası) test ediliyor, GÖRÜNÜM değil. CSS tarafı (etiket genişliği,
// break-all yasağı, hover/odak açığa çıkarma, dokunmatik) ayrı pin'de:
// `styles/keyValue.pin.test.ts`. Token'ların px karşılığı
// `styles/geometryTokens.test.ts`te.

import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { KeyValue, KeyValueRow, type KeyValueItem } from './KeyValue';
import { KeyValue as BarrelKeyValue, KeyValueRow as BarrelKeyValueRow } from './index';
import { IconButton } from './IconButton';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const ITEMS: KeyValueItem[] = [
  { k: 'Service', v: 'checkout-api' },
  { k: 'Trace ID', v: '4bf92f3577b34da6a3ce929d0e0e4736', mono: true },
  { k: 'k8s.pod.name', v: 'checkout-api-7c8977f965-7hrqz', mono: true },
  { k: 'Revizyon', v: 42 },
];

const rows = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>('dl.keyval > div.keyval__row')];

describe('KeyValue sözleşmesi — yapı (dl > div > dt + dd)', () => {
  it('items: her satır dl > div.keyval__row > dt.keyval__k + dd.keyval__v, sıra korunur', () => {
    const el = render(<KeyValue items={ITEMS} />);
    const dl = el.querySelector('dl');
    expect(dl, 'kök <dl> değil').toBeTruthy();
    expect(dl!.className).toBe('keyval');
    const rs = rows(el);
    expect(rs).toHaveLength(ITEMS.length);
    rs.forEach((r, i) => {
      const kids = [...r.children].map(c => c.tagName);
      expect(kids, `satır ${i}: tam olarak dt + dd`).toEqual(['DT', 'DD']);
      expect(r.children[0].className).toBe('keyval__k');
      expect(r.children[1].className).toBe('keyval__v');
      expect(r.children[0].textContent).toBe(String(ITEMS[i].k));
    });
    expect(rs.map(r => r.querySelector('dd')!.textContent))
      .toEqual(['checkout-api', '4bf92f3577b34da6a3ce929d0e0e4736', 'checkout-api-7c8977f965-7hrqz', '42']);
  });

  it('dl\'nin doğrudan çocukları yalnız satır kutusu (dt/dd dl\'ye sızmaz)', () => {
    const el = render(<KeyValue items={ITEMS} />);
    const direct = [...el.querySelector('dl')!.children].map(c => `${c.tagName}.${c.className}`);
    expect(new Set(direct)).toEqual(new Set(['DIV.keyval__row']));
  });

  it('kompozisyon biçimi: <KeyValueRow> çocukları aynı yapıyı üretir; items önce, çocuklar sonra', () => {
    const show = false;
    const el = render(
      <KeyValue items={[{ k: 'A', v: '1' }]}>
        <KeyValueRow k="B" v="2" />
        {show && <KeyValueRow k="gizli" v="x" />}
        <KeyValueRow k="C" v="3" mono />
      </KeyValue>,
    );
    const rs = rows(el);
    expect(rs.map(r => r.querySelector('dt')!.textContent)).toEqual(['A', 'B', 'C']);
    expect(rs[2].querySelector('.keyval__val--mono')).toBeTruthy();
  });

  it('etiket ve değer ReactNode taşıyabilir', () => {
    const el = render(<KeyValue items={[{ k: <em>vurgu</em>, v: <strong>kalın</strong> }]} />);
    expect(el.querySelector('dt em')?.textContent).toBe('vurgu');
    expect(el.querySelector('dd .keyval__val strong')?.textContent).toBe('kalın');
  });

  it('barrel iki bileşeni de dışa veriyor (ui/ kapalı sınır)', () => {
    expect(BarrelKeyValue).toBe(KeyValue);
    expect(BarrelKeyValueRow).toBe(KeyValueRow);
  });
});

describe('KeyValue sözleşmesi — monospace yalnız işaretli kimlikte', () => {
  it('mono işaretsiz değer mono sınıfı taşımaz; işaretli taşır', () => {
    const el = render(<KeyValue items={ITEMS} />);
    const monoOf = rows(el).map(r => !!r.querySelector('.keyval__val--mono'));
    expect(monoOf).toEqual([false, true, true, false]);
  });

  it('etiket HİÇBİR ZAMAN mono değil (mockup: k8s.pod.name etiketi arayüz fontunda)', () => {
    const el = render(<KeyValue items={ITEMS} />);
    for (const dt of el.querySelectorAll('dt')) {
      expect(dt.className).toBe('keyval__k');
      expect(dt.querySelector('[class*="mono"]')).toBeNull();
    }
  });

  it('global `.mono` sınıfına bağlanmaz (o 12px sabitler; yoğunluk küçültmesini ezerdi)', () => {
    const el = render(<KeyValue items={ITEMS} />);
    expect(el.querySelector('.mono')).toBeNull();
  });
});

describe('KeyValue sözleşmesi — boş değer soluk "—"', () => {
  it.each([
    ['null', null],
    ['undefined', undefined],
    ['boş dize', ''],
    ['false ({cond && x})', false],
  ] as const)('%s → .keyval__val--empty "—"', (_label, v) => {
    const el = render(<KeyValue items={[{ k: 'Alan', v }]} />);
    const val = el.querySelector('dd .keyval__val')!;
    expect(val.textContent).toBe('—');
    expect(val.classList.contains('keyval__val--empty')).toBe(true);
  });

  it('`v` hiç verilmezse de "—" (<dd> asla boş kalmaz)', () => {
    const el = render(<KeyValue><KeyValueRow k="Alan" /></KeyValue>);
    expect(el.querySelector('dd')!.textContent).toBe('—');
  });

  it.each([
    ['0 sayısı', 0, '0'],
    ["'0' dizesi", '0', '0'],
    ['boşluk', ' ', ' '],
  ] as const)('%s bir DEĞERDİR — aynen çizilir, glif yok', (_label, v, want) => {
    const el = render(<KeyValue items={[{ k: 'Alan', v }]} />);
    const val = el.querySelector('dd .keyval__val')!;
    expect(val.textContent).toBe(want);
    expect(val.classList.contains('keyval__val--empty')).toBe(false);
  });

  it('boş + mono: boş kazanır ("—" bir kimlik değil) ve ipucu basılmaz', () => {
    const el = render(<KeyValue items={[{ k: 'Span ID', v: null, mono: true, title: 'tam değer' }]} />);
    const val = el.querySelector('dd .keyval__val')!;
    expect(val.classList.contains('keyval__val--mono')).toBe(false);
    expect(val.classList.contains('keyval__val--empty')).toBe(true);
    expect(val.hasAttribute('title')).toBe(false);
  });

  it('dolu değerde `title` değer kutusuna basılır', () => {
    const el = render(<KeyValue items={[{ k: 'Statement', v: 'SELECT …', title: 'SELECT * FROM orders' }]} />);
    expect(el.querySelector('dd .keyval__val')!.getAttribute('title')).toBe('SELECT * FROM orders');
  });
});

describe('KeyValue sözleşmesi — eylem yuvası (hover + :focus-within kancası)', () => {
  const filterBtns = (
    <>
      <IconButton variant="bare" size="xs" icon="⊕" aria-label="Filter for k: v" tooltip="Filter for k: v" />
      <IconButton variant="bare" size="xs" icon="⊖" aria-label="Filter out k: v" tooltip="Filter out k: v" />
    </>
  );

  it('actions verilmezse yuva düğümü HİÇ üretilmez', () => {
    const el = render(<KeyValue items={ITEMS} />);
    expect(el.querySelector('.keyval__acts')).toBeNull();
    for (const dd of el.querySelectorAll('dd')) expect(dd.children).toHaveLength(1);
  });

  it('actions `.keyval__acts` içinde, değerin ARDINDA, aynı satırın <dd>sinde', () => {
    const el = render(<KeyValue items={[{ k: 'http.route', v: '/api/cart', actions: filterBtns }]} />);
    const dd = el.querySelector('dd')!;
    expect([...dd.children].map(c => c.className)).toEqual(['keyval__val', 'keyval__acts']);
    expect(dd.querySelectorAll('.keyval__acts button')).toHaveLength(2);
    // Açığa çıkarma kuralı `.keyval__row:hover|:focus-within .keyval__acts`:
    // yuvanın en yakın satır atası BU satır olmalı.
    expect(dd.querySelector('.keyval__acts')!.closest('.keyval__row')).toBe(el.querySelector('.keyval__row'));
  });

  it('klavye odağı eylem düğmesine inince satır :focus-within olur (CSS kancası tetiklenir)', () => {
    const el = render(
      <KeyValue items={[
        { k: 'a', v: '1', actions: filterBtns },
        { k: 'b', v: '2', actions: filterBtns },
      ]} />,
    );
    const [r1, r2] = rows(el);
    expect(r1.matches(':focus-within')).toBe(false);
    act(() => { r1.querySelector<HTMLButtonElement>('.keyval__acts button')!.focus(); });
    expect(r1.matches(':focus-within'), 'odaklı düğmenin satırı açığa çıkmıyor').toBe(true);
    expect(r2.matches(':focus-within'), 'odak başka satırı açıyor').toBe(false);
  });

  it('eylem düğmeleri gizliyken de sekme sırasında (disabled/tabindex=-1 değil)', () => {
    const el = render(<KeyValue items={[{ k: 'a', v: '1', actions: filterBtns }]} />);
    for (const b of el.querySelectorAll<HTMLButtonElement>('.keyval__acts button')) {
      expect(b.disabled).toBe(false);
      expect(b.tabIndex).toBe(0);
    }
  });

  it('boş değerli satırda da eylem yuvası çizilir (çağıran karar verir)', () => {
    const el = render(<KeyValue items={[{ k: 'a', v: null, actions: filterBtns }]} />);
    expect(el.querySelector('.keyval__acts')).toBeTruthy();
  });

  it('satır tıklanabilir değil: role/tabindex/data-row-action işareti yok (T2 — hover yalnız tıklanabilir satırda)', () => {
    const el = render(<KeyValue items={[{ k: 'a', v: '1', actions: filterBtns }]} />);
    const r = rows(el)[0];
    expect(r.hasAttribute('role')).toBe(false);
    expect(r.hasAttribute('tabindex')).toBe(false);
    expect(r.hasAttribute('data-row-action')).toBe(false);
  });
});

describe('KeyValue sözleşmesi — etiket genişliği ve geçişler', () => {
  it('varsayılan yalnız `keyval`; wide `keyval--wide` ekler', () => {
    expect(render(<KeyValue items={ITEMS} />).querySelector('dl')!.className).toBe('keyval');
    act(() => { root?.unmount(); }); host?.remove();
    expect(render(<KeyValue items={ITEMS} labelWidth="wide" />).querySelector('dl')!.className).toBe('keyval keyval--wide');
  });

  it('className ve dl öznitelikleri köke geçer', () => {
    const el = render(<KeyValue items={ITEMS} className="x-host" aria-label="Span attributes" id="kv1" />);
    const dl = el.querySelector('dl')!;
    expect(dl.className).toBe('keyval x-host');
    expect(dl.getAttribute('aria-label')).toBe('Span attributes');
    expect(dl.id).toBe('kv1');
  });

  it('hiçbir düğüm satır içi stil taşımaz (T5/T6: yoğunluk hücreye ulaşsın)', () => {
    const el = render(<KeyValue items={[...ITEMS, { k: 'e', v: null, actions: <span>act</span> }]} labelWidth="wide" />);
    expect(el.querySelectorAll('[style]')).toHaveLength(0);
  });
});

describe('KeyValue kaynağı — ham geometri sayısı yok (geometryTokens kapısının atom-içi hâli)', () => {
  const SRC = readFileSync(resolve(__dirname, 'KeyValue.tsx'), 'utf8');
  const CODE = SRC.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');

  it('satır içi `style` yok — ölçü CSS sınıflarında/token\'larda', () => {
    expect(CODE).not.toMatch(/\bstyle\s*=/);
  });

  it('geometri özelliğine ham sayı yazılmamış', () => {
    const PROP = /(fontSize|gap|padding\w*|margin\w*|width|minWidth|maxWidth|height|lineHeight)\s*:\s*\d/;
    expect(CODE).not.toMatch(PROP);
  });

  it('hex/rgba renk yok (colorLeaks kuralı)', () => {
    expect(CODE).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
  });
});
