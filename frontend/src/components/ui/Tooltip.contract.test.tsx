// @vitest-environment jsdom
//
// Tooltip.contract.test.tsx — v0.10.919 (buton bütünlüğü, Seçenek B).
//
// Depoda kanonik tooltip yoktu; bu atom ~320 butondaki yerel `title=`in
// yerini alacak. Çivilenen sözleşme davranış + ARIA; GÖRÜNÜM değil (jsdom
// globals.css yüklemez, ölçüm yapmaz — yerleşim lib/tipPlacement.test.ts'de).

import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Tooltip, TOOLTIP_DELAY, TOOLTIP_CLOSE_GRACE } from './Tooltip';
import { IconButton } from './IconButton';
import { Button } from './Button';
import { topEscLayer, __resetEscLayers } from '@/lib/escLayer';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}

beforeEach(() => { vi.useFakeTimers(); __resetEscLayers(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  vi.useRealTimers();
});

const btn = (el: HTMLElement) => el.querySelector('button')!;
const tip = (el: HTMLElement) => el.querySelector<HTMLElement>('[role="tooltip"]');

// React onPointerEnter/Leave, yerel pointerover/pointerout'tan sentezlenir.
function pointer(target: Element, type: 'pointerover' | 'pointerout', pointerType = 'mouse') {
  act(() => {
    target.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerType }));
  });
}
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms); });
// Klavye odağı: jsdom'un :focus-visible sezgisi testler arası taşınan olay
// durumuna bakıyor ve aynı sırada true/false/true dönüyor (ölçüldü) —
// kararsız. Tarayıcının cevabı burada AÇIKÇA verilir; atomun sınadığı şey
// "tarayıcı klavye odağı derse aç, fare odağı derse açma" mantığı.
function focusAs(target: HTMLElement, visible: boolean) {
  const orig = Element.prototype.matches;
  const spy = vi.spyOn(Element.prototype, 'matches').mockImplementation(function (this: Element, sel: string) {
    return sel === ':focus-visible' ? visible : orig.call(this, sel);
  });
  act(() => { target.focus(); });
  spy.mockRestore();
}
const keyFocus = (target: HTMLElement) => focusAs(target, true);

const sample = () => (
  <Tooltip content="Grafiği yenile">
    <IconButton aria-label="Yenile" icon="↻" />
  </Tooltip>
);

describe('Tooltip — fare', () => {
  it('gecikmeden önce açılmaz, sonra role="tooltip" ile açılır', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover');
    expect(tip(el)).toBeNull();
    advance(TOOLTIP_DELAY - 1);
    expect(tip(el)).toBeNull();
    advance(1);
    expect(tip(el)?.textContent).toBe('Grafiği yenile');
  });

  it('tetik aria-describedby ile ipucunun id\'sine bağlanır', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover');
    advance(TOOLTIP_DELAY);
    const t = tip(el)!;
    expect(t.id).toBeTruthy();
    expect(btn(el).getAttribute('aria-describedby')).toBe(t.id);
  });

  it('ayrılınca tolerans sonra kapanır; kapalıyken describedby yok', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover');
    advance(TOOLTIP_DELAY);
    pointer(btn(el), 'pointerout');
    advance(TOOLTIP_CLOSE_GRACE - 1);
    expect(tip(el)).not.toBeNull();
    advance(1);
    expect(tip(el)).toBeNull();
    expect(btn(el).hasAttribute('aria-describedby')).toBe(false);
  });

  it('ipucunun üstüne geçilirse açık kalır (WCAG 1.4.13 hoverable)', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover');
    advance(TOOLTIP_DELAY);
    pointer(btn(el), 'pointerout');
    pointer(tip(el)!, 'pointerover');
    advance(TOOLTIP_CLOSE_GRACE * 5);
    expect(tip(el)).not.toBeNull();
  });

  it('gecikme dolmadan ayrılınca hiç açılmaz', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover');
    advance(TOOLTIP_DELAY / 2);
    pointer(btn(el), 'pointerout');
    advance(TOOLTIP_DELAY * 2);
    expect(tip(el)).toBeNull();
  });

  it('dokunmatik işaretçi ipucu açmaz', () => {
    const el = render(sample());
    pointer(btn(el), 'pointerover', 'touch');
    advance(TOOLTIP_DELAY * 2);
    expect(tip(el)).toBeNull();
  });
});

