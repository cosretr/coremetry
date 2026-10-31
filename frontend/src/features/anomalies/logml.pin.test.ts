// v0.10.1743 — log ML dilim 0 pinleri: sekme çözümü ve karne etiketi (S4:
// kararsız satır isabet yazdırmaz).
import { describe, it, expect } from 'vitest';
import { resolveAnomaliesTab, scoreLabel } from '@/components/coremetry/anomalies/LogMLTab';

describe('log ml slice 0', () => {
  it('tab resolves unknown to detectors', () => {
    expect(resolveAnomaliesTab(null)).toBe('detectors');
    expect(resolveAnomaliesTab('x')).toBe('detectors');
    expect(resolveAnomaliesTab('logml')).toBe('logml');
  });
  it('score label omits hit rate without samples', () => {
    expect(scoreLabel({ detector: 'log_volume', events: 3, samples: 0, hits: 0, hitRate: 0 })).toBe('Log hacmi: 3 olay · karar yok');
    expect(scoreLabel({ detector: 'error_rate', events: 10, samples: 4, hits: 3, hitRate: 0.75 })).toBe('Hata oranı: 10 olay · %75 isabet (4 karar)');
  });
});
