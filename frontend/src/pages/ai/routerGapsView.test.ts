import { describe, expect, it } from 'vitest';
import { gapWho, gapChainTitle } from './routerGapsView';

describe('routerGapsView (v0.10.2448)', () => {
  it('Kim: e-postalar virgülle; e-postasız eski satır boş', () => {
    expect(gapWho({ emails: ['a@example.test', 'b@example.test'] })).toBe('a@example.test, b@example.test');
    expect(gapWho({ emails: [] })).toBe('');
    expect(gapWho({})).toBe('');
  });

  it('Tür title: yalnız genel → döngü zinciri olan loop satırında', () => {
    expect(gapChainTitle({ kind: 'loop', viaGeneral: 2 })).toMatch(/^2 tur önce genel soru/);
    expect(gapChainTitle({ kind: 'loop', viaGeneral: 0 })).toBeUndefined();
    expect(gapChainTitle({ kind: 'general', viaGeneral: 3 })).toBeUndefined();
    expect(gapChainTitle({ kind: 'loop' })).toBeUndefined();
  });
});
