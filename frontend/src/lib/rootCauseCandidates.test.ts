import { describe, it, expect } from 'vitest';
import { ribbonCandidates, pctLabel, coMovingCause, localizedNote } from './rootCauseCandidates';
import type { ChangedService, RootCause, RootCauseHypothesis } from '@/lib/types';

// v0.10.700 — ribbon adayları: kalıcı hipotez varsa o (sıra korunur, kendisi
// süzülür, yüzde etiketi, zamansal gerekçe), yoksa canlı correlations.
const corr = (service: string, score: number): ChangedService => ({
  service, baselineRate: 0, currentRate: 0, rateDeltaPct: 0, baselineErrorRate: 0,
  currentErrorRate: 0, errDeltaPct: 0, baselineP99Ms: 0, currentP99Ms: 0, p99DeltaPct: 0,
  score, reasons: [`r-${service}`],
});
const hyp = (cands: RootCauseHypothesis['candidates']): RootCauseHypothesis => ({
  anchorKind: 'problem', anchorId: 'p', service: 'shop-api', computedAt: 0,
  topSuspect: cands[0]?.service ?? '', topScore: 0, confidence: 0, candidates: cands, version: 1,
});

describe('ribbonCandidates', () => {
  it('hipotez adayları öncelikli; kendisi süzülür; yüzde + zamansal gerekçe', () => {
    const got = ribbonCandidates({
      service: 'shop-api',
      correlations: [corr('shop-x', 9)],
      hypothesis: hyp([
        { service: 'shop-db', score: 0.42, hops: 1, reason: 'downstream', temporalReason: 'co-moves (ρ=0.8, leads by 1×5m)', structural: 0.42, temporal: 0.8 },
        { service: 'shop-api', score: 0.3, hops: 0 },
        { service: 'node:w1', score: 0.2, hops: 0, kind: 'node' },
      ]),
    });
    expect(got.map(c => c.service)).toEqual(['shop-db', 'node:w1']);
    expect(got[0]).toMatchObject({ scoreLabel: '42%', hops: 1, temporalReason: 'co-moves (ρ=0.8, leads by 1×5m)', source: 'hypothesis' });
    expect(got[1].kind).toBe('node');
  });
  it('hipotez yoksa canlı correlations, skora göre sıralı, kendisi süzülür', () => {
    const got = ribbonCandidates({ service: 'shop-api', correlations: [corr('shop-api', 99), corr('a', 3), corr('b', 7)] });
    expect(got.map(c => c.service)).toEqual(['b', 'a']);
    expect(got[0]).toMatchObject({ scoreLabel: '7', hops: 0, reason: 'r-b', source: 'live' });
  });
  it('boş hipotez listesi canlıya düşer; hiçbiri yoksa boş', () => {
    expect(ribbonCandidates({ service: 's', correlations: [corr('a', 1)], hypothesis: hyp([]) })[0].service).toBe('a');
    expect(ribbonCandidates({ service: 's' })).toEqual([]);
  });
  it('pctLabel kırpar ve yuvarlar', () => {
    expect(pctLabel(0.746)).toBe('75%');
    expect(pctLabel(1.4)).toBe('100%');
    expect(pctLabel(-1)).toBe('0%');
  });
});

// v0.10.1063 regresyon testi — OPERATÖR BİLDİRİMİ: "<svc-B> ile ilgili
// olduğunu düşünüyor ama alakasız." Manşet en yüksek skorlu satırı (iyileşen,
// bağlantısız svc-b, skor 404) "Co-moving … propagation" diye gösteriyordu.
// Artık yalnız sunucunun causeEligible işaretlediği satır manşete çıkar.
describe('coMovingCause — iyileşen / bağlantısız servis manşet olmaz (v0.10.1063)', () => {
  const row = (o: Partial<ChangedService>): ChangedService => ({
    service: 'svc-b', baselineRate: 2.77, currentRate: 0.01, rateDeltaPct: -99.5,
    baselineErrorRate: 0.768, currentErrorRate: 0, errDeltaPct: -100,
    baselineP99Ms: 32, currentP99Ms: 2.01, p99DeltaPct: -93.7, score: 404,
    reasons: [], ...o,
  });
  const cases: Array<{ name: string; corr: ChangedService[]; want: string | null }> = [
    { name: 'ekran görüntüsü: svc-b lost, kenarsız → manşet YOK',
      corr: [row({ direction: 'lost' }), row({ service: 'svc-c', direction: 'better', score: 213 })], want: null },
    { name: 'causeEligible taşımayan eski yanıt → manşet YOK',
      corr: [row({})], want: null },
    { name: 'kenarlı + hata ↑ (sunucu uygun işaretledi) → svc-b',
      corr: [row({ direction: 'worse', relation: 'downstream', causeEligible: true })], want: 'svc-b' },
    { name: 'uygun ama skor < 20 → manşet YOK',
      corr: [row({ direction: 'worse', relation: 'upstream', causeEligible: true, score: 12 })], want: null },
    { name: 'öznenin kendisi uygun işaretli olsa da → YOK',
      corr: [row({ service: 'svc-a', causeEligible: true })], want: null },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const rc = { problemId: 'p1', service: 'svc-a', metric: 'http_p99_ms', startedAt: 0, fromNs: 0, toNs: 60e9, correlations: c.corr } as RootCause;
      expect(coMovingCause(rc, 'svc-a')?.service ?? null).toBe(c.want);
    });
  }
});

