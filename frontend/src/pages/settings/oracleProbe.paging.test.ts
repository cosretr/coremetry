// oracleProbe.paging.test — v0.10.1092 iki önkoşul düzeltmesi: (A) "Son UI
// testi" satırının hükmü — başarısız ama gerekçesiz kayıt dürüstçe "gerekçe yok"
// der; (B) özel SQL sayfa tavanı cümlesi Go ikiziyle (oracle.CustomTruncatedText)
// bayt bayt aynı.
import { describe, it, expect } from 'vitest';
import { customTruncatedText, uiTestVerdict } from './oracleProbe';

describe('oracleProbe — v0.10.1092', () => {
  it('UI testi hükmü', () => {
    expect(uiTestVerdict(true, undefined)).toEqual({ ok: true, text: 'başarılı' });
    expect(uiTestVerdict(false, 'ORA-12541: no listener')).toEqual({ ok: false, text: 'ORA-12541: no listener' });
    expect(uiTestVerdict(false, '')).toEqual({ ok: false, text: 'başarısız (gerekçe yok)' });
    expect(uiTestVerdict(undefined, '  ')).toEqual({ ok: false, text: 'başarısız (gerekçe yok)' });
  });

  it('sayfa tavanı cümlesi', () => {
    expect(customTruncatedText(10)).toBe('tavan: pencerenin tamamı okunamadı (10 sayfa)');
    expect(customTruncatedText(undefined)).toBe('tavan: pencerenin tamamı okunamadı (0 sayfa)');
  });
});
