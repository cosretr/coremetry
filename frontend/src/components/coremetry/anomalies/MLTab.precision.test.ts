import { describe, it, expect } from 'vitest';
import { precisionCell, labelSourcesTitle, reasonsText, detectorRows } from './MLTab';
import { labelQueueHref, LABEL_QUEUE_PARAM } from './labelQueue';
import type { MLDetectorScore, MLScorecard } from '@/lib/types';

// v0.10.2484 — precision programı B.1: karne hücresi. Muaf > yetersiz >
// değer; yetersiz gri metin (renk yok), renk yalnız yeterli etiketle Wilson
// üst sınırı < %50 olduğunda.
const base: MLDetectorScore = { detector: 'op_latency', events: 400, samples: 24, hits: 6, hitRate: 0.25,
  precision: 0.25, wilsonLo: 0.12, wilsonHi: 0.449, coverage: 0.06, insufficient: false, exempt: false,
  bySource: { vote: 20, case: 4, caseCorrect: 3 }, notActionable: 5,
  notAnomalyReasons: { noDeviation: 9, noImpact: 2, expected: 3, other: 4, none: 5 } };

describe('precisionCell', () => {
  it('yeterli etiket: nokta + %95 aralık; üst sınır < %50 → sapan değer', () => {
    const c = precisionCell(base, 'tr');
    expect(c).toMatchObject({ kind: 'value', text: '%25 (%12–%45)', low: true });
    expect(precisionCell({ ...base, wilsonHi: 0.7 }, 'en')).toMatchObject({ kind: 'value', text: '25% (12%–70%)', low: false });
  });
  it('n < 20 ya da kapsama < %5 → gri "karar için yetersiz", renk yok', () => {
    const c = precisionCell({ ...base, samples: 8, insufficient: true, coverage: 0.02 }, 'tr');
    expect(c.kind).toBe('insufficient');
    expect(c.text).toBe('karar için yetersiz');
    expect(c.low).toBeUndefined();
    expect(c.title).toContain('8 etiket');
  });
  it('muaf dedektör: "muaf" (yetersizden önce)', () => {
    expect(precisionCell({ ...base, detector: 'metric_kafka', exempt: true, insufficient: true }, 'tr')).toMatchObject({ kind: 'exempt', text: 'muaf' });
  });
  it('etiket yok / eski sunucu: —', () => {
    expect(precisionCell(undefined).kind).toBe('none');
    expect(precisionCell({ ...base, samples: 0 }).kind).toBe('none');
  });
});

describe('etiket kaynakları ve nedenler', () => {
  it('kaynak dökümü ve eyleme değmez sayısı', () => {
    expect(labelSourcesTitle(base, 'tr')).toBe('20 olay oyu · 4 onaylı vaka (3 doğru) · 5 eyleme değmez (sayılmaz)');
    expect(labelSourcesTitle(undefined)).toBeUndefined();
  });
  it('"Sorun değil" nedenleri: yanlış sayılanlar | ayrı gösterilenler', () => {
    expect(reasonsText(base, 'tr')).toBe('sapma yok 9 · nedensiz 5 · diğer 4 | etkisiz 2 · beklenen 3');
    expect(reasonsText({ ...base, notAnomalyReasons: { noDeviation: 0, noImpact: 0, expected: 0, other: 0, none: 0 } })).toBe('');
  });
  it('detectorRows karne satırını taşır (olay yoksa boş)', () => {
    const sc: MLScorecard = { windowDays: 30, detectors: [base], signals: { op_latency: 'trace', error_rate: 'log' }, enabled: {} } as MLScorecard;
    const rows = detectorRows(sc, '');
    expect(rows.find(r => r.detector === 'op_latency')?.score?.wilsonHi).toBe(0.449);
    expect(rows.find(r => r.detector === 'error_rate')?.score).toBeUndefined();
  });
});

describe('labelQueueHref', () => {
  it('olay sayfasına kuyruk işaretiyle gider', () => {
    expect(LABEL_QUEUE_PARAM).toBe('lq');
    expect(labelQueueHref('a/b')).toBe('/anomalies/ml/a%2Fb?lq=1');
  });
});
