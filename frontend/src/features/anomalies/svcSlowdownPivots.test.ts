// svcSlowdownPivots.test.ts — v0.10.1091 (operatör: "Dün söylediğim CRM sorunu
// yine oldu, bir sürü anomali geldi ama P1 problem gelmedi"). Yaygın yavaşlama
// kural id öneki ↔ Go ikizi, Problem detayının pivotları (servis sayfası,
// Operations sekmesi, ≥ eşik yavaş trace'ler) ve detayın onları gerçekten
// çizdiği (kaynak pini).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { isSvcSlowdownRule, SVC_SLOWDOWN_DEFAULT_MIN_P99_MS, SVC_SLOWDOWN_RULE_PREFIX, svcSlowdownPivots } from './svcSlowdownPivots';

const GO = (rel: string) => readFileSync(resolve(__dirname, '../../../../', rel), 'utf8');
const HOUR = 3600e9;
const T0 = 1_791_015_960 * 1e9;
const win = { fromNs: T0 - HOUR, toNs: T0 + 10 * 60e9 };
const prob = { ruleId: 'svc-slowdown:crm-svc', service: 'crm-svc', metric: 'p99_ms', threshold: 5000 };

describe('svc-slowdown kural id öneki', () => {
  it('Go sabitiyle aynı; varsayılan taban Go varsayılanıyla aynı', () => {
    expect(SVC_SLOWDOWN_RULE_PREFIX).toBe('svc-slowdown:');
    expect(GO('internal/chstore/problem.go')).toContain(`RuleSvcSlowdownPrefix = "${SVC_SLOWDOWN_RULE_PREFIX}"`);
    expect(GO('internal/chstore/anomaly_sensitivity_slowdown.go')).toContain(`svcSlowMinP99Def = 500.0, 600000.0, ${SVC_SLOWDOWN_DEFAULT_MIN_P99_MS}.0`);
  });
  it.each([
    ['svc-slowdown:crm-svc', true],
    ['svc-slowdown:', false],
    ['db-health:oracle@h/d', false],
    ['anomaly:crm-svc:p99_ms', false],
    ['', false],
  ])('%s → %s', (id, want) => { expect(isSvcSlowdownRule(id)).toBe(want); });
  it('null / undefined güvenli', () => {
    expect(isSvcSlowdownRule(undefined)).toBe(false);
    expect(isSvcSlowdownRule(null)).toBe(false);
  });
});

describe('svcSlowdownPivots', () => {
  // Problem threshold = en yavaş operasyonun kendi tabanı (120 ms) — süzgeç
  // ona BAKMAZ; ayardaki minP99Ms (sunucu dizi cevabının threshold'u) kullanılır.
  const p120 = { ...prob, threshold: 120 };
  it('servis sayfası + Operations sekmesi + ≥ minP99Ms yavaş trace, hepsi problem penceresiyle', () => {
    const p = svcSlowdownPivots(p120, win, 5000)!;
    expect(p).not.toBeNull();
    expect(p.serviceHref).toMatch(/^\/service\?name=crm-svc&range=custom%3A\d+-\d+$/);
    expect(p.operationsHref).toMatch(/^\/service\?name=crm-svc&tab=operations&range=custom%3A\d+-\d+$/);
    expect(p.slowTracesHref).toMatch(/^\/traces\?service=crm-svc&minMs=5000&rootOnly=false&range=custom%3A\d+-\d+$/);
    expect(p.slowMinMs).toBe(5000);
  });
  it('ayar değişince süzgeç onu izler; problemin threshold\'u (120 ms) asla kullanılmaz', () => {
    expect(svcSlowdownPivots(p120, win, 8000)!.slowMinMs).toBe(8000);
    const p300 = { ...prob, threshold: 300 }; // çöküş kolu: servis tabanı
    expect(svcSlowdownPivots(p300, win, 5000)!.slowMinMs).toBe(5000);
  });
  it('ayar henüz gelmediyse / geçersizse varsayılan 5000 ms', () => {
    expect(svcSlowdownPivots(p120, win)!.slowMinMs).toBe(SVC_SLOWDOWN_DEFAULT_MIN_P99_MS);
    expect(svcSlowdownPivots(p120, win, Number.NaN)!.slowMinMs).toBe(SVC_SLOWDOWN_DEFAULT_MIN_P99_MS);
    expect(svcSlowdownPivots(p120, win, 0)!.slowMinMs).toBe(SVC_SLOWDOWN_DEFAULT_MIN_P99_MS);
  });
  it('başka tür / servissiz → null', () => {
    expect(svcSlowdownPivots({ ...prob, ruleId: 'builtin-http-p99-5s' }, win, 5000)).toBeNull();
    expect(svcSlowdownPivots({ ...prob, service: '' }, win, 5000)).toBeNull();
  });
  it('Problem detayı pivotları sunucu eşiğiyle çizer, genel "Slow traces" linkini tekrar etmez', () => {
    const src = readFileSync(resolve(__dirname, 'ProblemDetail.tsx'), 'utf8');
    expect(src).toContain('const svcSlowLinks = svcSlowdownPivots(problem, probWindow, svcSlowSeries.data?.threshold);');
    expect(src).toContain('<SignalLink to={svcSlowLinks.operationsHref}');
    expect(src).toContain('<SignalLink to={svcSlowLinks.slowTracesHref}');
    expect(src).toContain('if (thr === null || svcSlowLinks) return null;');
  });
});
