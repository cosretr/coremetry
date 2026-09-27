// Trace.monoStack.pin.test.ts — v0.10.977 (tablo standardı T5, dilim 7).
// Trace.tsx'in 5 satır içi monospace yığını (`ui-monospace, …`) TEK yığına
// indi: tablo dışı öğe `fontFamily: 'var(--font-mono)'` (kendi yazı boyu
// kalır — bağlı trace bağlantısı 11, herkese açık bağlantı kutusu 11, KPI
// değeri 13), 12px olan kimlik satırı `.mono` sınıfı (tablo dışında 12px).
// Ratchet (styles/tableUnityRatchet inlineMonoStack 9 → 4) tüm ağacı sayar;
// bu çivi dosyanın KENDİ sitelerini tutar ki başka bir dosyadaki düşüş burada
// geri gelen bir yığını maskelemesin.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');
/** tableUnityRatchet.test.ts `inlineMonoStack` deseniyle birebir. */
const MONO_STACK = /\bfont(?:Family)?:\s*(['"`])[^'"`\n]*monospace[^'"`\n]*\1/g;

describe('Trace.tsx — satır içi monospace yığını yok (v0.10.977, T5)', () => {
  it('ikinci yazım yığını kalmadı', () => {
    expect(src.match(MONO_STACK) ?? []).toHaveLength(0);
    expect(src).not.toContain('ui-monospace');
  });
  it('tablo dışı dört öğe `var(--font-mono)` ile, yazı boyları olduğu gibi', () => {
    // bağlı trace bağlantısı (11px)
    expect(src).toContain("style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>");
    // herkese açık bağlantı kutusu (11px)
    expect(src).toContain("style={{ flex: 1, fontSize: 11, fontFamily: 'var(--font-mono)' }} />");
    // paylaşım belirteci satırı (üst kabın 11px'i miras)
    expect(src).toContain("fontFamily: 'var(--font-mono)',\n                      flex: 1, overflow: 'hidden'");
    // KPI değeri (13px, 600)
    expect(src).toContain("fontSize: 13, fontWeight: 600,\n        color: tone === 'err' ? 'var(--err)' : 'var(--text)',\n        fontFamily: 'var(--font-mono)',");
    expect(src.match(/fontFamily: 'var\(--font-mono\)'/g)).toHaveLength(4);
  });
  it('12px kimlik satırı `.mono` sınıfı (rengi olduğu gibi)', () => {
    expect(src).toContain('<span className="mono" style={{ color: \'var(--text)\' }}>{shortIdentity(cand.value)}</span>');
  });
});
