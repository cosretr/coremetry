// v0.10.1816 — servis profilleri metinleri: kanıt sayıları düz, tanımsız oran "—", kaynak kim.
import { describe, it, expect } from 'vitest';
import { profileEvidenceText, profileSourceText, PROFILE_TR, PROFILE_PINNABLE } from './mlProfiles';

describe('mlProfiles', () => {
  it('kanıt metni dört sayıyı düz yazar; tanımsız oran tire', () => {
    expect(profileEvidenceText({ dailyMedianCalls: 12400.4, emptyBizShare: 0.25, offOnRatio: 0.0123, weekendRatio: undefined, weeks: 4 }))
      .toBe('günlük medyan 12.400 çağrı · mesai boş %25 · mesai dışı/içi 0,01 · hafta sonu/içi —');
    expect(profileEvidenceText(undefined)).toBe('—');
  });
  it('kaynak: otomatik / elle · kişi', () => {
    const base = { service: 'orders-api', profile: 'always_on', changedAt: 1, rev: 1 };
    expect(profileSourceText({ ...base, pinned: false, source: 'auto' })).toBe('otomatik');
    expect(profileSourceText({ ...base, pinned: true, source: 'manual', by: 'ops@example.com' })).toBe('elle · ops@example.com');
  });
  it('her sabitlenebilir profilin Türkçe adı var', () => {
    for (const p of [...PROFILE_PINNABLE, 'unknown']) expect(PROFILE_TR[p]).toBeTruthy();
  });
});
