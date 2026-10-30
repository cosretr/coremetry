// criticalPath.parity.test.ts — v0.10.1625 (CoSRE kritik yol planı R1): Go ile
// AYNI fikstürler. testdata/critpath/*.json'u Go'nun
// internal/tracechain/critpath_parity_test.go'su da okur; beklenti elle
// yazılır (üretici yok — beklentinin kendisi sözleşmedir). "Go ikizi aynı
// kural" artık bir yorum değil: iki uygulamadan biri kayarsa o taraf kırmızı.
// tied-roots: aynı süreli köklerde EN ERKEN başlayan kazanır (Go kuralı).
import { describe, it, expect } from 'vitest';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { computeCriticalPath, type CriticalPathSpan } from './criticalPath';

const ROOT = resolve(__dirname, '../../..');
const DIR = join(ROOT, 'testdata', 'critpath');

interface FixtureSpan { spanId: string; parentId: string; startNs: number; durNs: number; name?: string; service?: string }
interface Fixture {
  name: string;
  spans: FixtureSpan[];
  expect: { order: string[]; rootWallNs: number; onPathSelfNs: Record<string, number> };
}

it('fikstür dizini bulunur (taşınan test yüksek sesle kırılsın)', () => {
  expect(existsSync(join(ROOT, 'go.mod'))).toBe(true);
  expect(readdirSync(DIR).filter(f => f.endsWith('.json')).length).toBeGreaterThanOrEqual(6);
});

const FILES = existsSync(DIR) ? readdirSync(DIR).filter(f => f.endsWith('.json')).sort() : [];

describe.each(FILES)('%s', file => {
  const fx = JSON.parse(readFileSync(join(DIR, file), 'utf8')) as Fixture;
  const spans: CriticalPathSpan[] = fx.spans.map(s => ({
    spanId: s.spanId, parentId: s.parentId, startTime: s.startNs, duration: s.durNs, name: s.name, serviceName: s.service,
  }));

  it('sıra, kök duvar saati ve yol-üstü öz süre Go ile aynı', () => {
    const cp = computeCriticalPath(spans);
    expect(cp.order).toEqual(fx.expect.order);
    expect(cp.rootWallNs).toBe(fx.expect.rootWallNs);
    expect(Object.fromEntries(cp.onPathSelfNs)).toEqual(fx.expect.onPathSelfNs);
  });
});
