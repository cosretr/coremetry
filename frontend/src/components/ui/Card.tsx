import type { CSSProperties, HTMLAttributes, ReactNode } from 'react';
import { Link, type LinkProps } from 'react-router-dom';

// Card is the headed-panel container that 30+ pages roll on
// their own with the same bg1+border+8px-radius inline style.
// `header`/`footer` slots keep the divider lines consistent
// without repeating border/padding in every caller.
//
// v0.10.928 (Y1, kartlar statik) — `.card` artık TEK kural ve statik:
// imleç, hover, geçiş yok. Tıklanabilir kart yalnız `CardLink` (aşağıda);
// gerçek bir `<a>` olduğu için Tab + Enter, ⌘/orta tık ve odak halkası
// tarayıcıdan gelir.
//
// `density="tight"` — v0.10.928'de KALDIRILDI. `.card-tight` hiçbir
// zaman uygulanmadı: ikinci `.card` bloğu aynı özgüllükte ve sonra
// geldiği için arka planı/dolguyu/radius'u eziyordu. O blok silinince
// sınıfı basmak 11 siteyi sessizce değiştirirdi (bg2, 10px, radius-sm);
// kural, prop ve çağrı yerlerindeki `density="tight"` birlikte gitti —
// görsel fark sıfır, uyumluluk kalıntısı yok.

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  header?: ReactNode;
  footer?: ReactNode;
  children?: ReactNode;
}

// v0.10.928 (Y2) — başlık/altlık çizgileri kartın İÇ ayracı → --divider.
const headStyle: CSSProperties = {
  marginBottom: 'var(--sp-5)',
  paddingBottom: 'var(--sp-4)',
  borderBottom: '1px solid var(--divider)',
  fontSize: 'var(--fs-md)', fontWeight: 600,
};
const footStyle: CSSProperties = {
  marginTop: 'var(--sp-5)',
  paddingTop: 'var(--sp-4)',
  borderTop: '1px solid var(--divider)',
  fontSize: 'var(--fs-xs)', color: 'var(--text3)',
};

// Card ve CardLink aynı başlık/gövde/altlık işaretlemesini paylaşır.
function CardSlots({ header, footer, children }: {
  header?: ReactNode; footer?: ReactNode; children?: ReactNode;
}) {
  return (
    <>
      {header && <div style={headStyle}>{header}</div>}
      {children}
      {footer && <div style={footStyle}>{footer}</div>}
    </>
  );
}

export function Card({
  header, footer, className, children, ...rest
}: CardProps) {
  const cls = ['card', className].filter(Boolean).join(' ');
  return (
    <div className={cls} {...rest}>
      <CardSlots header={header} footer={footer}>{children}</CardSlots>
    </div>
  );
}

// CardLink — v0.10.928 (Y1). Yalnız GERÇEKTEN bir yere giden kart.
// `.card-link` hover/odakta kenarlığı --border-strong'a çeker; renk ve
// alt çizgi `a {}` / `a:hover` kurallarından korunur. İçine düğme ya da
// başka link KOYULMAZ (`<a>` içinde etkileşimli öğe geçersiz) — içerik
// etiket, rozet, sayı. Sınıf listesi `cls` adında: `const classes`
// deseni primitiveClasses kapısında 'card'ı bu atomun TABAN sınıfı
// sayardı, oysa 21 dosya `className="card"` yazıyor.
export interface CardLinkProps extends LinkProps {
  header?: ReactNode;
  footer?: ReactNode;
}

export function CardLink({ header, footer, className, children, ...rest }: CardLinkProps) {
  const cls = ['card', 'card-link', className].filter(Boolean).join(' ');
  return (
    <Link className={cls} {...rest}>
      <CardSlots header={header} footer={footer}>{children}</CardSlots>
    </Link>
  );
}
