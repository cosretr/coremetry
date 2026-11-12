// v0.10.2041 — Kafka consumer lag ML olayının etki tabanı Messaging "Genel
// bakış eşiği" ile TEK sayı: Go sabiti (chstore.MLKafkaLagImpactFloor) ile bu
// dosyadaki `max_lag` tonunun sarı eşiği (ge(1000, 10000)) ayrışmasın.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('Kafka lag tabanı tek kaynak', () => {
  it('Go etki tabanı = Messaging sarı eşiği', () => {
    const go = readFileSync(resolve(__dirname, '../../../internal/chstore/ml_settings.go'), 'utf8');
    const m = go.match(/const MLKafkaLagImpactFloor = (\d+)/);
    expect(m).not.toBeNull();
    const ts = readFileSync(resolve(__dirname, 'kafkaMetrics.ts'), 'utf8');
    const lag = ts.match(/id: 'max_lag'[\s\S]*?tone: ge\((\d+), (\d+)\)/);
    expect(lag).not.toBeNull();
    expect(Number(lag![1])).toBe(Number(m![1]));
    expect(ts).toContain(`${Number(m![1]).toLocaleString('tr-TR')} üstü sarı`);
  });
});
