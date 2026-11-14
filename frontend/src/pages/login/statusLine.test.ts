// v0.10.1994 — login sol alt durum satırı.
import { describe, it, expect } from 'vitest';
import { loginStatusLine, fmtStorage } from './statusLine';

describe('loginStatusLine', () => {
  it('servis sayısı · ClickHouse sıkıştırılmamış · durum', () => {
    expect(loginStatusLine({ enabled: true, ready: true, services_monitored: 1300,
      components: { state: 'ok', checked_at: 0, clickhouse_uncompressed_bytes: 12.4e12 } }, 0))
      .toBe('1,300 services monitored · ClickHouse 12 TB uncompressed · All operational');
  });
  it('özet paneli kapalıyken servis sayısı topolojiden; eksik parça atlanır; hiçbiri yoksa null', () => {
    expect(loginStatusLine({ enabled: false, ready: true }, 420)).toBe('420 services monitored');
    expect(loginStatusLine({ enabled: true, ready: true, components: { state: 'degraded', checked_at: 0 } }, 0)).toBe('Degraded');
    expect(loginStatusLine(undefined, 0)).toBeNull();
  });
  it('birim', () => {
    expect([fmtStorage(1.1e12), fmtStorage(980e9), fmtStorage(2.5e15)]).toEqual(['1.1 TB', '980 GB', '2.5 PB']);
  });
  it('v0.10.2165 — saklanan toplam span servislerin hemen ardında; yoksa/0 ise atlanır', () => {
    expect(loginStatusLine({ enabled: true, ready: true, services_monitored: 1300,
      components: { state: 'ok', checked_at: 0, spans_stored: 4_230_000_000 } }, 0))
      .toBe('1,300 services monitored · 4.2B spans stored · All operational');
    expect(loginStatusLine({ enabled: true, ready: true, services_monitored: 5,
      components: { state: 'ok', checked_at: 0, spans_stored: 0 } }, 0)).toBe('5 services monitored · All operational');
    expect(loginStatusLine({ enabled: true, ready: true,
      components: { state: 'ok', checked_at: 0, spans_stored: 340_000_000, clickhouse_uncompressed_bytes: 12.4e12 } }, 0))
      .toBe('340.0M spans stored · ClickHouse 12 TB uncompressed · All operational');
  });
  it('v0.10.2165 — yalnız spans_today varsa span parçası YOK (bugünkü sayıya geri düşülmez)', () => {
    expect(loginStatusLine({ enabled: true, ready: true, services_monitored: 1300, spans_today: 4_230_000_000,
      components: { state: 'ok', checked_at: 0 } }, 0))
      .toBe('1,300 services monitored · All operational');
    // İkisi de varsa satır saklananı yazar; bugünkü sayı satıra hiç girmez.
    expect(loginStatusLine({ enabled: true, ready: true, services_monitored: 1300, spans_today: 61_000_000,
      components: { state: 'ok', checked_at: 0, spans_stored: 4_230_000_000 } }, 0))
      .toBe('1,300 services monitored · 4.2B spans stored · All operational');
  });
});
