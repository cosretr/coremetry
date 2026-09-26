import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';

// primitiveClasses.test.ts — v0.9.884 (tutarlılık denetimi Dalga 3, T1+T2).
//
// KAPATTIĞI KAPI BOŞLUĞU — Dalga 3'ün TEK büyük riski (plan R1):
// bir primitif `variant`/`size` haritasına yeni bir satır eklenir, karşılığı
// CSS kuralı AYNI COMMIT'te gelmezse buton element-seviyesi `button {}`
// kuralına düşer ve **dolu mavi primary** olur. Yıkıcı bir "Sil" butonu
// birdenbire sayfanın en davetkâr öğesi hâline gelir. `tsc` sessiz,
// `eslint` sessiz, `make audit`'te buton kuralı yok.
//
// NEDEN `undefinedCssRefs` BUNU GÖREMİYOR: o kapı `className="literal"`
// tarar. Primitiflerin bastığı sınıf literal DEĞİL — `variantClass[variant]`
// ile ÇALIŞMA ANINDA hesaplanır. Yani haritanın içi statik taramanın kör
// noktası; bu kapının tek varlık sebebi orası.
//
// TASARIM — kural KAYNAKTAN türetilir, burada LİSTELENMEZ. Haritalar
// `components/ui/*.tsx`ten okunur; yeni bir primitif eklendiğinde kapı onu
// kendiliğinden tarar. Elle yazılmış bir sınıf listesi olsaydı bu test kendi
// düzyazısını doğrulardı — bu depoda altı kez ısıran tuzak.

const UI  = __dirname;
const SRC = resolve(__dirname, '../..');

const stripComments = (s: string) =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');

// ── CSS'te TANIMLI sınıflar ───────────────────────────────────────────────
function cssFiles(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) cssFiles(p, out);
    else if (p.endsWith('.css')) out.push(p);
  }
  return out;
}
const CSS = cssFiles(SRC).map(p => stripComments(readFileSync(p, 'utf8'))).join('\n');
const definedClasses = new Set<string>(
  [...CSS.matchAll(/\.(-?[A-Za-z_][\w-]*)/g)].map(m => m[1]),
);

// ── Primitiflerin BASTIĞI sınıflar ────────────────────────────────────────
// `.test.` içeren dosyalar dışarıda: fixture'lar uydurma sınıf basar.
const TEST_MARK = '.' + 'test' + '.';
const primitives = readdirSync(UI)
  .filter(f => f.endsWith('.tsx') && !f.includes(TEST_MARK))
  .sort();

/**
 * Bir primitifin ÇALIŞMA ANINDA basabildiği bütün sınıflar. İki kaynak:
 *   1. `const xClass: Record<Foo, string> = { a: 'cls', … }` haritaları,
 *   2. `const classes = [ 'taban', xClass[…], koşul ? 'x' : '' ]` dizisi —
 *      taban sınıf (`btn-icon`) yalnız burada geçer, haritada geçmez.
 */
function emittedClasses(src: string): string[] {
  const out: string[] = [];
  const body = stripComments(src);
  for (const blk of body.matchAll(/Record<[^>]*,\s*string>\s*=\s*\{([\s\S]*?)\n\}/g))
    for (const m of blk[1].matchAll(/:\s*'([^']*)'/g))
      if (m[1]) out.push(m[1]);
  for (const blk of body.matchAll(/const classes\s*=\s*\[([\s\S]*?)\n\s*\]/g))
    for (const m of blk[1].matchAll(/'([^']*)'/g))
      if (m[1]) out.push(m[1]);
  return out;
}

/**
 * v0.10.939 (tablo standardı T1) — `classes` listesini `className`e taşıyan
 * JSX öğelerinin etiketleri (`<button … className={classes}`, Chip'in
 * `<span className={`${classes} ch-static`}`i). Eşleşme en yakın `<`dan
 * başlar (`[^<]` onu geçemez); açılış etiketinde className'den önce `<`
 * geçerse etiket kaçar — o zaman aşağıdaki "kök bulunamadı" iddiası
 * kırmızıya döner, kapı sessizce daralmaz.
 */
function classesHosts(body: string): string[] {
  return [...body.matchAll(/<([A-Za-z][\w.]*)\b[^<]*?\bclassName=\{[^}]*\bclasses\b/g)].map(m => m[1]);
}

