// logBusinessFields — v0.10.1853 (Log Search Faz 2 dilim 2.2).
import { describe, it, expect } from 'vitest';
import { resolveBusinessFields } from './logBusinessFields';

const DEFS = [
  { label: 'Kanal', fields: ['CHANNEL_CODE', 'channel_code', 'labels.channel'] },
  { label: 'İşlem', fields: ['FUNCTION_CODE', 'function_code'] },
  { label: 'Operasyon', fields: ['operation'] },
];

describe('resolveBusinessFields', () => {
  it('tanım yok ya da alan listesi boş (CH log yolu) → bölüm boş', () => {
    expect(resolveBusinessFields(undefined, ['CHANNEL_CODE'])).toEqual([]);
    expect(resolveBusinessFields([], ['CHANNEL_CODE'])).toEqual([]);
    expect(resolveBusinessFields(DEFS, [])).toEqual([]);
    expect(resolveBusinessFields(DEFS, [], 0)).toEqual([]);
  });
  it('eşlemede olan İLK aday seçilir, sıra tanım sırası', () => {
    const r = resolveBusinessFields(DEFS, ['message', 'function_code', 'channel_code', 'CHANNEL_CODE', 'operation'], 5);
    expect(r).toEqual([
      { label: 'Kanal', field: 'CHANNEL_CODE', verified: true },
      { label: 'İşlem', field: 'function_code', verified: true },
      { label: 'Operasyon', field: 'operation', verified: true },
    ]);
  });
  it('tam eşlemede adayı olmayan tanım düşer', () => {
    const r = resolveBusinessFields(DEFS, ['message', 'channel_code'], 2);
    expect(r).toEqual([{ label: 'Kanal', field: 'channel_code', verified: true }]);
  });
  it('kırpılmış eşlemede bulunamayan tanım ilk adayıyla doğrulanmamış kalır', () => {
    const r = resolveBusinessFields(DEFS, ['message', 'channel_code'], 4000);
    expect(r).toEqual([
      { label: 'Kanal', field: 'channel_code', verified: true },
      { label: 'İşlem', field: 'FUNCTION_CODE', verified: false },
      { label: 'Operasyon', field: 'operation', verified: false },
    ]);
  });
  it('iki tanım aynı alana çözülürse ilki kalır', () => {
    const r = resolveBusinessFields([{ label: 'A', fields: ['x'] }, { label: 'B', fields: ['x', 'y'] }], ['x', 'y']);
    expect(r).toEqual([{ label: 'A', field: 'x', verified: true }]);
  });
});
