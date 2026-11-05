// mlProfiles.ts — v0.10.1816 servis profilleri metinleri (SAF).
import type { MLServiceProfile, MLProfileEvidence } from './types';

export const PROFILE_PINNABLE = ['batch', 'low_traffic', 'business_hours', 'always_on'];

export const PROFILE_TR: Record<string, string> = {
  batch: 'Batch', low_traffic: 'Az çağrılan', business_hours: 'Mesai/şube', always_on: '7/24', unknown: 'Bilinmiyor',
};

const ratio = (v: number | undefined) => (v == null ? '—' : v.toLocaleString('tr-TR', { maximumFractionDigits: 2 }));

/** Kuralın kullandığı sayılar, düz: "günlük medyan 12.400 çağrı · mesai boş %0 · mesai dışı/içi 0,01 · hafta sonu/içi 0,02". */
export function profileEvidenceText(ev: MLProfileEvidence | undefined): string {
  if (!ev) return '—';
  return [
    `günlük medyan ${Math.round(ev.dailyMedianCalls).toLocaleString('tr-TR')} çağrı`,
    `mesai boş %${Math.round(ev.emptyBizShare * 100)}`,
    `mesai dışı/içi ${ratio(ev.offOnRatio)}`,
    `hafta sonu/içi ${ratio(ev.weekendRatio)}`,
  ].join(' · ');
}

/** "otomatik" | "elle · kişi". */
export function profileSourceText(r: MLServiceProfile): string {
  if (!r.pinned) return 'otomatik';
  return r.by ? `elle · ${r.by}` : 'elle';
}
