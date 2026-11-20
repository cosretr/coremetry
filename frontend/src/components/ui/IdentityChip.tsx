import type { HTMLAttributes, ReactNode } from 'react';
import { Link } from 'react-router';
import { CopyButton } from '@/components/CopyButton';

// IdentityChip — v0.10.1514 (trace başlığı, operatör onayı "A iyi" 2026-10-24).
//
// Bir iş kimliğinin (operasyon adı, fonksiyon kodu, kanal kodu) TEK çip
// anatomisi: gri küçük-büyük harf ETİKET hücresi · mono DEĞER (bağlantı
// varsa gerçek <Link>) · isteğe bağlı İPUCU hücresi (warn = tahmin, amber;
// neutral = "+N" gibi ek bilgi) · KOPYALA hücresi. Eskiden üç yazım vardı:
// "Operasyon:" cell-hint + .op-chip bağlantısı, "FUNCTION_CODE:" cell-hint,
// kanal kodu düz metin; üçü yan yana üç ayrı görünümdü.
//
// Kök varsayılan `role="listitem"`: çipler bir `role="list"` satırında
// yaşar (Trace başlığı satır 2). Kök `title`/`data-*` çağırandan gelir
// (kaynak ipucu, `data-op-source`); değer bağlantısının erişilebilir adı
// `valueLabel` ile verilir (ipucu metni ekran okuyucuya da ulaşsın).
export interface IdentityChipProps extends Omit<HTMLAttributes<HTMLSpanElement>, 'children'> {
  /** Etiket hücresi (ör. "OPERASYON"). */
  label: string;
  /** Mono değer; kopyalanan da budur. */
  value: string;
  /** Verilirse değer gerçek bağlantıdır. */
  to?: string;
  /** Değer bağlantısının aria-label'ı. */
  valueLabel?: string;
  /** Değer hücresinin title'ı. */
  valueTitle?: string;
  /** Bağlantısız değerde ekran okuyucuya ek not (sr-only). */
  srNote?: string;
  hint?: ReactNode;
  hintTitle?: string;
  /** warn = tahmin (amber), neutral = ek bilgi. Varsayılan neutral. */
  hintTone?: 'warn' | 'neutral';
  /** Kopyala düğmesinin adı ("Operasyon adını kopyala"). */
  copyTitle: string;
}

export function IdentityChip({
  label, value, to, valueLabel, valueTitle, srNote, hint, hintTitle, hintTone = 'neutral', copyTitle,
  className, role = 'listitem', ...rest
}: IdentityChipProps) {
  return (
    <span role={role} className={['idchip', className].filter(Boolean).join(' ')} {...rest}>
      <span className="idchip__label">{label}</span>
      {to ? (
        <Link className="idchip__val" to={to} aria-label={valueLabel} title={valueTitle}>{value}</Link>
      ) : (
        <span className="idchip__val" title={valueTitle}>
          {value}{srNote && <span className="sr-only"> — {srNote}</span>}
        </span>
      )}
      {hint != null && hint !== '' && (
        <span className={hintTone === 'warn' ? 'idchip__hint is-warn' : 'idchip__hint'} title={hintTitle}>{hint}</span>
      )}
      <CopyButton cell value={value} title={copyTitle} />
    </span>
  );
}
