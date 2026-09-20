import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { lastReachablePage, reverseEndSort, TRACE_STAGE2_MAX_IDS } from './traceReach';

// v0.9.645 — operatör: "next butonu çok gözükmüyor, daha iyi bir yerde
// olabilir mi? next last gibi butonlar".
//
// "Son sayfa" düğmesi HER ZAMAN sunulamaz: MV yolunun aşama-2 IN listesi
// sınırlı, ötesindeki sayfalar SUNULAMIYOR. v0.9.638 tam bu yüzden
// total'ı Pager'dan kesmişti — sayı asla sayfalama sınırı değil.

describe('lastReachablePage', () => {
  it('kesin ve ulaşılabilir sayıda son sayfayı verir', () => {
    expect(lastReachablePage(500, false, 50)).toBe(9);   // 10 sayfa
    expect(lastReachablePage(50, false, 50)).toBe(0);    // tek sayfa
    expect(lastReachablePage(51, false, 50)).toBe(1);
  });

  // TAVANLI sayıda gerçek son sayfa BİLİNMİYOR — düğme yalan söylerdi.
  it('tavanlı sayıda düğme çizilmez', () => {
    expect(lastReachablePage(10000, true, 50)).toBeUndefined();
  });

  // Sunulabilir tavanın ötesi: düğme operatörü BOŞLUĞA götürürdü.
  it('sunulamayan aralıkta düğme çizilmez', () => {
    expect(lastReachablePage(TRACE_STAGE2_MAX_IDS + 1, false, 50)).toBeUndefined();
    expect(lastReachablePage(TRACE_STAGE2_MAX_IDS, false, 50)).toBeDefined();
  });

  it('sayı yoksa / bozuksa düğme çizilmez', () => {
    expect(lastReachablePage(undefined, false, 50)).toBeUndefined();
    expect(lastReachablePage(0, false, 50)).toBeUndefined();
    expect(lastReachablePage(100, false, 0)).toBeUndefined();
  });
});

// EN ÖNEMLİSİ: sabit backend'den KOPYA. Ayrışırsa Last düğmesi
// sunulamayan bir sayfaya götürür — bugünkü tekrar eden hata sınıfı
// ("bir kural iki yerde, zamanla ayrışıyor") sessiz bir UX hatasına
// dönüşür.
describe('backend sabitiyle eşleşme', () => {
  const go = readFileSync(
    resolve(__dirname, '../../../internal/chstore/repo.go'), 'utf8',
  );
  const m = go.match(/traceStage2MaxIDs\s*=\s*(\d+)/);

  it('backend sabiti okunabiliyor', () => {
    expect(m).not.toBeNull();
  });

  it('frontend kopyası backend ile AYNI', () => {
    expect(TRACE_STAGE2_MAX_IDS).toBe(Number(m![1]));
  });
});

// ── v0.10.827 (operatör-bildirimli, 2026-09-20) ─────────────────────────────
//
// SEMPTOM: /traces'te "Last ⇥"e basınca sayfa göstergesi hâlâ "1" diyor —
// operatör hangi sayfada olduğunu anlayamıyor. (v0.10.727 aynı yanılgıyı
// "Sondan sayfa" etiketiyle kapatmıştı; o etiket `order`ı okuyor.)
//
// KÖK NEDEN: tık `dt.setSort({ id: dt.sort.id ?? 'startTime', … })` yazıyordu,
// gösterge ise `order`ı okuyor. İkisini bağlayan TEK köprü `dt.sort`u sunucu
// sırasına çeviren efekt ve o efekt tanımadığı bir kolon kimliğinde SESSİZCE
// dönüyor (`if (!server) return;`). 'startTime' bir kolon kimliği DEĞİL
// (kolon `time`), yani o dalda tık tamamen yutuluyor: sıra dönmüyor, `order`
// değişmiyor, etiket "Page" kalıyor, gösterge 1'de duruyor.
//
// Bu blok kuralı iki uçtan çiviliyor: (1) üretilen kimlik HER zaman sunucunun
// sıralayabildiği bir kolon, (2) o kolon kümesi sayfanın KENDİ SERVER_SORTABLE
// haritasından okunuyor — iki taraf ayrışırsa test kırılır ("bir kural iki
// yerde" sınıfı, yukarıdaki backend-sabiti pininin aynısı).
describe('reverseEndSort — listenin sonu = ters sıranın ilk sayfası (v0.10.827)', () => {
  const traces = readFileSync(resolve(__dirname, '../pages/Traces.tsx'), 'utf8');
  const block = traces.match(/const SERVER_SORTABLE[^=]*=\s*\{([\s\S]*?)\};/);
  const serverSortable = (block?.[1] ?? '')
    .split(',').map(s => s.split(':')[0].trim()).filter(Boolean);

  it('sayfanın SERVER_SORTABLE haritası okunabiliyor', () => {
    expect([...serverSortable].sort()).toEqual(
      ['duration', 'operation', 'service', 'spans', 'status', 'time']);
  });

  // Tablo-güdümlü: HER kolon × HER yön. Birim-karışımı dersinin (v0.6.36)
  // sıralama hâli — tek dalı test edip diğerini varsaymak bu bug'ın ta kendisi.
  const cols = ['time', 'duration', 'spans', 'service', 'operation', 'status'] as const;
  for (const col of cols) {
    it(`${col}: desc → asc, asc → desc; kimlik korunur`, () => {
      expect(reverseEndSort(col, 'desc')).toEqual({ id: col, dir: 'asc' });
      expect(reverseEndSort(col, 'asc')).toEqual({ id: col, dir: 'desc' });
    });
    it(`${col}: üretilen kimliği sayfanın çeviricisi TANIYOR`, () => {
      expect(serverSortable).toContain(reverseEndSort(col, 'desc').id);
      expect(serverSortable).toContain(reverseEndSort(col, 'asc').id);
    });
  }

  // Bug'ın kendisi: eski ifade bu kimliği üretiyordu ve çevirici onu
  // TANIMIYOR — tık sessizce yutuluyordu. Bir daha kimse oraya yazmasın.
  it("'startTime' çevirici haritasında YOK (eski ifadenin ürettiği kimlik)", () => {
    expect(serverSortable).not.toContain('startTime');
  });
});
