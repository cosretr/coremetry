// oracleGroup.test — v0.10.1092 (operatör: "Oracle hataları Exceptions gibi
// görünsün"). Satır metinleri (başlık "<kod> · <operasyon>", soluk satır
// "<etiket> · <kaynak> · N servis"), sentetik servis, kanal kırılımı, çip URL'i.
// v0.10.1108 (operatör: "exceptionsta Oracle yazıyor onun yerine … Teknik Hata
// gibi") — görünen ad parametre: varsayılan "Teknik hata", özel etiket aynen;
// branding erişimcisi kırpar, 40 karaktere keser, boşu varsayılana düşürür.
import { describe, it, expect } from 'vitest';
import type { ExceptionGroup } from '@/lib/types';
import {
  isOracleGroup, isSyntheticOracleService, oracleRowTitle, oracleRowDetail, oracleSourceName,
  oracleChannelsText, oracleRowTooltip, parseOracleFacet, oracleFacetParam, oracleExplainLine,
  oracleFacetLabels, oracleInboxDetail,
} from './oracleGroup';
import { DEFAULT_BRANDING, oracleGroupLabelOf, resolveBranding } from '@/lib/branding';

const L = DEFAULT_BRANDING.oracleGroupLabel; // "Teknik hata"

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
    expect(oracleRowDetail(g(), L)).toBe('Teknik hata · app-err · 2 servis');
    // Bilgi yoksa (deep-link GET) kaynak sentetik servisten, servis bilinmiyor.
    expect(oracleRowDetail(g({ oracle: undefined, service: 'oracle:app-err' }), L)).toBe('Teknik hata · app-err · servis bilinmiyor');
    expect(oracleRowDetail(g(), 'DB hatası')).toBe('DB hatası · app-err · 2 servis');
    expect(oracleSourceName(g({ oracle: undefined, service: 'svc-x' }))).toBe('?');
  });

  // v0.10.1109 — Problems kuyruğu satırı da etiketi taşır (1108'de yalnız Exceptions).
  it('Problems satırı soluk satırı: "<etiket> · <operasyon>"', () => {
    expect(oracleInboxDetail('OP_TRANSFER', L)).toBe('Teknik hata · OP_TRANSFER');
    expect(oracleInboxDetail('  ', L)).toBe('Teknik hata');
    expect(oracleInboxDetail('OP_TRANSFER', 'DB hatası')).toBe('DB hatası · OP_TRANSFER');
  });

  it('sentetik servis link almaz', () => {
    expect(isSyntheticOracleService('oracle:app-err')).toBe(true);
    expect(isSyntheticOracleService('')).toBe(true);
    expect(isSyntheticOracleService('svc-payments')).toBe(false);
  });

  it('kanal kırılımı yüzde + taşma', () => {
    expect(oracleChannelsText(g().oracle)).toBe('MOB %60 · WEB %30 · ATM %6 · +1');
    expect(oracleChannelsText(undefined)).toBe('');
    expect(oracleRowTooltip(g(), L)).toContain('Servisler: svc-payments, svc-cards');
    expect(oracleRowTooltip(g(), 'DB hatası')).toContain('DB hatası · app-err · 2 servis');
    expect(oracleRowTooltip(g(), L).split('\n')[1]).toBe('Teknik hata · app-err · 2 servis');
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
    expect(oracleExplainLine(g().oracle, L)).toBe('Teknik hata · app-err · APP_ERR_001 · OP_TRANSFER');
    expect(oracleExplainLine({ sourceName: ' ', code: 'APP_ERR_001', operation: '' }, L)).toBe('Teknik hata · APP_ERR_001');
    expect(oracleExplainLine(undefined, L)).toBe('Teknik hata grubu');
    expect(oracleExplainLine(null, 'DB hatası')).toBe('DB hatası grubu');
    expect(oracleExplainLine(g().oracle, 'DB hatası')).toBe('DB hatası · app-err · APP_ERR_001 · OP_TRANSFER');
  });

  // v0.10.1108 — çip etiketleri + branding erişimcisi.
  it('çip etiketleri görünen adı taşır', () => {
    const fmt = (n: number) => String(n);
    expect(oracleFacetLabels(L, 2, fmt)).toEqual({ only: 'Teknik hata 2', exclude: 'Teknik hata hariç' });
    expect(oracleFacetLabels('DB hatası', -1, fmt)).toEqual({ only: 'DB hatası', exclude: 'DB hatası hariç' });
  });

  it('branding etiketi: varsayılan, kırpma, 40 karakter tavanı', () => {
    expect(L).toBe('Teknik hata');
    expect(oracleGroupLabelOf(undefined)).toBe('Teknik hata');
    expect(oracleGroupLabelOf({ oracleGroupLabel: '   ' })).toBe('Teknik hata');
    expect(oracleGroupLabelOf({ oracleGroupLabel: '  DB hatası ' })).toBe('DB hatası');
    expect(oracleGroupLabelOf({ oracleGroupLabel: 'ş'.repeat(45) })).toBe('ş'.repeat(40));
    expect(resolveBranding(null).oracleGroupLabel).toBe('Teknik hata');
    expect(resolveBranding({ oracleGroupLabel: 'DB hatası' }).oracleGroupLabel).toBe('DB hatası');
  });
});
