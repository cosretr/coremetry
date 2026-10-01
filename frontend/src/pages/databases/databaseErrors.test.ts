import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { dbErrorLabel, dbErrorKindLabel, dbErrorShare, dbErrorServicesText } from './databaseErrors';

// v0.10.1020 — /database "hangi hata" bölümü (Databases × Dynatrace, dilim 2).

describe('hata imzası gösterimi', () => {
  it('mesajsız hata boş imza yerine açık bir etiket alır', () => {
    expect(dbErrorLabel({ signature: 'ORA-00001', kind: 'code' })).toBe('ORA-00001');
    expect(dbErrorLabel({ signature: '', kind: 'none' })).toBe('(mesajsız hata)');
    expect(dbErrorLabel({ signature: '', kind: 'message' })).toBe('(mesajsız hata)');
  });
  it('imza kaynağı etiketleri sunucunun dört türünü karşılar', () => {
    expect(dbErrorKindLabel('code')).toBe('hata kodu');
    expect(dbErrorKindLabel('type')).toBe('exception');
    expect(dbErrorKindLabel('message')).toBe('mesaj');
    expect(dbErrorKindLabel('none')).toBe('bilinmiyor');
    const go = readFileSync(resolve(__dirname, '../../../../internal/chstore/db_errors.go'), 'utf8');
    for (const k of ["'code'", "'type'", "'message'", "'none'"]) expect(go).toContain(k);
  });
  it('pay: toplam 0 iken yüzde uydurulmaz', () => {
    expect(dbErrorShare(25, 100)).toBe(25);
    expect(dbErrorShare(5, 0)).toBeNull();
  });
  it('çağıran özeti: en çok hata üreten + diğerlerinin sayısı', () => {
    expect(dbErrorServicesText({ topService: 'pay-api', services: 1 })).toBe('pay-api');
    expect(dbErrorServicesText({ topService: 'pay-api', services: 4 })).toBe('pay-api +3');
    expect(dbErrorServicesText({ topService: '', services: 2 })).toBe('2 servis');
    expect(dbErrorServicesText({ topService: '', services: 0 })).toBe('—');
  });
});

describe('kablolama', () => {
  it('sayfa bölümü detayla aynı kimlik + pencereyle çiziyor; bölüm kendi ucunu okuyor', () => {
    const page = readFileSync(resolve(__dirname, '../DatabaseDetail.tsx'), 'utf8');
    expect(page).toContain('<DatabaseErrorsSection refObj={refObj} range={range} fromNs={from} toNs={to} />');
    const sec = readFileSync(resolve(__dirname, './DatabaseErrorsSection.tsx'), 'utf8');
    expect(sec).toContain('api.databaseErrors(refObj.system, refObj.instance, refObj.dbName, fromNs, toNs, signal)');
    expect(sec).toContain("queryKey: ['database-errors', refObj.system, refObj.instance, refObj.dbName, fromNs, toNs]");
    expect(sec).toContain('<DataTableState dt={dt} {...state} />');
    expect(sec).toContain('hasError: true');
    const apiSrc = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
    expect(apiSrc).toContain('/api/databases/errors?system=');
  });
});
