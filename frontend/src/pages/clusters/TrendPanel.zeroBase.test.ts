import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.2479 — operatör-bildirimli (prod pod sayfası): "Memory grafiğinde çok
// az yükselmiş olmasına rağmen çok fazla yükselmiş gibi gözüküyor."
//
// CPU / bellek trend panelleri y eksenini veriye oturtuyordu: ≈ 3 MiB'lik
// (%0,1) bir artış paneli boydan boya dolduran bir sıçrama gibi çiziliyordu.
// Kaynak panelleri miktardır ve sıfırdan çizilir (CorePanel.zeroBase,
// v0.10.916). Bu test iki grafiğin de zeroBase taşıdığını çiviler; biri
// düşerse aynı yanıltıcı görüntü geri gelir.
const src = readFileSync(resolve(__dirname, './TrendPanel.tsx'), 'utf8');

describe('ThanosTrendPanel y tabanı', () => {
  it('CPU ve Memory grafikleri sıfırdan çizilir', () => {
    const charts = src.match(/<MultiLineChart[^>]*>/g) ?? [];
    expect(charts.length).toBe(2);
    for (const c of charts) expect(c).toMatch(/\bzeroBase\b/);
  });
});
