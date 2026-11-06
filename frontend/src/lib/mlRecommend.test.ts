// v0.10.1886 — önerilen ML kurulumu görünümü (saf).
import { describe, expect, it } from 'vitest';
import { joinTr, mlRecommendView } from './mlRecommend';
import type { MLConfig, MLRecommended } from './types';

const cfg = {} as MLConfig;
const label = (d: string) => ({ op_latency: 'Endpoint gecikmesi', metric_kafka: 'Kafka', metric_jvm: 'JVM' }[d] ?? d);

describe('joinTr', () => {
  it('Türkçe birleştirir', () => {
    expect(joinTr([])).toBe('');
    expect(joinTr(['A'])).toBe('A');
    expect(joinTr(['A', 'B'])).toBe('A ve B');
    expect(joinTr(['A', 'B', 'C'])).toBe('A, B ve C');
  });
});

describe('mlRecommendView', () => {
  const base: MLRecommended = {
    vmConfigured: true, config: cfg,
    checks: [
      { id: 'http_server', label: 'HTTP sunucu', checked: true, present: true },
      { id: 'metric_jvm', label: 'JVM', checked: true, present: false },
      { id: 'db_client', label: 'Veritabanı client', checked: false, present: false, note: 'ölçülemedi: vm 503' },
    ],
    changes: [
      { key: 'enabled', from: false, to: true, reason: '' },
      { key: 'op_latency', from: false, to: true, reason: '' },
      { key: 'metric_kafka', from: false, to: true, reason: '' },
      { key: 'metric_jvm', from: true, to: false, reason: '' },
    ],
  };
  it('kaynakları ve değişiklikleri düz dille ayırır', () => {
    const v = mlRecommendView(base, label);
    expect(v.present).toEqual(['HTTP sunucu']);
    expect(v.absent).toEqual(['JVM']);
    expect(v.unknown).toEqual(['Veritabanı client']); // ölçülemeyen "yok" sayılmaz
    expect(v.turnOn).toEqual(['Endpoint gecikmesi', 'Kafka']);
    expect(v.turnOff).toEqual(['JVM']);
    expect(v.summary).toBe('Önerilen kurulum 2 dedektörü açar, 1 dedektörü kapatır ve ML ana anahtarını açar.');
    expect(v.canApply).toBe(true);
  });
  it('fark yoksa uygulanacak şey yok', () => {
    const v = mlRecommendView({ ...base, changes: [] }, label);
    expect(v.summary).toBe('Bugünkü ayar önerilen kurulumla aynı; değişiklik gerekmiyor.');
    expect(v.canApply).toBe(false);
  });
  it('VM bağlı değilken öneri yok', () => {
    const v = mlRecommendView({ ...base, vmConfigured: false, changes: [] }, label);
    expect(v.summary).toMatch(/VictoriaMetrics bağlı değil/);
    expect(v.canApply).toBe(false);
  });
});
