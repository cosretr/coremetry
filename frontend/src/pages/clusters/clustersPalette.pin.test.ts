// clustersPalette.pin.test.ts — v0.10.922 (sade palet adım 1). Kaynak pini.
//
// K5: sağlıklı/normal durum NÖTR — Clusters sayfasında geçiş (resolved/
// recovered) yok, dolayısıyla hiç yeşil (b-ok / var(--ok)) kalmamalı.
// Kategoriler (rol, süzgeç çipleri) b-info değil b-gray. "Live" noktası bir
// etkinlik göstergesi: nabız kalır, renk --text3. Sapma renkleri (b-err/
// b-warn, CPU/Mem >85 --err) KALIR — kelimeler de (healthy/reachable).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, '../Clusters.tsx'), 'utf8');

describe('Clusters — sade palet adım 1', () => {
  it('normal durum için yeşil yok (b-ok / var(--ok))', () => {
    expect(src).not.toMatch(/\bb-ok\b/);
    expect(src).not.toContain('var(--ok)');
  });
  it('kategoriler nötr: b-info yok, rol ve süzgeç çipleri b-gray', () => {
    expect(src).not.toMatch(/\bb-info\b/);
    expect(src).toContain('<span className="badge b-gray">{r.role}</span>');
    expect(src.match(/className="badge b-gray" style=\{\{ cursor: 'pointer' \}\}/g)?.length).toBe(3);
  });
  it('healthy/reachable kelimeleri --text3 metin olarak kalır', () => {
    expect(src).toContain(`<span style={{ fontSize: 11, color: 'var(--text3)' }}>reachable</span>`);
    expect(src.match(/color: 'var\(--text3\)' \}\}>healthy<\/span>/g)?.length).toBe(2);
  });
  it('sapma renkleri korunur (unreachable/failing b-err, >85 --err)', () => {
    expect(src).toContain('<span className="badge b-err">unreachable</span>');
    expect(src).toContain('failing</span>');
    // v0.10.943 (tablo standardı dilim 3) — eşik rengi satır içi üçlüden kolon
    // tonuna taşındı (`pctTone` → .cell-err/.cell-warn/.cell-faint); eşikler aynı.
    expect(src).toContain("(p ?? 0) > 85 ? 'err' : (p ?? 0) > 60 ? 'warn' : 'faint'");
    expect(src.match(/tone: r => pctTone\(r\.(cpuPct|memPct)\)/g)?.length).toBe(4);
  });
  it('Live noktası nabız atar ama renk nötr', () => {
    expect(src).toContain("leftIcon={<span className={live ? 'pulse-dot' : ''}");
    expect(src).toMatch(/borderRadius: '50%',\s*background: 'var\(--text3\)',/);
  });
});
