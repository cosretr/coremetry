// MLTab — v0.10.1874: liste "Seyir" + "Servis tipi" sütunları (mockup
// görünüm 1). Seyir sunucudan (satır başına istek yok — liste ucu tek toplu
// okuma); bu dosya kaynakta satır başına bir ML isteği açılmadığını da çiviler.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { MLEvent } from '@/lib/types';
import { sparkPeak } from './MLTab';

const base: MLEvent = {
  id: 'e1', detector: 'error_rate', signal: 'log', stage: 'shadow', service: 'orders-api',
  startedAt: 0, lastSeen: 0, windowSec: 300, baseline: 1, observed: 2, score: 5, direction: 'up',
};

describe('sparkPeak', () => {
  it('tepe değer; seyir yoksa -1', () => {
    expect(sparkPeak({ ...base, spark: { v: [1, 9, 4], a: 1 } })).toBe(9);
    expect(sparkPeak(base)).toBe(-1);
    expect(sparkPeak({ ...base, spark: { v: [], a: 0 } })).toBe(-1);
  });
});

describe('MLTab sütunları', () => {
  const src = readFileSync(resolve(__dirname, 'MLTab.tsx'), 'utf8');
  // v0.10.2021 — Seyir / Servis tipi sütunları liste satırı = grup düzeninde
  // kalktı (mockup anomaly-unified.html; sıra MLTab.group.test.ts'de çivili).
  it('satır başına istek yok: seyir satırın kendi alanından', () => {
    expect(src).toContain('e.spark');
    expect(src).not.toMatch(/api\.mlReplay\(/);
  });
});
