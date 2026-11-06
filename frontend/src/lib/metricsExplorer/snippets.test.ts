// v0.10.1864 — MetricsQL parçacıkları (Faz 2 dilim 2.4).
import { describe, expect, it } from 'vitest';
import { applySnippet, SNIPPETS } from './snippets';

describe('applySnippet', () => {
  it.each([
    ['with', 'sum(up)', 'WITH (\n  f = {label="değer"}\n)\nsum(up)'],
    ['default', 'sum(up)', 'sum(up) default 0'],
    ['topk_avg', 'sum by (job) (up)', 'topk_avg(5, sum by (job) (up))'],
    ['rollup_rate', 'x_total', 'rollup_rate(x_total[$__rate_interval])'],
    ['keep_metric_names', 'abs(x)', 'abs(x) keep_metric_names'],
    ['topk_avg', '', 'topk_avg(5, metrik_adi)'],
  ] as const)('%s', (id, text, want) => expect(applySnippet(id, text)).toBe(want));
  it('beş parçacık (audit §3.1 satır 8)', () => expect(SNIPPETS.map(s => s.id)).toEqual(['with', 'default', 'topk_avg', 'rollup_rate', 'keep_metric_names']));
});
