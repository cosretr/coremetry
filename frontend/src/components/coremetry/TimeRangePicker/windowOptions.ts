import { QUICK_PRESETS } from '@/lib/rangePicker';

/** `window` seçenekleri: istenen önayarlardan QUICK_PRESETS'te olanlar, onun sırasıyla. */
export function windowOptions(presets?: readonly string[]): string[] {
  if (!presets) return [...QUICK_PRESETS];
  return QUICK_PRESETS.filter(p => presets.includes(p));
}
