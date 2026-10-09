import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

// stickToBottom — v0.10.650 (ai-ui-patterns bulgusu #4). İki sohbet yüzeyi
// de her `turns` değişiminde (yani her delta'da) dibe kaydırıyordu: operatör
// yukarıdaki cevabı okurken her token onu dibe geri fırlatıyordu. Kalıp:
// yalnız zaten dipteyken yapış; kullanıcı yukarı kaydırdıysa rahat bırak;
// kullanıcı yeni soru gönderince (pin) her hâlükârda dibe in.
//
// Delta yapışması ANLIK (scrollTop ataması): smooth kaydırma sırasında ara
// konumlar "dipte değil" okunur ve yapışma kendi kendini kapatırdı. Smooth
// yalnız pin'de.

/** Dibe bu kadar px yakınsa "dipte" sayılır (son satırın yarısı payı). */
export const STICK_THRESHOLD_PX = 48;

export function isNearBottom(
  el: { scrollTop: number; scrollHeight: number; clientHeight: number },
  threshold = STICK_THRESHOLD_PX,
): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

/**
 * findScrollParent — el'in kendisi ya da en yakın kaydırılabilir atası
 * (overflow-y auto/scroll). Bulunamazsa null (çağıran yapışmayı atlar).
 */
export function findScrollParent(el: HTMLElement | null): HTMLElement | null {
  let cur: HTMLElement | null = el;
  while (cur) {
    const oy = (cur.style.overflowY || (typeof getComputedStyle === 'function' ? getComputedStyle(cur).overflowY : '')) ?? '';
    if (oy === 'auto' || oy === 'scroll') return cur;
    cur = cur.parentElement;
  }
  return null;
}

/**
 * useStickToBottom — `ref` (kaydırma kabı ya da kabın içindeki bir düğüm;
 * kap findScrollParent ile bulunur) için: deps değişince yalnız kullanıcı
 * dipteyse dibe kaydır. Dönen `pin()` koşulsuz dibe indirir ve yapışmayı
 * yeniden açar (yeni soru gönderimi).
 */
export function useStickToBottom(ref: RefObject<HTMLElement | null>, deps: unknown[]) {
  return useStickToBottomState(ref, deps).pin;
}

/**
 * v0.10.1137 — aynı kalıp + `atBottom` durumu: kullanıcı yukarı kaydırınca
 * false olur ve sohbet "↓ En alta" düğmesini gösterir; düğme `pin()`i çağırır.
 */
export function useStickToBottomState(ref: RefObject<HTMLElement | null>, deps: unknown[]) {
  const stuck = useRef(true);
  const elRef = useRef<HTMLElement | null>(null);
  const [atBottom, setAtBottom] = useState(true);

  // v0.10.1137 — dinleyici kap DEĞİŞİNCE yeniden bağlanır. Eskiden yalnız
  // ilk commit'te ([ref]) bağlanıyordu: çekmece kapalı / sohbet yüklenirken
  // kap henüz yoktu, dinleyici hiç kurulmuyor ve "dipte değil" hiç
  // öğrenilmiyordu (yukarı kaydıran operatör yine dibe fırlatılırdı).
  const unbindRef = useRef<(() => void) | null>(null);
  const bind = useCallback((el: HTMLElement | null) => {
    if (el === elRef.current) return;
    unbindRef.current?.();
    unbindRef.current = null;
    elRef.current = el;
    if (!el) return;
    const onScroll = () => { stuck.current = isNearBottom(el); setAtBottom(stuck.current); };
    el.addEventListener('scroll', onScroll, { passive: true });
    unbindRef.current = () => el.removeEventListener('scroll', onScroll);
  }, []);
  useEffect(() => () => { unbindRef.current?.(); unbindRef.current = null; elRef.current = null; }, []);

  useEffect(() => {
    bind(findScrollParent(ref.current));
    const el = elRef.current;
    if (!el || !stuck.current) return;
    el.scrollTop = el.scrollHeight;
    // deps çağıranın listesi (turns/open): kuralın statik doğrulaması bilerek atlanır.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  const pin = useCallback(() => {
    stuck.current = true;
    setAtBottom(true);
    const el = elRef.current ?? findScrollParent(ref.current);
    if (!el) return;
    const reduce = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (typeof el.scrollTo === 'function') el.scrollTo({ top: el.scrollHeight, behavior: reduce ? 'auto' : 'smooth' });
    else el.scrollTop = el.scrollHeight;
  }, [ref]);

  return { pin, atBottom };
}
