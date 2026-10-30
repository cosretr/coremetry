// coremetry/Field — Faz 2 dilim 1 R2 (plan G1 + G10, D1). Etiketli
// Input / NativeSelect / Textarea / Checkbox alanları; shadcn label +
// input + native-select + textarea + checkbox üstünde TEK alan anatomisi:
// etiket 12px --text2 üstte (yanında isteğe bağlı `labelAside`: "· stored",
// rozet …), ipucu 11px --text3 altta, hata ipucunun YERİNDE
// --status-critical-fg. Denetim zemini `bg-field` (shadcn input'u
// bg-transparent — plan §2.2; v0.10.1423 kuralı). `useId` → htmlFor +
// aria-describedby (çağıranın aria-describedby'ı korunur, ipucu/hata id'si
// eklenir) + aria-invalid. Genişlik düzenden; `small` = 180px (Field2 small).
//
// Saf geçiş (plan R-1, secret sözleşmesi): value / defaultValue /
// placeholder / type / autoComplete OLDUĞU GİBİ denetime gider — kırpma,
// varsayılan değer, gizli input yok. Saklı bir secret'ı yankılamamak
// çağıranın işi (value=''), alan bunu bozmaz.
import { useId, type ComponentProps, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { Label } from '@/components/shadcn/label';
import { Input } from '@/components/shadcn/input';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Textarea } from '@/components/shadcn/textarea';
import { Checkbox } from '@/components/shadcn/checkbox';

export interface FieldBaseProps {
  label: ReactNode;
  hint?: ReactNode;
  /** Verilince ipucunun YERİNE basılır, denetim aria-invalid olur. */
  error?: ReactNode;
  /** Etiketin yanında (aynı satır): "· stored" metni, kimlik rozeti … */
  labelAside?: ReactNode;
  /** 180px sabit genişlik (yan yana küçük alanlar). */
  small?: boolean;
  /** Alan kabının (etiket + denetim + ipucu) sınıfı. */
  className?: string;
  /** Denetimin kendi sınıfı (yalnız düzen: genişlik, font-mono …). */
  controlClassName?: string;
}

const CONTROL = 'bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

function useFieldIds(id: string | undefined, hasNote: boolean, describedBy: string | undefined) {
  const auto = useId();
  const fieldId = id ?? auto;
  const noteId = `${fieldId}-hint`;
  const desc = [describedBy, hasNote ? noteId : undefined].filter(Boolean).join(' ') || undefined;
  return { fieldId, noteId, desc };
}

function FieldShell({ fieldId, noteId, label, labelAside, hint, error, small, className, children }: FieldBaseProps & {
  fieldId: string; noteId: string; children: ReactNode;
}) {
  return (
    <div data-cm-field="" className={cn('flex min-w-0 flex-col gap-1', small ? 'w-[180px]' : 'w-full', className)}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <Label htmlFor={fieldId} className="text-[length:var(--fs-sm)] font-normal leading-tight text-(--text2)">{label}</Label>
        {labelAside}
      </div>
      {children}
      {error
        ? <span id={noteId} className="text-[length:var(--fs-xs)] leading-snug text-status-critical">{error}</span>
        : hint
          ? <span id={noteId} className="text-[length:var(--fs-xs)] leading-snug text-(--text3)">{hint}</span>
          : null}
    </div>
  );
}

export interface FieldProps extends FieldBaseProps, Omit<ComponentProps<'input'>, 'children' | 'className' | 'size'> {}

export function Field({ label, hint, error, labelAside, small, className, controlClassName, id, 'aria-describedby': describedBy, ...input }: FieldProps) {
  const { fieldId, noteId, desc } = useFieldIds(id, !!(error || hint), describedBy);
  return (
    <FieldShell {...{ fieldId, noteId, label, labelAside, hint, error, small, className }}>
      <Input id={fieldId} aria-invalid={error ? true : input['aria-invalid']} aria-describedby={desc}
        className={cn('h-8', CONTROL, controlClassName)} {...input} />
    </FieldShell>
  );
}

export interface SelectFieldProps extends FieldBaseProps, Omit<ComponentProps<'select'>, 'className' | 'size'> {}

export function SelectField({ label, hint, error, labelAside, small, className, controlClassName, id, 'aria-describedby': describedBy, children, ...select }: SelectFieldProps) {
  const { fieldId, noteId, desc } = useFieldIds(id, !!(error || hint), describedBy);
  return (
    <FieldShell {...{ fieldId, noteId, label, labelAside, hint, error, small, className }}>
      <div className="w-full *:w-full">
        <NativeSelect id={fieldId} size="sm" aria-invalid={error ? true : select['aria-invalid']} aria-describedby={desc}
          className={cn(CONTROL, controlClassName)} {...select}>
          {children}
        </NativeSelect>
      </div>
    </FieldShell>
  );
}

export interface TextareaFieldProps extends FieldBaseProps, Omit<ComponentProps<'textarea'>, 'className'> {}

export function TextareaField({ label, hint, error, labelAside, small, className, controlClassName, id, rows = 4, 'aria-describedby': describedBy, ...ta }: TextareaFieldProps) {
  const { fieldId, noteId, desc } = useFieldIds(id, !!(error || hint), describedBy);
  return (
    <FieldShell {...{ fieldId, noteId, label, labelAside, hint, error, small, className }}>
      <Textarea id={fieldId} rows={rows} aria-invalid={error ? true : ta['aria-invalid']} aria-describedby={desc}
        className={cn('min-h-0 field-sizing-fixed', CONTROL, controlClassName)} {...ta} />
    </FieldShell>
  );
}

export interface CheckboxFieldProps {
  label: ReactNode;
  hint?: ReactNode;
  checked: boolean;
  onCheckedChange(checked: boolean): void;
  disabled?: boolean;
  id?: string;
  'aria-describedby'?: string;
  className?: string;
}

/** Kutu solda, etiket SAĞDA (D13). Radix Checkbox `<button role="checkbox">`;
 *  `<form>` içinde gizli input basmaz (name verilmez). */
export function CheckboxField({ label, hint, checked, onCheckedChange, disabled, id, 'aria-describedby': describedBy, className }: CheckboxFieldProps) {
  const { fieldId, noteId, desc } = useFieldIds(id, !!hint, describedBy);
  return (
    <div data-cm-field="" className={cn('flex min-w-0 flex-col gap-0.5', className)}>
      <div className="flex items-center gap-2">
        <Checkbox id={fieldId} checked={checked} disabled={disabled} aria-describedby={desc}
          onCheckedChange={v => onCheckedChange(v === true)} />
        <Label htmlFor={fieldId} className="text-[length:var(--fs-sm)] font-normal leading-tight">{label}</Label>
      </div>
      {hint && <span id={noteId} className="pl-6 text-[length:var(--fs-xs)] leading-snug text-(--text3)">{hint}</span>}
    </div>
  );
}
