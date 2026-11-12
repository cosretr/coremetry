// MLTab — ML sekmesi yardımcıları. v0.10.2021: D2'nin "Grup" sütunu (v0.10.2013)
// liste satırı = grup düzeninde kaldırıldı (mlGroupRows.test.ts); burada
// dedektör adı, tik özeti ve Thanos ölçümü kalır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { tickLabel, ML_DETECTOR_TR, detectorGroup, thanosTickText } from './MLTab';
import { signalKindOf } from '@/lib/signalKind';

// v0.10.2018 — T-err: yeni trace dedektörü adı, türü ve tik özeti grubu.
describe('trace_error_count', () => {
  it('Türkçe ad, trace türü, trace_errors tik grubu', () => {
    expect(ML_DETECTOR_TR.trace_error_count).toBe('Operasyon hata sayısı');
    expect(signalKindOf('trace_error_count')).toBe('trace');
    expect(detectorGroup('trace_error_count')).toBe('trace_errors');
  });
});

describe('tik satırı', () => {
  it('gölge sayacını söyler (v0.10.2015 D3)', () => {
    const t = { at: 100, durationMs: 1000, events: 3, backoff: [], shadowGroups: 4, shadowWouldOpen: 1 };
    expect(tickLabel(t, 100)).toContain('gölge terfi: 4 grup, 1 Problem açardı (açılmadı)');
  });
});

// v0.10.2019 — tikin Thanos ölçümü ayrı sayaçla görünür.
describe('thanosTickText', () => {
  it('sorgu, süre, hata, tavan, eşlenemeyen, yanıtsız cluster', () => {
    const s = thanosTickText({ queries: 14, ms: 5300, maxMs: 2100, errors: 1, budgetCut: 2, unmatched: 40, failed: ['prod-west'] });
    expect(s).toBe('Thanos 14 sorgu · 5.3 sn (en yavaş 2.1 sn) · 1 hata/kısmi · 2 ifade tavan dışı · 40 pod servise eşlenemedi · yanıtsız: prod-west');
    expect(tickLabel({ at: 1, durationMs: 1, events: 0, backoff: [], thanos: { queries: 3, ms: 100, maxMs: 50, errors: 0 } }, 1)).toContain('Thanos 3 sorgu');
  });
});

// v0.10.2021 — liste: satır = grup, mockup sütunları, fazla sütun yok.
describe('ML olay tablosu', () => {
  const src = readFileSync(resolve(__dirname, 'MLTab.tsx'), 'utf8');
  it('sütun sırası Başlangıç · Servis · Ne oldu · Sinyaller · Durum', () => {
    const ids = [...src.matchAll(/\{ id: '(\w+)', label: '([^']+)'/g)].map(m => m[1]);
    const i = ids.indexOf('started');
    expect(ids.slice(i, i + 5)).toEqual(['started', 'service', 'headline', 'signals', 'status']);
    expect(src).not.toContain("id: 'group'");
    expect(src).toContain('storageKey="anomalies-ml-groups"');
  });
});
