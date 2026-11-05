// chatHistoryDayGroups — v0.10.1788 (CoSRE v2 tam sayfası, mockup A): kenar
// çubuğunda "Bugün / Dün / <tarih>" gün grupları. Gün operatör saat diliminde
// kesilir (UTC günüyle DEĞİL — memory feedback-model-timestamps-operator-zone):
// İstanbul 01:30 = UTC 22:30 önceki gün; UTC'ye göre bölen bir uygulama bu
// konuşmayı "Dün"e koyardı.
import { describe, expect, it } from 'vitest';
import type { AiConversationSummary } from '@/lib/types';
import { dayKey, groupConversationsByDay } from './chatHistoryGroups';

const IST = 'Europe/Istanbul';
const at = (iso: string) => Date.parse(iso);
const conv = (id: string, iso: string, title = id): AiConversationSummary =>
  ({ id, title, updatedAt: at(iso) * 1e6, messages: 2 });
// "Şimdi": İstanbul 2026-11-05 10:00 (UTC 07:00).
const NOW = at('2026-11-05T07:00:00Z');

describe('dayKey (saf)', () => {
  it.each([
    ['2026-11-04T22:30:00Z', IST, '2026-11-05'], // İstanbul 01:30 → ertesi gün
    ['2026-11-04T22:30:00Z', 'UTC', '2026-11-04'],
    ['2026-11-04T20:59:59Z', IST, '2026-11-04'], // İstanbul 23:59:59
    ['2026-11-04T21:00:00Z', IST, '2026-11-05'], // İstanbul gece yarısı
    ['2026-03-29T01:30:00Z', 'Europe/Berlin', '2026-03-29'], // DST geçiş günü
  ])('%s @ %s → %s', (iso, tz, want) => {
    expect(dayKey(at(iso), tz)).toBe(want);
  });
});

describe('groupConversationsByDay (saf)', () => {
  const THREADS = [
    conv('old-year', '2025-12-31T09:00:00Z'),
    conv('today-early', '2026-11-04T22:30:00Z', 'gece yarısı sonrası'),
    conv('yday', '2026-11-04T20:00:00Z'),
    conv('today', '2026-11-05T06:00:00Z', 'Jenkins linki'),
    conv('nov2', '2026-11-02T12:00:00Z'),
  ];

  it('en yeni üstte; Bugün / Dün / tarih (aynı yıl yılsız, başka yıl yıllı)', () => {
    const g = groupConversationsByDay(THREADS, NOW, '', IST);
    expect(g.map(x => x.label)).toEqual(['Bugün', 'Dün', '2 Kasım', '31 Aralık 2025']);
    expect(g[0].items.map(t => t.id)).toEqual(['today', 'today-early']);
    expect(g[1].items.map(t => t.id)).toEqual(['yday']);
    expect(g.map(x => x.key)).toEqual(['2026-11-05', '2026-11-04', '2026-11-02', '2025-12-31']);
  });

  it('gün operatör diliminde kesilir: UTC dilimiyle aynı konuşma "Dün"e düşer', () => {
    const utc = groupConversationsByDay(THREADS, NOW, '', 'UTC');
    expect(utc[1].label).toBe('Dün');
    expect(utc[1].items.map(t => t.id)).toEqual(['today-early', 'yday']);
  });

  it('ay başında Dün önceki ayın son günü', () => {
    const now = at('2026-12-01T09:00:00Z');
    const g = groupConversationsByDay([conv('a', '2026-11-30T12:00:00Z'), conv('b', '2026-11-29T12:00:00Z')], now, '', IST);
    expect(g.map(x => x.label)).toEqual(['Dün', '29 Kasım']);
  });

  it('arama Türkçe katlamalı; eşleşme yoksa boş; undefined liste boş', () => {
    expect(groupConversationsByDay(THREADS, NOW, 'JENKİNS', IST).flatMap(g => g.items.map(t => t.id))).toEqual(['today']);
    expect(groupConversationsByDay(THREADS, NOW, 'yok-boyle', IST)).toEqual([]);
    expect(groupConversationsByDay(undefined, NOW, '', IST)).toEqual([]);
  });
});
