// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { act, createElement, type RefObject } from 'react';
import { createRoot } from 'react-dom/client';
import { isNearBottom, findScrollParent, nextStuck, useStickToBottomState, STICK_THRESHOLD_PX } from './stickToBottom';

// v0.10.650 — ai-ui-patterns #4: oto-kaydırma yalnız dipteyken yapışır.
// Saf eşik + kap bulucu + BAĞLANMA pinleri (iki yüzey de hook'u kullanır,
// her-delta'da-dibe-zorla deseni kalktı).

describe('isNearBottom', () => {
  it('tam dipte → true', () => {
    expect(isNearBottom({ scrollTop: 900, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
  });
  it('eşik içinde → true, eşik dışında → false', () => {
    expect(isNearBottom({ scrollTop: 900 - STICK_THRESHOLD_PX, scrollHeight: 1000, clientHeight: 100 })).toBe(true);
    expect(isNearBottom({ scrollTop: 900 - STICK_THRESHOLD_PX - 1, scrollHeight: 1000, clientHeight: 100 })).toBe(false);
  });
  it('yukarıda okuyan kullanıcı → false (yapışma kapanır)', () => {
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 5000, clientHeight: 600 })).toBe(false);
  });
  it('içerik kaptan kısaysa → true', () => {
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 300, clientHeight: 600 })).toBe(true);
  });
});

describe('findScrollParent', () => {
  it('kendisi kaydırılabilirse kendisi; değilse en yakın overflow-y auto/scroll atası; yoksa null', () => {
    const outer = document.createElement('div'); outer.style.overflowY = 'auto';
    const mid = document.createElement('div');
    const inner = document.createElement('div');
    outer.appendChild(mid); mid.appendChild(inner); document.body.appendChild(outer);
    expect(findScrollParent(outer)).toBe(outer);
    expect(findScrollParent(inner)).toBe(outer);
    const lone = document.createElement('div'); document.body.appendChild(lone);
    expect(findScrollParent(lone)).toBeNull();
    expect(findScrollParent(null)).toBeNull();
    outer.remove(); lone.remove();
  });
});

