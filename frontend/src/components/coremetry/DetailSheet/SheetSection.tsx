// SheetSection / SheetTrendRow — v0.10.1669 (etkileşim dilimi S1, plan §2.1).
// `ui/Drawer`ın DrawerSection + DrawerTrendRow'unun halefleri; aynı görünüm,
// inline stil yok. `.sheet-section` içindeki `.table-wrap` çerçevesini bırakır
// (globals.css, `.drawer-section` ile aynı kural — kutu içinde kutu yok).
import type { ReactNode } from 'react';
import { Sparkline } from '@/components/Sparkline';

export function SheetSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="sheet-section mb-[18px]">
      <div className="mb-[var(--sp-3)] text-[length:var(--fs-2xs)] font-semibold tracking-[0.5px] text-[var(--text3)] uppercase">{title}</div>
      {children}
    </div>
  );
}

/** Etiket + Sparkline satırı; `onClick` verilirse çağıran tıkla-büyüt davranışı ekler. */
export function SheetTrendRow({ label, values, color, onClick }: {
  label: string;
  values: number[];
  color: string;
  onClick?: () => void;
}) {
  return (
    <div className="flex items-center gap-[var(--sp-5)]">
      <span className="w-[60px] text-[length:var(--fs-xs)] text-[var(--text2)]">{label}</span>
      <Sparkline values={values} width={420} height={34} color={color}
        title={onClick ? `${label} — click to expand` : label} onClick={onClick} />
    </div>
  );
}
