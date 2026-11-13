// signalKind.test.ts — v0.10.1844 (öğrenme döngüsü Faz 2 dilim 2.4):
// dedektör → tür eşlemesi TEK yerde ve sunucu sözlüğünün tamamını kapsar;
// her türün etiketi + ikonu + renk sınıfı var ve sınıf token'ın kendisini
// kullanır (renk tek taşıyıcı değil; sınıf/tokens ayrışmaz).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { SIGNAL_KINDS, signalKindOf, type SignalKind } from './signalKind';
import { ML_DETECTOR_TR } from '@/components/coremetry/anomalies/MLTab';
import { SIGNAL_KIND_CLASS } from '@/components/coremetry/signalKindClass';

describe('signalKind', () => {
  it('every server detector maps to its signal', () => {
    const want: Record<string, SignalKind> = {
      log_volume: 'log', error_rate: 'log', service_silent: 'log', template_spike: 'log', rare_template: 'log', rare_error_code: 'log',
      pod_outlier: 'log', change_point: 'log', new_template: 'log', op_latency: 'trace', op_error_rate: 'trace', throughput_drop: 'trace',
      dependency_latency: 'trace', trace_pod_outlier: 'trace', trace_change_point: 'trace', trace_error_count: 'trace', dependency_error_rate: 'trace', svc_error_rate: 'trace', svc_breadth: 'trace', svc_volume_spike: 'trace',
      metric_jvm: 'metric', metric_pool: 'metric', metric_kafka: 'metric', metric_http_client: 'metric', metric_k8s: 'metric',
    };
    expect(Object.keys(want).sort()).toEqual(Object.keys(ML_DETECTOR_TR).sort());
    for (const [d, k] of Object.entries(want)) expect(signalKindOf(d), d).toBe(k);
  });
  it('unknown detector falls back to server signal, then log', () => {
    expect(signalKindOf('new_thing', 'trace')).toBe('trace');
    expect(signalKindOf('new_thing', 'metric')).toBe('metric');
    expect(signalKindOf('new_thing')).toBe('log');
  });
  it('each kind has label, icon and a class built from its token (class lives under a Tailwind @source dir)', () => {
    for (const [k, s] of Object.entries(SIGNAL_KINDS)) {
      expect(s.label, k).not.toBe('');
      expect(s.icon, k).toBeTruthy();
      expect(SIGNAL_KIND_CLASS[k as SignalKind], k).toBe(`text-[color:${s.token}]`);
    }
    // Tailwind yalnız @source dizinlerini tarar: sınıf haritası taranan dizinde.
    expect(readFileSync(resolve(__dirname, '../styles/index.css'), 'utf8')).toContain("@source '../components/coremetry';");
    expect(SIGNAL_KINDS.metric.token).toBe('var(--sig-metric)');
    expect(SIGNAL_KINDS.trace.token).toBe('var(--sig-trace)');
    expect(SIGNAL_KINDS.log.token).toBe('var(--sig-log)');
  });
});
