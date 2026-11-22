import { describe, expect, it } from 'vitest';
import { gapWho, gapChainTitle, GAP_KIND_LABEL, gapKindOptions, filterGapsByKind } from './routerGapsView';

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

describe('routerGapsView (v0.10.2465 — netleştirme türü)', () => {
  const gaps = [{ kind: 'loop' as const }, { kind: 'clarify' as const }, { kind: 'clarify' as const }, { kind: 'general' as const }];
  it('etiket + süzgeç seçenekleri sayılarıyla', () => {
    expect(GAP_KIND_LABEL.clarify).toBe('Netleştirme');
    expect(gapKindOptions(gaps).map(o => o.label)).toEqual(['Tümü · 4', 'Serbest döngü · 1', 'Genel soru · 1', 'Netleştirme · 2']);
  });
  it('süzgeç: tür seçilince yalnız o tür, Tümü hepsi', () => {
    expect(filterGapsByKind(gaps, 'clarify')).toHaveLength(2);
    expect(filterGapsByKind(gaps, 'all')).toHaveLength(4);
  });
});
