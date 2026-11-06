import { describe, it, expect } from 'vitest';
import {
  fmtMillions, fmtCount, fmtClock, isLive, summaryUsable, shouldFetchSummary,
  splitTemplate, activityParts, componentsLine, leadKey, kpis, LIVE_WINDOW_MS,
  dayOfYear, pickQuote, httpCardUsable, fmtRps, fmtSeconds, throughputHead, responseHead, httpCardTitle,
} from './loginSummary';
import type { LoginSummary } from '@/lib/types';

const T: Record<string, string> = {
  'login.sum.rollouts': '{n} rollout izlendi',
  'login.sum.logins': '{n} giriş yapıldı',
  'login.sum.cosre': 'CoSRE {n} soru yanıtladı',
  'login.sum.traces': '{n} trace incelendi',
  'login.sum.allOk': 'Tüm bileşenler çalışıyor',
  'login.sum.degraded': 'Bazı bileşenler sorunlu · ayrıntı giriş sonrasında',
  'login.sum.checked': 'son kontrol',
  'login.http.now': 'şimdi {now} req/s · tepe {peak} ({at})',
  'login.http.avg': 'ort',
  'login.http.title': '{org} OpenTelemetry metrikleri · son 24 saat',
  'login.http.titleNoOrg': 'OpenTelemetry metrikleri · son 24 saat',
};
const t = (k: string) => T[k] ?? k;

describe('fmtMillions', () => {
  it.each([
    [1_214_000_000, '1,214M'],
    [1_213_600_000, '1,214M'],
    [48_200_000, '48.2M'],
    [48_249_999, '48.2M'],
    [99_960_000, '100M'],
    [1_000_000, '1.0M'],
    [999_999, '999,999'],
    [2148, '2,148'],
    [0, '0'],
    [-5, '0'],
    [NaN, '0'],
    [7, '7'],
  ])('%s → %s', (n, want) => {
    expect(fmtMillions(n)).toBe(want);
  });
});

describe('fmtCount', () => {
  it.each([
    [3, '3'], [47, '47'], [999, '999'], [1000, '1.0K'], [12_400, '12.4K'],
    [120_000, '120K'], [999_999, '1.0M'], [2_500_000, '2.5M'], [0, '0'],
  ])('%s → %s', (n, want) => expect(fmtCount(n)).toBe(want));
});

describe('fmtClock / isLive', () => {
  it('HH:mm sıfır doldurur', () => {
    expect(fmtClock(new Date(2026, 10, 6, 7, 5).getTime())).toBe('07:05');
  });
  it('3 dk içinde canlı; eski, gelecek ya da yok → değil', () => {
    const now = 1_000_000_000;
    expect(isLive(now - 1000, now)).toBe(true);
    expect(isLive(now - LIVE_WINDOW_MS, now)).toBe(true);
    expect(isLive(now - LIVE_WINDOW_MS - 1, now)).toBe(false);
    expect(isLive(now + 5000, now)).toBe(false);
    expect(isLive(undefined, now)).toBe(false);
  });
});

describe('düşüş kararı', () => {
  it('yalnız enabled && ready çizilir', () => {
    expect(summaryUsable({ enabled: true, ready: true })).toBe(true);
    expect(summaryUsable({ enabled: true, ready: false })).toBe(false);
    expect(summaryUsable({ enabled: false, ready: false })).toBe(false);
    expect(summaryUsable(undefined)).toBe(false);
    expect(summaryUsable(null)).toBe(false);
  });
  it('çağrı: dar ekranda ya da marka off iken YOK', () => {
    expect(shouldFetchSummary(true, undefined)).toBe(true);
    expect(shouldFetchSummary(true, 'on')).toBe(true);
    expect(shouldFetchSummary(true, '')).toBe(true);
    expect(shouldFetchSummary(true, 'off')).toBe(false);
    expect(shouldFetchSummary(false, 'on')).toBe(false);
  });
});

describe('satır metinleri', () => {
  it('splitTemplate {n}\'yi ayırır', () => {
    expect(splitTemplate('CoSRE {n} soru', '61')).toEqual({ pre: 'CoSRE ', n: '61', post: ' soru' });
    expect(splitTemplate('yok', '1')).toEqual({ pre: 'yok', n: '', post: '' });
  });
  it('aktivite: dört madde, sıra rollout · giriş · CoSRE · trace', () => {
    const p = activityParts({ rollouts_tracked: 3, logins: 47, cosre_questions: 61, traces_viewed: 12_400 }, t);
    expect(p.map(x => `${x.pre}${x.n}${x.post}`)).toEqual([
      '3 rollout izlendi', '47 giriş yapıldı', 'CoSRE 61 soru yanıtladı', '12.4K trace incelendi',
    ]);
  });
  it('rollout alanı yoksa yalnız o madde düşer; aktivite yoksa boş', () => {
    const p = activityParts({ logins: 1, cosre_questions: 0, traces_viewed: 2 }, t);
    expect(p).toHaveLength(3);
    expect(p[0].post).toBe(' giriş yapıldı');
    expect(activityParts(undefined, t)).toEqual([]);
  });
  it('bileşenler: ok + boyut + saat; boyut yoksa o parça düşer', () => {
    const at = new Date(2026, 10, 6, 20, 34).getTime();
    expect(componentsLine({ state: 'ok', checked_at: at, clickhouse_uncompressed_bytes: 4.2 * 1024 ** 4 }, t))
      .toBe('Tüm bileşenler çalışıyor · ClickHouse 4.20 TiB · son kontrol 20:34');
    expect(componentsLine({ state: 'ok', checked_at: at }, t))
      .toBe('Tüm bileşenler çalışıyor · son kontrol 20:34');
  });
  it('degraded sabit cümle; bilinmeyen durum ve yok → satır yok', () => {
    expect(componentsLine({ state: 'degraded', checked_at: 1 }, t))
      .toBe('Bazı bileşenler sorunlu · ayrıntı giriş sonrasında');
    expect(componentsLine({ state: 'weird', checked_at: 1 }, t)).toBeNull();
    expect(componentsLine(undefined, t)).toBeNull();
  });
  it('alt başlık: isimli topolojide farklı anahtar', () => {
    const base: LoginSummary = { enabled: true, ready: true };
    expect(leadKey(base)).toBe('login.sum.lead');
    expect(leadKey({ ...base, topology: { window_s: 900, named: true, nodes: [], edges: [] } }))
      .toBe('login.sum.leadNamed');
    expect(leadKey({ ...base, topology: { window_s: 900, named: false, nodes: [], edges: [] } }))
      .toBe('login.sum.lead');
  });
  it('KPI: eksik alan yalnız kendi kutusunu düşürür', () => {
    expect(kpis({ enabled: true, ready: true, spans_today: 1_214_000_000, services_monitored: 2148 }))
      .toEqual([{ key: 'spans', value: '1,214M' }, { key: 'services', value: '2,148' }]);
    expect(kpis({ enabled: true, ready: true })).toEqual([]);
  });
});

