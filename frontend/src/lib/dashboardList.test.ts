import { describe, it, expect } from 'vitest';
import { copyName, filterDashboards, tagChips, toggleTag } from './dashboardList';
import type { DashboardSummary } from './types';

const d = (id: string, name: string, tags: string[] = [], description = ''): DashboardSummary =>
  ({ id, name, description, tags, createdAt: 1, updatedAt: 1 });

const items = [
  d('1', 'Service RED', ['red', 'servis']),
  d('2', 'Kafka gecikme', ['kafka']),
  d('3', 'Ödeme akışı', ['red', 'ödeme'], 'iade ve bakiye'),
  d('4', 'JVM', []),
];

describe('filterDashboards (dilim 3.1)', () => {
  const none = new Set<string>();
  it('boş süzgeç hepsini sırasıyla döner', () => {
    expect(filterDashboards(items, { q: '', tags: [], fav: false }, none).map(x => x.id)).toEqual(['1', '2', '3', '4']);
  });
  it('arama ad + açıklama + etiket, büyük/küçük harf duyarsız', () => {
    expect(filterDashboards(items, { q: 'KAFKA', tags: [], fav: false }, none).map(x => x.id)).toEqual(['2']);
    expect(filterDashboards(items, { q: 'bakiye', tags: [], fav: false }, none).map(x => x.id)).toEqual(['3']);
    expect(filterDashboards(items, { q: 'ödeme', tags: [], fav: false }, none).map(x => x.id)).toEqual(['3']);
  });
  it('etiketler VE ile daraltır', () => {
    expect(filterDashboards(items, { q: '', tags: ['red'], fav: false }, none).map(x => x.id)).toEqual(['1', '3']);
    expect(filterDashboards(items, { q: '', tags: ['red', 'ödeme'], fav: false }, none).map(x => x.id)).toEqual(['3']);
  });
  it('yalnız favoriler', () => {
    expect(filterDashboards(items, { q: '', tags: [], fav: true }, new Set(['2', '4'])).map(x => x.id)).toEqual(['2', '4']);
  });
});

describe('tagChips', () => {
  it('sıklık, eşitlikte alfabetik; tavan; seçili hep görünür', () => {
    expect(tagChips(items, []).map(t => t.tag)).toEqual(['red', 'kafka', 'ödeme', 'servis']);
    expect(tagChips(items, [], 1).map(t => t.tag)).toEqual(['red']);
    expect(tagChips(items, ['servis'], 1).map(t => t.tag)).toEqual(['red', 'servis']);
    expect(tagChips(items, ['bayat'], 1).find(t => t.tag === 'bayat')?.n).toBe(0);
  });
  it('toggleTag', () => {
    expect(toggleTag(['a'], 'b')).toEqual(['a', 'b']);
    expect(toggleTag(['a', 'b'], 'a')).toEqual(['b']);
  });
});

describe('copyName', () => {
  it('çakışmada sayı ekler', () => {
    expect(copyName('X', [])).toBe('X (kopya)');
    expect(copyName('X', ['X (kopya)'])).toBe('X (kopya 2)');
    expect(copyName('X', ['X (kopya)', 'X (kopya 2)'])).toBe('X (kopya 3)');
  });
});
