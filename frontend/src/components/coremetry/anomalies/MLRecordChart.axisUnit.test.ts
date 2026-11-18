import { describe, it, expect } from 'vitest';
import { axisUnit } from './mlRecord';

// v0.10.2344 — uzun birim her eksen etiketine yazılınca etiket kırpılıyordu
// (operatör ekranında "…est"); eksen yalın sayı gösterir, ölçü adı başlıkta.
describe('axisUnit', () => {
  it('kısa birim eksende kalır, uzun birim düşer', () => {
    expect(axisUnit('ms')).toBe('ms');
    expect(axisUnit('percent')).toBe('%');
    expect(axisUnit('')).toBe('');
    expect(axisUnit('spans/request')).toBe('');
    expect(axisUnit('req/s')).toBe('req/s');
    expect(axisUnit('spans/min')).toBe('');
  });
});
