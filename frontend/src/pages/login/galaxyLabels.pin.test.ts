// v0.10.1933 (operatörün mockup'ta seçtiği öneri: "etiket çerçevesi"): galaksi
// etiketleri zemin renginde çerçeveyle çizilir. Öneri 1 (merkez kartın dışında)
// REDDEDİLDİ — galaksi ortada kalır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';

const src = readFileSync(resolve(__dirname, 'TopologyBackdrop.tsx'), 'utf8');

describe('login galaksisi etiketleri', () => {
  it('etiket zemin renginde çerçeveyle çizilir', () => {
    expect(src).toMatch(/strokeStyle = col\.bg/);
    expect(src).toContain('ctx.strokeText(text, x, y)');
  });
  it('galaksi merkezi ekranın ortası (kart dışına kaydırma yok)', () => {
    expect(src).toContain('cx: w / 2, cy: h / 2');
  });
  // v0.10.1935 — üzerine gelince tür satırı.
  it('tür adları iki dilde tanımlı ve hover çiziminde kullanılır', () => {
    for (const k of ['gw', 'bff', 'svc', 'db', 'queue']) {
      const key = `login.topo.kind.${k}`;
      expect(t(key, 'en'), key).not.toBe(key);
      expect(t(key, 'tr'), key).not.toBe(key);
    }
    expect(t('login.topo.kind.db', 'tr')).toBe('veritabanı');
    expect(t('login.topo.kind.queue', 'tr')).toBe('kuyruk');
    expect(src).toContain('const kind = f.kinds[nd.tier]');
  });
  // v0.10.1938 (operatör: "açılış efekti olmasın, direkt açılsın") — 1936'nın açılış hareketi söküldü.
  it('açılış hareketi yok: galaksi doğrudan yerinde açılır', () => {
    expect(src).not.toMatch(/introProgress|intro:/);
  });
});
