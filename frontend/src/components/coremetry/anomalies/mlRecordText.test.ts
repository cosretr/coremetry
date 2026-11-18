// v0.10.2248 (anomali kartı Dilim D) — kayıtlı kartın SAF metinleri: düz dilde
// başlık (tespit anı / şu an, hacim, beklenen), zaman satırı + adlandırılmış
// tepe, telemetry_volume şablonu (audit C), kanıt metni kayıt alanlarından,
// EN İngilizce / TR Türkçe. Kritik yol yüzdesi cümlesi yok.
import { describe, it, expect } from 'vitest';
import type { MLRecordView } from '@/lib/types';
import { durText, evidenceTag, evidenceText, ingestFactor, recordHeadline, recordStatus, recordTimeline, backInBandAtDetection } from './mlRecordText';

const T0 = 1_800_000_000;
function lat(over: Partial<MLRecordView> = {}): MLRecordView {
  const v = Array.from({ length: 30 }, (_, i) => (i >= 10 && i < 15 ? 135 : i === 15 ? 107 : 43));
  return {
    v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
    series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 60 },
    baseline: { expected: 43, lo: 31, hi: 58, method: 'm', stepSec: 60 },
    direction: 'up', onsetAt: (T0 + 600) * 1e9, detectedAt: (T0 + 900) * 1e9, endedAt: (T0 + 960) * 1e9,
    peakValue: 135, valueAtDetection: 107, expectedValue: 43, volume: 1240,
    evidence: [{ kind: 'series', source: 'vm', summary: 'Endpoint p95 yanıt süresi, RED ailesi http', independent: false },
      { kind: 'confirm', source: 'ml', summary: 'Aynı serviste başka dedektörün olayı: abc123', independent: true }],
    class: 'service_anomaly',
    snapshot: { t0: T0, step: 60, aggregation: 'p95', v, lo: v.map(() => 31), hi: v.map(() => 58), n: v.map(() => 100) },
    title: '', detector: 'op_latency', ...over,
  };
}

function vol(): MLRecordView {
  const v = Array.from({ length: 24 }, (_, i) => (i >= 12 ? 26 : 6.2));
  const n = Array.from({ length: 24 }, (_, i) => (i >= 12 ? 420 : 100));
  const flat = (x: number) => v.map(() => x);
  const ctx = (measure: string, unit: string, x: number) => ({ measure, source: 'vm', query: 'q', unit, method: 'm',
    snapshot: { t0: T0, step: 60, aggregation: 'a', v: flat(x), lo: flat(0), hi: flat(x * 3) } });
  return lat({
    subject: { service: 'checkout-api', resolved: true }, detector: 'svc_telemetry_volume', class: 'telemetry_volume',
    series: { source: 'ch:service_summary_5m', query: 'q', aggregation: 'spans_per_request', unit: 'span/request', stepSec: 300 },
    baseline: { expected: 6.2, lo: 5.2, hi: 7.3, method: 'm', stepSec: 300 },
    onsetAt: (T0 + 720) * 1e9, detectedAt: (T0 + 840) * 1e9, endedAt: undefined,
    peakValue: 26, valueAtDetection: 26, expectedValue: 6.2, volume: 5000,
    snapshot: { t0: T0, step: 60, aggregation: 'spans_per_request', v, lo: flat(5.2), hi: flat(7.3), n },
    context: [ctx('error_rate', 'percent', 0.3), ctx('latency', 'ms', 47)],
    gates: [{ name: 'service_impact', passed: false, value: 0, threshold: 1 }],
    evidence: [{ kind: 'latency', source: 'vm', summary: 'Servis p95 yanıt süresi aynı pencerede', independent: true }],
  });
}

