// SegmentedControl — v0.10.914 (operatör 2026-09-25: "butonlarda bütünlük
// olsun, Traces'te bazıları farklılaşıyor"; spec + mockup Onay).
//
// Tek seçimli geçiş (Volume | Latency, Traces | Aggregated | Shapes). Depoda
// 14 dosya `.segmented` + ham `<button className={x ? 'active' : ''}>` kalıbını
// elle kuruyordu: her kopya kendi aria'sını (çoğu hiç) ve kendi klavye
// davranışını (hiç) taşıyordu. Görünüm BİLİNÇLİ olarak aynı `.segmented` /
// `.sg-sm` sınıfları (globals.css) — baskın kalıp; mevcut ekranlarda görsel
// fark yok. Eklenen: role=radiogroup/radio + aria-checked, gezici tabindex
// (grup tek Tab durağı), ←/→/Home/End ile seçim.
import { useRef, type KeyboardEvent, type ReactNode } from 'react';

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
  /** 'sm' → yoğun rung (.sg-sm, kart başlıkları). */
  size?: 'md' | 'sm';
  title?: string;
  className?: string;
}

export function SegmentedControl<T extends string>({
  value, onChange, options, size = 'md', title, className, ...aria
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
    onChange(options[idx].value);
    refs.current[idx]?.focus();
  };

  const onKey = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    const map: Record<string, 1 | -1 | 'home' | 'end'> = {
      ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1, Home: 'home', End: 'end',
    };
    const d = map[e.key];
    if (d === undefined) return;
    e.preventDefault();
    move(i, d);
  };

  const selectedIdx = Math.max(0, options.findIndex(o => o.value === value));
  return (
    <div role="radiogroup" aria-label={aria['aria-label']} title={title}
      className={['segmented', size === 'sm' ? 'sg-sm' : '', className].filter(Boolean).join(' ')}>
      {options.map((o, i) => {
        const on = o.value === value;
        return (
          <button key={o.value} ref={el => { refs.current[i] = el; }}
            type="button" role="radio" aria-checked={on}
            tabIndex={i === selectedIdx ? 0 : -1}
            className={on ? 'active' : undefined}
            title={o.title} disabled={o.disabled}
            onClick={() => onChange(o.value)}
            onKeyDown={e => onKey(e, i)}>
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
