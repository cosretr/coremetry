import type { ReactNode } from 'react';

// SectionHead — v0.10.715 (servis sekmeleri tasarım etüdü, mockup 3b03fe22,
// operatör onayı 2026-09-13). Sayfa içi bölüm başlığının TEK atomu: ad ·
// kaynak rozeti (hangi tablodan/depodan okunuyor) · ek rozetler (kapsam) ·
// çizgi · sağda sayı/tazelik ve eylemler. Details'in `.dtl-sech` mikro
// başlık dilini (uppercase, çizgi) temel alır; slotlar eklenir. Kaynak
// rozeti dürüstlük içindir: operatör "bu sayı nereden" sorusunu başlıktan
// okur. `id` ToC scroll-spy için elemanın kendisinde kalır.
export interface SectionHeadProps {
  id?: string;
  title: ReactNode;
  /** Okunan kaynak (tablo/MV/depo adı) — mono rozet. */
  source?: string;
  /** Kapsam vb. ek rozetler (başlığın hemen sağında). */
  badges?: ReactNode;
  /** Sağ uç: sayı, tazelik, link. */
  meta?: ReactNode;
  /** Sağ uç: düğmeler. */
  actions?: ReactNode;
}

export function SectionHead({ id, title, source, badges, meta, actions }: SectionHeadProps) {
  return (
    <div className="dtl-sech sec-head" id={id}>
      <span className="sec-head__title">{title}</span>
      {source && <span className="badge b-gray sec-head__src" title="Bu bölümün okuduğu kaynak">{source}</span>}
      {badges}
      <span className="sec-head__rule" aria-hidden="true" />
      {meta && <span className="sec-head__meta">{meta}</span>}
      {actions && <span className="sec-head__actions">{actions}</span>}
    </div>
  );
}
