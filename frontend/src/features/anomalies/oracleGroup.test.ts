// oracleGroup.test — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün"). Satır metinleri (başlık "<kod> · <operasyon>", soluk satır
// "Oracle · <kaynak> · N servis"), sentetik servis, kanal kırılımı, çip URL'i.
import { describe, it, expect } from 'vitest';
import type { ExceptionGroup } from '@/lib/types';
import {
  isOracleGroup, isSyntheticOracleService, oracleRowTitle, oracleRowDetail, oracleSourceName,
  oracleChannelsText, oracleRowTooltip, parseOracleFacet, oracleFacetParam, oracleExplainLine,
} from './oracleGroup';

const g = (over: Partial<ExceptionGroup> = {}): ExceptionGroup => ({
  fingerprint: 'ora:42c30d3ac1ddbf2c', type: 'APP_ERR_001', message: 'OP_TRANSFER', service: 'svc-payments',
  state: 'new', assignee: '', firstSeen: 1, lastSeen: 2, occurrences: 6000, notes: '',
  oracle: {
    sourceId: 'o-11111111', sourceName: 'app-err', code: 'APP_ERR_001', operation: 'OP_TRANSFER',
    channels: [{ name: 'MOB', count: 60 }, { name: 'WEB', count: 30 }, { name: 'ATM', count: 6 }, { name: 'IVR', count: 4 }],
    services: [{ name: 'svc-payments', count: 9 }, { name: 'svc-cards', count: 3 }],
    serviceCount: 2, known: true,
  },
  ...over,
});

describe('oracleGroup', () => {
  it('ora: öneki Oracle grubunu ayırır', () => {
    expect(isOracleGroup(g())).toBe(true);
    expect(isOracleGroup('0a1b2c3d4e5f6a7b')).toBe(false);
  });

  it('satır başlığı ve soluk satır', () => {
    expect(oracleRowTitle(g())).toBe('APP_ERR_001 · OP_TRANSFER');
    expect(oracleRowTitle(g({ message: ' ' }))).toBe('APP_ERR_001');
    expect(oracleRowDetail(g())).toBe('Oracle · app-err · 2 servis');
    // Bilgi yoksa (deep-link GET) kaynak sentetik servisten, servis bilinmiyor.
    expect(oracleRowDetail(g({ oracle: undefined, service: 'oracle:app-err' }))).toBe('Oracle · app-err · servis bilinmiyor');
    expect(oracleSourceName(g({ oracle: undefined, service: 'svc-x' }))).toBe('?');
  });

  it('sentetik servis link almaz', () => {
    expect(isSyntheticOracleService('oracle:app-err')).toBe(true);
    expect(isSyntheticOracleService('')).toBe(true);
    expect(isSyntheticOracleService('svc-payments')).toBe(false);
  });

  it('kanal kırılımı yüzde + taşma', () => {
    expect(oracleChannelsText(g().oracle)).toBe('MOB %60 · WEB %30 · ATM %6 · +1');
    expect(oracleChannelsText(undefined)).toBe('');
    expect(oracleRowTooltip(g())).toContain('Servisler: svc-payments, svc-cards');
  });

  it('Oracle çipi URL değeri: yok = dahil', () => {
    expect(parseOracleFacet(null)).toBe('all');
    expect(parseOracleFacet('only')).toBe('only');
    expect(parseOracleFacet('exclude')).toBe('exclude');
    expect(parseOracleFacet('ONLY')).toBe('all');
    expect(oracleFacetParam('all')).toBeUndefined();
    expect(oracleFacetParam('only')).toBe('only');
  });

  // v0.10.1100 — AI paneli başlık satırı (kod çipi yerine).
  it('AI paneli Oracle bağlam satırı', () => {
    expect(oracleExplainLine(g().oracle)).toBe('Oracle · app-err · APP_ERR_001 · OP_TRANSFER');
    expect(oracleExplainLine({ sourceName: ' ', code: 'APP_ERR_001', operation: '' })).toBe('Oracle · APP_ERR_001');
    expect(oracleExplainLine(undefined)).toBe('Oracle hata grubu');
    expect(oracleExplainLine(null)).toBe('Oracle hata grubu');
  });
});
