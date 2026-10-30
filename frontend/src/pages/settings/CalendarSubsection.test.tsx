// @vitest-environment jsdom
//
// CalendarSubsection — v0.10.1483: özel gün listesi (ekle / düzenle / sil) ve
// otomatik ay sonu kutusu; eski blob (bölüm yok) boş takvimle çizilir.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R17 — Radix Checkbox
import { describe, it, expect, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { AnomalyCalendarConfig } from '@/lib/types';
import { CalendarSubsection } from './CalendarSubsection';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let last: AnomalyCalendarConfig | undefined;

function Harness({ init }: { init?: AnomalyCalendarConfig }) {
  const [v, setV] = useState<AnomalyCalendarConfig | undefined>(init);
  return <CalendarSubsection value={v} onChange={n => { last = n; setV(n); }} />;
}

function mount(init?: AnomalyCalendarConfig) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(<Harness init={init} />));
  return host;
}

function setInput(el: HTMLInputElement | HTMLSelectElement, value: string) {
  const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value);
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
}

const btn = (h: HTMLElement, text: string) =>
  [...h.querySelectorAll('button')].find(b => b.textContent?.trim() === text) as HTMLButtonElement;

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null; last = undefined;
});

describe('CalendarSubsection', () => {
  it('eski blob: boş takvim, otomatik ay sonu kapalı', () => {
    const h = mount(undefined);
    expect(h.textContent).toContain('Özel gün yok.');
    expect(h.querySelector('button[role="checkbox"][aria-label="Otomatik ay sonu sınıfı"]')?.getAttribute('aria-checked')).toBe('false');
    expect(h.querySelector('[aria-label="Ayın son kaç iş günü"]')).toBeNull();
  });

  it('ekle → düzenle → sil', () => {
    const h = mount({ days: [], monthEndAuto: false, monthEndBusinessDays: 2 });
    act(() => btn(h, 'Özel gün ekle').click());
    expect(last?.days).toEqual([{ from: '', label: '', kind: 'holiday' }]);
    act(() => setInput(h.querySelector('[aria-label="Özel gün 1 başlangıç"]') as HTMLInputElement, '2031-01-01'));
    act(() => setInput(h.querySelector('[aria-label="Özel gün 1 etiket"]') as HTMLInputElement, 'Yılbaşı'));
    act(() => setInput(h.querySelector('[aria-label="Özel gün 1 tür"]') as HTMLSelectElement, 'half-day'));
    expect(last?.days[0]).toEqual({ from: '2031-01-01', label: 'Yılbaşı', kind: 'half-day' });
    act(() => btn(h, 'Sil').click());
    expect(last?.days).toEqual([]);
  });

  it('otomatik ay sonu açılınca iş günü alanı görünür', () => {
    const h = mount({ days: [{ from: '2031-03-12', to: '2031-03-14', label: 'Bayram', kind: 'holiday' }], monthEndAuto: false, monthEndBusinessDays: 2 });
    expect((h.querySelector('[aria-label="Özel gün 1 bitiş"]') as HTMLInputElement).value).toBe('2031-03-14');
    act(() => (h.querySelector('button[role="checkbox"][aria-label="Otomatik ay sonu sınıfı"]') as HTMLButtonElement).click());
    expect(last?.monthEndAuto).toBe(true);
    act(() => setInput(h.querySelector('[aria-label="Ayın son kaç iş günü"]') as HTMLInputElement, '3'));
    expect(last?.monthEndBusinessDays).toBe(3);
    expect(last?.days).toHaveLength(1);
  });
});

describe('CalendarSubsection — takvim saat dilimi (v0.10.1490)', () => {
  it('eski blob: dilim boş (UTC), tarayıcı dilimi yalnız önerilir; tık yazar', () => {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const h = mount({ days: [], monthEndAuto: false, monthEndBusinessDays: 2 });
    const input = h.querySelector('[aria-label="Takvim saat dilimi"]') as HTMLInputElement;
    expect(input.value).toBe('');
    expect(last).toBeUndefined(); // çizim alanı kendiliğinden doldurmaz
    const suggest = [...h.querySelectorAll('button')].find(b => b.textContent?.startsWith('Tarayıcının dilimini kullan'));
    if (tz) {
      expect(input.placeholder).toContain(tz);
      act(() => suggest!.click());
      expect(last?.timezone).toBe(tz);
    }
  });

  it('elle yazılan dilim taşınır, silinince alan düşer (UTC)', () => {
    const h = mount({ days: [], monthEndAuto: false, monthEndBusinessDays: 2, timezone: 'Europe/Berlin' });
    const input = h.querySelector('[aria-label="Takvim saat dilimi"]') as HTMLInputElement;
    expect(input.value).toBe('Europe/Berlin');
    act(() => setInput(input, 'Europe/Istanbul'));
    expect(last?.timezone).toBe('Europe/Istanbul');
    act(() => setInput(input, ''));
    expect(last?.timezone).toBeUndefined();
    expect(last?.days).toEqual([]);
  });
});
