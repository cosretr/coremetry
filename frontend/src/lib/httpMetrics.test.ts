import { describe, it, expect } from 'vitest';
import { httpQueries, serverSel, promStr, rateWindow, seriesLabels, legendOf, relabel, scalarOf, barsOf, mergeTable, filterOptions, fmtCount, fmtRatio, fmtSeconds, latencyTone, errorRatioTone, clientErrorTone, sliTone, httpSeriesColors, donutSlices, fmtShare, sparkValues, statusCodeRateQuery, statusCodeSeries, totalRequestsQuery, HTTP_LABELS, RATE_WINDOW_FLOOR_S, rateWindowSec } from './httpMetrics';
import type { SpanMetricSeries } from './types';

// v0.10.1244 — HTTP Metrics sekmesi (Grafana panosu → VictoriaMetrics).
const ser = (labels: Record<string, string>, vals: number[]): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, v]) => `${k}="${v}"`),
  points: vals.map((v, i) => ({ time: i * 1e9, value: v })),
});

describe('sorgular', () => {
  it('seçici: kaçış + isteğe bağlı route/method', () => {
    expect(promStr('a"b\\c')).toBe('"a\\"b\\\\c"');
    expect(serverSel({ app: 'shop' })).toBe('{service_name="shop"}');
    expect(serverSel({ app: 'shop', route: '/a', method: 'GET' }, 'le="0.5"'))
      .toBe('{service_name="shop", http_route="/a", http_request_method="GET", le="0.5"}');
  });
  it('rate penceresi en az 180 sn (Overview ile tek taban), en az 4 adım', () => {
    expect(RATE_WINDOW_FLOOR_S).toBe(180);
    expect(rateWindow(600)).toBe('180s');
    expect(rateWindow(3600)).toBe('180s'); // adım 12 sn → 4 adım 48 sn < taban
    expect(rateWindowSec(6 * 3600)).toBe(288); // adım 72 sn → 4 adım tabanı aşar
    expect(rateWindow(86400)).toBe(`${4 * 288}s`);
  });
  it('panodaki metrik adları ve pencere', () => {
    const q = httpQueries({ app: 'shop' }, 3600);
    expect(q.total).toBe('sum(increase(http_server_request_duration_seconds_count{service_name="shop"}[3600s]))');
    expect(q.p95).toContain('histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name="shop"}[180s])))');
    expect(q.statusClass).toContain('"${1}xx"');
    expect(q.dConnErr).toContain('http_response_status_code=""');
    expect(Object.keys(q).length).toBeGreaterThanOrEqual(38);
  });
});

describe('etiket ve dönüşümler', () => {
  it('groupKey ayrıştırma ve lejant', () => {
    const l = seriesLabels({ groupKey: ['http_route="/a"', 'm="x\\"y"'] });
    expect(l).toEqual({ http_route: '/a', m: 'x"y' });
    expect(legendOf('{{m}} {{http_route}}', l)).toBe('x"y /a');
    expect(relabel([ser({ a: '1' }, [0.5])], '→ {{a}}', 100)[0]).toEqual({ groupKey: ['→ 1'], points: [{ time: 0, value: 50 }] });
  });
  it('stat son geçerli değer; boş → null', () => {
    expect(scalarOf([ser({}, [1, 2, NaN])])).toBe(2);
    expect(scalarOf([])).toBeNull();
  });
  it('çubuklar azalan, sıfırlar atılır', () => {
    expect(barsOf([ser({ r: 'a' }, [1]), ser({ r: 'b' }, [5]), ser({ r: 'c' }, [0])], 'r')).toEqual([{ label: 'b', value: 5 }, { label: 'a', value: 1 }]);
  });
  it('tablo birleştirme anahtarlara göre', () => {
    const rows = mergeTable({
      total: [ser({ http_request_method: 'GET', http_route: '/a' }, [10])],
      p95: [ser({ http_request_method: 'GET', http_route: '/a' }, [0.2]), ser({ http_request_method: 'POST', http_route: '/b' }, [1])],
    }, ['http_request_method', 'http_route']);
    expect(rows).toEqual([
      { http_request_method: 'GET', http_route: '/a', total: 10, p95: 0.2 },
      { http_request_method: 'POST', http_route: '/b', p95: 1 },
    ]);
  });
  it('süzgeç seçenekleri, "none" route hariç', () => {
    expect(filterOptions([ser({ http_route: '/b', http_request_method: 'POST' }, [1]), ser({ http_route: 'none', http_request_method: 'GET' }, [1])]))
      .toEqual({ routes: ['/b'], methods: ['GET', 'POST'] });
  });
});

// v0.10.1245 — prod: eksik tablo hücresi (undefined) sekmeyi düşürüyordu.
describe('biçimlendiriciler eksik değere dayanıklı', () => {
  it.each([undefined, null, NaN, Infinity, ''])('%s → —', v => {
    expect(fmtCount(v as never)).toBe('—');
    expect(fmtRatio(v as never)).toBe('—');
    expect(fmtSeconds(v as never, n => String(n))).toBe('—');
  });
  it('değerler biçimlenir', () => {
    expect(fmtRatio(0.0123)).toBe('%1.23');
    expect(fmtSeconds(0.2, n => `${n}s`)).toBe('0.2s');
    expect(fmtCount(1234, 0)).toBe((1234).toLocaleString('tr-TR'));
  });
});

describe('relabel null noktalara dayanıklı (v0.10.1245)', () => {
  it('points: null → boş', () => {
    expect(relabel([{ groupKey: ['a="1"'], points: null as never }], '{{a}}', 100)).toEqual([{ groupKey: ['1'], points: [] }]);
  });
});

