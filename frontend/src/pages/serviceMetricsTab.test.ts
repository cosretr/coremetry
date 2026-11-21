// v0.10.2436 (operatör onayı 2026-11-21) — Service sekmesi "Details" → "Metrics"
// (Elastic APM adı). Kaynak pini: URL anahtarı `details` çalışmaya devam eder
// (derin link, CoSRE hedefi, ?tab=details&#deploys, dtl-*), `?tab=metrics` aynı
// sekmeyi açar (eskiden Infrastructure'a gidiyordu); eski `?tab=details&op=X`
// Operations'a süzülü döner; Runtime Infra'dan Metrics'in ilk bölümüne taşındı
// ve lazy yüklenir; Infra'da yalnız "Runtime → Metrics" bağlantısı kaldı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const svc = readFileSync(join(__dirname, 'Service.tsx'), 'utf8');
const infra = readFileSync(join(__dirname, 'service/ServiceInfraTab.tsx'), 'utf8');

describe('Service Metrics sekmesi (v0.10.2436)', () => {
  it('etiket Metrics, anahtar details; ?tab=metrics de details', () => {
    expect(svc).toContain("{ key: 'details',    label: 'Metrics' }");
    expect(svc).not.toContain("label: 'Details'");
    expect(svc).toContain("(tabParam === 'details' || tabParam === 'metrics') ? 'details'");
    expect(svc).toContain("(tabParam === 'infra' || tabParam === 'pods') ? 'infra'");
  });
  it('eski ?tab=details&op= → Operations (replace), kapsam şeridi yok', () => {
    expect(svc).toMatch(/if \(tab !== 'details' \|\| !opParam\) return;[\s\S]*?p\.set\('tab', 'operations'\);[\s\S]*?\{ replace: true \}/);
    expect(svc).not.toContain('clearOpScope');
    expect(svc).not.toContain('Latency ✓');
  });
  it('Runtime Metrics sekmesinde lazy; Infra yalnız bağlantı', () => {
    expect(svc).toContain("const ServiceRuntimeSection = lazy(() => import('./service/ServiceRuntimeSection'));");
    expect(svc).toMatch(/<ServiceRuntimeSection service=\{svc\} range=\{range\}/);
    expect(infra).not.toContain('<ServiceRuntimeSection');
    expect(infra).toContain('Runtime → Metrics');
    expect(infra).toContain("p.set('tab', 'details')");
  });
});
