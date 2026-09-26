// entityDetailRows.pin.test.ts — v0.10.857 (scale-audit 2026-09-23): EntityDetail'in
// iki düz tablosu >100 satırda content-visibility taşır (cluster entity'sinde
// penceredeki HER servis gelir); AdminCatalog serviceNames'i sunucu tavanına
// (1000) kadar ister — varsayılan 200 kataloğu sessizce kırpıyordu.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('büyük tablolar — content-visibility + katalog limiti', () => {
  // v0.10.947 (tablo standardı T6) — content-visibility tek sınıfta (`.cv-row`,
  // satır ritmi --row-h); >100 koşulu aynen korunur.
  it('EntityDetail iki satır tipi de >100 koşuluyla cv taşır', () => {
    const src = readFileSync(resolve(__dirname, 'EntityDetail.tsx'), 'utf8');
    expect(src).toContain("svc.services.length > 100 ? 'cv-row' : undefined");
    expect(src).toContain("rows.length > 100 ? 'cv-row' : undefined");
    expect(src).not.toContain('containIntrinsicSize');
  });
  it('AdminCatalog serviceNames limitini sunucu tavanına (1000) çıkarır', () => {
    const src = readFileSync(resolve(__dirname, 'AdminCatalog.tsx'), 'utf8');
    expect(src).toContain('api.serviceNames(undefined, 1000)');
    expect(src).not.toContain('api.serviceNames(),');
  });
});

// v0.10.876 — sunucu tavanına çarpınca etiket "liste EKSİK" der (hasMore/total okunur).
describe('AdminCatalog — tavan dürüstlüğü', () => {
  it('hasMore okunur ve etikete yansır', () => {
    const src = readFileSync(resolve(__dirname, 'AdminCatalog.tsx'), 'utf8');
    expect(src).toContain('svcResp?.hasMore');
    expect(src).toContain('liste EKSİK');
  });
});
