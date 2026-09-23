import { describe, it, expect } from 'vitest';
import { metricLabelQ, METRIC_LABEL_Q_MIN } from './metricLabelQuery';

// v0.10.875 — q rungu: <3 karakter sunucuya gitmez (tek cache girdisi, istemci
// süzer); ≥3 karakter kırpılmış olarak gider.
describe('metricLabelQ', () => {
  it('kısa girdi boş, uzun girdi kırpılmış', () => {
    expect(METRIC_LABEL_Q_MIN).toBe(3);
    expect(metricLabelQ('')).toBe('');
    expect(metricLabelQ(' ab ')).toBe('');
    expect(metricLabelQ(' abc ')).toBe('abc');
    expect(metricLabelQ('api-1')).toBe('api-1');
  });
});
