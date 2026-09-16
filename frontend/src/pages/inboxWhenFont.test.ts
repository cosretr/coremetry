// inboxWhenFont.test.ts — v0.10.736 (operatör, prod ekran görüntüsü: "tarih
// fontu biraz daha büyük olabilir"). Inbox First seen / Last seen hücreleri
// 11 px satır-içi stilden 13 px sınıfa; yaş alt satırı 11 px soluk; kolon
// genişlikleri damgayı sığdırır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const page = readFileSync(resolve(__dirname, 'Inbox.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');

describe('Inbox tarih hücreleri (v0.10.736)', () => {
  it('iki damga hücresi sınıfta (satır-içi 11 px yok); yaş alt satırı sınıfta', () => {
    expect((page.match(/<td className="mono ib-when">/g) ?? []).length).toBe(2);
    expect(page).not.toContain('<td className="mono" style={{ fontSize: 11 }}>');
    expect(page).toContain('<div className="ib-when__ago">');
  });
  it('CSS: damga --fs-md (13 px), yaş --fs-xs soluk', () => {
    expect(css).toContain('.ib-when { font-size: var(--fs-md); }');
    expect(css).toMatch(/\.ib-when__ago \{ color: var\(--text3\);[^}]*font-size: var\(--fs-xs\)/);
  });
  it('kolon genişlikleri 13 px damgayı sığdırır', () => {
    expect(page).toContain("{ id: 'firstSeen', label: 'First seen', sortValue: it => it.startedAt, naturalDir: 'desc', width: 168 }");
    expect(page).toContain("{ id: 'lastSeen', label: 'Last seen', sortValue: it => it.lastSeen,        naturalDir: 'desc', width: 180 }");
  });
});

// v0.10.739 — aynı sınıf operatör-yüzeyli listelere yayıldı; satır-içi 11 px
// tarih hücresi bu dosyalarda kalmadı.
describe('tarih damgası sınıfı diğer listelerde (v0.10.739)', () => {
  const files: [string, number][] = [
    ['../features/anomalies/AnomaliesPage.tsx', 2],
    ['../features/anomalies/streams.tsx', 2],
    ['Incidents.tsx', 1],
    ['explore/TracesResult.tsx', 1],
  ];
  for (const [f, n] of files) {
    it(`${f}: ${n} hücre .ib-when, satır-içi 11 px tsLong hücresi yok`, () => {
      const src = readFileSync(resolve(__dirname, f), 'utf8');
      expect((src.match(/className="mono(?: row-cell)? ib-when"/g) ?? []).length).toBe(n);
      expect(src).not.toMatch(/<td className="mono(?: row-cell)?" style=\{\{ fontSize: 11[^}]*\}\}>(?:<Link[^>]*>)?\{tsLong\(/);
    });
  }
});