describe('primitiveClasses — atomun bastığı her sınıfın CSS karşılığı var', () => {
  it('en az bir primitif taranıyor (kapı boşa dönmüyor)', () => {
    // Dosya adı deseni kayarsa (ör. ui/ klasörü bölünürse) bu kapı sessizce
    // 0 dosya tarayıp yeşil kalırdı. Boş taramayı hata sayıyoruz.
    expect(primitives.length).toBeGreaterThan(5);
  });

  it('variant/size haritalarındaki hiçbir sınıf CSS\'te karşılıksız değil', () => {
    const offenders: string[] = [];
    for (const f of primitives) {
      const src = readFileSync(join(UI, f), 'utf8');
      for (const cls of emittedClasses(src)) {
        if (/\s/.test(cls)) {
          offenders.push(`ui/${f}: '${cls}' çok-token — birleştirme/ayrıştırma bozulur`);
        } else if (!definedClasses.has(cls)) {
          offenders.push(`ui/${f}: .${cls} CSS'te YOK → element-seviyesi button{} kuralına düşer (dolu mavi primary)`);
        }
      }
    }
    expect(offenders, `Karşılıksız primitif sınıfı:\n${offenders.join('\n')}`).toEqual([]);
  });

  it('Button\'un iki yeni rungu (xs, ghost-danger) gerçekten boyanıyor', () => {
    // Nokta iddia: haritada VAR olmaları yetmez, kuralın `button.` ön ekiyle
    // yazılmış olması gerekir — `.xs` diye serbest bir sınıf, element-seviyesi
    // `button` kuralının özgüllüğünü (0,1,1 vs 0,0,1) yenemezdi.
    const css = stripComments(readFileSync(resolve(SRC, 'styles/globals.css'), 'utf8'));
    expect(css).toMatch(/button\.xs\s*\{/);
    expect(css).toMatch(/button\.ghost-danger\s*\{/);
  });

  // ── SAHİPLENİLMEMİŞ ÇAKIŞMA (v0.9.894) ──────────────────────────────
  // Yukarıdaki iddia "sınıfın CSS'te KARŞILIĞI var mı" diye sorar. Onu
  // geçen ama yine de yıkıcı olan bir durum var: sınıf CSS'te TANIMLI —
  // ama BAŞKA BİR ŞEY olarak. P5'te tam bu oldu: Chip atomunun doğal adı
  // `chip`di, ve `.chip` bu depoda zaten CANLI (ProblemDetail + Incident
  // sayfalarının statik key/value meta rozeti, radius 20px, sekiz
  // tüketici). Atom o adı basmış olsaydı iki detay sayfası sessizce
  // yeniden boyanırdı ve YUKARIDAKİ KAPI YEŞİL KALIRDI.
  //
  // Kural: bir primitifin TABAN sınıfı (classes dizisinin ilk literali)
  // yalnız o atoma ait olmalı — `components/ui/` dışında hiçbir dosya
  // onu elle className olarak yazmamalı. Değiştiriciler (`ib-star`,
  // `ch-dashed`, `active`) bu kuralın DIŞINDA: onları çağıranın yazması
  // tasarımın parçası. Yalnız taban adı tekil olmak zorunda.
  //
  // KAPSAM SINIRI DÜRÜSTÇE: yalnız TSX'te literal className yazımını
  // görür. Bir sınıf CSS'te var olup yalnız torun seçiciyle
  // (`.foo .bar`) kullanılıyorsa bu kapı onu kaçırır.
  it('primitif taban sınıfları ui/ dışında elle yazılmamış (sahipsiz çakışma)', () => {
    const srcFiles: string[] = [];
    (function walk(dir: string) {
      for (const e of readdirSync(dir, { withFileTypes: true })) {
        const p = join(dir, e.name);
        if (e.isDirectory()) { if (e.name !== 'node_modules') walk(p); }
        else if (/\.tsx?$/.test(p) && !p.includes(TEST_MARK)) srcFiles.push(p);
      }
    })(SRC);
    const outside = srcFiles.filter(p => !p.startsWith(join(SRC, 'components', 'ui')));

    const bases: Array<[string, string]> = [];
    for (const f of primitives) {
      const body = stripComments(readFileSync(join(UI, f), 'utf8'));
      const m = body.match(/const classes\s*=\s*\[\s*'([^']+)'/);
      if (m) bases.push([f, m[1]]);
    }
    expect(bases.length, 'taban sınıflı primitif bulunamadı — desen kaymış').toBeGreaterThan(2);
    // v0.10.939 (tablo standardı T1) — KeyValue kapıdan kaçmasın: `.keyval`
    // tabanı yalnız atomun (dilim 3'te 21 elle yazım ona göçüyor). Atom
    // `const classes = [...]` desenini bırakırsa bu satır kırmızıya döner.
    expect(bases.map(([, b]) => b), 'KeyValue tabanı taranmıyor — `const classes = [\'keyval\', …]` deseni kaymış').toContain('keyval');

    const offenders: string[] = [];
    for (const [f, base] of bases) {
      // Sınır `\b` OLAMAZ: regex'te `-` bir kelime sınırıdır, dolayısıyla
      // `\bbtn-chip\b` `btn-chip-x`in İÇİNDE de eşleşir — ve `btn-chip-x`
      // farklı bir sınıf (MB6'nın kanonik ×'i, `.badge` gövdeli sitelerde
      // tek başına kullanılıyor). Tam token istiyoruz.
      const re = new RegExp(`className=[{]?["'\`][^"'\`]*(?<![\\w-])${base}(?![\\w-])`);
      for (const p of outside) {
        const lines = stripComments(readFileSync(p, 'utf8')).split('\n');
        lines.forEach((l, i) => {
          if (re.test(l)) offenders.push(`${p.slice(SRC.length + 1)}:${i + 1} elle '.${base}' yazıyor — ui/${f}'in taban sınıfı`);
        });
      }
    }
    expect(offenders, `Taban sınıfı çakışması:\n${offenders.join('\n')}`).toEqual([]);
  });

  // ── :hover'da ARKA PLAN KAYBI (v0.9.895 regresyon testi) ────────────
  // SEMPTOM: `variant="bare"` IconButton'ların on iki sitesi (log satırı
  // ⊕/⊖, trace peek 👁, Services pin, Clusters, Dashboards, GroupTable)
  // fare üzerine gelince DOLU MAVİ KAREYE dönüyordu.
  //
  // KÖK NEDEN — özgüllük: element seviyesindeki
  // `button:hover:not(:disabled) { background: var(--accent2) }` (0,2,1)
  // bir :hover kuralı. `.btn-icon { background: transparent }` ise (0,1,0)
  // VE :hover kuralı değil. `.btn-icon.ib-bare:hover:not(:disabled)`
  // yalnız `color` bildiriyordu; `background` bildirmediği için o
  // özelliğin kazananı `button:hover` oldu. `undefinedCssRefs` sessiz
  // (sınıf tanımlı), `primitiveClasses`ın ilk iddiası sessiz (karşılığı
  // var), `tsc` sessiz. Yalnız GÖZLE görülür bir hataydı.
  //
  // KURAL: bir primitifin TABAN :hover kuralı `background` bildirmiyorsa,
  // o primitifin HER değiştirici :hover kuralı bildirmek ZORUNDA.
  // `.btn-link` bunu taban kuralında yapıyor → değiştiricileri muaf
  // (yanlış pozitif üretmiyor).
  //
  // v0.10.939 (tablo standardı T1) — KAPSAM: yalnız hover öznesi bir
  // <button> olabilen kurallar. Kaçağın kaynağı ELEMENT-seviyesi
  // `button:hover`; <button> olmayan bir öğeye (KeyValue'nun <dl> satırı,
  // Tooltip'in <span>'ı) o kural hiç uygulanmaz. Kapsamdakiler:
  //   • kökü <button> olan atomun (classes listesini bir `<button`a basan —
  //     Chip'in <span> dalı da olsa) BÜTÜN :hover kuralları, eskisi gibi;
  //   • kökü <button> olmayan atomda (ButtonGroup <div>) yalnız :hover'ı
  //     bir `button` bileşiğine düşen kurallar (`.btn-group > button:hover`).
  // Öncesi: `.keyval__row:hover .keyval__acts { opacity: 1 }` (arka plan
  // bildirmeyen, opaklık boyayan açığa çıkarma kuralı; satırın hover zemini
  // OLMAMALI, T2) kaçak diye işaretleniyordu ve KeyValue bu yüzden ev
  // desenini (`const classes = [...]`) bırakıp sahiplik kapısından da
  // kaçmıştı.
  it('primitif :hover kuralları arka planı geri bildiriyor (button:hover kaçağı)', () => {
    const css = stripComments(readFileSync(resolve(SRC, 'styles/globals.css'), 'utf8'));

    // Kaçağın KAYNAĞI hâlâ orada mı? Kural kaldırılmışsa bu kapı gereksiz
    // yere kısıtlıyor demektir — o zaman burası kırmızıya dönüp haber verir.
    const LEAK = 'button:' + 'hover:not(:disabled)';
    expect(css, `${LEAK} kuralı yok — bu kapının dayanağı kalmamış`).toContain(LEAK);

    const bases: Array<{ base: string; button: boolean }> = [];
    const rootless: string[] = [];
    for (const f of primitives) {
      const body = stripComments(readFileSync(join(UI, f), 'utf8'));
      const m = body.match(/const classes\s*=\s*\[\s*'([^']+)'/);
      if (!m) continue;
      const hosts = classesHosts(body);
      if (!hosts.length) rootless.push(`ui/${f} (.${m[1]})`);
      bases.push({ base: m[1], button: hosts.includes('button') });
    }
    // Kapsam daraltması SESSİZ kalmasın (v0.10.939): kök etiketi okunamayan
    // atom <button> değil sayılıp kapıdan düşerdi.
    expect(rootless, `classes listesini taşıyan JSX öğesi bulunamadı:\n${rootless.join('\n')}`).toEqual([]);
    const buttonBases = bases.filter(b => b.button).map(b => b.base);
    expect(buttonBases, 'v0.9.895 kaçağının atomu (IconButton) kapsam dışı kaldı').toContain('btn-icon');
    expect(buttonBases.length, 'buton kökü tespiti kaymış — kapı boşa dönüyor').toBeGreaterThan(4);
    expect(buttonBases, 'KeyValue <dl> — buton sayılmamalı').not.toContain('keyval');

    const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
      .map(m => ({ sel: m[1].trim().replace(/\s+/g, ' '), body: m[2] }));
    // :hover'ı bir `button` bileşiğine düşen seçici (`> button:hover`,
    // `button.sec:hover:not(:disabled)`).
    const HOVER_ON_BUTTON = /(^|[\s>+~,(])button(?![\w-])[^\s>+~,]*:hover/;

    const offenders: string[] = [];
    for (const { base, button } of bases) {
      const mine = rules.filter(r => r.sel.includes(`.${base}`) && r.sel.includes(':hover')
        && (button || HOVER_ON_BUTTON.test(r.sel)));
      // Taban :hover kuralı = seçicide `.base` var, başka bir `.mod` yok.
      const baseHover = mine.filter(r =>
        new RegExp(`\\.${base}(?![\\w-])(:|,|\\s|$)`).test(r.sel) &&
        !new RegExp(`\\.${base}\\.`).test(r.sel));
      if (baseHover.some(r => r.body.includes('background'))) continue;
      for (const r of mine) {
        // v0.10.928 — hiçbir şey BOYAMAYAN hover kuralı (yalnız z-index /
        // position: bitişik grupta hover edeni öne alma) kaçak değildir:
        // arka planın kazananı yine varyantın kendi hover kuralı.
        const paints = /(^|;|\s)(background|color|border[\w-]*|box-shadow|outline[\w-]*|opacity|filter)\s*:/.test(r.body);
        if (!paints) continue;
        if (!r.body.includes('background')) {
          offenders.push(`${r.sel} → 'background' bildirmiyor; ${LEAK} kazanır ve dolu accent olur`);
        }
      }
    }
    expect(offenders, `:hover arka plan kaçağı:\n${offenders.join('\n')}`).toEqual([]);
  });

  it('.spinner.sm bileşik kuralı var — loading butonu büyütmesin (R7)', () => {
    // Bu, `undefinedCssRefs`in yapısal olarak KAÇIRDIĞI durum: Button
    // `className="spinner sm"` basıyor, iki token da ayrı ayrı tanımlı
    // (`.spinner` ve `button.sm`), dolayısıyla o kapı yeşil. Ama BİLEŞİK
    // kural yoksa spinner 14px kalır, `sm` butonun satır kutusu 11px'tir ve
    // buton istek sırasında ZIPLAR. Bileşik seçiciyi ayrıca yokluyoruz.
    const css = stripComments(readFileSync(resolve(SRC, 'styles/globals.css'), 'utf8'));
    expect(css).toMatch(/\.spinner\.sm\s*\{/);
    const btn = stripComments(readFileSync(join(UI, 'Button.tsx'), 'utf8'));
    expect(btn).toMatch(/className="spinner sm"/);
  });
});
