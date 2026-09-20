// danglingMv.test.ts — v0.10.762 sarkan MV sihirbazı kablolama pinleri;
// v0.10.825 kart "MV onarımı"na genişledi (sarkan · düz · eksik).
//
// Operatör vakası 2026-09-20 (test kümesi): kanonik bir MV bir host'ta DÜZ
// iç tabloyla, öteki host'ta HİÇ YOK duruyordu — ikisi de "view var, iç
// tablo yok" olmadığı için kart "sarkan MV yok" diyordu. Pinler üç durumun
// da çizildiğini, iki ayrı onarım ucunun bağlı olduğunu ve yeniden kurma
// onayının tarihçe kaybını SÖYLEDİĞİNİ çiviler.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('MV onarımı — kablolama', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
  it('panel Trace hattı sağlığının altında; iki onarım ucu ve onay diyaloğu', () => {
    expect(page).toContain('<DanglingMVPanel />');
    expect(page.indexOf('<TraceHealthPanel />')).toBeLessThan(page.indexOf('<DanglingMVPanel />'));
    expect(page).toContain('api.chDanglingMVs()');
    // Eşten kur (iç tablo eş replikadan, aynı uuid) VE Yeniden kur (bu host'ta
    // DROP + kanonik DDL) — iki ayrı uç, tek tablo.
    // "Eşten kur" onayı sunucuya AÇIKÇA bildirilir: eş çözülemezse 409, sessiz
    // DROP + kanonik CREATE yok (v0.10.825 incelemesi).
    expect(page).toContain('api.chDanglingMVRepair(r.host, r.view, true)');
    expect(api).toContain('chDanglingMVRepair: (host: string, view: string, peer: boolean)');
    expect(api).toContain('JSON.stringify({ host, view, peer })');
    expect(page).toContain('api.chMVRebuild(r.host, r.view)');
    expect(page).toContain('title={`MV onarımı — ');
    expect(api).toContain("'/api/admin/clickhouse/dangling-mv'");
    expect(api).toContain("'/api/admin/clickhouse/dangling-mv/repair'");
    expect(api).toContain("'/api/admin/clickhouse/dangling-mv/rebuild'");
    expect(api).toContain('confirm: true');
    expect(types).toContain('export interface CHDanglingMV {');
    expect(types).toContain('export interface CHMVHostState {');
    expect(types).toContain('export interface CHMVRebuildResult {');
    expect(types).toContain("export type CHMVState = 'ok' | 'plain' | 'dangling' | 'missing';");
    expect(types).toContain('coverage?: CHMVHostState[]');
    expect(types).toContain('coverageError?: string');
  });
  it('üç durum rozetli; ok satırları tabloya girmez, boş durum sayıyı söyler', () => {
    expect(page).toContain("{ ok: 'sağlıklı', plain: 'düz', dangling: 'sarkan', missing: 'yok' }");
    expect(page).toContain("{ ok: 'b-ok', plain: 'b-warn', dangling: 'b-err', missing: 'b-err' }");
    expect(page).toContain('iç tablo Replicated değil');
    // Sağlıklı hücreler tabloya GİRMEZ (kapsama ok satırlarını da döner).
    expect(page).toContain("if (c.state === 'ok') continue;");
    // Aynı MV iki kez çizilmez: sarkan liste yalnız kapsamada OLMAYAN satırı ekler.
    expect(page).toContain('if (seen.has(`${d.host}/${d.view}`)) continue;');
    // Yeşil rozet ÖLÇÜLMÜŞ kapsama ister: kapsama yoksa/hatalıysa "0 MV × 0 host"
    // diye sağlıklı denmez; hata b-err rozetiyle mesajıyla birlikte görünür.
    // v0.10.830 — kapı büyüdü: artık (kalıntı/öksüz) ÖLÇÜLMÜŞ ve BOŞ olmalı.
    expect(page).toContain('{coverage && !coverageError && leftovers && !leftoverError && repairRows.length === 0 && leftoverRows.length === 0 && (');
    expect(page).toContain('MV&apos;ler sağlıklı · {mvCount} MV × {hostCount} host');
    expect(page).toContain('<span className="badge b-err" title={coverageError}>kapsama ölçülemedi: {coverageError}</span>');
    expect(page).not.toContain('const scanned =');
  });
  it('yeniden kurma onayı tarihçe kaybını söyler', () => {
    expect(page).toContain('MV tarihçesi bu host&apos;ta sıfırlanır; yalnız yeni yazımlarla dolar. Audit&apos;e düşer.');
    expect(page).toContain('SYNC</code> + kanonik DDL (ON CLUSTER&apos;sız, Replicated)');
  });
});
