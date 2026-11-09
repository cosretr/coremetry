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
});
