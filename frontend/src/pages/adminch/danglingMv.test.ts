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
    // v0.10.835 — kapsama artık `dangling` DIŞINDAKİ hücrelerde de eş adayı
    // taşıyor (hedef uuid onarımı adı VAR olan bir hücrede koşar). "Eşten
    // kur" düğmesinin kapısı bundan ETKİLENMEMELİ: hâlâ yalnız sarkan satır.
    expect(page).toContain("const peerable = (r: MVRepairRow) => r.state === 'dangling' && !!r.peerHost && !peerRefused.has(r.key);");
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
    // v0.10.835 — bulgu listesinde ARTIK tek bir düğme var ve YALNIZ şekil-1
    // satırında çizilir; `live` (MV topluyor) ve `unknown` (ölçülmedi)
    // satırları düğmesiz kalır. v0.10.833'ün "hiç düğme yok" pini bu sürümde
    // BİLEREK daraltıldı — 833 saptıyordu, 835 onarıyor.
    const details = page.slice(page.indexOf('Hedef uuid bulguları'), page.indexOf('{targetConfirm && ('));
    expect(details.match(/<Button/g) ?? []).toHaveLength(1);
    // SATIR BAŞINA TEK YIKICI EYLEM: kapsaması `ok` OLMAYAN hücre (örn.
    // `plain` + `mismatch`) üstteki tabloda ZATEN "Yeniden kur" taşıyor.
    expect(details).toContain("{f.kind === 'broken' && f.row.state === 'ok'");
    expect(details).toContain('>Hedefi onar</Button>');
    expect(details).toContain('üstteki tabloda');
    expect(details).toContain('eylem yok');
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
  // v0.10.835 — ONARIM: şekil-1 (targetResolves === false) satırında tek
  // düğme; uç, onay penceresi ve iki dalın sözü kablolanmış.
  it('hedef uuid onarımı: yalnız şekil-1 satırında, iki uuid + dal + tarihçe onayda', () => {
    expect(types).toContain('export interface CHMVTargetRepairResult {');
    expect(api).toContain("'/api/admin/clickhouse/mv-target/repair'");
    expect(api).toContain('chMVTargetRepair: (host: string, view: string, peer: boolean, dropEmpty: boolean)');
    expect(api).toContain('JSON.stringify({ host, view, peer, dropEmpty, confirm: true })');
    // Dal EKRANDA ne yazıyorsa sunucuya o gider (v0.10.825 duruşu): eş varsa
    // eşten, yoksa kanonik. Sunucu sözü tutamazsa 409 döner, sessizce öteki
    // dala düşmez.
    // Dal kararı sunucunun kapısıyla AYNI alandan (peerAddr): eş adı var ama
    // adresi çözülemediğinde ekran "eşten" derken sunucu reddederdi ve
    // operatör ilerleyemezdi (v0.10.825 peerRefused sınıfı).
    expect(page).toContain('const fromPeer = !!f.row.peerAddr;');
    expect(page).toContain('{targetConfirm.row.peerAddr');
    // Onay penceresi: İKİ uuid, dal, tarihçe sözü, yalnız bu host, YARIM.
    const modal = page.slice(page.indexOf('{targetConfirm && ('), page.indexOf('{leftoverConfirm && ('));
    expect(modal).toContain('{targetConfirm.row.innerUUID');
    expect(modal).toContain('{targetConfirm.row.targetUUID}');
    expect(modal).toContain('tarihçeyi eşten çeker');
    expect(modal).toContain('MV tarihçesi bu host&apos;ta SIFIRLANIR');
    expect(modal).toContain('ON CLUSTER yok');
    expect(modal).toContain('YARIM');
    // Boş adın DROP'u AYRI onay; doluluk kararını SUNUCU tazeden ÖLÇER ve
    // "boş" tek bir sorguyla kanıtlanmaz (DETACHED parça / motor ailesi /
    // başka bir MV'nin hedefi — v0.10.835 KRİTİK bulgusu).
    expect(modal).toContain('checked={targetDropEmpty}');
    expect(modal).toContain('tazeden ÖLÇER');
    expect(modal).toContain('DETACHED parça');
    expect(modal).toContain('hedefiyse');
    // Kutu YALNIZ eşten dalında anlamlı: kanonik dal o adı hiç kullanmaz.
    expect(modal).toContain('{targetConfirm.row.peerAddr && (');
    expect(page).toContain('api.chMVTargetRepair(f.row.host, f.row.view, fromPeer, fromPeer && targetDropEmpty)');
    // ÜÇ dal: "eş yok" bir OLGU, "adresi çözülemedi" bizim körlüğümüz —
    // ikincisinde tarihçesiz kurulum VARSAYILAN eylem olamaz.
    expect(modal).toContain('adresi system.clusters&apos;tan çözülemedi');
    expect(modal).toContain('checked={canonicalAck}');
    expect(page).toContain('disabled={!targetConfirm.row.peerAddr && !!targetConfirm.row.peerHost && !canonicalAck}');
    // Kanonik dalda eski ad ÖKSÜZ kalır ve v0.10.830'un listesine düşer.
    expect(modal).toContain('sahipsiz iç tablo olarak');
    // YARIM bir onarımın KOŞAN adımları 409 gövdesinden okunup ekrana düşer.
    expect(page).toContain('const d = apiErrorDetail(e);');
    expect(page).toContain('setResult({ key: k, ok: false, text: d.message, steps: d.steps });');
    expect(api).toContain('export function apiErrorDetail(');
    // Runbook artık düğmeye yönlendirir (833'te "bu sürüm onarmaz" diyordu).
    expect(page).toContain('satırdaki <i>Hedefi onar</i> düğmesi bunu yapar');
    expect(page).not.toContain('bu bilinçli: bu sürüm yalnız saptar');
  });
  it('yeniden kurma onayı tarihçe kaybını söyler', () => {
    expect(page).toContain('MV tarihçesi bu host&apos;ta sıfırlanır; yalnız yeni yazımlarla dolar. Audit&apos;e düşer.');
    expect(page).toContain('SYNC</code> + kanonik DDL (ON CLUSTER&apos;sız, Replicated)');
  });
});
