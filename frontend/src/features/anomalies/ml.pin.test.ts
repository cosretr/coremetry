// v0.10.1743 — log ML dilim 0 pinleri: sekme çözümü ve karne etiketi (S4:
// kararsız satır isabet yazdırmaz).
import { describe, it, expect } from 'vitest';
import { gateLabel, resolveAnomaliesTab, resolveMLSignal, scoreLabel, signalScoreLabel } from '@/components/coremetry/anomalies/MLTab';

describe('log ml slice 0', () => {
  it('tab resolves unknown to detectors', () => {
    expect(resolveAnomaliesTab(null)).toBe('detectors');
    expect(resolveAnomaliesTab('x')).toBe('detectors');
    expect(resolveAnomaliesTab('ml')).toBe('ml');
  });
  it('score label omits hit rate without samples', () => {
    expect(scoreLabel({ detector: 'log_volume', events: 3, samples: 0, hits: 0, hitRate: 0 })).toBe('Log hacmi: 3 olay · karar yok');
    expect(scoreLabel({ detector: 'error_rate', events: 10, samples: 4, hits: 3, hitRate: 0.75 })).toBe('Hata oranı: 10 olay · %75 isabet (4 karar)');
  });
  // v0.10.1751 — kalite çerçevesi: sinyal karnesi + terfi kapısı gerekçeleri.
  it('signal score and gate labels', () => {
    expect(signalScoreLabel({ detector: 'trace', events: 7, samples: 0, hits: 0, hitRate: 0 })).toBe('Trace: 7 olay · karar yok');
    expect(signalScoreLabel({ detector: 'metric', events: 9, samples: 4, hits: 2, hitRate: 0.5 })).toBe('Metrik: %50 isabet (4 karar / 9 olay)');
    expect(gateLabel({ ok: false, reasons: ['ana anahtar kapalı', 'takvim dilimi tanımlı değil'] })).toBe('terfi kapısı: ana anahtar kapalı · takvim dilimi tanımlı değil');
    expect(gateLabel({ ok: true })).toBe('terfi kapısı geçildi');
    expect(gateLabel(undefined)).toBe('');
  });
});
