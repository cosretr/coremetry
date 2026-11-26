// alertDetailBoxes — v0.10.2127 (Problems sadeleştirme S7). Dört kutunun
// metni deterministik; alan yoksa kutu gizli (null), uydurma dolgu yok.
import { describe, it, expect } from 'vitest';
import { alertWhatText, alertWhoText, alertCause } from './alertDetailBoxes';
import type { Problem, ProblemStandard } from '@/lib/types';

const base = {
  ruleId: 'builtin:error_rate', ruleName: 'High error rate', service: 'checkout',
  metric: 'error_rate', value: 12.4, threshold: 5,
} as Pick<Problem, 'ruleId' | 'ruleName' | 'service' | 'metric' | 'value' | 'threshold'>;

const std = (over: Partial<ProblemStandard> = {}): ProblemStandard => ({
  family: 'error', kind: 'anomaly.error_rate', subject: 'checkout', headline: 'checkout: hata oranı arttı',
  impact: { primary: { text: 'hata oranı %12,4 (normal %5)', value: 12.4, normal: 5 }, known: true, floor: {} },
  since: { startedAt: 1, durationSec: 60, ongoing: true },
  evidence: { sentence: 'Kural eşiği aşıldı; ölçü span\'lerden (örneklenmiş).', source: 'span' },
  confidence: { level: 'high', gates: [] },
  nextStep: { text: 'x' },
  notify: {},
  ...over,
} as unknown as ProblemStandard);

describe('alertWhatText', () => {
  it.each([
    ['standart yok', {}, undefined, 'High error rate — checkout: error_rate değeri 12.40, eşik 5.00.'],
    ['kanıt cümlesi eklenir', {}, std(), 'High error rate — checkout: error_rate değeri 12.40, eşik 5.00. Kural eşiği aşıldı; ölçü span\'lerden (örneklenmiş).'],
    ['öncelik gerekçesi eklenir', { priorityReason: 'kullanıcı trafiği etkileniyor' }, undefined,
      'High error rate — checkout: error_rate değeri 12.40, eşik 5.00. Öncelik gerekçesi: kullanıcı trafiği etkileniyor.'],
  ] as const)('%s', (_n, over, s, want) => {
    expect(alertWhatText({ ...base, ...over }, s)).toBe(want);
  });
});

describe('alertWhoText', () => {
  it('özne yoksa kutu yok', () => {
    expect(alertWhoText({ ...base, service: '' })).toBeNull();
  });
  it('yalnız özne', () => {
    expect(alertWhoText(base)).toBe('checkout etkileniyor.');
  });
  it('ölçülen etki + çağıranlar (3 ve fazlası) + pod + küme', () => {
    const s = std({ impact: { primary: { text: 'hata oranı %12,4 (normal %5)' }, known: true, floor: {}, callers: ['a', 'b', 'c', 'd', 'e'] } as unknown as ProblemStandard['impact'] });
    expect(alertWhoText({ ...base, pod: 'checkout-7f9', clusters: ['prod-a', 'prod-b'] }, s))
      .toBe('checkout etkileniyor (hata oranı %12,4 (normal %5)) · çağıran 5 servis: a, b, c ve 2 diğeri · pod checkout-7f9 · küme: prod-a, prod-b.');
  });
  it('etki bilinmiyorsa ölçü yazılmaz; pod servisle aynıysa yazılmaz', () => {
    const s = std({ impact: { primary: { text: 'kullanıcı etkisi doğrulanmadı' }, known: false, floor: {} } as unknown as ProblemStandard['impact'] });
    expect(alertWhoText({ ...base, pod: 'checkout' }, s)).toBe('checkout etkileniyor.');
  });
});

describe('alertCause', () => {
  it.each([
    ['hipotez yok', undefined, null],
    ['şüpheli boş', { topSuspect: '', topScore: 0, confidence: 0.9 }, null],
    ['güven eşiğin altında (0.49)', { topSuspect: 'payments', topScore: 1, confidence: 0.49 }, null],
  ] as const)('%s → kutu yok', (_n, rc, want) => {
    expect(alertCause({ ...base, rootCause: rc })).toBe(want);
  });
  it('eşikte (0.5) başka servis', () => {
    expect(alertCause({ ...base, rootCause: { topSuspect: 'payments', topScore: 1, confidence: 0.5 } }))
      .toEqual({ text: 'En olası kaynak: payments.', confidence: '%50 güven' });
  });
  it('kendisi + deploy', () => {
    expect(alertCause({
      ...base, rootCause: { topSuspect: 'checkout', topScore: 1, confidence: 0.724 },
      recentDeploy: { version: 'v2.0.0', timeUnixNs: 1, ageSeconds: 600 },
    })).toEqual({ text: 'Sorun büyük olasılıkla checkout servisinin kendisinde. Başlangıçtan 10 dk önce v2.0.0 deploy edildi.', confidence: '%72 güven' });
  });
});

// v0.10.2475 — consumer lag Problem'i: ham ölçü satırı ("ml_group_lag value
// 17936.00, threshold 5.00") yerine manşet; "kim" topic + mesaj + başlangıç.
describe('consumer lag kutuları', () => {
  const lagP = { ruleId: 'anomaly-auto:group:a', ruleName: 'ML · anomali grubu', service: 'checkout', metric: 'ml_group_lag', value: 17936, threshold: 5 } as typeof base;
  const s = std({
    family: 'rule', kind: 'anomaly_auto',
    headline: 'checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 26 (topic orders-topic).',
    headlineMsg: { k: 'h.mlLagT', a: { svc: 'checkout', n: '5', v: '26', t: 'orders-topic' } },
    impact: { primary: { text: 'topic orders-topic: 26 mesaj geride (normalde ~5)', msg: { k: 'imp.lagT', a: { t: 'orders-topic', v: '26', n: '5' } } }, known: true, floor: {} } as unknown as ProblemStandard['impact'],
    evidence: { sentence: 'Lag etki kuralının eşiğini aştı; ölçü VictoriaMetrics\'ten (örneklenmemiş).', source: 'vm' } as ProblemStandard['evidence'],
    since: { startedAt: new Date(2026, 10, 26, 12, 35).getTime() * 1e6, durationSec: 60, ongoing: true },
    lag: { topic: 'orders-topic', observed: 26, baseline: 5 },
  });
  it('ne oluyor: manşet + kanıt, ham ölçü adı yok', () => {
    const txt = alertWhatText(lagP, s);
    expect(txt).toBe('checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 26 (topic orders-topic). Lag etki kuralının eşiğini aştı; ölçü VictoriaMetrics\'ten (örneklenmemiş).');
    expect(txt).not.toMatch(/ml_group_lag|eşik 5/);
  });
  it('kim etkileniyor: topic, mesaj ve başlangıç (TR / EN)', () => {
    expect(alertWhoText(lagP, s)).toMatch(/^checkout \(consumer\) — topic orders-topic: 26 mesaj geride \(normalde ~5\) · (26\.11 )?12:35 başladı\.$/);
    expect(alertWhoText(lagP, s, 'en')).toMatch(/^checkout \(consumer\) — topic orders-topic: 26 messages behind \(normally ~5\) · started (26\.11 )?12:35\.$/);
  });
});
