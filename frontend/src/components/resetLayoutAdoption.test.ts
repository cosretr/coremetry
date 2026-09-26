// resetLayoutAdoption — v0.9.1332 → v0.10.939 (tablo standardı S8).
//
// TARİHÇE: sürüklenmiş kolon genişliği localStorage'da kalıcı
// (`dt.<key>.widths`); tabloyu ekrandan taşıran bir sürüklemenin geri
// dönüşü yoktu (v0.9.660 Users, v0.9.1332 /inbox "neden sayfa yatayda
// kayıyor", v0.9.1333 /problems). Kaçış yolu sayfa başına bir
// `ResetLayoutButton`du ve bu kapı onun BENİMSENME açığını sesli tutuyordu:
// 75 tablonun 2'si bağlıyordu, açık 73 → 69'a indi, 18 sitede kaldı —
// her birinde başka bir yerde, kalıcı genişlik yokken gizli.
//
// v0.10.939 (operatör cevabı S8: "başlık satırının sağ ucunda soluk ⋯
// menüde"): sıfırlama PRİMİTİFE taşındı. DataTableHead her tabloda son
// başlık hücresine ⋯ basar, menüde "Kolonları sıfırla" (HeadMenu.tsx;
// davranış DataTable/headMenu.contract.test.tsx). Benimseme artık
// sayfanın işi değil — kapı üç şeyi çiviliyor:
//   1. sayfa başına düğme GERİ GELMEZ (ResetLayoutButton adı kodda yok);
//   2. DataTableHead menüyü GERÇEKTEN basıyor (yazılmış-ama-bağlanmamış,
//      v0.9.660 sınıfı: resetLayout döndürülüp hiçbir yere bağlanmamıştı);
//   3. paylaşılan başlığı KULLANMAYAN tablo (kendi başlığını çizen) ⋯
//      menüsünü alamaz — yalnız tutamağa çift tık kalır. O açık SESLİ ve
//      yalnız azalır.
// v0.10.939 (tablo standardı S8, inceleme) — 3'ün iki gevşekliği kapandı:
// sayım dosya metnini değil `useDataTable(` ÇAĞRISINI sayar (BulkBar.tsx
// belgesindeki ad açığı 69 → 70 yapmıştı) ve muafiyet yalnız `dt`yi başlık
// ÇİZEN bir alt bileşene geçiren dosyaya (DataTableColgroup / DataTableState /
// BulkBar / DataTableCell başlık çizmez).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';

const SRC = resolve(__dirname, '..');
const PRIMITIVE_DIR = join('components', 'ui', 'DataTable');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(p) && !p.includes('.test.')) out.push(p);
  }
  return out;
}
// Yorumlar soyulur: gerekçe yorumları eski adı ANIYOR (bu dosya dahil
// değil — testler taranmıyor). "Test kendi düzyazısını kural sanır" tuzağı.
const strip = (t: string) => t
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
const code = (p: string) => strip(readFileSync(p, 'utf8'));

const FILES = walk(SRC);

