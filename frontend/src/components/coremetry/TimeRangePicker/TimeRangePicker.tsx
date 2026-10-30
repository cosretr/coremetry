// coremetry/TimeRangePicker — v0.10.1617 (shadcn Faz 1 R9, plan §6).
// DONDURULMUŞ API: bir küresel seçici + ~10 yerel pencere seçicisi
// (TREND_WINDOWS, COORD_WINDOWS, SINCE_OPTS … audit §4.1) tek bileşen.
// Faz 1 gövdesi: `global` → bugünkü components/TimeRangePicker (aynı
// { value, onChange }); `window` → shadcn native-select, seçenekler
// QUICK_PRESETS'in alt kümesi (PRESET_SECONDS tek kaynak). Kabuk bugünkü
// bileşeni doğrudan içe aktarmaya devam eder (kabuk dilimine dek).
import type { TimeRange } from '@/lib/types';
import { windowOptions } from './windowOptions';
import { TimeRangePicker as GlobalTimeRangePicker } from '@/components/TimeRangePicker';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { CM_STRINGS } from '../strings';

export interface TimeRangePickerProps {
  value: TimeRange;
  onChange(r: TimeRange): void;
  variant?: 'global' | 'window';
  /** QUICK_PRESETS alt kümesi (lib/rangePicker.ts). */
  presets?: readonly string[];
  /** `window` kipinde özel aralık; Faz 1'de desteklenmiyor (global kip ister). */
  allowCustom?: boolean;
  label?: string;
}

export function TimeRangePicker({ value, onChange, variant = 'global', presets, label }: TimeRangePickerProps) {
  if (variant === 'global') return <GlobalTimeRangePicker value={value} onChange={onChange} />;
  const opts = windowOptions(presets);
  return (
    <NativeSelect size="sm" aria-label={label ?? CM_STRINGS.window} value={value.preset}
      onChange={(e) => onChange({ preset: e.target.value })}>
      {opts.map(p => <NativeSelectOption key={p} value={p}>{p}</NativeSelectOption>)}
    </NativeSelect>
  );
}