describe('BAĞLANMA (kaynak pinleri)', () => {
  const src = (f: string) => readFileSync(new URL(f, import.meta.url), 'utf8');
  it('iki yüzey de useStickToBottom kullanır ve her turns değişiminde dibe zorlamaz', () => {
    // v0.10.1407 — çekmecenin özne-kipi sohbeti (AIDrawerBody) silindi: tek yüzey.
    const chat = src('../CopilotChat.tsx');
    expect(chat).toMatch(/useStickToBottom(State)?\(/); // v0.10.1137 — atBottom'lı varyant
    // Eski desen: her delta'da koşulsuz dibe.
    expect(chat).not.toContain("scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });\n  }, [turns, open]);");
  });
  it('kullanıcı sorusu gönderince pin (koşulsuz dip)', () => {
    for (const f of ['../CopilotChat.tsx']) {
      const s = src(f);
      const submit = s.indexOf('const submit = ');
      expect(submit, f).toBeGreaterThan(-1);
      expect(s.slice(submit, submit + 200), f).toContain('pinBottom()');
    }
  });
});

// v0.10.2319 — operatör bulgusu: çekmece kapatılıp yeniden açılınca (trace
// sorusu) sohbet üstte kalıyor, elle kaydırmak gerekiyordu.
describe('nextStuck (yalnız yukarı hareket yapışmayı kapatır)', () => {
  const el = (scrollTop: number, scrollHeight: number, clientHeight = 100) => ({ scrollTop, scrollHeight, clientHeight });
  it('dibe yakınsa her zaman yapışık', () => {
    expect(nextStuck(false, 0, el(900, 1000))).toBe(true);
  });
  it('içerik büyüdü, scrollTop aynı → önceki durum korunur (eski hata: kapanıyordu)', () => {
    expect(nextStuck(true, 100, el(100, 1000))).toBe(true);
    expect(nextStuck(false, 100, el(100, 1000))).toBe(false);
  });
  it('aşağı doğru iniş sürerken (smooth pin) kapanmaz', () => {
    expect(nextStuck(true, 100, el(400, 1000))).toBe(true);
  });
  it('kullanıcı yukarı kaydırdı → kapanır', () => {
    expect(nextStuck(true, 900, el(300, 1000))).toBe(false);
  });
});

describe('useStickToBottomState (kap yeniden kurulunca ve içerik geç oturunca)', () => {
  function box(scrollHeight: number, clientHeight = 100) {
    const el = document.createElement('div');
    el.style.overflowY = 'auto';
    let h = scrollHeight;
    Object.defineProperty(el, 'scrollHeight', { get: () => h, configurable: true });
    Object.defineProperty(el, 'clientHeight', { get: () => clientHeight, configurable: true });
    let top = 0;
    Object.defineProperty(el, 'scrollTop', { get: () => top, set: (v: number) => { top = Math.max(0, Math.min(v, h - clientHeight)); }, configurable: true });
    document.body.append(el);
    return { el, grow: (n: number) => { h = n; } };
  }
  const scroll = (el: HTMLElement) => act(() => { el.dispatchEvent(new Event('scroll')); });
  // Proje deseni: createRoot + act (testing-library yok). Kanca sonucunu dışarı veren ufak koşum.
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  function renderHook(ref: RefObject<HTMLElement | null>, opts: { initialProps: { d: number } }) {
    const result = { current: { pin: () => {}, atBottom: true } };
    function Probe({ d }: { d: number }) { result.current = useStickToBottomState(ref, [d]); return null; }
    const host = document.createElement('div');
    const root = createRoot(host);
    const rerender = (p: { d: number }) => act(() => { root.render(createElement(Probe, p)); });
    rerender(opts.initialProps);
    return { result, rerender };
  }

  it('programatik inişin geç gelen olayı, içerik büyümüş olsa da yapışmayı kapatmaz', () => {
    const a = box(100);
    const ref = { current: a.el as HTMLElement | null };
    const h = renderHook(ref, { initialProps: { d: 0 } });
    expect(a.el.scrollTop).toBe(0); // içerik kaba sığıyor
    a.grow(1000); // eski mesajların yüksekliği sonradan oturdu
    scroll(a.el); // kendi kaydırmamızın eşzamansız olayı: scrollTop değişmedi
    h.rerender({ d: 1 }); // cevap akıyor
    expect(a.el.scrollTop).toBe(900);
    expect(h.result.current.atBottom).toBe(true);
    a.el.remove();
  });

  it('kullanıcı yukarı kaydırınca yapışma kapanır; yeni delta onu dibe fırlatmaz', () => {
    const a = box(1000);
    const ref = { current: a.el as HTMLElement | null };
    const h = renderHook(ref, { initialProps: { d: 0 } });
    expect(a.el.scrollTop).toBe(900);
    a.el.scrollTop = 300;
    scroll(a.el);
    expect(h.result.current.atBottom).toBe(false);
    a.grow(1200);
    h.rerender({ d: 1 });
    expect(a.el.scrollTop).toBe(300);
    a.el.remove();
  });

  it('kap yeniden kurulunca (çekmece kapatılıp açıldı) yukarı kaydırılmış eski durum taşınmaz: dipten başlar', () => {
    const a = box(1000);
    const ref = { current: a.el as HTMLElement | null };
    const h = renderHook(ref, { initialProps: { d: 0 } });
    a.el.scrollTop = 200;
    scroll(a.el);
    expect(h.result.current.atBottom).toBe(false);
    const b = box(800); // yeni DOM düğümü, scrollTop 0
    ref.current = b.el;
    h.rerender({ d: 1 });
    expect(b.el.scrollTop).toBe(700);
    expect(h.result.current.atBottom).toBe(true);
    a.el.remove(); b.el.remove();
  });

  it('kap yokken çağrılan pin kaybolmaz: kap bağlanınca dipte', () => {
    const ref = { current: null as HTMLElement | null };
    const h = renderHook(ref, { initialProps: { d: 0 } });
    act(() => { h.result.current.pin(); }); // çekmece henüz kurulmadı
    const b = box(600);
    ref.current = b.el;
    h.rerender({ d: 1 });
    expect(b.el.scrollTop).toBe(500);
    b.el.remove();
  });
});
