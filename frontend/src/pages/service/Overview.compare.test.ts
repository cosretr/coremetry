// Overview.compare.test.ts — v0.10.316 kaynak pini, v0.10.2490 güncellemesi
// (Operator-reported: "Compare to çalışmıyor"): metrik kipinde ÜÇ grafik de
// hayalet alır (Response time, Throughput, HTTP Status/Failure rate); span
// kipinde Failure rate · trace hayaleti aynen; ghost sorguları yalnız compare
// açıkken; toggle Details ile aynı hook + tohum anahtarı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8');

describe('Overview önceki-dönem hayaleti', () => {
  it('üç grafik de ghostItems alır', () => {
    expect(src.split('ghostItems={').length - 1).toBe(3);
    const rt = src.indexOf('ghostItems={metricGhosts.rt}');
    const tp = src.indexOf('ghostItems={metricGhosts.tput}');
    const fr = src.indexOf('ghostItems={showHttpStatus ? metricGhosts.err5xx : failureGhost}');
    expect(rt).toBeGreaterThan(src.indexOf('storageKey="ov-response-time-metric-v2"'));
    expect(tp).toBeGreaterThan(src.indexOf('storageKey="ov-throughput-metric-v2"'));
    expect(fr).toBeGreaterThan(src.indexOf("'ov-throughput-failure-v2'"));
    expect(rt).toBeLessThan(tp);
    expect(tp).toBeLessThan(fr);
  });
  it('metrik hayaletleri yalnız compare açıkken; kapalıyken {} (prop geçmez)', () => {
    expect(src).toContain('metricMode && ghostEnabled\n      ? overviewMetricGhosts(metricRedGhostQ.data?.series, compareOff, rtUnit, compareGhostLabel(compare, lang), {');
    expect(src).toContain('rt: liveGrid(rtAvgQ.data?.series), tput: liveGrid(metricTputQ.data?.series), err5xx: liveGrid(httpStatus.series),');
  });
  it('ek istek yok: hayaletler kaydırılmış /metric-red yanıtından', () => {
    expect(src.split('api.serviceMetricRED(').length - 1).toBe(2);
    expect(src).toContain('enabled: !!service && metricMode && ghostEnabled && fam.ready');
  });
  it('ghost sorguları yalnız compare açıkken; Details ile aynı tohum', () => {
    expect(src.split('enabled: ghostEnabled').length - 1).toBe(2);
    expect(src).toContain('useCompareParam({ seedKey: STORAGE_KEYS.svcChartsCompare })');
    expect(src).toContain('<CompareToggle value={compare} onChange={setCompare} />');
  });
  it('not gerçeği söyler; veri yoksa rozet', () => {
    expect(src).toContain("tl(metricMode ? 'ov.ghostNoteAll' : 'ov.ghostNote', lang)");
    expect(src).toContain('{noPriorData && <span className="badge b-gray">{tl(\'ov.ghost.noPrior\', lang)}</span>}');
  });
});
