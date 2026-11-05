// v0.10.1743 — log ML dilim 0 pinleri: sekme çözümü ve karne etiketi (S4:
// kararsız satır isabet yazdırmaz).
import { describe, it, expect } from 'vitest';
import { anomalyEventHref, legacyAnomalyRedirect, mlEventHref } from '@/lib/anomalyHref';
import { detectorGroup, detectorRows, statusTiles, backfillLabel, durationLabel, gateLabel, stageLabel, signalTickLabel, tickLabel, kindTickLines, baselineStatusLine, resolveAnomaliesTab, resolveMLSignal, scoreLabel, signalScoreLabel } from '@/components/coremetry/anomalies/MLTab';

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
    // v0.10.1811 — batch kuralı kesimi görünür.
    expect(tickLabel({ at: 1000, durationMs: 1000, events: 1, backoff: [], batchCut: 3 }, 1000)).toBe('son tik 0 dk önce · 1 sn · 1 olay · batch kuralı 3 adayı kesti');
  });
  // v0.10.1812 — sinyal türü başına tik ölçümü + tavan dışı servis (audit §13 D3).
  it('kind tick lines', () => {
    const t = { at: 1, durationMs: 1, events: 0, backoff: [], kinds: {
      metric: { services: 3, series: 9, seriesRows: 9, es: 0, vm: 4, ch: 2, dropped: 0, durationMs: 800 },
      log: { services: 500, series: 1000, seriesRows: 2000, es: 2, vm: 0, ch: 6, dropped: 17, durationMs: 2100 },
    } };
    const all = kindTickLines(t, '');
    expect(all.map(l => l.kind)).toEqual(['log', 'metric']);
    expect(all[0]).toEqual({ kind: 'log', warn: true, text: 'Log · 500 servis · 1.000 seri (2.000 satır) · sorgu ES 2 · VM 0 · CH 6 · 2.1 sn · log sinyalinde 17 servis tavan dışında kaldı' });
    expect(all[1].warn).toBe(false);
    expect(kindTickLines(t, 'trace')).toEqual([]);
    expect(kindTickLines(undefined, '')).toEqual([]);
  });
  // v0.10.1767 — dedektör başına son tik satırı.
  it('signal tick label', () => {
    expect(signalTickLabel('log_red', { ran: false, services: 0, sourceMs: 0, candidates: 0, suppressed: 0, written: 0, partial: 0 })).toBe('log_red: çalışmadı');
    expect(signalTickLabel('log_red', { ran: true, services: 420, sourceMs: 12300, candidates: 5, suppressed: 0, written: 2, partial: 1 })).toBe('log_red: 420 servis · 5 aday · 2 yazıldı · 12.3 sn · kısmi ES');
    expect(signalTickLabel('log_red', { ran: true, services: 3, sourceMs: 1000, candidates: 4, suppressed: 0, written: 2, partial: 0, batchCut: 2 })).toBe('log_red: 3 servis · 4 aday · 2 yazıldı · 1.0 sn · 2 batch kesildi');
  });
  // v0.10.1768 — B3 epizot: Durum ve Süre sütunları.
  it('stage and duration labels', () => {
    expect(stageLabel('shadow')).toBe('açık');
    expect(stageLabel('closed')).toBe('kapandı');
    expect(durationLabel(25 * 60e9)).toBe('25 dk');
    expect(durationLabel(130 * 60e9)).toBe('2 sa 10 dk');
    expect(durationLabel(72 * 3600e9)).toBe('3 g');
  });
  // v0.10.1770 (C1) — tam sayfa adresleri ve eski link yönlendirmesi.
  it('full page hrefs and legacy redirect', () => {
    expect(anomalyEventHref('a b')).toBe('/anomalies/a%20b');
    expect(mlEventHref('x')).toBe('/anomalies/ml/x');
    expect(legacyAnomalyRedirect(new URLSearchParams('event=e1'))).toBe('/anomalies/e1');
    expect(legacyAnomalyRedirect(new URLSearchParams('tab=ml&replay=r1'))).toBe('/anomalies/ml/r1');
    expect(legacyAnomalyRedirect(new URLSearchParams('tab=ml'))).toBeNull();
  });
  // v0.10.1779 — ML sekmesi düzeni (mockup): durum kutuları + dedektör karnesi tablosu.
  it('detector rows and status tiles', () => {
    const sc = { windowDays: 30, detectors: [{ detector: 'trace_pod_outlier', events: 3, samples: 0, hits: 0, hitRate: 0 }],
      bySignal: [{ detector: 'trace', events: 3, samples: 0, hits: 0, hitRate: 0 }], promotion: {},
      gates: { log_volume: { ok: false, reasons: ['dedektör kapalı'] }, trace_pod_outlier: { ok: false, reasons: ['terfi anahtarı kapalı', 'karar örneği 0 < 20'] } },
      signals: { log_volume: 'log', trace_pod_outlier: 'trace' },
      tick: { at: 1000 - 180, durationMs: 2000, events: 1, backoff: [{ family: 'dep:http', until: 1000 + 1320, failures: 2 }], signals: { trace_pods: { ran: true, services: 1, sourceMs: 400, candidates: 1, suppressed: 0, written: 1, partial: 0 } } },
      backfill: [{ signal: 'log', total: 504, done: 92, missing: [], doneUntil: 0, planEnd: 0, complete: false, running: true, startedAt: 0, updatedAt: 0 }] };
    expect(detectorGroup('trace_pod_outlier')).toBe('trace_pods');
    expect(detectorGroup('metric_jvm')).toBe('metric_ml');
    const rows = detectorRows(sc, '');
    expect(rows.map(r => `${r.detector}:${r.enabled}:${r.events}:${r.tick?.written ?? '-'}`)).toEqual(['log_volume:false:0:-', 'trace_pod_outlier:true:3:1']);
    expect(detectorRows(sc, 'log').length).toBe(1);
    const tiles = statusTiles(sc, '', 1000);
    expect(tiles.map(t => t.v)).toEqual(['3 dk önce', '92 / 504 sa', '3', '1 aile']);
    expect(tiles[3].s).toBe('dep:http · 22 dk');
    // v0.10.1811 kesimi görünür kutuda (tickLabel artık çizilmiyor — 1812'de düzeltildi)
    expect(statusTiles({ ...sc, tick: { ...sc.tick!, batchCut: 4 } }, '', 1000)[0].s).toBe('2 sn · 1 olay · batch kuralı 4 adayı kesti');
  });
});

