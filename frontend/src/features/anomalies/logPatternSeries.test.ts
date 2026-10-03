// logPatternSeries — v0.10.1060 (operatör: "Bunu doğru yakalamış ama
// artışın ne zaman başladığını göstermiyor. Elastic'e gidip bakınca barlardan
// net görüyorum."). Log deseni "Desen sayısı" grafiğinin saf çekirdeği.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent, LogPatternSeries } from '@/lib/types';
import {
  anomalyRegion, bucketLabel, hasLogPatternSeries, logPatternSeriesArgs, logPatternSeriesState,
  logPatternSeriesToSpan, logPatternSeriesWindow, patternLogsPivot,
} from './logPatternSeries';

const MIN = 60e9;
const T0 = 1_759_399_980 * 1e9; // dakikaya hizalı

describe('hasLogPatternSeries', () => {
  it.each<[AnomalyEvent['kind'], string, boolean]>([
    ['log_pattern', 'Oracle errors (ORA-)', true],
    ['log_pattern', '', false],
    ['log_template_new', 'user <*> logged in', false],
    ['elastic_ml', 'ml-job', false],
    ['trace_op', 'POST /pay', false],
    ['trace_op_latency', 'POST /pay', false],
    ['behavior_change', 'p99_ms', false],
  ])('%s / %j → %s', (kind, pattern, want) => {
    expect(hasLogPatternSeries({ kind, pattern })).toBe(want);
  });
});

describe('logPatternSeriesWindow', () => {
  it.each<[string, Pick<AnomalyEvent, 'startedAt' | 'lastSeen' | 'status'>, { fromNs: number; toNs: number | null }]>([
    // Kısa olay: olay penceresinin girişi 30 dk; 1 sa öncesi daha erken → 1 sa.
    ['aktif, kısa: başlangıç − 1 sa, bitiş şimdi (null)',
      { startedAt: T0, lastSeen: T0 + 10 * MIN, status: 'active' },
      { fromNs: T0 - 60 * MIN, toNs: null }],
    // Uzun olay: 3 × 3 sa = 9 sa → 6 sa tavanı; 1 sa'ten erken → olay penceresi.
    ['bitmiş, uzun: olay penceresinin başı (6 sa), bitiş son gözlem + 10 dk',
      { startedAt: T0, lastSeen: T0 + 180 * MIN, status: 'cleared' },
      { fromNs: T0 - 360 * MIN, toNs: T0 + 190 * MIN }],
    ['dakika ortası başlangıç aşağı yuvarlanır',
      { startedAt: T0 + 30e9, lastSeen: T0 + 30e9, status: 'cleared' },
      { fromNs: T0 - 60 * MIN, toNs: T0 + 11 * MIN }],
  ])('%s', (_n, e, want) => {
    expect(logPatternSeriesWindow(e)).toEqual(want);
  });
});

describe('bucketLabel', () => {
  it.each<[number, string]>([
    [60, '1 dk'], [120, '2 dk'], [300, '5 dk'], [3600, '1 sa'], [7200, '2 sa'], [86400, '1 gün'],
    [45, '45 sn'], [0, '—'], [Number.NaN, '—'],
  ])('%s → %s', (sec, want) => {
    expect(bucketLabel(sec)).toBe(want);
  });
});

describe('logPatternSeriesToSpan', () => {
  it('tek seri, zaman ns, değer = sayım', () => {
    const s: LogPatternSeries = { pattern: 'p', bucketSec: 60, from: T0, to: T0 + 2 * MIN,
      points: [{ t: T0, v: 3 }, { t: T0 + MIN, v: 9000 }] };
    expect(logPatternSeriesToSpan(s)).toEqual([{ groupKey: [], points: [{ time: T0, value: 3 }, { time: T0 + MIN, value: 9000 }] }]);
  });
});

describe('anomalyRegion', () => {
  it.each<[string, Pick<AnomalyEvent, 'startedAt' | 'lastSeen' | 'status'>, number, object]>([
    ['aktif: başlangıç → grafiğin sağ ucu, endSec yok',
      { startedAt: T0, lastSeen: T0 + 5 * MIN, status: 'active' }, T0 + 20 * MIN,
      { fromSec: T0 / 1e9, toSec: (T0 + 20 * MIN) / 1e9, label: 'başladı', color: 'var(--err)' }],
    ['bitmiş: başlangıç → son gözlem, endSec gerçek bitiş',
      { startedAt: T0, lastSeen: T0 + 5 * MIN, status: 'cleared' }, T0 + 20 * MIN,
      { fromSec: T0 / 1e9, toSec: (T0 + 5 * MIN) / 1e9, endSec: (T0 + 5 * MIN) / 1e9, label: 'başladı', color: 'var(--err)' }],
    ['bozuk sıra (lastSeen < startedAt): bölge ters dönmez',
      { startedAt: T0, lastSeen: T0 - MIN, status: 'cleared' }, T0 + 20 * MIN,
      { fromSec: T0 / 1e9, toSec: T0 / 1e9, endSec: T0 / 1e9, label: 'başladı', color: 'var(--err)' }],
  ])('%s', (_n, e, to, want) => {
    expect(anomalyRegion(e, to)).toEqual(want);
  });
});

