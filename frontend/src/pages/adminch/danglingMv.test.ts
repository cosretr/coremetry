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
    // v0.10.833 — hedef uuid de ÖLÇÜLMÜŞ ve bulgusuz olmalı: kapsaması `ok`
    // olan bir hücre hedefini bulamıyor olabilir ve eski koşul onu yeşile boyardı.
    expect(page).toContain('{coverage && !coverageError && !targetError && leftovers && !leftoverError && repairRows.length === 0 && leftoverRows.length === 0 &&');
    expect(page).toContain('findings.length === 0 && (');
    expect(page).toContain('MV&apos;ler sağlıklı · {mvCount} MV × {hostCount} host');
    expect(page).toContain('<span className="badge b-err" title={coverageError}>kapsama ölçülemedi: {coverageError}</span>');
    expect(page).not.toContain('const scanned =');
  });
  // v0.10.833 — hedef uuid'nin İKİ yönü: "ad doğru, nesne uuid'si yanlış"
  // (kapsama `ok` der) ve "nesne uuid'si doğru, ad beklenen değil" (kapsama
  // `dangling` der ve CANLI bir tabloya iki danger düğme doğrultur).
  // Uyuşmazlığın SONUCU ölçülür: gerçek CH 24.8'de MV toplamaya devam
  // edebiliyor. Bu sürüm YALNIZ saptar.
  it('hedef uuid bulgusu rozet + katlanır listede, satırda DÜĞME YOK', () => {
    expect(types).toContain("export type CHMVTarget = 'ok' | 'mismatch' | 'byname' | 'unmeasured';");
    expect(types).toContain('target: CHMVTarget;');
    expect(types).toContain('targetResolves?: boolean;');
    expect(types).toContain('targetError?: string');
    // Üç sınıf ve TONLARI: çözülmeyen kırmızı, çözülen sarı (veri akıyor).
    expect(page).toContain("if (c.targetResolves === true) return 'live';");
    expect(page).toContain("if (c.target === 'mismatch') return c.targetResolves === false ? 'broken' : 'unknown';");
    expect(page).toContain("const MV_FINDING_TONE: Record<MVTargetFindingKind, string> = { broken: 'b-err', live: 'b-warn', unknown: 'b-warn' };");
    expect(page).toContain('MV hedefini bulamıyor — ingest bu host&apos;ta düşüyor');
    expect(page).toContain('MV başka adlı nesneye yazıyor (veri akıyor)');
    expect(page).toContain('hedef uuid ölçülemedi: {targetUnknown.length} hücre');
    // A — hedefini ÇÖZEN satır onarım tablosuna GİRMEZ: iki danger düğme
    // (DROP … SYNC / Öksüzü temizle) CANLI veriye doğrultulmaz.
    expect(page).toContain("if (c.targetResolves === true) { seen.add(`${c.host}/${c.view}`); continue; }");
    // Bulgu listesinde düğme yoktur.
    const details = page.slice(page.indexOf('Hedef uuid bulguları'), page.indexOf('{leftoverConfirm && ('));
    expect(details).not.toContain('<Button');
    // Depo kuralı: bu tablo da useDataTable (sıralanabilir + genişletilebilir).
    expect(page).toContain("storageKey: 'ch-mv-target-findings'");
    expect(details).toContain('<DataTableColgroup dt={findingDt} />');
    // C — runbook ŞEKLE göre dallanır; çözülen dalda CREATE … UUID YOK,
    // çözülmeyen dalda kod 57 uyarısı VAR.
    const live = details.slice(details.indexOf('Hedefi ÇÖZÜLEN'), details.indexOf('Hedefi ÇÖZÜLMEYEN'));
    expect(live).toContain('Yeni tablo KURMA');
    expect(live).not.toContain('INSERT INTO `.inner_id');
    const broken = details.slice(details.indexOf('Hedefi ÇÖZÜLMEYEN'));
    expect(broken).toContain("UUID &apos;{'<TO INNER UUID>'}&apos;");
    expect(broken).toContain('RENAME TABLE');
    expect(broken).toContain('Kod 57 (TABLE_ALREADY_EXISTS) alırsan DUR');
    // E — 409 onay modalından SONRA gelmesin: düğme baştan kapalı.
    expect(page).toContain('const stopped = l.blocked || leftoverBlockedBy(l);');
    expect(page).toContain('{stopped\n                      ? <span className="badge b-warn" title={stopped}>elle</span>');
    // Yıkıcı eylem kapısı exhaustive: yeni bir CHMVState değeri derleyiciyi durdurur.
    expect(page).toContain("const MV_STATE_ACTION: Record<CHMVState, 'rebuild' | 'manual'> = {");
    expect(page).toContain("{fromPeer || (r.canonical && MV_STATE_ACTION[r.state] === 'rebuild')");
  });
  it('yeniden kurma onayı tarihçe kaybını söyler', () => {
    expect(page).toContain('MV tarihçesi bu host&apos;ta sıfırlanır; yalnız yeni yazımlarla dolar. Audit&apos;e düşer.');
    expect(page).toContain('SYNC</code> + kanonik DDL (ON CLUSTER&apos;sız, Replicated)');
  });
});
