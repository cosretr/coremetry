import { useEffect, useRef, useState } from 'react';

// LazyMount — v0.5.302. Renders a placeholder until the operator
// scrolls near the wrapped child, then swaps in the real component.
// Once mounted, it stays mounted — scrolling back up doesn't unmount,
// so the panel keeps its in-memory state + cached query results.
//
// Why: the Service detail Details tab has ~9 panels that all
// fetch on mount. Operator-reported: opening Details produces a
// burst of ~12 parallel CH queries → CH busy → blank-then-pop
// flash. With LazyMount, only the panels in (or near) the
// viewport fire requests; the rest queue progressively as the
// operator scrolls.
//
// rootMargin defaults to 200px so a panel one screen down from
// the viewport edge starts loading just before the operator
// reaches it. minHeight gives the placeholder vertical mass so
// the page doesn't jump as panels rebalance after mount.
export function LazyMount({
  children,
  minHeight = 120,
  rootMargin = '200px 0px',
  compact = false,
}: {
  children: React.ReactNode;
  minHeight?: number | string;
  rootMargin?: string;
  /** v0.10.854 — hücre içi kullanım: çizgili panel kutusu yerine küçük "…" yer tutucu
   *  (tablo satırındaki çipler; satır başına fetch'i görünür satırla sınırlar). */
  compact?: boolean;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    if (mounted) return;
    const el = ref.current;
    if (!el) return;
    // Browsers without IntersectionObserver (none in our target
    // matrix, but cheap to guard) get an immediate mount — the
    // perf regression is preferable to a panel that never loads.
    if (typeof IntersectionObserver === 'undefined') {
      setMounted(true);
      return;
    }
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          setMounted(true);
          io.disconnect();
          return;
        }
      }
    }, { rootMargin });
    io.observe(el);
    return () => io.disconnect();
  }, [mounted, rootMargin]);

  if (mounted) return <>{children}</>;
  if (compact) {
    return <div ref={ref} style={{ display: 'inline-block', minHeight, minWidth: 24, color: 'var(--text3)', fontSize: 11 }}>…</div>;
  }
  return (
    <div ref={ref} style={{
      minHeight,
      borderRadius: 6,
      border: '1px dashed var(--border)',
      background: 'var(--bg2)',
      opacity: 0.4,
    }} />
  );
}
