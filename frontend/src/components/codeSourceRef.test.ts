import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { codeSourceRef } from './codeSourceRef';

// v0.10.1044 (operatör: "Kod, dalın ucundan değil çalışan sürümden okunsun") —
// "Kaynak:" satırı kodun okunduğu ref'i söyler: çalışan sürüm mü, dal mı.
describe('codeSourceRef', () => {
  it('çalışan sürümün commit\'inden okunduysa sürüm', () => {
    expect(codeSourceRef({ version: '1.4.2', branch: 'release' })).toBe(' · 1.4.2 (çalışan sürüm)');
  });
  it('sürüm yoksa ya da bulunamadıysa dal', () => {
    expect(codeSourceRef({ branch: 'release' })).toBe(' · release (dal)');
  });
  it('ikisi de yoksa boş', () => {
    expect(codeSourceRef({})).toBe('');
  });
  // v0.10.1041 panel kod durumunu `code` ve `codeCtx` diye ayırdı; iki çizim
  // yeri hangi adı okursa okusun aynı fonksiyondan geçmeli.
  it('iki "Kaynak:" çizim yeri de aynı fonksiyondan çizer', () => {
    const src = readFileSync(resolve(__dirname, 'CopilotExplain.tsx'), 'utf8');
    expect(src.match(/\{codeSourceRef\((code|codeCtx)\)\}/g)?.length).toBe(2);
    expect(src).not.toMatch(/(code|codeCtx)\.branch \? ` · \$\{(code|codeCtx)\.branch\}` : ''/);
  });
});