describe('kayıtlı kart metinleri', () => {
  it('biten gecikme olayı: tespit anı değeri, çağrı, beklenen (EN + TR)', () => {
    expect(recordHeadline(lat(), 'p95 latency', 'en')).toBe('GET /cart p95 latency at detection 107 ms (1,240 calls); expected ≈ 43 ms.');
    expect(recordHeadline(lat(), 'p95 gecikmesi', 'tr')).toBe('GET /cart p95 gecikmesi tespit anında 107 ms (1.240 çağrı); beklenen ≈ 43 ms.');
  });
  // v0.10.2344 — operatör bulgusu: tespit anında değer banda dönmüşken başlık
  // "tespit anında 4,94; beklenen ≈ 5,78" diyordu; asıl olay (tepe) görünmüyordu.
  it('biten olayda tespit anı değeri banda dönmüşse başlık bant dışı uç değerle başlar (EN + TR)', () => {
    const r = lat({ valueAtDetection: 47 });
    expect(backInBandAtDetection(r)).toBe(true);
    const en = recordHeadline(r, 'p95 latency', 'en');
    expect(en).toBe(`GET /cart p95 latency rose to 135 ms, above the expected band (${en.match(/\((\d\d:\d\d)\)/)![1]}) (1,240 calls); expected ≈ 43 ms, band 31 ms–58 ms. Back in band at 47 ms by detection.`);
    const tr = recordHeadline(r, 'p95 gecikmesi', 'tr');
    expect(tr).toContain('GET /cart p95 gecikmesi 135 ms ile beklenen bandın üstüne çıktı (');
    expect(tr).toContain('(1.240 çağrı); beklenen ≈ 43 ms, bant 31 ms–58 ms. Tespit anında 47 ms ile banda dönmüştü.');
    expect(tr).not.toContain('tespit anında 47');
  });
  it('düşüş yönünde "altına indi"; tespit anı bant dışındaysa ya da olay sürüyorsa eski başlık', () => {
    const down = lat({ direction: 'down', peakValue: 12, valueAtDetection: 40 });
    expect(recordHeadline(down, 'p95 gecikmesi', 'tr')).toContain('12 ms ile beklenen bandın altına indi (');
    expect(recordHeadline(down, 'p95 latency', 'en')).toContain('fell to 12 ms, below the expected band (');
    expect(backInBandAtDetection(lat())).toBe(false); // 107 ms bandın dışında
    expect(recordHeadline(lat({ valueAtDetection: 47, endedAt: 0 }), 'p95 latency', 'en')).toContain(' now '); // süren olay
    // uç değer de bandın içindeyse (kayıt tutarsız) eski başlık kalır
    expect(backInBandAtDetection(lat({ valueAtDetection: 47, peakValue: 50 }))).toBe(false);
  });
  it('süren olay "şu an" son noktayı yazar', () => {
    const r = lat({ endedAt: undefined });
    expect(recordHeadline(r, 'p95 latency', 'en')).toBe('GET /cart p95 latency now 43 ms (1,240 calls); expected ≈ 43 ms.');
    expect(recordStatus(r, (T0 + 600) * 1e9 + 14 * 6e10, 'en')).toBe('Ongoing · 14 min');
  });
  it('zaman satırı ve adlandırılmış tepe; süre biçimi', () => {
    const s = recordTimeline(lat(), 'en');
    expect(s).toMatch(/^Started \d\d:\d\d, detected \d\d:\d\d, back in band \d\d:\d\d · peak 135 ms \(\d\d:\d\d\)$/);
    expect(recordStatus(lat(), 0, 'tr')).toBe('Bitti · 6 dk sürdü');
    expect(durText(125 * 6e10, 'en')).toBe('2 h 5 min');
  });
  it('telemetry_volume şablonu (audit C): a → b, değişmeyenler, ingest kat', () => {
    expect(ingestFactor(vol())).toBeCloseTo(4.2, 5);
    const en = recordHeadline(vol(), 'spans per request', 'en');
    expect(en).toMatch(/^checkout-api spans per request went from 6\.2 to 26 \(since \d\d\.\d\d\.\d{4} \d\d:\d\d\)\. Error rate and latency did not change\. No service impact; ingest volume 4\.2×\.$/);
    const tr = recordHeadline(vol(), 'istek başına span sayısı', 'tr');
    expect(tr).toContain('6,2 değerinden 26 değerine çıktı');
    expect(tr).toContain('Servis etkisi yok; ingest hacmi 4,2 kat.');
  });
  it('kritik yol yüzdesi cümlesi yok', () => {
    for (const lang of ['en', 'tr'] as const) {
      expect(recordHeadline(lat(), 'x', lang)).not.toMatch(/critical path|kritik yol/i);
    }
  });
  it('kanıt metni kayıttan, dile göre; bağlantı süzülmüş sayfaya', () => {
    const r = lat();
    const s = evidenceText(r.evidence![0], r, 'p95 latency', 'en');
    expect(s.text).toBe('Tested series: p95 latency · VictoriaMetrics');
    expect(s.href).toContain('checkout-api');
    const c = evidenceText(r.evidence![1], r, 'p95 latency', 'en');
    expect(c.text).toBe('Another detector fired on the same service');
    expect(c.href).toContain('abc123');
    const v = vol();
    expect(evidenceText(v.evidence![0], v, 'x', 'en').text).toBe('Service p95 latency in the same window: 47 ms → 47 ms');
    expect(evidenceTag('series', false)).toBe('sameSeries');
    expect(evidenceTag('latency', false)).toBe('context');
    expect(evidenceTag('rollout', true)).toBe('independent');
  });
});
