// grafanaImport.test — v0.10.2002 (Dashboards Faz 3 dilim 3.9) GOLDEN.
//
// Fikstür testdata/grafana/sample-dashboard.json (nötr com.acme): alınan /
// atlanan her panel ve değişken nedeniyle, golden dosyayla birebir
// karşılaştırılır. Golden'ı güncellemek: UPDATE_GOLDEN=1 TZ=UTC npx vitest run
// src/lib/grafanaImport.test.ts — farkı gözle incele, sonra commit et.
import { describe, it, expect } from 'vitest';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { importGrafanaDashboard, normalizeExpr } from './grafanaImport';

const dir = resolve(__dirname, 'testdata/grafana');
const fixture = readFileSync(resolve(dir, 'sample-dashboard.json'), 'utf8');
const goldenPath = resolve(dir, 'sample-dashboard.golden.json');

describe('importGrafanaDashboard — golden', () => {
  it('pano + kuru çalıştırma raporu golden ile aynı', () => {
    const got = JSON.stringify(importGrafanaDashboard(fixture), null, 2) + '\n';
    if (process.env.UPDATE_GOLDEN === '1') writeFileSync(goldenPath, got);
    expect(got).toBe(readFileSync(goldenPath, 'utf8'));
  });

  it('atlanan her panelin nedeni var; alınan + atlanan = kaynaktaki panel sayısı', () => {
    const { report } = importGrafanaDashboard(fixture);
    for (const s of [...report.skipped, ...report.skippedVariables]) expect(s.reason.length).toBeGreaterThan(3);
    // 9 üst düzey (satır dahil) + satırın içindeki 2 = 11.
    expect(report.panels.length + report.skipped.length).toBe(11);
  });
});

describe('normalizeExpr', () => {
  it('Grafana değişken ve aralık sözdizimi', () => {
    const notes: string[] = [];
    expect(normalizeExpr('rate(x{a="$svc",b="[[env]]",c=~"${db:regex}"}[$__rate_interval])', notes))
      .toBe('rate(x{a="${svc}",b="${env}",c=~"${db}"}[5m])');
    expect(notes.length).toBe(1);
  });
});

describe('hatalar', () => {
  it('JSON değil / panelsiz / eski şema okunur mesaj verir', () => {
    expect(() => importGrafanaDashboard('{')).toThrow(/JSON/);
    expect(() => importGrafanaDashboard('{"title":"x"}')).toThrow(/panels/);
    expect(() => importGrafanaDashboard('{"rows":[]}')).toThrow(/rows/);
  });
});
