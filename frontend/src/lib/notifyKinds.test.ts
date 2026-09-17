// notifyKinds.test.ts — v0.10.747 kanal başına olay türü süzgeci.
// Saf yardımcılar + kablolama pinleri: modal `kinds`i serileştirir,
// kanal tablosunda "Türler" kolonu var, tip sunucuyla aynı üç değer.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { NOTIFY_KIND_OPTIONS, kindsSummary, normalizeKinds, toggleKind } from './notifyKinds';

describe('notifyKinds — saf', () => {
  it('normalizeKinds: kırp, küçült, tekrar at, bilinmeyeni düşür, sıra korunur', () => {
    expect(normalizeKinds(undefined)).toEqual([]);
    expect(normalizeKinds([' Anomaly', 'problem', 'anomaly', 'bogus', ''])).toEqual(['anomaly', 'problem']);
    expect(normalizeKinds(['exception'])).toEqual(['exception']); // v0.10.782
    expect(normalizeKinds(['incident'])).toEqual(['incident']);
  });

  it('toggleKind: ekle/çıkar, sonuç kanonik sırada', () => {
    expect(toggleKind([], 'anomaly')).toEqual(['anomaly']);
    expect(toggleKind(['anomaly'], 'problem')).toEqual(['problem', 'anomaly']);
    expect(toggleKind(['problem', 'anomaly'], 'anomaly')).toEqual(['problem']);
  });

  it('kindsSummary: boş = Hepsi, dolu = etiketler', () => {
    expect(kindsSummary(undefined)).toBe('Hepsi');
    expect(kindsSummary([])).toBe('Hepsi');
    expect(kindsSummary(['anomaly'])).toBe('Anomali');
    expect(kindsSummary(['problem', 'anomaly'])).toBe('Problem · Anomali');
    // v0.10.782 — exception artık bilinen tür; bilinmeyen değer yine düşer.
    expect(kindsSummary(['exception'])).toBe('Exception');
    expect(kindsSummary(['bogus'])).toBe('Hepsi');
  });

  it('UI seçenekleri dört tür (incident v0.10.748, exception v0.10.782 opt-in)', () => {
    expect(NOTIFY_KIND_OPTIONS.map(o => o.value)).toEqual(['problem', 'anomaly', 'incident', 'exception']);
    expect(NOTIFY_KIND_OPTIONS[3].hint).toContain('Boş süzgeç bu türü kapsamaz');
  });
});

describe('notifyKinds — kablolama', () => {
  const modal = readFileSync(resolve(__dirname, '../pages/settings/ChannelModal.tsx'), 'utf8');
  const tab = readFileSync(resolve(__dirname, '../pages/settings/ChannelsTab.tsx'), 'utf8');
  const types = readFileSync(resolve(__dirname, 'types.ts'), 'utf8');

  it('modal kinds alanını serileştirir ve seçenekleri tek kaynaktan çizer', () => {
    expect(modal).toContain('kinds:');
    expect(modal).toContain('NOTIFY_KIND_OPTIONS.map(');
    expect(modal).toContain('toggleKind(');
  });

  it('kanal tablosunda Türler kolonu var', () => {
    expect(tab).toContain("id: 'kinds'");
    expect(tab).toContain('kindsSummary(');
  });

  it('tip sunucuyla aynı dört değer ve matchRules.kinds', () => {
    expect(types).toContain("export type NotifyKind = 'problem' | 'anomaly' | 'incident' | 'exception';");
    expect(types).toContain('kinds?: NotifyKind[];');
  });
});
