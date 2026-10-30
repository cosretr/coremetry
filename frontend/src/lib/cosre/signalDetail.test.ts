// signalDetail.test — v0.10.1659 (CoSRE v2 Faz 2b B4, plan §5.6): not ayrıntısı
// 512 BAYT UTF-8 (karakter değil); kesim kod noktası sınırında.
import { describe, it, expect } from 'vitest';
import { noteDetail, truncateUtf8, SIGNAL_DETAIL_MAX_BYTES } from './signalDetail';

const bytes = (s: string) => new TextEncoder().encode(s).length;

describe('truncateUtf8', () => {
  it.each([
    ['ascii sığar', 'a'.repeat(512), 512, 512],
    ['ascii taşar', 'a'.repeat(513), 512, 512],
    ['Türkçe 2 bayt: 256 ş = 512', 'ş'.repeat(256), 256, 512],
    ['Türkçe 2 bayt: 257 ş → 256', 'ş'.repeat(257), 256, 512],
    ['tek bayt + Türkçe: sınırda yarım ş yok', 'a' + 'ş'.repeat(300), 256, 511],
    ['emoji 4 bayt (vekil çift bölünmez)', '🔥'.repeat(200), 128, 512],
    ['boş', '', 0, 0],
  ])('%s', (_n, input, wantChars, wantBytes) => {
    const out = truncateUtf8(input);
    expect([...out].length).toBe(wantChars);
    expect(bytes(out)).toBe(wantBytes);
    expect(bytes(out)).toBeLessThanOrEqual(SIGNAL_DETAIL_MAX_BYTES);
    expect(input.startsWith(out)).toBe(true);
  });

  it('noteDetail: kırpar + boşluk temizler; boş → null', () => {
    expect(noteDetail('   ')).toBeNull();
    expect(noteDetail('  not  ')).toBe('not');
    expect(bytes(noteDetail('ğ'.repeat(400))!)).toBe(512);
  });
});
