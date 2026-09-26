import { forwardRef, type ButtonHTMLAttributes } from 'react';

// TileButton — v0.10.927 (buton bütünlüğü, Faz 2 artığı).
//
// Tıklanabilir KARO: çerçeveli blok kart (etiket / değer / alt satır
// ALT ALTA), tıkla → ilgili grafiğe/sayfaya git. Bağımlılık panellerinin
// `Stat` / `GaugeStat` karoları bunu gerekçeli istisnayla ham `<button>`
// + JS hover (`onMouseEnter` ile satır-içi renk) olarak kuruyordu:
// `Button` çocuklarını YATAY `.row` span'ına sarar, dikey karo düzenini
// bozar.
//
// Çerçeve aynı ızgaradaki statik `div` ikiziyle AYNI (bg2, kenarlık,
// --radius-xs, 8/10 dolgu) — tıklanabilir ve tıklanamaz karo yan yana
// durur. Hover (kenarlık --accent2 + bg3) ve odak halkası CSS'te
// (`.tile-btn`). Çocuklar doğrudan düğmeye; `<button>` içine blok
// (`div`) konamaz → çağıran `span` + `display: block` kullanır.
// Detay sayfalarının `StatTile`ı farklı bir çerçeve (bg1) — onun
// tıklanabilir hâli `StatTile onClick`.
export type TileButtonProps = ButtonHTMLAttributes<HTMLButtonElement>;

export const TileButton = forwardRef<HTMLButtonElement, TileButtonProps>(function TileButton(
  { className, type = 'button', children, ...rest },
  ref,
) {
  const classes = ['tile-btn', className].filter(Boolean).join(' ');
  return (
    <button ref={ref} type={type} className={classes} {...rest}>
      {children}
    </button>
  );
});