// v0.10.1063 — "localized" manşet eki: "hiçbiri bağlı + kötüleşen değil"
// yalnız topoloji gerçekten okunduysa; okunamadıysa / alan yoksa
// "bağlantı doğrulanamadı". Kıpırdayan servis yoksa ek yok.
describe('localizedNote — manşet metni dürüst (v0.10.1063)', () => {
  const moved: ChangedService = {
    service: 'svc-b', baselineRate: 2.77, currentRate: 0.01, rateDeltaPct: -99.5,
    baselineErrorRate: 0.768, currentErrorRate: 0, errDeltaPct: -100,
    baselineP99Ms: 32, currentP99Ms: 2.01, p99DeltaPct: -93.7, score: 404,
    reasons: [], direction: 'lost',
  };
  const rcOf = (corr: ChangedService[], topologyKnown?: boolean): RootCause => ({
    problemId: 'p1', service: 'svc-a', metric: 'http_p99_ms', startedAt: 0, fromNs: 0, toNs: 60e9,
    correlations: corr, ...(topologyKnown === undefined ? {} : { topologyKnown }),
  } as RootCause);
  const cases: Array<{ name: string; rc: RootCause; want: string }> = [
    { name: 'kıpırdayan servis yok → ek yok', rc: rcOf([], true), want: '' },
    { name: 'yalnız öznenin kendi satırı → ek yok', rc: rcOf([{ ...moved, service: 'svc-a' }], true), want: '' },
    { name: 'topoloji okundu, uygun satır yok → "none is a connected, worsening dependency"',
      rc: rcOf([moved], true),
      want: '; services below moved in the same window but none is a connected, worsening dependency of svc-a' },
    { name: 'topoloji OKUNAMADI → bağlantı doğrulanamadı',
      rc: rcOf([moved], false),
      want: '; services below moved in the same window — bağlantı doğrulanamadı (topology unavailable)' },
    { name: 'alan YOK (eski önbellekli yanıt) → bağlantı doğrulanamadı',
      rc: rcOf([moved]),
      want: '; services below moved in the same window — bağlantı doğrulanamadı (topology unavailable)' },
  ];
  for (const c of cases) {
    it(c.name, () => { expect(localizedNote(c.rc, 'svc-a')).toBe(c.want); });
  }
});

// v0.10.1063 — ribbon canlı yolu: iyileşen / yalnız sakinleşen aday değil;
// uygun işaretli satır skoru düşük olsa da önce.
describe('ribbonCandidates canlı yol — yön + uygunluk (v0.10.1063)', () => {
  const r = (service: string, score: number, o: Partial<ChangedService> = {}): ChangedService => ({
    service, baselineRate: 0, currentRate: 0, rateDeltaPct: 0, baselineErrorRate: 0,
    currentErrorRate: 0, errDeltaPct: 0, baselineP99Ms: 0, currentP99Ms: 0, p99DeltaPct: 0,
    score, reasons: [], ...o,
  });
  it('svc-b (lost, bağlantısız) uygun svc-x\'in ARKASINDA; better/quieter düşer', () => {
    const got = ribbonCandidates({
      service: 'svc-a',
      correlations: [
        r('svc-b', 404, { direction: 'lost' }),
        r('svc-c', 213, { direction: 'better' }),
        r('svc-z', 90, { direction: 'quieter' }),
        r('svc-x', 62, { direction: 'worse', relation: 'downstream', causeEligible: true }),
      ],
    }).map(c => c.service);
    expect(got).toEqual(['svc-x', 'svc-b']);
  });
});