describe('Kolonları sıfırla — tek yer: başlık ⋯ menüsü (v0.10.939)', () => {
  it('sayfa başına sıfırlama düğmesi yok (ResetLayoutButton kodda geçmiyor)', () => {
    const name = 'ResetLayout' + 'Button';
    const hits = FILES.filter(p => code(p).includes(name)).map(p => relative(SRC, p));
    expect(hits, 'sıfırlama DataTableHead ⋯ menüsünde — sayfaya ikinci bir düğme eklenmez').toEqual([]);
  });

  it('DataTableHead menüyü basıyor ve menü resetLayout\'u çağırıyor', () => {
    const head = code(resolve(SRC, PRIMITIVE_DIR, 'DataTable.tsx'));
    expect(head).toContain('<DataTableHeadMenu dt={dt} />');
    const menu = code(resolve(SRC, PRIMITIVE_DIR, 'HeadMenu.tsx'));
    expect(menu).toContain("label: 'Kolonları sıfırla', onSelect: dt.resetLayout");
  });

  it('paylaşılan başlığı kullanmayan tablo açığı SESLİ (yalnız azalır)', () => {
    // useDataTable ÇAĞIRAN ama <DataTableHead>/<VirtualTable> basmayan dosya:
    // başlığını kendisi çiziyor, ⋯ menüsü yok. `dt`yi başlığı çizen bir alt
    // bileşene geçiren dosya (ServicePodsTab → ServicePodsTable) başlığı
    // ORADA basar — muaf. Ama yalnız o: bkz. passesDtToHeadOwner.
    const own: string[] = [];
    let uses = 0;
    for (const p of FILES) {
      if (p.includes(PRIMITIVE_DIR + '/')) continue;
      const s = code(p);
      if (!callsUseDataTable(s)) continue;
      uses++;
      if (/<DataTableHead\b|<VirtualTable\b/.test(s)) continue;
      if (passesDtToHeadOwner(s)) continue;
      own.push(relative(SRC, p));
    }
    // v0.10.939 ölçümü: yalnız AdminSql (sanal CSS-grid sonuç ızgarası,
    // kendi başlık + tutamağı). Artarsa yeni bir tablo kendi başlığını
    // çiziyor: DataTableHead'e geçir, listeyi büyütme.
    expect(own).toEqual([join('pages', 'AdminSql.tsx')]);
    // Boş küme tuzağı: yürüyüş gerçekten tüketici buluyor.
    expect(uses).toBeGreaterThan(50);
  });

  // v0.10.939 (tablo standardı S8, inceleme) — sayım bir DOSYA METNİ değil
  // bir ÇAĞRI sayar. Eski kapı `src.includes('useDataTable')` idi: BulkBar.tsx
  // belgesinde "useDataTable'ın `selection` seçeneği" geçtiği için süpürülmemiş
  // tablo sayıldı ve açık 69 → 70'e "yükseldi" — tablo eklenmeden. Çağrı
  // biçimi (`= useDataTable(`, `return useDataTable<…>(`) şart; yorum soyulur,
  // `function useDataTable` tanımı ve dizge içindeki ad sayılmaz.
  it('sayım çağrı sayar: BulkBar-benzeri belge / tanım / dizge tablo değildir', () => {
    const bulk = resolve(SRC, PRIMITIVE_DIR, 'BulkBar.tsx');
    expect(readFileSync(bulk, 'utf8')).toContain('useDataTable'); // belge adı ANIYOR…
    expect(callsUseDataTable(code(bulk))).toBe(false);             // …ama çağırmıyor
    expect(callsUseDataTable(code(resolve(SRC, PRIMITIVE_DIR, 'DataTable.tsx')))).toBe(false); // tanım + hata metni
    expect(callsUseDataTable(strip('// const dt = useDataTable({ … })'))).toBe(false);
    expect(callsUseDataTable(strip('/* const dt = useDataTable({}) */'))).toBe(false);
    expect(callsUseDataTable('console.error(`useDataTable(${k}): …`)')).toBe(false);
    expect(callsUseDataTable('const dt = useDataTable<Row>({ storageKey: "x" })')).toBe(true);
    expect(callsUseDataTable('const dt = useDataTable({ storageKey: "x" })')).toBe(true);
    expect(callsUseDataTable('  return useDataTable({ storageKey })')).toBe(true);
  });

  // v0.10.939 (tablo standardı S8, inceleme) — muafiyet `dt={…}` GEÇEN her
  // dosya değil: DataTableColgroup / DataTableState / BulkBar / DataTableCell
  // başlık ÇİZMEZ. Kendi <thead>'ini çizip yalnız colgroup'u (ya da durum
  // satırını) paylaşan bir tablo eskiden bu yüzden sessizce muaftı.
  it('dt\'yi yalnız başlık çizmeyen parçalara geçiren dosya muaf DEĞİL', () => {
    expect(passesDtToHeadOwner('<DataTableColgroup dt={dt} /><DataTableState dt={dt} kind="empty" />')).toBe(false);
    expect(passesDtToHeadOwner('<BulkBar dt={dt} actions={[]} /><DataTableCell<Row> dt={dt} col="a" row={r} />')).toBe(false);
    expect(passesDtToHeadOwner('<ServicePodsTable rows={rows}\n  dt={dt} />')).toBe(true);
    expect(passesDtToHeadOwner('<DataTableColgroup dt={dt} /><PodsTable dt={dt} />')).toBe(true);
    expect(passesDtToHeadOwner('const x = 1;')).toBe(false);
  });
});

// Çağrı biçimi: atama / return / argüman / nesne alanı konumunda. Tanım
// (`function useDataTable<T>(`) ve dizge (`\`useDataTable(${k})\``) eşleşmez.
function callsUseDataTable(s: string): boolean {
  return /(?:[=(,:?]|\breturn)\s*useDataTable\s*(?:<[^(]*>)?\s*\(/.test(s);
}

// Başlık çizmeyen paylaşılan parçalar: `dt` alırlar ama <thead> basmazlar.
const NOT_HEAD_OWNERS = new Set(['DataTableColgroup', 'DataTableState', 'BulkBar', 'DataTableCell']);
// `dt={x}` alan her JSX etiketi (en yakın açılış etiketi; jenerik `<X<Row>`
// dahil). Biri başlık çizmeyen parçalar DIŞINDA bir bileşense dosya başlığı
// ona devretmiştir (ServicePodsTab → ServicePodsTable).
function passesDtToHeadOwner(s: string): boolean {
  for (const m of s.matchAll(/\bdt=\{\w+\}/g)) {
    const tags = [...s.slice(0, m.index).matchAll(/<([A-Z][\w.]*)(?:<[^<>]*>)?/g)];
    const tag = tags.length ? tags[tags.length - 1][1] : '';
    if (tag && !NOT_HEAD_OWNERS.has(tag)) return true;
  }
  return false;
}
