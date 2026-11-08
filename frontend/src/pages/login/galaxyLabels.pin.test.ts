// v0.10.1933 (operatörün mockup'ta seçtiği öneri: "etiket çerçevesi"): galaksi
// etiketleri zemin renginde çerçeveyle çizilir. Öneri 1 (merkez kartın dışında)
// REDDEDİLDİ — galaksi ortada kalır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'TopologyBackdrop.tsx'), 'utf8');

describe('login galaksisi etiketleri', () => {
  it('etiket zemin renginde çerçeveyle çizilir', () => {
    expect(src).toMatch(/strokeStyle = col\.bg/);
    expect(src).toContain('ctx.strokeText(text, x, y)');
  });
  it('galaksi merkezi ekranın ortası (kart dışına kaydırma yok)', () => {
    expect(src).toContain('cx: w / 2, cy: h / 2');
  });
  // v0.10.1938 (operatör: "açılış efekti olmasın, direkt açılsın") — 1936'nın açılış hareketi söküldü.
  it('açılış hareketi yok: galaksi doğrudan yerinde açılır', () => {
    expect(src).not.toMatch(/introProgress|intro:/);
  });
});
