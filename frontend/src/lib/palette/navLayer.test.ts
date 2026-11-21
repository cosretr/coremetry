import { describe, it, expect } from 'vitest';
import type { EntityRecord, SavedView } from '@/lib/types';
import { askRow, podRows, rangeRows, savedViewRows, shortcutRows, PALETTE_VIEW_EMPTY_MAX } from './navLayer';

const t = (k: string) => `${k}|{q}{r}{page}{cluster}`;
const view = (i: number, page = 'logs', name = `v${i}`): SavedView =>
  ({ id: `id${i}`, ownerId: 'u', name, page, queryString: `q=${i}`, pinned: false, createdAt: i });

describe('navLayer (wave 3 palet gezinme katmanı)', () => {
  it('kayıtlı görünüm → rota + sorgu; bilinmeyen sayfa düşer; boş sorguda tavan', () => {
    const rows = savedViewRows([view(1), view(2, 'ai-evalset'), view(3, 'slowqueries')], '', t);
    expect(rows.map(r => r.to)).toEqual(['/logs?q=1', '/databases/slow-queries?q=3']);
    expect(rows.every(r => r.group === 'views')).toBe(true);
    const many = Array.from({ length: 12 }, (_, i) => view(i));
    expect(savedViewRows(many, '', t)).toHaveLength(PALETTE_VIEW_EMPTY_MAX);
    expect(savedViewRows([view(1, 'logs', 'Payment errors'), view(2)], 'paym', t).map(r => r.label)).toEqual(['Payment errors']);
  });
  it('zaman aralığı: anahtar kelime → hazır liste, hazır ad → tek satır, ilgisiz → yok', () => {
    expect(rangeRows('range', t).map(r => r.rangePreset)).toEqual(['15m', '1h', '3h', '6h', '24h', '7d']);
    expect(rangeRows('zaman', t)).toHaveLength(6);
    expect(rangeRows('1h', t).map(r => r.rangePreset)).toEqual(['1h']);
    expect(rangeRows('checkout', t)).toEqual([]);
    expect(rangeRows('', t)).toEqual([]);
  });
  it('pod satırı entity modelinden (/pod linki, küme adıyla)', () => {
    const rec = { type: 'pod', clusterId: 'c-1', id: 'pod:c-1/ns/web-1', namespace: 'ns', name: 'web-1' } as EntityRecord;
    const [r] = podRows([{ rec, clusterName: 'prod' }], t);
    expect(r.label).toBe('ns/web-1');
    expect(r.to).toContain('/pod?');
    expect(r.to).toContain('web-1');
  });
  it('kısayollar: "?" hepsi, metin eşleşmesi ≤6, tekrar yok', () => {
    const sc = [
      { keys: 'mod+k', label: 'Open command palette', handler: () => {} },
      { keys: 'mod+k', label: 'dup', handler: () => {} },
      { keys: 'g l', label: 'Go to Logs', handler: () => {} },
    ];
    expect(shortcutRows(sc, '?').map(r => r.hint)).toEqual(['⌘/Ctrl+k', 'g l']);
    expect(shortcutRows(sc, 'logs').map(r => r.label)).toEqual(['Go to Logs']);
    expect(shortcutRows(sc, '')).toEqual([]);
  });
  it('CoSRE\'ye sor: yalnız copilot açıkken ve ≥3 karakter', () => {
    expect(askRow('why is checkout slow', true, t)[0].askCosre).toBe('why is checkout slow');
    expect(askRow('ab', true, t)).toEqual([]);
    expect(askRow('why is checkout slow', false, t)).toEqual([]);
  });
});
