import type { CSSProperties, MouseEventHandler, ReactNode } from 'react';
import { Link, useInRouterContext } from 'react-router';
import { appLinkKind } from '@/lib/safeAppPath';

// SafeHref — v0.10.1315 (olay linki XSS): API/editör kaynaklı bir URL'yi
// href'e çizen TEK kapı.
//
// Neden: olay `link`i, katalog runbook/oncall/repo URL'leri, duyuru linki,
// DevOps dosya URL'leri `<a href={x}>` ile doğrudan çiziliyordu; React 18
// `javascript:` href'lerini engellemez → saklanan XSS. Karar appLinkKind'de
// (lib/safeAppPath.ts; Go aynası internal/safelink):
//   - iç (isSafeAppPath)  → react-router `<Link to>` (SPA; "/\host" asla)
//   - dış (http(s)://)     → `<a target=_blank rel="noopener noreferrer">`
//   - diğer (javascript:, data:, "//host", boş) → `fallback` (varsayılan hiç)
// Router dışında (bağlam yok) iç yol düz `<a href>` olur — yol zaten kapıdan
// geçti, yalnız SPA gezinmesi kaybolur. Ratchet: safeAppPath.ratchet.test.ts
// (v0.10.1315 bloğu).

export interface SafeHrefProps {
  href: string | null | undefined;
  children: ReactNode;
  title?: string;
  className?: string;
  style?: CSSProperties;
  onClick?: MouseEventHandler<HTMLAnchorElement>;
  /** Link çizilemezse (güvensiz ya da boş) yerine çizilen. */
  fallback?: ReactNode;
}

export function SafeHref({ href, children, title, className, style, onClick, fallback = null }: SafeHrefProps) {
  const inRouter = useInRouterContext();
  const kind = href ? appLinkKind(href) : null;
  if (!href || kind === null) return <>{fallback}</>;
  if (kind === 'internal' && inRouter) {
    return <Link to={href} title={title} className={className} style={style} onClick={onClick}>{children}</Link>;
  }
  if (kind === 'internal') {
    return <a href={href} title={title} className={className} style={style} onClick={onClick}>{children}</a>;
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer"
      title={title} className={className} style={style} onClick={onClick}>{children}</a>
  );
}
