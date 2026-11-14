import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.697 — operatör: "buradaki sparkline'lar kullanışsız" (prod ekran
// görüntüsü). Trend hücresi üç mikro grafik yerine TEK geniş TrendSpark;
// diğer kolonlar ve sıralama aynen (klasik tablo kuralı, mockup onaylı).
const src = readFileSync(resolve(__dirname, 'OperationsTable.tsx'), 'utf8');

describe('Operations trend hücresi (v0.10.697)', () => {
  it('tek TrendSpark; üç ayrı Sparkline düğmesi yok', () => {
    expect(src).toContain('<TrendSpark calls={op.sparkline ?? []} errors={op.errorsSparkline ?? []} p99={op.p99Sparkline ?? []}');
    expect(src).not.toContain('color={TREND_C.errors} width='); // eski errors mikro grafiği
    expect(src).not.toContain('SPARK_W');
    expect(src).not.toContain("setOpFocus('errors'); setOpDetail(op);");
  });
  // v0.10.1179 — trend kolonu artanı emiyordu (taban 200). v0.10.2153 (operatör: "çok yer
  // kaplıyor") — Trend sabit 220 px, artanı Operation emer; grafik yine kolon genişliğinde.
  it('trend kolonu sabit 220, artanı Operation emer, grafik kolon genişliğinde', () => {
    expect(src).toContain("{ id: 'trend',     label: 'Trend',     width: 220, minWidth: 160 }");
    expect(src).toMatch(/id: 'name',[^\n]*flex: true/);
    expect(src).toContain('width={trendW}');
  });
  it('v0.10.2153 — kolon sırası: Operation · Trend · Impact · Calls · Avg · Err % · P50 · P99 (P95 yok)', () => {
    const ids = [...src.matchAll(/^  \{ id: '(\w+)',\s+label:/gm)].map(m => m[1]);
    expect(ids).toEqual(['name', 'trend', 'impact', 'spanCount', 'avg', 'errorRate', 'p50', 'p99']);
    expect(src).not.toContain('op.p95DurationMs');
  });
});