// v0.10.1247 — Grafana eşikleri ve sabit renkler.
describe('Grafana eşikleri', () => {
  it('gecikme 0,5 / 1 sn', () => {
    expect([0.2, 0.5, 1.2, undefined].map(latencyTone)).toEqual([undefined, 'warn', 'err', undefined]);
  });
  it('hata oranı %1 / %5; 4xx kartı %5 / %15; SLI %99 / %95', () => {
    expect([0.001, 0.02, 0.06].map(errorRatioTone)).toEqual([undefined, 'warn', 'err']);
    expect([0.01, 0.06, 0.2].map(clientErrorTone)).toEqual([undefined, 'warn', 'err']);
    expect([0.999, 0.97, 0.9, null].map(sliTone)).toEqual([undefined, 'warn', 'err', undefined]);
  });
  it('sabit renkler yalnız bilinen adlara, çözülemeyen token atlanır', () => {
    const m = httpSeriesColors(['2xx', '5xx', 'GET /a', 'p99'], t => (t === '--err' ? '#f00' : t === '--ok' ? '#0f0' : ''));
    expect([...m.entries()]).toEqual([['2xx', '#0f0'], ['5xx', '#f00'], ['p99', '#f00']]);
  });
});

// v0.10.1247 — Grafana birebir yardımcıları.
describe('heatmap / donut / spark', () => {
  it('donut yüzdeleri', () => {
    expect(donutSlices([{ label: 'a', value: 3 }, { label: 'b', value: 1 }]).map(d => d.pct)).toEqual([75, 25]);
    expect(donutSlices([{ label: 'a', value: 0 }])[0].pct).toBe(0);
  });
  it('spark değerleri, NaN → null', () => {
    expect(sparkValues([ser({}, [1, NaN, 3])])).toEqual([1, null, 3]);
    expect(sparkValues(undefined)).toEqual([]);
  });
});

// v0.10.1253 — Overview HTTP status kodu paneli.
describe('status kodu serileri', () => {
  it('sorgu ve kod bazlı seriler, sınıf renk tokenı', () => {
    expect(statusCodeRateQuery('shop', 1800)).toBe('sum by (http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name="shop"}[180s]))');
    const r = statusCodeSeries([ser({ http_response_status_code: '500' }, [1]), ser({ http_response_status_code: '200' }, [9]), ser({}, [3])]);
    expect(r.series.map(s => s.groupKey[0])).toEqual(['200', '500']);
    expect(r.tokens.get('200')).toBe('--ok');
    expect(r.tokens.get('500')).toBe('--err');
  });
});

// v0.10.1259 — Overview toplam istek karosu sorgusu.
describe('totalRequestsQuery', () => {
  it('pencere toplamı', () => {
    expect(totalRequestsQuery('shop', 10800)).toBe('sum(increase(http_server_request_duration_seconds_count{service_name="shop"}[10800s]))');
  });
});

// v0.10.1325 — operatör (prod, Status Code Dağılımı lejantı): "%0.01 olarak
// da dağılım gösterebilir, burada 0 yazıyor çoğu." Pay tam sayıya
// yuvarlanıyordu; 200 dışı her kod "%0" okunuyordu.
describe('fmtShare — uyarlanır yüzde payı (v0.10.1325)', () => {
  it.each([
    [0, '%0'],
    [0.004, '<%0,01'],
    [0.0099, '<%0,01'],
    [0.01, '%0,01'],
    [0.33, '%0,33'],
    [2.44, '%2,4'],
    [9.99, '%10'],
    [10, '%10'],
    [42.4, '%42'],
    [98.6, '%99'],
    [99.3, '%99,3'],
    [99.97, '%99,97'],
    [99.999, '%99,99'],
    [100, '%100'],
  ])('fmtShare(%s) = %s', (v, want) => { expect(fmtShare(v)).toBe(want); });
  it('< 100 pay asla "%100" okunmaz', () => {
    for (const v of [99, 99.5, 99.99, 99.994, 99.995, 99.9999999]) expect(fmtShare(v)).not.toBe('%100');
  });
  it('eksik / geçersiz değer → —, negatif → %0', () => {
    for (const v of [null, undefined, NaN, Infinity]) expect(fmtShare(v as never)).toBe('—');
    expect(fmtShare(-1)).toBe('%0');
  });
});

// v0.10.1350 (operatör bildirimi: metrik kipi "durum-kodu etiketi yok" derken
// bu sekme 200/400 çiziyordu) — etiket adları tek kaynakta; backend aynı
// OTel anahtarını promLabel ile buna sanitize eder (Go: TestHTTPLabelsMatchFrontend).
describe('HTTP_LABELS', () => {
  it('VM etiket adları ve kurucuların kullandığı yazım', () => {
    expect(HTTP_LABELS).toEqual({
      service: 'service_name', route: 'http_route', method: 'http_request_method', status: 'http_response_status_code',
    });
    const q = httpQueries({ app: 'shop', route: '/a', method: 'GET' }, 3600);
    expect(q.err5).toContain(`${HTTP_LABELS.status}=~"5.."`);
    expect(q.statusCodes).toContain(`sum by (${HTTP_LABELS.status})`);
    expect(serverSel({ app: 'shop', route: '/a', method: 'GET' }))
      .toBe(`{${HTTP_LABELS.service}="shop", ${HTTP_LABELS.route}="/a", ${HTTP_LABELS.method}="GET"}`);
    expect(statusCodeRateQuery('x', 600)).toContain(`sum by (${HTTP_LABELS.status})`);
  });
});
