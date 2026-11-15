// autoRefresh.test.ts — v0.10.1430 Grafana tarzı global otomatik yenileme,
// SAF kurallar. Operatör: "Sağ üstte zaman yanında Grafana gibi ayrı refresh
// olabilir. 60 saniye default olabilir, özellikle metrikler için işe yarar."
//
// Tablolar: `?refresh=` ayrıştırma/biçimleme (off/0/10s/1m/legacy 30/çöp),
// öncelik (URL > localStorage > varsayılan; v0.10.2264: 5 dk), taban (10 s; /dashboard 30 s), rota
// politikası (kapalı allowlist, önek eşleşmesi, /dashboards ≠ /dashboard),
// ağır sorgu rung kesmesi, mağaza (bump / hold).
import { afterEach, describe, expect, it } from 'vitest';
import {
  AUTO_REFRESH_ROUTES, DASHBOARD_REFRESH_MIN_SEC, DEFAULT_REFRESH_SEC, LIVE_META, REFRESH_CHOICES,
  REFRESH_CHOICE_SECONDS, REFRESH_MIN_SEC, __resetRefreshStore, bumpRefresh, clampRefresh,
  formatRefreshParam, heavyRungNs, holdRefresh, isLiveQuery, parseRefreshParam, refreshPolicy,
  refreshSnapshot, resolveRefresh, snapHeavyWindow,
} from './autoRefresh';

afterEach(() => { __resetRefreshStore(); });

describe('parseRefreshParam / formatRefreshParam', () => {
  it('seçenek belirteçleri ve 0', () => {
    expect(parseRefreshParam('off')).toBe(0);
    expect(parseRefreshParam('0')).toBe(0);
    expect(parseRefreshParam('10s')).toBe(10);
    expect(parseRefreshParam('30s')).toBe(30);
    expect(parseRefreshParam('1m')).toBe(60);
    expect(parseRefreshParam('5m')).toBe(300);
    expect(parseRefreshParam('15m')).toBe(900);
    expect(parseRefreshParam(' 1M ')).toBe(60); // kırpma + küçük harf
  });

  it('eski Dashboard tam sayıları (v0.9.779: 30/60/300) anlamını korur', () => {
    expect(parseRefreshParam('30')).toBe(30);
    expect(parseRefreshParam('60')).toBe(60);
    expect(parseRefreshParam('300')).toBe(300);
  });

  it('bilinmeyen → null (bir sonraki basamağa düşer), varsayılana DEĞİL', () => {
    for (const junk of ['abc', '-30', '7', '45', '86400', 'Infinity', '1h', '']) {
      expect(parseRefreshParam(junk), junk).toBeNull();
    }
    expect(parseRefreshParam(null)).toBeNull();
    expect(parseRefreshParam(undefined)).toBeNull();
  });

  it('biçimleme gidiş-dönüş; seçenek dışı saniye bir ÜST runga oturur', () => {
    for (const c of REFRESH_CHOICES) {
      expect(formatRefreshParam(REFRESH_CHOICE_SECONDS[c])).toBe(c);
      expect(parseRefreshParam(formatRefreshParam(REFRESH_CHOICE_SECONDS[c]))).toBe(REFRESH_CHOICE_SECONDS[c]);
    }
    expect(formatRefreshParam(45)).toBe('1m');
    expect(formatRefreshParam(7)).toBe('10s');
    expect(formatRefreshParam(99999)).toBe('15m');
    expect(formatRefreshParam(-1)).toBe('off');
  });
});

describe('resolveRefresh — URL > localStorage > 5 dk, sonra taban', () => {
  it('öncelik zinciri', () => {
    expect(resolveRefresh('5m', '30s', REFRESH_MIN_SEC)).toBe(300);
    expect(resolveRefresh(null, '30s', REFRESH_MIN_SEC)).toBe(30);
    expect(resolveRefresh(null, null, REFRESH_MIN_SEC)).toBe(DEFAULT_REFRESH_SEC);
    expect(DEFAULT_REFRESH_SEC).toBe(300); // v0.10.2264 — operatör kararı: varsayılan 5 dakika
  });

  it('URL\'deki açık "off" kaydı EZER (paylaşılan link gönderenin niyetini taşır)', () => {
    expect(resolveRefresh('off', '1m', REFRESH_MIN_SEC)).toBe(0);
    expect(resolveRefresh('0', '1m', REFRESH_MIN_SEC)).toBe(0);
  });

  it('çöp URL bir sonraki basamağa düşer', () => {
    expect(resolveRefresh('junk', '5m', REFRESH_MIN_SEC)).toBe(300);
    expect(resolveRefresh('junk', 'junk', REFRESH_MIN_SEC)).toBe(DEFAULT_REFRESH_SEC);
  });

  it('taban: 10 s genel, 30 s /dashboard; off tabana takılmaz', () => {
    expect(clampRefresh(10, REFRESH_MIN_SEC)).toBe(10);
    expect(clampRefresh(10, DASHBOARD_REFRESH_MIN_SEC)).toBe(30);
    expect(clampRefresh(0, DASHBOARD_REFRESH_MIN_SEC)).toBe(0);
    expect(resolveRefresh('10s', null, DASHBOARD_REFRESH_MIN_SEC)).toBe(30);
    expect(resolveRefresh('off', null, DASHBOARD_REFRESH_MIN_SEC)).toBe(0);
  });
});

