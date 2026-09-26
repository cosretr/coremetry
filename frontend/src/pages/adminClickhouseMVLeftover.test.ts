// adminClickhouseMVLeftover.test.ts — v0.10.830 "MV artığı" kart + istemci
// pinleri ve replika kartının host'a duyarlı etiketi.
//
// Sözleşme: v0.10.825 sihirbazı "MV'ler sağlıklı · 21 MV × 4 host" derken
// Replika tutarlılığı kartı `.inner_id.<uuid>` satırlarında KALICI kırmızı
// gösteriyordu ve satırda hiç eylem yoktu. Ölçülmeyen iki sınıf vardı; MV
// onarımı kartı ikisini de sahiplenir, replika kartı SALT OKUNUR kalır.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { innerViewLabel, canRepair } from './adminch/replicaConsistency';
import type { CHReplicaShard, CHReplicaState } from '@/lib/types';

const page = readFileSync(resolve(__dirname, 'AdminClickhouse.tsx'), 'utf8');
const apiSrc = readFileSync(resolve(__dirname, '../lib/api.ts'), 'utf8');
const types = readFileSync(resolve(__dirname, '../lib/types.ts'), 'utf8');

describe('MV artığı — istemci ve tipler (v0.10.830)', () => {
  it('iki istemci ucu, confirm:true ve POST', () => {
    expect(apiSrc).toContain("chMVLeftoverDropView: (host: string, view: string)");
    expect(apiSrc).toContain("chMVLeftoverDropInner: (host: string, uuid: string)");
    expect(apiSrc).toContain("'/api/admin/clickhouse/mv-leftover/drop-view'");
    expect(apiSrc).toContain("'/api/admin/clickhouse/mv-leftover/drop-inner'");
    const i = apiSrc.indexOf('chMVLeftoverDropView');
    const block = apiSrc.slice(i, i + 900);
    expect(block).toContain('confirm: true');
    expect(block).not.toContain('inner:'); // öksüz ucu ADI değil uuid gönderir
  });
  it('tip lib/types.ts’te tek kaynak; leftovers zorunlu dizi', () => {
    expect(types).toContain("export type CHMVLeftoverKind = 'artik' | 'oksuz';");
    expect(types).toContain('export interface CHMVLeftover {');
    expect(types).toContain('leftovers: CHMVLeftover[]; leftoverError?: string }');
    // v0.10.830 inceleme: hayatta kalan tarafın boyutu ve düğmesiz satır nedeni.
    expect(types).toContain('storageInner?: string; storageRows?: number; storageBytes?: number;');
    expect(types).toContain('blocked?: string;');
  });
});

describe('MV artığı — kart (v0.10.830)', () => {
  it('iki rozet, iki danger eylem', () => {
    expect(page).toContain("artik: 'terfi öncesi çıplak MV (kalıntı)'");
    expect(page).toContain("oksuz: 'sahipsiz iç tablo (view yok)'");
    expect(page).toContain("artik: 'b-warn', oksuz: 'b-err'");
    expect(page).toContain('Kalıntıyı düşür');
    expect(page).toContain('Öksüzü temizle');
  });
  it('eylemler Modal arkasında; modal host, kaskad+boyut, sarmalayıcı, tek host ve audit der', () => {
    expect(page).toContain('setLeftoverConfirm(l)');
    const i = page.indexOf('{leftoverConfirm && (');
    expect(i).toBeGreaterThan(0);
    const modal = page.slice(i, i + 2800);
    expect(modal).toContain('leftoverConfirm.host');
    expect(modal).toContain('kaskadla gizli iç tablosunu');
    expect(modal).toContain('mvLeftoverSize(leftoverConfirm)');
    expect(modal).toContain('leftoverConfirm.storage');
    expect(modal).toContain("YALNIZ bu host&apos;ta");
    expect(modal).toContain('ON CLUSTER yok');
    expect(modal).toContain("Audit&apos;e düşer");
  });
  // v0.10.830 inceleme (KRİTİK): metin eylemi DOĞRU anlatmalı — "storage
  // çalışmaya devam eder" tek başına yanlıştı, çıplak ad da geri kuruluyor.
  it('modal sarmalayıcının geri kurulduğunu ve veri kapısını söyler', () => {
    const i = page.indexOf('{leftoverConfirm && (');
    const modal = page.slice(i, i + 2800);
    expect(modal).toContain('Distributed sarmalayıcı olarak yeniden kurulur');
    expect(modal).toContain('UNKNOWN_TABLE');
    expect(modal).toContain('mvLeftoverStorageSize(leftoverConfirm)');
    expect(modal).toContain('kalıntı doluyken kanonik boşsa');
    expect(modal).toContain('TAŞIMAZ');
  });
  // v0.10.830 inceleme (MINOR): guarded kalıntı DÜĞMESİZ — asla geçemeyecek
  // bir kapıya bağlı düğme çıkarmak, 409'u var olmayan bir düğmeye yolluyordu.
  it('blocked satır düğme çizmez, nedeni gösterir', () => {
    const i = page.indexOf('{leftoverRows.map(l => {');
    // v0.10.833 — pencere satırın GERÇEK sonuna kadar (düğme + pay), sihirli
    // 2000 karakter değil: 831'de aynı sınıf (tracesCountUniverse) yeni bir
    // prop'u pencerenin dışına itmişti.
    const end = page.indexOf('Kalıntıyı düşür', i);
    expect(end).toBeGreaterThan(i);
    const block = page.slice(i, end + 400);
    // v0.10.833 — neden artık tek kaynaktan gelmiyor: sunucunun `blocked`ı
    // VE istemcinin hedef-uuid kapısı (leftoverBlockedBy) tek `stopped`
    // değerinde birleşir; title o değeri gösterir. Pin yeni yazımı izler ama
    // sözleşme aynı: sunucu sebebi HÂLÂ ilk kaynaktır.
    expect(block).toContain('const stopped = l.blocked || leftoverBlockedBy(l)');
    expect(block).toContain('title={stopped}');
    const btn = block.indexOf('Kalıntıyı düşür');
    expect(block.slice(0, btn)).toContain('? <span className="badge b-warn"');
  });
  it('sağlıklı rozeti artık yokluğunu DA ister', () => {
    const i = page.indexOf("MV&apos;ler sağlıklı");
    expect(i).toBeGreaterThan(0);
    const guard = page.slice(Math.max(0, i - 300), i);
    expect(guard).toContain('leftovers && !leftoverError');
    expect(guard).toContain('leftoverRows.length === 0');
  });
  it('ölçülemeyen artık ayrı rozet (sessiz yeşil yok) ve >100 satır kapağı', () => {
    expect(page).toContain('artık ölçülemedi');
    const i = page.indexOf('{leftoverRows.map(l => {');
    expect(i).toBeGreaterThan(0);
    // v0.10.942 — tablo standardı T6: satır içi content-visibility yerine tek sınıf.
    expect(page.slice(i, i + 700)).toContain('className="cv-row"');
  });
  it('boyut okunamadıysa dürüst metin (0 satır DEĞİL)', () => {
    expect(page).toContain("'boyut okunamadı'");
  });
});

