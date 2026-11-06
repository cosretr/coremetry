// logsSmartColumns — v0.10.1856 (Log Search Faz 2 dilim 2.5).
import { describe, it, expect } from 'vitest';
import { businessCandidates, requestColumns, smartBusinessColumns, withSmartColumns, SOURCE_FIELDS_MAX } from './logsSmartColumns';
import type { LogRow } from '@/lib/types';

const row = (attrs: Record<string, string>, res: Record<string, string> = {}) =>
  ({ attributes: attrs, resourceAttributes: res } as unknown as LogRow);
const DEFS = [
  { label: 'Kanal', fields: ['CHANNEL_CODE', 'channel_code'] },
  { label: 'İşlem', fields: ['FUNCTION_CODE'] },
  { label: 'Operasyon', fields: ['operation'] },
];

describe('smartBusinessColumns', () => {
  it('doluluk ≥ %80 olan iş alanı girer; altı girmez; boş değer dolu sayılmaz', () => {
    const rows = [
      ...Array.from({ length: 8 }, () => row({ CHANNEL_CODE: 'MOBILE', operation: 'x' })),
      row({ CHANNEL_CODE: 'WEB', operation: ' ' }),
      row({ FUNCTION_CODE: 'ORD_CREATE' }),
    ];
    // CHANNEL_CODE 9/10, operation 8/10 (boşluk dolu değil), FUNCTION_CODE 1/10.
    expect(smartBusinessColumns(DEFS, rows)).toEqual(['CHANNEL_CODE', 'operation']);
  });
  it('tanım başına EN dolu aday; resourceAttributes da sayılır', () => {
    const rows = [row({ CHANNEL_CODE: 'A' }), ...Array.from({ length: 9 }, () => row({}, { channel_code: 'b' }))];
    expect(smartBusinessColumns(DEFS, rows)).toEqual(['channel_code']);
  });
  it('tam eşik %80 girer, hemen altı girmez', () => {
    const at = [...Array.from({ length: 4 }, () => row({ operation: 'o' })), row({})];
    expect(smartBusinessColumns(DEFS, at)).toEqual(['operation']);
    const below = [...Array.from({ length: 79 }, () => row({ operation: 'o' })), ...Array.from({ length: 21 }, () => row({}))];
    expect(smartBusinessColumns(DEFS, below)).toEqual([]);
  });
  it('tanım yok / örnek boş → yok', () => {
    expect(smartBusinessColumns(undefined, [row({ CHANNEL_CODE: 'x' })])).toEqual([]);
    expect(smartBusinessColumns(DEFS, [])).toEqual([]);
  });
});

describe('kolon yardımcıları', () => {
  it('withSmartColumns: Mesaj kolonunun önüne, tekrar yok', () => {
    expect(withSmartColumns(['cluster', 'message', 'service', 'pod'], ['CHANNEL_CODE'])).toEqual(['cluster', 'CHANNEL_CODE', 'message', 'service', 'pod']);
    expect(withSmartColumns(['service', 'CHANNEL_CODE', 'message'], ['CHANNEL_CODE'])).toEqual(['service', 'CHANNEL_CODE', 'message']);
    expect(withSmartColumns(['service'], ['x'])).toEqual(['service', 'x']);
  });
  it('businessCandidates + requestColumns tavanı', () => {
    expect(businessCandidates(DEFS)).toEqual(['CHANNEL_CODE', 'channel_code', 'FUNCTION_CODE', 'operation']);
    expect(requestColumns(['message', 'CHANNEL_CODE'], ['CHANNEL_CODE', 'x'])).toEqual(['message', 'CHANNEL_CODE', 'x']);
    const many = Array.from({ length: 80 }, (_, i) => `f${i}`);
    expect(requestColumns(['message'], many)).toHaveLength(SOURCE_FIELDS_MAX);
  });
});
