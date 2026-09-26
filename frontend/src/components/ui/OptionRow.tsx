import { forwardRef, type ButtonHTMLAttributes } from 'react';

// OptionRow — v0.10.927 (buton bütünlüğü, Faz 2 artığı).
//
// Seçim listesi SATIRI: tam genişlik, sola yaslı, çok sütunlu içerik,
// "şu anki değer" hâli. Metrik seçicinin iki listesi ve alarm hedefinin
// SQL arama sonuçları bunu gerekçeli istisnayla ham `<button>` olarak
// kuruyordu: `Button` çocuklarını ortalı bir `.row` span'ına sarar (çok
// sütunlu satırı bozar), `MenuItem` `role=menuitem` basar (bu listeler
// menü değil). Yan etkisi de vardı: sayfanın `.mqe-opt:hover` (0,2,0)
// kuralı `button:hover:not(:disabled)` (0,2,1) karşısında KAYBEDİYORDU —
// fareyle üzerine gelinen satır dolu accent maviye boyanıyordu.
//
// Sözleşme: çocuklar DOĞRUDAN düğmeye (sarmalayıcı yok) — sütun düzeni
// çağıranın (`mqe-optcol`, `.stmt-pick-row` gibi YALNIZ yerleşim
// sınıfları). `selected` → `is-sel` + `aria-current` (listbox değil:
// `role=option` + `aria-activedescendant` isteyen girdi-güdümlü listeler
// Combobox / FilterQueryBox desenini kullanır).
export interface OptionRowProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Satır, alanın ŞU ANKİ değeri. */
  selected?: boolean;
}

export const OptionRow = forwardRef<HTMLButtonElement, OptionRowProps>(function OptionRow(
  { selected, className, type = 'button', children, ...rest },
  ref,
) {
  const classes = [
    'opt-row',
    selected ? 'is-sel' : '',
    className,
  ].filter(Boolean).join(' ');
  return (
    <button ref={ref} type={type} className={classes}
      aria-current={selected ? 'true' : undefined} {...rest}>
      {children}
    </button>
  );
});
