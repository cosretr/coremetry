// useTour.ts — v0.10.1334 (rehberli tur, R1). Saf motoru (engine.ts) DOM'a
// ve yönlendiriciye bağlayan TEK kanca:
//
//   • Tur-içi gezinme: adımın rotasına navigate(url, {replace:true}) —
//     tur geçmişi kirletmez; Geri düğmesi tarayıcı geçmişinde gezinmez.
//     Yalnız PATHNAME farklıysa gidilir: aynı sayfadaki kullanıcı
//     parametreleri (sort, filtre) korunur.
//   • Bitiş: navigate(returnTo, {replace:true}) — LOCATION_LEFT hariç
//     (kullanıcı kendi seçtiği yere gitti; geri çekilmez).
//   • LOCATION_LEFT: pathname turun "sahip olduğu" yoldan ayrılırsa.
//   • Hedef: waitForTarget (MutationObserver + görünürlük, 8 s tavan).
//     Bulununca görünür değilse scrollIntoView — prefers-reduced-motion'da
//     anlık (components/ai/stickToBottom.ts deseni).
//   • Yeniden konum: ResizeObserver (varsa — jsdom/eski tarayıcı bekçisi)
//     + window resize + YAKALAMA fazında scroll (iç kaydırma kapları da),
//     rAF ile kısılmış. Kart yerleşimi örtüde, lib/tipPlacement.placeTip.
//   • v0.10.1336 — hedef ADIMIN değil şu anki KARENİN (alt adım) hedefi;
//     bekleme/zaman aşımı kare anahtarıyla (engine.currentFrame). Rota adımın.

import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import type { TipRect } from '@/lib/tipPlacement';
import type { TourCtx } from './steps';
import { currentFrame, currentStep, initialTourState, shouldReturn, tourReducer, type TourAction, type TourState } from './engine';
import { waitForTarget } from './waitForTarget';

export type TourTargetStatus = 'none' | 'waiting' | 'found' | 'timeout';
export interface TourTarget { status: TourTargetStatus; rect: TipRect | null }

const NO_TARGET: TourTarget = { status: 'none', rect: null };

