// coremetry/SegmentedControl — Faz 2 dilim 1 R6 (plan G8, operatör Q-IA10:
// radiogroup anlamı KALIR). shadcn toggle-group (Radix ToggleGroup
// type="single") üstünde, ui/SegmentedControl ile AYNI props ve AYNI
// klavye sözleşmesi:
//  - kök role="radiogroup" + aria-label, öğeler role="radio" + aria-checked
//    (Radix single kipi bunu kendisi basar; shadcn'in aria-pressed
//    varsayılanı KULLANILMAZ);
//  - tek Tab durağı = seçili seçenek; o devre dışıysa ilk etkin seçenek;
//  - ←/→/↑/↓ sarar, Home/End; devre dışı atlanır; `activation="auto"` ok
//    seçer, `"manual"` yalnız odağı taşır (seçim tık/Enter/Boşluk);
//  - seçili öğeye tık seçimi KALDIRMAZ (Radix single'ın '' değeri yutulur).
// Radix'in gezici odağı KAPALI (rovingFocus={false}): klavye mantığı
// bugünkü atomun birebir kopyası — Radix'in setTimeout'lu odak taşıması ve
// "devre dışı seçili" durumundaki Tab davranışı sözleşmeyi değiştirirdi.
import { useRef, type KeyboardEvent, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { ToggleGroup, ToggleGroupItem } from '@/components/shadcn/toggle-group';

export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
  title?: string;
  disabled?: boolean;
}

export interface SegmentedControlProps<T extends string> {
  value: T;
  onChange: (v: T) => void;
  options: ReadonlyArray<SegmentedOption<T>>;
  /** Zorunlu: grubun erişilebilir adı. */
  'aria-label': string;
  size?: 'md' | 'sm';
  title?: string;
  className?: string;
  activation?: 'auto' | 'manual';
}

const KEYS: Record<string, 1 | -1 | 'home' | 'end'> = {
  ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1, Home: 'home', End: 'end',
};

export function SegmentedControl<T extends string>({
  value, onChange, options, size = 'md', title, className, activation = 'auto', ...aria
}: SegmentedControlProps<T>) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const enabled = options.map((o, i) => (o.disabled ? -1 : i)).filter(i => i >= 0);

  const move = (from: number, dir: 1 | -1 | 'home' | 'end') => {
    if (enabled.length === 0) return;
    let idx: number;
    if (dir === 'home') idx = enabled[0];
    else if (dir === 'end') idx = enabled[enabled.length - 1];
    else {
      const pos = enabled.indexOf(from);
      idx = enabled[(pos + dir + enabled.length) % enabled.length];
    }
    if (activation === 'auto') onChange(options[idx].value);
    refs.current[idx]?.focus();
  };
  const onKey = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    const d = KEYS[e.key];
    if (d === undefined) return;
    e.preventDefault();
    move(i, d);
  };

  const sel = options.findIndex(o => o.value === value);
  const selectedIdx = sel >= 0 && !options[sel].disabled ? sel : (enabled[0] ?? 0);
  return (
    <ToggleGroup type="single" rovingFocus={false} variant="outline" size={size === 'sm' ? 'sm' : 'default'}
      value={value} onValueChange={v => { if (v) onChange(v as T); }}
      aria-label={aria['aria-label']} title={title}
      className={cn(size === 'sm' && '[&>*]:h-7 [&>*]:text-xs', className)}>
      {options.map((o, i) => (
        <ToggleGroupItem key={o.value} value={o.value} ref={el => { refs.current[i] = el; }}
          tabIndex={i === selectedIdx ? 0 : -1} title={o.title} disabled={o.disabled}
          onKeyDown={e => onKey(e, i)}>
          {o.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
