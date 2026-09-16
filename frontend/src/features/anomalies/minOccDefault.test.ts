// minOccDefault.test.ts — v0.10.740 (operatör 2026-09-16: "exceptions'ta 1
// tane geldiyse dahil etme"). Varsayılan occurrence tabanı 2, istemci ve
// sunucu aynı; "show all" 0'ı URL'e yazar (silerse varsayılan geri gelir).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const anom = readFileSync(resolve(__dirname, 'AnomaliesPage.tsx'), 'utf8');
const inbox = readFileSync(resolve(__dirname, '../../pages/Inbox.tsx'), 'utf8');
const go = readFileSync(resolve(__dirname, '../../../../internal/api/inbox.go'), 'utf8');

describe('varsayılan occurrence tabanı (v0.10.740)', () => {
  it('istemci ve sunucu aynı sayı: 2', () => {
    expect(anom).toContain('const DEFAULT_MIN_OCC = 2;');
    expect(inbox).toContain('const DEFAULT_MIN_OCC = 2;');
    expect(go).toContain('const inboxDefaultMinOcc = 2');
  });
  for (const [name, src] of [['Exceptions', anom], ['Inbox', inbox]] as const) {
    it(`${name}: param yoksa varsayılan; show all 0'ı yazar; varsayılana dönüş düğmesi`, () => {
      expect(src).toContain('if (raw === null) return DEFAULT_MIN_OCC;');
      expect(src).toContain("if (v === DEFAULT_MIN_OCC) next.delete('minOcc'); else next.set('minOcc', String(v));");
      expect(src).toContain('onClick={() => setMinOcc(0)}>show all</Button>');
      expect(src).toContain('onClick={() => setMinOcc(DEFAULT_MIN_OCC)}>{DEFAULT_MIN_OCC}+ (varsayılan)</Button>');
    });
  }
});