// v0.10.1814 — günlük baseline işi: "Baseline: son gün, satır, süre" satırı (render: MLTab ml-baseline-line).
describe('baselineStatusLine', () => {
  it('son gün, satır, süre; hata uyarı rengi', () => {
    const l = baselineStatusLine({ lastDay: '2026-11-04', last: { day: '2026-11-04', trigger: 'auto', rows: 12345, series: 10, slices: 16, durationMs: 42000, at: 1 } });
    expect(l?.text).toContain('son gün 2026-11-04');
    expect(l?.text).toContain('42.0 sn');
    expect(l?.warn).toBe(false);
    const e = baselineStatusLine({ last: { day: '2026-11-04', trigger: 'auto', rows: 0, series: 0, slices: 3, durationMs: 100, at: 1, error: 'timeout' } });
    expect(e?.warn).toBe(true);
    expect(e?.text).toContain('2026-11-04 hatalı: timeout');
  });
  it('v0.10.1816: haftalık profil koşusu aynı satırda', () => {
    const l = baselineStatusLine({ lastDay: '2026-11-02', profiles: { week: '2026-11-02', services: 1400, changed: 12, durationMs: 900, at: 1 } });
    expect(l?.text).toContain('profil haftası 2026-11-02: 1.400 servis, 12 değişim');
    expect(baselineStatusLine({ profiles: { week: '2026-11-02', services: 0, changed: 0, durationMs: 1, at: 1, error: 'timeout' } })?.warn).toBe(true);
  });
  it('kayıt yoksa satır yok; boş kayıt = henüz gün yok', () => {
    expect(baselineStatusLine(undefined)).toBeNull();
    expect(baselineStatusLine({})?.text).toBe('Baseline · henüz işlenmiş gün yok');
  });
});
