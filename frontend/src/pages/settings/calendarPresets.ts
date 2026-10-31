// calendarPresets.ts — v0.10.1750: "Türkiye" takvim ön ayarı (SAF; test).
// Sabit tatiller sunucuda her yıl otomatik (fixedHolidaysTR); dini bayramlar
// yıla bağlı → özel gün listesi (2027 verildi; sonraki yıllar Settings'ten).
import type { AnomalyCalendarConfig, AnomalySpecialDay } from '@/lib/types';

export const TR_RELIGIOUS_2027: AnomalySpecialDay[] = [
  { from: '2027-03-08', label: 'Ramazan Bayramı arifesi', kind: 'half-day' },
  { from: '2027-03-09', to: '2027-03-11', label: 'Ramazan Bayramı', kind: 'holiday' },
  { from: '2027-05-15', label: 'Kurban Bayramı arifesi', kind: 'half-day' },
  { from: '2027-05-16', to: '2027-05-19', label: 'Kurban Bayramı', kind: 'holiday' },
];

/** Ön ayarı mevcut ayarın üstüne uygular; elle girilmiş günler korunur, aynı tarih eklenmez. */
export function applyTurkeyPreset(v: AnomalyCalendarConfig): AnomalyCalendarConfig {
  const have = new Set(v.days.map(d => d.from));
  const days = [...v.days, ...TR_RELIGIOUS_2027.filter(d => !have.has(d.from))];
  return { ...v, timezone: 'Europe/Istanbul', fixedHolidaysTR: true, days,
    autoRules: { mid15: true, lastBusinessDay: true, yearEnd: true, preHolidayLastBusinessDay: true } };
}

/** Gelecek yıl için dini bayram (holiday/half-day) girdisi yoksa uyarı metni, varsa null. */
export function religiousWarning(days: AnomalySpecialDay[], now: Date): string | null {
  const y = now.getFullYear() + 1;
  const ok = days.some(d => d.from.startsWith(`${y}-`) && (d.kind === 'holiday' || d.kind === 'half-day'));
  return ok ? null : `${y} için dini bayram günleri listede yok — Diyanet takvimi yayımlanınca Settings'ten ekle.`;
}
