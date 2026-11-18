// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import moment from 'moment-timezone';
import { dateTimeFormat } from '@grafana/data';

// v0.10.2351 — `moment-timezone` paketin 10 yıllık veri derlemesine takma
// adlı (vite.config.ts + vitest ikizi). Kanıt: @grafana/data adı verilen bir
// bölgede biçimlendirir (DST dahil), konsola tz hatası basılmaz ve yüklenen
// veri tam veritabanı DEĞİL (ilk geçiş 2020'den önce olamaz).
const FE = resolve(__dirname, '..', '..', '..');
const TARGET = 'node_modules/moment-timezone/builds/moment-timezone-with-data-10-year-range.js';

describe('kırpılmış saat dilimi verisi', () => {
  it('vite ve vitest aynı derlemeye takma ad verir', () => {
    for (const f of ['vite.config.ts', 'vitest.config.ts']) {
      const src = readFileSync(resolve(FE, f), 'utf8');
      expect(src, f).toContain('{ find: /^moment-timezone$/');
      expect(src, f).toContain(TARGET);
    }
  });

  it('adı verilen bölgede biçimlendirir, konsola hata basmaz', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    const t = Date.UTC(2026, 6, 1, 12, 0, 0); // yaz saati (DST) içinde
    expect(dateTimeFormat(t, { timeZone: 'America/New_York', format: 'YYYY-MM-DD HH:mm Z' })).toBe('2026-07-01 08:00 -04:00');
    expect(dateTimeFormat(t, { timeZone: 'Europe/Istanbul', format: 'YYYY-MM-DD HH:mm Z' })).toBe('2026-07-01 15:00 +03:00');
    expect(dateTimeFormat(Date.UTC(2026, 0, 15, 12), { timeZone: 'America/New_York', format: 'HH:mm Z' })).toBe('07:00 -05:00');
    expect(err).not.toHaveBeenCalled();
    err.mockRestore();
  });

  it('yüklenen veri 10 yıllık derleme (tam veritabanı değil)', () => {
    const untils = moment.tz.zone('America/New_York')!.untils.filter(Number.isFinite);
    expect(untils[0]).toBeGreaterThanOrEqual(Date.UTC(2015, 0, 1));
    expect(moment.tz.names().length).toBeGreaterThan(400);
  });
});