describe('logPatternSeriesState', () => {
  const series = (vals: number[]): LogPatternSeries => ({
    pattern: 'p', bucketSec: 60, from: T0, to: T0 + vals.length * MIN,
    points: vals.map((v, i) => ({ t: T0 + i * MIN, v })),
  });
  it.each<[string, { isPending: boolean; isError: boolean; data: LogPatternSeries | null | undefined }, string]>([
    ['bekliyor', { isPending: true, isError: false, data: undefined }, 'loading'],
    ['hata (önceki veri olsa da)', { isPending: false, isError: true, data: series([5]) }, 'error'],
    ['404 → tanım yok', { isPending: false, isError: false, data: null }, 'gone'],
    ['hepsi 0 → boş', { isPending: false, isError: false, data: series([0, 0, 0]) }, 'empty'],
    ['nokta yok → boş', { isPending: false, isError: false, data: series([]) }, 'empty'],
    ['veri', { isPending: false, isError: false, data: series([0, 12, 9000]) }, 'ready'],
  ])('%s', (_n, q, want) => {
    expect(logPatternSeriesState(q)).toBe(want);
  });
});

// v0.10.1062 (operatör, prod ES: servissiz log deseni anomalisinde "Ne
// yapabilirim" yalnız "servis adı yok" diyordu) — servissiz olayın /logs
// bağlantısı: arama metni SUNUCUDAN (dedektörün token'ları), pencere olayınki.
describe('logPatternSeriesArgs', () => {
  it('grafik bölümüyle aynı anahtar: desen + logPatternSeriesWindow', () => {
    const e = { pattern: 'Service quota', startedAt: T0, lastSeen: T0 + 10 * MIN, status: 'cleared' as const };
    expect(logPatternSeriesArgs(e)).toEqual({ pattern: 'Service quota', ...logPatternSeriesWindow(e) });
    expect(logPatternSeriesArgs({ ...e, status: 'active' }).toNs).toBeNull();
  });
});

// v0.10.1071 — bağlantı `pattern=<ad>` yazar (1062'nin token'lardan kurulan
// `q=` metni silindi): /logs sunucusu dedektörün yüklemini uygular.
describe('patternLogsPivot', () => {
  const win = { fromNs: T0 - 30 * MIN, toNs: T0 + 20 * MIN };
  const range = `custom:${(T0 - 30 * MIN) / 1e6}-${(T0 + 20 * MIN) / 1e6}`;
  const href = (pattern: string) => `/logs?${new URLSearchParams({ pattern, range }).toString()}`;
  type In = Pick<LogPatternSeries, 'topServices'> | null | undefined;
  it.each<[string, string, In, { href: string; topServices: string[] } | null]>([
    ['ad yok → bağlantı yok', '', undefined, null],
    ['boşluk ad → bağlantı yok', '   ', {}, null],
    ['okuma beklenmez (yükleniyor): ad yeter', 'Service quota', undefined,
      { href: href('Service quota'), topServices: [] }],
    ['servisler (ES), en çok 3, boş ad atılır', 'SQL exception',
      { topServices: [
        { service: 'orders-svc', count: 700 }, { service: '', count: 90 }, { service: 'billing-svc', count: 81 },
        { service: 'ORDER_QUEUE_LISTENER', count: 9 }, { service: 'audit-svc', count: 2 }] },
      { href: href('SQL exception'), topServices: ['orders-svc', 'billing-svc', 'ORDER_QUEUE_LISTENER'] }],
  ])('%s', (_name, pattern, series, want) => {
    expect(patternLogsPivot(pattern, series, win)).toEqual(want);
  });
  it('servis kapsamı YAZMAZ, q YAZMAZ — yalnız pattern + range', () => {
    const p = patternLogsPivot('External system rejected', { topServices: [{ service: 'orders-svc', count: 3 }] }, win)!;
    const sp = new URL(p.href, 'http://x').searchParams;
    expect(sp.has('service')).toBe(false);
    expect(sp.has('q')).toBe(false);
    expect(sp.get('pattern')).toBe('External system rejected');
    expect([...sp.keys()].sort()).toEqual(['pattern', 'range']);
  });
});
