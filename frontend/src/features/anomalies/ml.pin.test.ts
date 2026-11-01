// v0.10.1743 — log ML dilim 0 pinleri: sekme çözümü ve karne etiketi (S4:
// kararsız satır isabet yazdırmaz).
import { describe, it, expect } from 'vitest';
import { backfillLabel, gateLabel, signalTickLabel, tickLabel, resolveAnomaliesTab, resolveMLSignal, scoreLabel, signalScoreLabel } from '@/components/coremetry/anomalies/MLTab';

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
  // v0.10.1763 — backfill ilerleme satırı: "0 olay" ile "çalışmıyor" ayrımı.
  it('backfill label', () => {
    const b = { signal: 'log', total: 504, done: 312, missing: [1, 2], doneUntil: 0, planEnd: 0, complete: false, running: true, startedAt: 0, updatedAt: 0 };
    expect(backfillLabel(b)).toBe('Log geçmişi: 314/504 saat · 2 eksik · sürüyor');
    expect(backfillLabel({ ...b, done: 502, complete: true, running: false })).toBe('Log geçmişi: 504/504 saat · 2 eksik · tamam');
    expect(backfillLabel({ ...b, missing: null, running: false, lastError: 'timeout' })).toBe('Log geçmişi: 312/504 saat · duraklı (timeout)');
  });
  // v0.10.1764 — son tik + VM bekleme satırı.
  it('tick label', () => {
    expect(tickLabel(undefined, 0)).toBe('son tik yok');
    expect(tickLabel({ at: 1000 - 300, durationMs: 34000, events: 2, backoff: [] }, 1000)).toBe('son tik 5 dk önce · 34 sn · 2 olay');
    expect(tickLabel({ at: 1000, durationMs: 1000, events: 0, backoff: [], docs: 1500, partial: 1 }, 1000)).toBe('son tik 0 dk önce · 1 sn · 0 olay · 1.500 belge · 1 kısmi ES sonucu (yazılmadı)');
    expect(tickLabel({ at: 1000, durationMs: 1000, events: 0, backoff: [{ family: 'dep:http', until: 1000 + 1200, failures: 3 }], lastError: 'x' }, 1000))
      .toBe('son tik 0 dk önce · 1 sn · 0 olay · VM bekleme: dep:http (20 dk) · hata: x');
  });
  // v0.10.1767 — dedektör başına son tik satırı.
  it('signal tick label', () => {
    expect(signalTickLabel('log_red', { ran: false, services: 0, sourceMs: 0, candidates: 0, suppressed: 0, written: 0, partial: 0 })).toBe('log_red: çalışmadı');
    expect(signalTickLabel('log_red', { ran: true, services: 420, sourceMs: 12300, candidates: 5, suppressed: 0, written: 2, partial: 1 })).toBe('log_red: 420 servis · 5 aday · 2 yazıldı · 12.3 sn · kısmi ES');
  });
});
