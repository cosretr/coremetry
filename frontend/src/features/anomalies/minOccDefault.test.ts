// minOccDefault.test.ts — v0.10.740 (operatör 2026-09-16: "exceptions'ta 1
// tane geldiyse dahil etme"). Varsayılan occurrence tabanı istemci ve sunucu
// aynı; "show all" 0'ı URL'e yazar (silerse varsayılan geri gelir).
//
// v0.10.949 (operatör 2026-09-26: "tek servisten gelen 5'ten küçük
// exception'ları göstermeyebiliriz") — taban 5, çoklu-servis istisnalı.
// Varsayılan kip = URL'de param yok: Inbox tabanı GÖNDERMEZ, /problems
// `floor: 'default'` gönderir; istisna sunucuda. "5+ only" düğmesi kalktı
// (varsayılanla aynı sayı, farklı anlam — kafa karıştırırdı).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const anom = readFileSync(resolve(__dirname, 'AnomaliesPage.tsx'), 'utf8');
const inbox = readFileSync(resolve(__dirname, '../../pages/Inbox.tsx'), 'utf8');
const go = readFileSync(resolve(__dirname, '../../../../internal/api/inbox.go'), 'utf8');

describe('varsayılan occurrence tabanı (v0.10.740 → v0.10.949)', () => {
  it('istemci ve sunucu aynı sayı: 5', () => {
    expect(anom).toContain('const DEFAULT_MIN_OCC = 5;');
    expect(inbox).toContain('const DEFAULT_MIN_OCC = 5;');
    expect(go).toContain('const inboxDefaultMinOcc = 5');
  });
  for (const [name, src] of [['Exceptions', anom], ['Inbox', inbox]] as const) {
    it(`${name}: param yoksa varsayılan; show all 0'ı yazar; varsayılana dönüş düğmesi`, () => {
      expect(src).toContain('if (raw === null) return DEFAULT_MIN_OCC;');
      expect(src).toContain("if (v === DEFAULT_MIN_OCC) next.delete('minOcc'); else next.set('minOcc', String(v));");
      expect(src).toContain('onClick={() => setMinOcc(0)}>show all</Button>');
      expect(src).toContain('onClick={() => setMinOcc(DEFAULT_MIN_OCC)}>{DEFAULT_MIN_OCC}+ (varsayılan)</Button>');
    });
    it(`${name}: varsayılan kip param yokluğu; istisna şeridi ve satır işareti var; "5+ only" yok`, () => {
      expect(src).toContain("const floorDefault = searchParams.get('minOcc') === null;");
      expect(src).toContain('minOcc > 0 && floorDefault ? (');
      expect(src).toContain('groups seen in ≥2 services at the same time');
      expect(src).toContain('single service, hidden');
      expect(src).toContain('spreadTitle(spread.n, spread.partners,');
      expect(src).not.toContain('5+ only');
    });
  }
  it('Inbox: varsayılan kipte tabanı göndermez (sunucu istisnalı varsayılanı uygular)', () => {
    expect(inbox).toContain("minOcc: searchParams.get('minOcc') === null ? undefined : minOcc,");
    expect(inbox).toContain('multi-service groups shown although below');
  });
  // v0.10.949 (operatör kararı 2026-09-26) — regressed gruplar tabanın
  // altında ve tek serviste de olsa varsayılan kipte görünür; şerit bunu
  // yayılım açık da kapalı da söyler, Inbox tutulan regressed sayısını yazar.
  it('regressed istisnası şeritte: iki sayfa da söyler; Inbox keptRegressed yalnız varsayılan-kip yanıtından', () => {
    for (const src of [anom, inbox]) {
      expect(src).toContain("{spreadOff ? ' and regressed groups' : <>, regressed groups and groups seen in ≥2 services");
    }
    expect(inbox).toContain('const keptRegressed = floorLive ? (inboxQ.data?.keptRegressed ?? 0) : 0;');
    expect(inbox).toContain('regressed groups shown although below {effFloor}');
    expect(go).toContain('"keptRegressed":   keptRegressed,');
  });
  // v0.10.949 — keepPreviousData: kip değişiminin hemen ardından yanıt
  // ÖNCEKİ (açık kip) yanıttır; varsayılan şerit onun tabanını/gizli
  // sayısını okumasın. Yayılım soft-fail'de (spreadAvailable=false)
  // "çoklu-servis" dili düşer.
  it('Inbox: sunucu taban alanlarına yalnız varsayılan-kip yanıtında güvenir; spreadOff düz dile düşer', () => {
    expect(inbox).toContain('const floorLive = inboxQ.data?.minOccDefault === true;');
    expect(inbox).toContain('const effFloor = floorLive ? (inboxQ.data?.minOcc ?? DEFAULT_MIN_OCC) : DEFAULT_MIN_OCC;');
    expect(inbox).toContain('{floorLive && hiddenByMinOcc > 0 &&');
    expect(inbox).toContain('const spreadOff = inboxQ.data?.spreadAvailable === false;');
    expect(inbox).toContain('{!spreadOff && keptBySpread > 0 &&');
  });
  it('/problems: varsayılan şerit floorDefault yanıtına bağlı; spreadOff düz dile düşer', () => {
    expect(anom).toContain('const defFloor = floorMeta.floorDefault === true ? (floorMeta.minOcc ?? DEFAULT_MIN_OCC) : DEFAULT_MIN_OCC;');
    expect(anom).toContain('{floorMeta.floorDefault === true && (floorMeta.hiddenByMinOcc ?? 0) > 0 &&');
    expect(anom).toContain('const spreadOff = floorMeta.spreadAvailable === false;');
    for (const src of [anom, inbox]) {
      expect(src).toContain("{spreadOff ? 'hidden' : <>groups below");
      expect(src).toContain('spreadOffFloorTitle(');
    }
  });
  it("/problems: varsayılan kipte floor: 'default', açık kipte minOccurrences", () => {
    expect(anom).toContain("floor: floorDefault ? 'default' : undefined,");
    expect(anom).toContain('minOccurrences: !floorDefault && minOcc > 0 ? minOcc : undefined,');
  });
});
