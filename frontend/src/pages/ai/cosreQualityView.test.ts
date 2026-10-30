import { describe, it, expect } from 'vitest';
import { reasonLabel, reasonIsPositive, pct, scoreTone, prefixStatsLine } from './cosreQualityView';

describe('cosreQualityView (CoSRE öğrenme Faz 2)', () => {
  it('bilinen türler Türkçe etiket, bilinmeyen aynen', () => {
    expect(reasonLabel('no_evidence')).toBe('kanıtsız cevap');
    expect(reasonLabel('rephrase')).toBe('yeniden soruldu');
    expect(reasonLabel('yeni_tur')).toBe('yeni_tur');
  });
  it('olumlu/olumsuz ayrımı', () => {
    expect(reasonIsPositive('copy')).toBe(true);
    expect(reasonIsPositive('problem_resolved')).toBe(true);
    expect(reasonIsPositive('tool_error')).toBe(false);
  });
  it('pct sınırlar ve NaN', () => {
    expect(pct(0.424)).toBe('%42');
    expect(pct(1.4)).toBe('%100');
    expect(pct(Number.NaN)).toBe('—');
  });
  it('yalnız sapan puan renklenir', () => {
    expect(scoreTone(0.1)).toBe('err');
    expect(scoreTone(0.3)).toBe('warn');
    expect(scoreTone(0.5)).toBeUndefined();
  });
  // v0.10.1637 — haftalık "dış bağımlılık öneki" oranı, tek satır.
  it('önek satırı: payda yok → boş-durum, var → sayı + oran', () => {
    expect(prefixStatsLine(undefined)).toContain('uygulandığı trace açıklaması yok');
    expect(prefixStatsLine({ applied: 0, added: 0, corrected: 0, addedRate: 0, correctedRate: 0 })).toContain('yok');
    expect(prefixStatsLine({ applied: 40, added: 6, corrected: 2, addedRate: 0.15, correctedRate: 0.05 }))
      .toBe('Dış bağımlılık öneki (son 7 gün, 40 açıklama): eklendi 6 (%15) · düzeltildi 2 (%5)');
  });
});
