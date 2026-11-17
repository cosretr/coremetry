// i18n.test.ts — v0.10.1334 rehberli tur metinleri: kullanılan her tour.*
// anahtarı (adım başlık/gövdeleri + çerçeve + notlar) İKİ katalogda da var
// ve iki katalogdaki tour.* kümeleri birebir aynı. Eksik TR anahtarı sessizce
// İngilizce'ye düşer (lib/i18n.ts t() geri dönüşü) — hiçbir kapı görmez.
// v0.10.1336 (R2) — adım 3–5'in kare (alt adım) metinleri, copilot_disabled
// notu ve CoSRE soru şablonu ({svc}) da kapsamda.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';
import { TOUR_CHROME_KEYS, TOUR_STEPS, stepFrames } from './steps';

// v0.10.2307 — EN gövdesi lib/i18n.en.ts'te (tembel chunk); i18n.ts'te TR'den
// önce yalnız EN_EAGER kalır — EN kümesi ikisinin birleşimi.
const SRC = readFileSync(resolve(__dirname, '../../lib/i18n.ts'), 'utf8');
const EN_SRC = readFileSync(resolve(__dirname, '../../lib/i18n.en.ts'), 'utf8');
const enStart = EN_SRC.indexOf('export const EN: Record<string, string> = {');
const eagerStart = SRC.indexOf('const EN_EAGER: Catalog = {');
const trStart = SRC.indexOf('const TR: Catalog = {');
const end = SRC.indexOf('let EN: Catalog | null');
const block = (a: number, b: number) => SRC.slice(a, b);
const tourKeys = (s: string) => new Set([...s.matchAll(/^\s*'(tour\.[\w.]+)'\s*:/gm)].map(m => m[1]));

const EN = tourKeys(EN_SRC.slice(enStart) + block(eagerStart, trStart));
const TR = tourKeys(block(trStart, end));

const USED = [
  ...TOUR_STEPS.flatMap(s => [s.titleKey, s.bodyKey]),
  ...TOUR_STEPS.flatMap(s => stepFrames(s).flatMap(f => [f.bodyKey, ...(f.titleKey ? [f.titleKey] : [])])),
  ...TOUR_CHROME_KEYS,
  ...(['not_yet', 'no_services', 'copilot_disabled'] as const).map(r => `tour.note.${r}`),
];

describe('tour i18n (v0.10.1334)', () => {
  it('katalog blokları bulundu', () => {
    expect(enStart).toBeGreaterThan(-1);
    expect(eagerStart).toBeGreaterThan(-1);
    expect(trStart).toBeGreaterThan(eagerStart);
    expect(end).toBeGreaterThan(trStart);
    expect(EN.size).toBeGreaterThan(0);
  });

  it('kullanılan her tour.* anahtarı EN ve TR\'de', () => {
    const missing = USED.flatMap(k => [!EN.has(k) ? `EN ${k}` : '', !TR.has(k) ? `TR ${k}` : '']).filter(Boolean);
    expect(missing).toEqual([]);
  });

  it('EN ve TR tour.* kümeleri birebir', () => {
    expect([...EN].sort()).toEqual([...TR].sort());
  });

  it('TR çevirisi İngilizce\'ye düşmüyor (çalışma zamanı)', () => {
    for (const k of USED) {
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'en'), k).not.toBe(k);
    }
    expect(t('tour.note.no_services', 'tr')).toContain('Henüz servis raporlamıyor');
  });

  it('v0.10.1336 — CoSRE sorusu iki dilde {svc} taşıyor; kapalı notu operatörün metni', () => {
    expect(t('tour.cosre.prompt', 'en')).toBe('Why is {svc} slow?');
    expect(t('tour.cosre.prompt', 'tr')).toBe('Son 1 saatte {svc} hatalı mı?');
    expect(t('tour.note.copilot_disabled', 'tr')).toContain('CoSRE bu kurulumda kapalı');
    expect(t('tour.note.copilot_disabled', 'tr')).toContain('Settings › CoSRE');
    // R1'in yer tutucu gövdeleri kalmadı.
    expect(t('tour.operations.body', 'en')).not.toBe('Latency and errors per operation of one service.');
  });

  it('stepOf şablonu iki dilde {n} ve {total} taşıyor', () => {
    for (const lang of ['en', 'tr'] as const) {
      expect(t('tour.chrome.stepOf', lang)).toContain('{n}');
      expect(t('tour.chrome.stepOf', lang)).toContain('{total}');
    }
  });
});