describe('HTTP kartı yardımcıları', () => {
  const pt = (n: number) => Array.from({ length: n }, (_, i) => ({ time: i, value: 1 }));
  it('httpCardUsable: ≥ 2 noktalı en az bir durum serisi', () => {
    const base = { from: 0, to: 1, step_s: 300, summary: { now_rps: 1, peak_rps: 2, peak_at: 0 } };
    expect(httpCardUsable({ ...base, status: [{ groupKey: ['200'], points: pt(2) }] })).toBe(true);
    expect(httpCardUsable({ ...base, status: [{ groupKey: ['200'], points: pt(1) }] })).toBe(false);
    expect(httpCardUsable({ ...base, status: [] })).toBe(false);
    expect(httpCardUsable({ ...base, status: [{ groupKey: ['200'], points: null as never }] })).toBe(false);
    expect(httpCardUsable(undefined)).toBe(false);
  });
  it.each([[0, '0'], [0.04, '0.0'], [3.26, '3.3'], [42.4, '42'], [999, '999'], [12_400, '12.4K'], [NaN, '0']])(
    'fmtRps %s → %s', (v, want) => expect(fmtRps(v)).toBe(want));
  it.each([[0.186, '186 ms'], [0.0004, '0 ms'], [1.2345, '1.23 s'], [12.34, '12.3 s'], [-1, '—']])(
    'fmtSeconds %s → %s', (v, want) => expect(fmtSeconds(v)).toBe(want));
  it('başlık satırları', () => {
    const at = new Date(2026, 10, 6, 12, 5).getTime();
    expect(throughputHead({ now_rps: 1500, peak_rps: 2200, peak_at: at }, t)).toBe('şimdi 1.5K req/s · tepe 2.2K (12:05)');
    expect(responseHead({ now_rps: 1, peak_rps: 1, peak_at: 0, avg_now_s: 0.186, p95_now_s: 0.412 }, t)).toBe('ort 186 ms · p95 412 ms');
    expect(responseHead({ now_rps: 1, peak_rps: 1, peak_at: 0, avg_now_s: 0.186 }, t)).toBe('ort 186 ms');
    expect(responseHead({ now_rps: 1, peak_rps: 1, peak_at: 0 }, t)).toBe('');
  });
  it('kart başlığı: orgName boşsa ad düşer', () => {
    expect(httpCardTitle('acme', t)).toBe('acme OpenTelemetry metrikleri · son 24 saat');
    expect(httpCardTitle('  ', t)).toBe('OpenTelemetry metrikleri · son 24 saat');
    expect(httpCardTitle(undefined, t)).toBe('OpenTelemetry metrikleri · son 24 saat');
  });
});

describe('günün sözü seçimi', () => {
  // [yıl, ay(1-12), gün, dayOfYear, len=60 dizini]
  it.each([
    [2025, 1, 1, 1, 1],
    [2025, 2, 28, 59, 59],
    [2025, 3, 1, 60, 0],        // artık olmayan yıl: 1 Mart = 60
    [2025, 12, 31, 365, 5],
    [2026, 1, 1, 1, 1],         // yıl sınırı: dizin 5 → 1
    [2024, 2, 29, 60, 0],       // artık yıl
    [2024, 3, 1, 61, 1],        // artık yılda 1 Mart = 61
    [2024, 12, 31, 366, 6],
  ])('%s-%s-%s → gün %s, dizin %s', (y, mo, d, doy, idx) => {
    const date = new Date(y, mo - 1, d, 13, 0, 0);
    expect(dayOfYear(date)).toBe(doy);
    expect(pickQuote(date, 60)).toBe(idx);
  });
  it('gün içinde saat değişse de aynı söz (yerel gün)', () => {
    expect(pickQuote(new Date(2026, 5, 10, 0, 0, 1), 60)).toBe(pickQuote(new Date(2026, 5, 10, 23, 59, 59), 60));
  });
  it('dizin her zaman [0,len); boş liste 0', () => {
    for (let d = 1; d <= 366; d++) {
      const i = pickQuote(new Date(2024, 0, d), 7);
      expect(i).toBeGreaterThanOrEqual(0); expect(i).toBeLessThan(7);
    }
    expect(pickQuote(new Date(2026, 0, 5), 0)).toBe(0);
  });
});
