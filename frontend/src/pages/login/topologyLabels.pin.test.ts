// v0.10.1926 (operatör-raporlu: login arka planında birden fazla "oracle" /
// "queue" yazıyor) — db/kuyruk düğümlerinin adı yalnız sistem adıdır
// (sunucu: loginsummary.Project); sabit etiket alırlarsa aynı ad onlarca kez
// tekrar eder. Etiket uygunluğu veri katmanını dışlamalı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('login topoloji etiketleri', () => {
  it('veri katmanı (db/kuyruk) sabit etiket almaz', () => {
    const src = readFileSync(resolve(__dirname, 'TopologyBackdrop.tsx'), 'utf8');
    expect(src).toMatch(/labelNodes\(layout, MAX_LABELS, Math\.random, i => depth\[i\] === 0 && layout\.nodes\[i\]\.tier !== 'db'\)/);
  });
});
