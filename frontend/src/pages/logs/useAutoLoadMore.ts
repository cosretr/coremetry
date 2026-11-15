import { useEffect, useRef, useState } from 'react';

// v0.10.2238 — Logs: aşağı kaydırınca sonraki sayfa kendiliğinden yüklenir.
// Listenin sonundaki görünmez bir nöbetçi (sentinel) görünüme yaklaşınca
// "Load more" düğmesinin AYNI işleyicisi çağrılır (aynı imleç, aynı sayfa boyu).
// ES maliyet kuralları (frontend-design-system §6):
//  - tek istek: `busy` (staticQ.isFetching) iken tetik yok; tetikten sonra
//    bir sonraki `busy` düşüşüne kadar ikinci tetik yok (ref kilidi);
//  - `document.hidden` iken yükleme yok (sekme görünür olunca yeniden bakılır);
//  - önceki sayfa hata verdiyse yok (tablo hata durumu + ↻ Retry kalır);
//  - AUTO_LOAD_MAX_ROWS satırdan sonra durur, düğme tek yol olarak kalır;
//  - canlı kuyrukta kapalı (çağıran `enabled`'ı düşürür).
// Kaydırma olayı dinlenmez; yalnız IntersectionObserver.
export const AUTO_LOAD_MAX_ROWS = 2000;

export interface AutoLoadArgs {
  /** Kip uygun mu (geçmiş görünüm, canlı kuyruk kapalı, sonraki imleç var). */
  enabled: boolean;
  /** Bir sayfa yolda mı (tek istek kuralı). */
  busy: boolean;
  /** Son istek hata verdi mi. */
  failed: boolean;
  /** Yüklenmiş satır sayısı (tavan için). */
  loaded: number;
  onLoad: () => void;
  /** Nöbetçi görünüme bu kadar yaklaşınca tetikle. */
  rootMargin?: string;
}

/** Otomatik yükleme açık mı — saf karar, test edilir. */
export function autoLoadActive(a: Pick<AutoLoadArgs, 'enabled' | 'busy' | 'failed' | 'loaded'>): boolean {
  return a.enabled && !a.busy && !a.failed && a.loaded < AUTO_LOAD_MAX_ROWS;
}

/** Nöbetçi öğeye verilecek ref'i ve otomatik kipin açık olup olmadığını döner.
 *  `capped` true iken çağıran yalnız düğmeyi gösterir. */
export function useAutoLoadMore(args: AutoLoadArgs) {
  const { busy, onLoad, rootMargin = '0px 0px 400px 0px' } = args;
  const active = autoLoadActive(args);
  const capped = args.enabled && args.loaded >= AUTO_LOAD_MAX_ROWS;
  const [node, setNode] = useState<Element | null>(null);
  const pending = useRef(false);
  const onLoadRef = useRef(onLoad);
  onLoadRef.current = onLoad;
  // Kilit yalnız istek bitince açılır (busy true→false).
  useEffect(() => { if (!busy) pending.current = false; }, [busy]);
  const [visTick, setVisTick] = useState(0);
  useEffect(() => {
    const onVis = () => { if (!document.hidden) setVisTick(t => t + 1); };
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  }, []);
  useEffect(() => {
    if (!active || !node || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver(entries => {
      if (!entries.some(e => e.isIntersecting)) return;
      if (document.hidden || pending.current) return;
      pending.current = true;
      onLoadRef.current();
    }, { rootMargin });
    io.observe(node);
    return () => io.disconnect();
  }, [active, node, rootMargin, visTick]);
  return { sentinelRef: setNode, active, capped };
}