/** URL'nin yol kısmı (sorgu/fragman hariç). */
export function pathOf(url: string): string {
  const i = url.search(/[?#]/);
  return i < 0 ? url : url.slice(0, i);
}

function measure(el: Element): TipRect {
  const r = el.getBoundingClientRect();
  return { left: r.left, top: r.top, width: r.width, height: r.height };
}

function sameRect(a: TipRect | null, b: TipRect): boolean {
  return !!a && a.left === b.left && a.top === b.top && a.width === b.width && a.height === b.height;
}

export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

function revealIfOffscreen(el: Element): void {
  const r = el.getBoundingClientRect();
  const vh = typeof window !== 'undefined' ? window.innerHeight : 0;
  if (r.top >= 0 && r.bottom <= vh) return;
  if (typeof (el as HTMLElement).scrollIntoView !== 'function') return;
  (el as HTMLElement).scrollIntoView({ block: 'center', inline: 'nearest', behavior: prefersReducedMotion() ? 'auto' : 'smooth' });
}

export interface UseTour {
  state: TourState;
  target: TourTarget;
  start: (ctx: TourCtx) => void;
  next: () => void;
  back: () => void;
  skip: () => void;
  finish: () => void;
}

export function useTour(opts: { onDone: (s: TourState) => void }): UseTour {
  const navigate = useNavigate();
  const { pathname, search } = useLocation();
  const hereRef = useRef(pathname + search);
  hereRef.current = pathname + search;
  const [state, dispatch] = useReducer((s: TourState, a: TourAction) => tourReducer(s, a), initialTourState);
  const [target, setTarget] = useState<TourTarget>(NO_TARGET);
  const [retarget, setRetarget] = useState(0);
  const ownedPathRef = useRef<string | null>(null);
  const elRef = useRef<Element | null>(null);
  const onDoneRef = useRef(opts.onDone);
  onDoneRef.current = opts.onDone;

  const step = currentStep(state);
  const running = state.status === 'running';
  const route = running && step && state.ctx ? step.route(state.ctx) : null;

  const start = useCallback((ctx: TourCtx) => {
    ownedPathRef.current = pathOf(ctx.returnTo);
    dispatch({ type: 'START', ctx });
  }, []);

  // ① Tur-içi gezinme — adım değişince.
  useEffect(() => {
    if (!running || !route) return;
    const path = pathOf(route);
    ownedPathRef.current = path;
    if (pathname !== path) navigate(route, { replace: true });
    // pathname bilerek bağımlılık DEĞİL: yalnız adım değişince gidilir;
    // pathname değişimi ② LOCATION_LEFT'in işi.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- yalnız adım geçişi tetikler (pathname ②'de)
  }, [running, state.index, route, navigate]);

  // ② Kullanıcı tur dışı bir yola gitti.
  useEffect(() => {
    if (!running) return;
    const owned = ownedPathRef.current;
    if (owned && pathname !== owned) dispatch({ type: 'LOCATION_LEFT' });
  }, [pathname, running]);

  // ③ Hedef bekleme — v0.10.1336: karenin hedefi, kare anahtarıyla.
  const stepId = step?.id;
  const frame = currentFrame(state);
  const frameKey = frame?.key;
  const sel = frame?.target ?? null;
  const fallback = frame?.fallbackTarget;
  const onRoute = !route || pathname === pathOf(route);
  useEffect(() => {
    elRef.current = null;
    if (!running || !stepId || !frameKey || !sel) { setTarget(NO_TARGET); return; }
    if (!onRoute) { setTarget({ status: 'waiting', rect: null }); return; } // gezinme sürüyor
    const ac = new AbortController();
    setTarget({ status: 'waiting', rect: null });
    void waitForTarget(sel, { fallback, signal: ac.signal }).then(r => {
      if (r.status === 'found') {
        elRef.current = r.el;
        revealIfOffscreen(r.el);
        setTarget({ status: 'found', rect: measure(r.el) });
      } else if (r.status === 'timeout') {
        dispatch({ type: 'TARGET_TIMEOUT', id: stepId, frame: frameKey });
        setTarget({ status: 'timeout', rect: null });
      }
    });
    return () => ac.abort();
  }, [running, stepId, frameKey, sel, fallback, onRoute, retarget]);

  // ④ Yeniden konumlama (rAF-kısılmış).
  const found = target.status === 'found';
  useEffect(() => {
    const el = elRef.current;
    if (!found || !el) return;
    let raf = 0;
    const update = () => {
      raf = 0;
      if (!el.isConnected) { setRetarget(n => n + 1); return; } // eleman değişti → yeniden bekle
      const r = measure(el);
      setTarget(t => (t.status === 'found' && sameRect(t.rect, r) ? t : { status: 'found', rect: r }));
    };
    const schedule = () => {
      if (raf) return;
      raf = typeof requestAnimationFrame === 'function' ? requestAnimationFrame(update) : (update(), 0);
    };
    window.addEventListener('resize', schedule);
    window.addEventListener('scroll', schedule, { capture: true, passive: true });
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(schedule);
      ro.observe(el);
    }
    return () => {
      window.removeEventListener('resize', schedule);
      window.removeEventListener('scroll', schedule, { capture: true });
      ro?.disconnect();
      if (raf && typeof cancelAnimationFrame === 'function') cancelAnimationFrame(raf);
    };
  }, [found, retarget]);

  // ⑤ Bitiş: bir kez bildir + returnTo.
  const doneHandledRef = useRef<TourState | null>(null);
  useEffect(() => {
    if (state.status !== 'done' || doneHandledRef.current === state) return;
    doneHandledRef.current = state;
    ownedPathRef.current = null;
    onDoneRef.current(state);
    if (shouldReturn(state) && state.returnTo) {
      const back = state.returnTo;
      if (hereRef.current !== back) navigate(back, { replace: true });
    }
  }, [state, navigate]);

  return {
    state,
    target,
    start,
    next: useCallback(() => dispatch({ type: 'NEXT' }), []),
    back: useCallback(() => dispatch({ type: 'BACK' }), []),
    skip: useCallback(() => dispatch({ type: 'SKIP_TOUR' }), []),
    finish: useCallback(() => dispatch({ type: 'FINISH' }), []),
  };
}
