import type { ReactNode } from 'react';

// SectionHead — v0.10.715 (Faz 2 dilim 1 R4: ui/ → coremetry/, değişmeden + `description`) (servis sekmeleri tasarım etüdü, mockup 3b03fe22,
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
  /** Faz 2 dilim 1 R4 (G6, additive): başlığın ALTINDA bir satır açıklama
   *  (13px --text2). Verilmezse çıktı bugünküyle birebir aynı. */
  description?: ReactNode;
}

export function SectionHead({ id, title, source, badges, meta, actions, description }: SectionHeadProps) {
  const head = (
    <div className="dtl-sech sec-head" id={id}>
      <span className="sec-head__title">{title}</span>
      {source && <span className="badge b-gray sec-head__src" title="Bu bölümün okuduğu kaynak">{source}</span>}
      {badges}
      <span className="sec-head__rule" aria-hidden="true" />
      {meta && <span className="sec-head__meta">{meta}</span>}
      {actions && <span className="sec-head__actions">{actions}</span>}
    </div>
  );
  if (!description) return head;
  return (
    <>
      {head}
      <p className="-mt-1 mb-2.5 text-[length:var(--fs-md)] text-(--text2)">{description}</p>
    </>
  );
}