// Replika kartı SALT OKUNUR kalır: canRepair `.inner*` için hâlâ false;
// değişen yalnız etikettir.
function shard(missing: { host: string; engine?: string }[]): CHReplicaShard {
  const r: CHReplicaState = { host: 'ch-01', shard: 1, replica: 1, zkPath: '/t/1/x', replicaName: 'ch-01', totalReplicas: 2, activeReplicas: 2, readonly: false, sessionExpired: false, delayS: 0, queue: 0, rows: {}, totalRows: 0 };
  return { shard: 1, replicas: [r], verdict: 'missing_replica', hint: '', missing };
}

describe('replika kartı iç tablo etiketi (v0.10.830)', () => {
  it('sahibi olmayan uuid → öksüz; temizlik yeri söylenir', () => {
    const s = innerViewLabel({ inner: true, orphan: true, shards: [shard([{ host: 'ch-02' }])] });
    expect(s).toContain('sahibi yok (öksüz)');
    expect(s).toContain('MV onarımı kartından temizlenir');
  });
  it('sahip başka host’ta → "(bu host’ta yok)"', () => {
    const s = innerViewLabel({ inner: true, view: 'db_summary_5m', viewHosts: ['ch-01'], shards: [shard([{ host: 'ch-03' }, { host: 'ch-04' }])] });
    expect(s).toContain('view: db_summary_5m');
    expect(s).toContain("(bu host'ta yok)");
  });
  it('sahip sorunlu host’ta duruyorsa ek not YOK', () => {
    const s = innerViewLabel({ inner: true, view: 'db_summary_5m', viewHosts: ['ch-03'], shards: [shard([{ host: 'ch-03' }])] });
    expect(s).toContain('view: db_summary_5m');
    expect(s).not.toContain("(bu host'ta yok)");
  });
  it('roster eksikken öksüz İDDİA EDİLMEZ (orphan false + view boş → çözülemedi)', () => {
    expect(innerViewLabel({ inner: true, shards: [shard([{ host: 'ch-02' }])] })).toBe('MV iç tablosu · view çözülemedi');
  });
  it('iç tablo değilse etiket yok; onarım düğmesi hâlâ kapalı', () => {
    expect(innerViewLabel({ inner: false, view: 'x' })).toBe('');
    expect(canRepair({ table: '.inner_id.aaaaaaaa-1111-2222-3333-444444444444' }, shard([{ host: 'ch-02' }]), { host: 'ch-02' })).toBe(false);
  });
  it('kart etiketi saf gövdeden gelir (ikinci metin yok)', () => {
    expect(page).toContain('{innerViewLabel(t)}');
    expect(page).not.toContain("MV iç tablosu · ${t.view");
  });
});
