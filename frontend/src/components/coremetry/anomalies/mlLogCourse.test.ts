// mlLogCourse — v0.10.1871: log olayının yığılı grafiği. Varsayılan mesaj
// kalıbı; "Seviye" yalnız sunucu verdiyse; gölge önce (arkada), yığın ilk =
// en alt; zaman ekseni kayda birebir değilse yığın çizilmez.
import { describe, it, expect } from 'vitest';
import type { LogCourse } from '@/lib/types';
import { LOG_REST_COLOR, logCourseSeries, resolveLogStackView } from './mlLogCourse';
import { logCourseAligned } from './MLLogPlayer';

const lc: LogCourse = {
  times: [100, 400, 700],
  shadow: [7, null, 7],
  byPattern: [
    { key: 'tpl|Connection pool exhausted', label: 'Connection pool exhausted', new: true, values: [0, 40, 45] },
    { key: 'tpl|Timeout calling payments-mobile', label: 'Timeout calling payments-mobile', values: [1, 20, 17] },
    { key: '_rest', label: 'diğer hata mesajları', values: [4, 0, 0] },
  ],
  byLevel: [{ key: '_error', label: 'hata', values: [5, 60, 62] }, { key: '_other', label: 'diğer seviyeler', values: [45, 50, 50] }],
};

describe('resolveLogStackView', () => {
  it('varsayılan mesaj kalıbı; Seviye yalnız veri varsa', () => {
    expect(resolveLogStackView(null, lc)).toBe('pattern');
    expect(resolveLogStackView('level', lc)).toBe('level');
    expect(resolveLogStackView('level', { ...lc, byLevel: undefined })).toBe('pattern');
    expect(resolveLogStackView('x', lc)).toBe('pattern');
  });
});

describe('logCourseSeries', () => {
  it('gölge arkada, yığın sırası sunucudan, kalan nötr renk', () => {
    const s = logCourseSeries(lc, 'pattern');
    expect(s.map(x => x.key)).toEqual(['_shadow', 'tpl|Connection pool exhausted', 'tpl|Timeout calling payments-mobile', '_rest']);
    expect(s[0]).toMatchObject({ type: 'bar', faint: true });
    expect(s.slice(1).every(x => x.type === 'bar' && x.stack === 'log')).toBe(true);
    expect(s[1].color).toBe('var(--err-solid)');
    expect(s[2].color).toBe('var(--warn-solid)');
    expect(s[3].color).toBe(LOG_REST_COLOR);
  });
  it('seviye görünümü; gölge yoksa seri yok', () => {
    const s = logCourseSeries({ ...lc, shadow: undefined }, 'level');
    expect(s.map(x => x.label)).toEqual(['hata', 'diğer seviyeler']);
    expect(s[0].color).toBe('var(--err-solid)');
    expect(s[1].color).toBe(LOG_REST_COLOR);
  });
});

describe('logCourseAligned', () => {
  it('kayıt ekseniyle birebir', () => {
    expect(logCourseAligned(lc, [100, 400, 700])).toBe(true);
    expect(logCourseAligned(lc, [100, 400])).toBe(false);
    expect(logCourseAligned(lc, [100, 400, 701])).toBe(false);
  });
});
