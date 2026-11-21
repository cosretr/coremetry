import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1253 — Overview metrik kipinde Failure rate yerine HTTP status kodları
// (VM); HTTP metriği yoksa eski OK/Errors paneline düşer.
const src = readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8');
const cat = readFileSync(resolve(__dirname, '../../lib/i18n.tr.ts'), 'utf8'); // v0.10.2315

describe('Overview HTTP Status paneli', () => {
  it('metrik kipinde status kodu sorgusu, başlık ve geri düşüş', () => {
    expect(src).toContain('statusCodeRateQuery(service,');
    // v0.10.1377 — env seçiliyse ortam eşleyicisi çözülene dek bekler.
    expect(src).toContain("enabled: metricMode && envReady,");
    expect(src).toContain('statusCodeRateQuery(service, Math.max(60, (to - from) / 1e9), envSel, family, infraEx)'); // v0.10.1429 aile; v0.10.2434 tesisat süzgeci
    // v0.10.2315 — başlıklar katalogda (ov.httpStatusTitle / ov.failTitleMetric); TR değerleri aynen.
    expect(src).toContain("title={showHttpStatus ? tl('ov.httpStatusTitle', lang) : metricMode ? tl('ov.failTitleMetric', lang)");
    expect(cat).toContain("'ov.httpStatusTitle': 'HTTP Status · metrik',");
    expect(cat).toContain("'ov.failTitleMetric': 'Failure rate · metrik',");
    expect(src).toMatch(/const showHttpStatus = metricMode && httpStatus\.series\.length > 0;/);
  });
  // v0.10.1259 — hata oranı hesaplanamıyorsa karo toplam isteği gösterir.
  it('Failure rate "—" yerine Toplam istek', () => {
    expect(src).toContain('{metricErrorsUnknown && totalReq !== null');
    expect(src).toContain("<KpiTile lab={tl('ov.totalReq', lang)}"); // v0.10.2315 — katalog anahtarı
    expect(cat).toContain("'ov.totalReq': 'Toplam istek',");
    expect(src).toContain("instant: '1'");
  });
});
