// resetReducer.test — v0.10.1618 (CoSRE v2 Faz 2a): saf indirgeyici.
// Operatör kuralı (2026-10-30, "(2) revizyonu KESİN"): onData yalnız
// messageId → {sıra} kümesine yazar; render gizler; onFinish kalıcı siler;
// temizlik idempotent. "Son mesaj / son text parçası" kestirmesi YASAK.
import { describe, it, expect } from 'vitest';
import { applyResets, markFinished, recordReset, visibleParts, type CosreUIMessage, type ResetSet } from './resetReducer';

const texts = (m: CosreUIMessage) => m.parts.flatMap(p => (p.type === 'text' ? [p.text] : []));
const msg = (id: string, ...t: string[]): CosreUIMessage => ({
  id, role: 'assistant',
  parts: [{ type: 'step-start' }, ...t.map(text => ({ type: 'text' as const, text, state: 'done' as const }))],
});

describe('recordReset', () => {
  it('messageId başına kümeye yazar, mesajlara dokunmaz', () => {
    const set: ResetSet = new Map();
    recordReset(set, 'A', 0);
    recordReset(set, 'A', 1);
    recordReset(set, 'B', 0);
    expect([...set.get('A')!]).toEqual([0, 1]);
    expect([...set.get('B')!]).toEqual([0]);
  });
});

describe('visibleParts', () => {
  it('akış sırasında kümedeki text parçalarını gizler, diğer parçalar kalır', () => {
    const m = msg('A', 'taslak', 'son');
    const set: ResetSet = new Map([['A', new Set([0])]]);
    expect(visibleParts(m, set).map(p => (p.type === 'text' ? p.text : p.type))).toEqual(['step-start', 'son']);
  });
  it('kümesi olmayan mesaj: aynı dizi referansı (memo dostu)', () => {
    const m = msg('A', 'x');
    expect(visibleParts(m, new Map([['B', new Set([0])]]))).toBe(m.parts);
  });
});

describe('applyResets', () => {
  it('YALNIZ hedef mesajın hedef sırasını siler — son mesaj kestirmesi yok', () => {
    const a = msg('A', 'A0', 'A1');
    const b = msg('B', 'B0', 'B1');
    const out = applyResets([a, b], new Map([['A', new Set([0])]]));
    expect(texts(out[0])).toEqual(['A1']);
    expect(out[1]).toBe(b);
  });

  it('reset2 (t1→0, t2→1): t1 sıfırlandıktan SONRA t2 hâlâ doğru çözülür, t3 kalır', () => {
    // t1 temizliği t2'yi sıra 0'a kaydırır; özgün sıra muhasebesi olmasa
    // ikinci geçiş t3'ü silerdi.
    const set: ResetSet = new Map([['A', new Set([0])]]);
    const step1 = applyResets([msg('A', 'Birinci taslak', 'İkinci taslak', 'Son tam metin.')], set);
    expect(texts(step1[0])).toEqual(['İkinci taslak', 'Son tam metin.']);
    set.get('A')!.add(1);
    const step2 = applyResets(step1, set);
    expect(texts(step2[0])).toEqual(['Son tam metin.']);
    expect(step2[0].metadata?.resetApplied).toEqual([0, 1]);
  });

  it('idempotent: iki kez koşmak aynı sonuç (aynı referans)', () => {
    const set: ResetSet = new Map([['A', new Set([0, 1])]]);
    const once = applyResets([msg('A', 'x', 'y', 'z')], set);
    const twice = applyResets(once, set);
    expect(twice).toBe(once);
    expect(texts(twice[0])).toEqual(['z']);
  });

  it('SDK kopyası taslağı geri yazarsa (metadata da eski) yeniden uygulanır', () => {
    const set: ResetSet = new Map([['A', new Set([0])]]);
    const fresh = msg('A', 'taslak', 'son');
    applyResets([fresh], set);
    expect(texts(applyResets([fresh], set)[0])).toEqual(['son']);
  });

  it('değişiklik yoksa aynı dizi', () => {
    const list = [msg('A', 'x')];
    expect(applyResets(list, new Map())).toBe(list);
  });
});

describe('markFinished', () => {
  it('partial / error bayrağı id ile yazılır, idempotent', () => {
    const list = [msg('A', 'x'), msg('B', 'y')];
    const once = markFinished(list, 'A', { partial: true });
    expect(once[0].metadata).toEqual({ partial: true });
    expect(once[1]).toBe(list[1]);
    expect(markFinished(once, 'A', { partial: true })).toBe(once);
    expect(markFinished(list, 'A', {})).toBe(list);
    expect(markFinished(list, 'Z', { error: true })).toBe(list);
  });
});