describe('refreshPolicy — kapalı allowlist', () => {
  it('metrik/genel bakış rotaları auto, alt yollar dahil', () => {
    for (const p of AUTO_REFRESH_ROUTES) {
      expect(refreshPolicy(p).mode, p).toBe('auto');
      expect(refreshPolicy(p + '/x').mode, p + '/x').toBe('auto');
    }
    expect(refreshPolicy('/service/backtrace')).toEqual({ mode: 'auto', minSec: 10 });
    expect(refreshPolicy('/dashboard')).toEqual({ mode: 'auto', minSec: 30 });
  });

  it('listede olmayan her şey manuel (log kuyruğu, trace şelalesi, explore, ayarlar…)', () => {
    for (const p of ['/', '/logs', '/traces', '/trace', '/explore', '/search', '/inbox', '/problems',
      '/anomalies', '/databases', '/messaging', '/external', '/settings', '/settings/clusters', '/admin/stats',
      '/dashboards', '/pod', '/entity', '/servicesX']) {
      expect(refreshPolicy(p).mode, p).toBe('manual');
    }
  });

  it('allowlist üyeleri gerçek rotalar (App.tsx) — bayat giriş kalmasın', () => {
    expect(AUTO_REFRESH_ROUTES).toEqual([
      '/services', '/service', '/endpoints', '/endpoint', '/metrics',
      '/dashboard', '/service-map', '/topology', '/hosts', '/clusters',
    ]);
  });
});

describe('LIVE_META / isLiveQuery', () => {
  it('yalnız refresh:"live" etiketi', () => {
    expect(isLiveQuery(LIVE_META)).toBe(true);
    expect(isLiveQuery({ refresh: 'poll' })).toBe(false);
    expect(isLiveQuery(undefined)).toBe(false);
    expect(isLiveQuery({})).toBe(false);
  });
});

describe('snapHeavyWindow — v0.8.270 rung disiplini', () => {
  const H = 3600e9;
  it('≥1h pencere 5 dk runga, kısa pencere 1 dk runga; uzunluk korunur', () => {
    expect(heavyRungNs(3 * H)).toBe(300e9);
    expect(heavyRungNs(H)).toBe(300e9);
    expect(heavyRungNs(15 * 60e9)).toBe(60e9);
    const to = 1_700_000_000_000e6 + 123e9;
    const out = snapHeavyWindow({ preset: '3h' }, { from: to - 3 * H, to });
    expect(out.to % 300e9).toBe(0);
    expect(out.to).toBeLessThanOrEqual(to);
    expect(to - out.to).toBeLessThan(300e9);
    expect(out.to - out.from).toBe(3 * H);
    const short = snapHeavyWindow({ preset: '15m' }, { from: to - 15 * 60e9, to });
    expect(short.to % 60e9).toBe(0);
    expect(to - short.to).toBeLessThan(60e9);
  });

  it('runga oturan pencere ve mutlak pencere aynen geçer', () => {
    const win = { from: 600e9, to: 1500e9 };
    expect(snapHeavyWindow({ preset: '15m' }, win)).toBe(win);
    const abs = { from: 1001e9, to: 5003e9 };
    expect(snapHeavyWindow({ preset: 'custom', fromMs: 1001, toMs: 5003 }, abs)).toBe(abs);
  });

  it('bir rung içindeki ardışık tik\'ler AYNI anahtarı üretir (dakikada bir tarama yok)', () => {
    const base = 1_700_000_000_000e6;
    const a = snapHeavyWindow({ preset: '3h' }, { from: base - 3 * H + 10e9, to: base + 10e9 });
    const b = snapHeavyWindow({ preset: '3h' }, { from: base - 3 * H + 70e9, to: base + 70e9 });
    expect(a).toEqual(b);
  });
});

describe('mağaza — bump / hold', () => {
  it('bumpRefresh anchor ve lastRefreshAt\'i taşır', () => {
    __resetRefreshStore(1000);
    expect(refreshSnapshot()).toMatchObject({ anchorMs: 1000, lastRefreshAt: 1000, holds: [] });
    bumpRefresh(5000);
    expect(refreshSnapshot()).toMatchObject({ anchorMs: 5000, lastRefreshAt: 5000 });
  });

  it('hold sayılır; serbest bırakma idempotent; iki hold tek neden', () => {
    const r1 = holdRefresh('edit');
    const r2 = holdRefresh('edit');
    expect(refreshSnapshot().holds).toEqual(['edit']);
    r1(); r1();
    expect(refreshSnapshot().holds).toEqual(['edit']);
    r2();
    expect(refreshSnapshot().holds).toEqual([]);
  });
});