describe('Tooltip — klavye ve Esc', () => {
  it('fare tıkıyla gelen odak (focus-visible değil) ipucu açmaz', () => {
    const el = render(sample());
    focusAs(btn(el), false);
    expect(tip(el)).toBeNull();
  });

  it('klavye odağı gecikmesiz açar, blur kapatır', () => {
    const el = render(sample());
    keyFocus(btn(el));
    expect(tip(el)).not.toBeNull();
    act(() => { btn(el).blur(); });
    expect(tip(el)).toBeNull();
  });

  it('Esc yalnız açıkken bir katman ekler ve ipucunu kapatır', () => {
    const el = render(sample());
    expect(topEscLayer()).toBeNull();
    keyFocus(btn(el));
    const esc = topEscLayer();
    expect(esc).not.toBeNull();
    act(() => { esc!(); });
    expect(tip(el)).toBeNull();
    expect(topEscLayer()).toBeNull();
  });

  it('dosyada kendi keydown dinleyicisi yok (tek Esc kanalı)', () => {
    const src = readFileSync(resolve(__dirname, 'Tooltip.tsx'), 'utf8');
    expect(src).not.toMatch(/addEventListener\(\s*'keydown'/);
    expect(src).not.toMatch(/key\s*===\s*'Escape'/);
  });
});

describe('Tooltip — ARIA ve tetik prop\'ları', () => {
  it('içerik aria-label ile aynıysa describedby basılmaz (çift okuma yok)', () => {
    const el = render(
      <Tooltip content="Yenile"><IconButton aria-label="Yenile" icon="↻" /></Tooltip>);
    keyFocus(btn(el));
    expect(tip(el)).not.toBeNull();
    expect(btn(el).hasAttribute('aria-describedby')).toBe(false);
  });

  it('tetikteki mevcut describedby korunur, ipucu id\'si eklenir', () => {
    const el = render(
      <Tooltip content="Açıklama"><Button variant="secondary" aria-describedby="help">Kaydet</Button></Tooltip>);
    keyFocus(btn(el));
    expect(btn(el).getAttribute('aria-describedby')).toBe(`help ${tip(el)!.id}`);
  });

  it('tetikteki title düşürülür (yerel ipucu ile üst üste binmesin)', () => {
    const el = render(
      <Tooltip content="Sil"><IconButton aria-label="Sil" icon="×" title="Sil" /></Tooltip>);
    expect(btn(el).hasAttribute('title')).toBe(false);
  });

  it('çocuğun kendi odak/işaretçi işleyicileri de çağrılır', () => {
    const calls: string[] = [];
    const el = render(
      <Tooltip content="x">
        <Button variant="ghost" onFocus={() => calls.push('focus')} onBlur={() => calls.push('blur')}
          onPointerEnter={() => calls.push('enter')} onPointerLeave={() => calls.push('leave')}>b</Button>
      </Tooltip>);
    pointer(btn(el), 'pointerover');
    pointer(btn(el), 'pointerout');
    keyFocus(btn(el));
    act(() => { btn(el).blur(); });
    expect(calls).toEqual(['enter', 'leave', 'focus', 'blur']);
  });

  it('ölçülene dek görünmez sınıfla çizilir, sınıflar CSS\'te tanımlı', () => {
    const css = readFileSync(resolve(__dirname, '..', '..', 'styles', 'globals.css'), 'utf8');
    expect(css).toMatch(/\n\.tip \{[^}]*position: fixed;[^}]*z-index: var\(--z-tooltip\)/);
    expect(css).toMatch(/\.tip\.is-measuring \{[^}]*visibility: hidden/);
    // v0.9.631 dersi: kıstırılmış left/top'un üstüne transform binmez.
    const rule = /\n\.tip \{([^}]*)\}/.exec(css)![1];
    expect(rule).not.toMatch(/(?<![\w-])transform\s*:/); // `text-transform` serbest
  });
});

// ── v0.10.919 inceleme turu — Chromium'da yeniden üretilen dört kusurun
// regresyonları (ölçümler ui/Tooltip.tsx başlığında).
describe('Tooltip — inceleme turu regresyonları', () => {
  it('klavyeyle açılan ipucu fare üstünden geçip ayrılınca KAPANMAZ (1.4.13 persistent)', () => {
    const el = render(sample());
    keyFocus(btn(el));
    pointer(btn(el), 'pointerover');
    pointer(btn(el), 'pointerout');
    advance(TOOLTIP_CLOSE_GRACE * 5);
    expect(tip(el)).not.toBeNull();
    act(() => { btn(el).blur(); });
    expect(tip(el)).toBeNull();
  });

  it('fare üstündeyken blur olursa açık kalır; fare ayrılınca kapanır', () => {
    const el = render(sample());
    keyFocus(btn(el));
    pointer(btn(el), 'pointerover');
    act(() => { btn(el).blur(); });
    expect(tip(el)).not.toBeNull();
    pointer(btn(el), 'pointerout');
    advance(TOOLTIP_CLOSE_GRACE);
    expect(tip(el)).toBeNull();
  });

  it('kaydırma ipucunu kapatmaz, yeniden yerleştirir (Tab odak kaydırması)', () => {
    const el = render(sample());
    keyFocus(btn(el));
    act(() => { window.dispatchEvent(new Event('scroll')); });
    advance(32);
    expect(tip(el)).not.toBeNull();
  });

  it('kaydırmada çapa görünür alandan çıkınca kapanır', () => {
    const el = render(sample());
    keyFocus(btn(el));
    const b = btn(el);
    const spy = vi.spyOn(b, 'getBoundingClientRect').mockReturnValue(
      { left: 10, top: -500, right: 40, bottom: -470, width: 30, height: 30, x: 10, y: -500, toJSON: () => ({}) } as DOMRect);
    act(() => { window.dispatchEvent(new Event('scroll')); });
    advance(32);
    expect(tip(el)).toBeNull();
    spy.mockRestore();
  });

  it('içerik açıkken değişince yeniden ölçülür', () => {
    const el = render(sample());
    keyFocus(btn(el));
    const spy = vi.spyOn(btn(el), 'getBoundingClientRect');
    act(() => {
      root!.render(
        <Tooltip content="Kopyalandı — tam kalıcı bağlantı panoda">
          <IconButton aria-label="Yenile" icon="↻" />
        </Tooltip>);
    });
    expect(spy).toHaveBeenCalled();
    expect(tip(el)?.textContent).toBe('Kopyalandı — tam kalıcı bağlantı panoda');
    expect(tip(el)?.className).toBe('tip');
    spy.mockRestore();
  });

  it('ipucu kutusuna tıklamak satırın onClick\'ine kabarcıklanmaz', () => {
    let rowClicks = 0;
    const el = render(
      <div onClick={() => { rowClicks++; }}>
        <Tooltip content="Satır eylemi"><IconButton aria-label="Düzenle" icon="✎" /></Tooltip>
      </div>);
    keyFocus(btn(el));
    act(() => { tip(el)!.click(); });
    expect(rowClicks).toBe(0);
  });
});
