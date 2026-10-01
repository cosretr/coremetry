// compareWiring.pin — v0.10.1025 (Databases dilim 3).
//
// NE ÇİVİLİYOR: /databases'in "Compare vs prior" kutusu v0.9.433'ten beri
// ÖLÜYDÜ. Zincirin üç halkası da yerindeydi — kutu URL'e `?compare=prior`
// yazıyor, sorgu prior'u istiyor, toRow prior* alanlarını satıra
// kopyalıyordu — ama span satırlarının `<DependenciesTable kind="db">`
// mount'u `compare` GEÇMİYORDU ve tablonun her delta rozeti `compare &&`
// kapılı. Hiçbir şey kırılmadı, hiçbir test kızarmadı; kutu sadece hiçbir
// şey yapmadı. Bu pin zincirin dört halkasını birden tutuyor ki biri
// sessizce düşerse kızarsın.
//
// Receiver mount'u BİLEREK compare geçmez (RED'i tanım gereği sıfır, prior
// okuması receiver keşfi yapmaz) — o karar da pinli, iki yönde: span
// mount'u geçmeli, receiver mount'u geçmemeli.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const SRC = readFileSync(resolve(__dirname, '..', 'Databases.tsx'), 'utf8');
// Yorumlar süzülür — kapı koda bakar; şerhler eski davranışı TARİHÇE
// olarak anıyor. JSX yorumu `{/* */}` boş `{}` bırakır, derinlik sayımı
// bundan etkilenmez.
const CODE = SRC.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');

/** `<Tag …>` açılış penceresi — `{}` derinliğine duyarlı (iç JSX'te erken kapanmaz). */
function tagWindows(src: string, tag: string): string[] {
  const out: string[] = [];
  let from = 0;
  for (;;) {
    const start = src.indexOf(`<${tag}`, from);
    if (start < 0) return out;
    let depth = 0;
    let end = -1;
    for (let i = start + tag.length + 1; i < src.length; i++) {
      const c = src[i];
      if (c === '{') depth++;
      else if (c === '}') depth--;
      else if (c === '>' && depth === 0) { end = i + 1; break; }
    }
    if (end < 0) return out;
    out.push(src.slice(start, end));
    from = end;
  }
}

const dbMounts = tagWindows(CODE, 'DependenciesTable').filter(w => /kind\s*=\s*["']db["']/.test(w));

describe('/databases compare kutusu tabloya bağlı (v0.10.1025)', () => {
  it('iki db mount noktası var (span + receiver) — boş küme kapıyı yeşil bırakmasın', () => {
    expect(dbMounts.length).toBe(2);
  });

  it('span satırları mount\'u compare={compare} GEÇİYOR', () => {
    const span = dbMounts.filter(w => /rows=\{tableRows\}/.test(w));
    expect(span.length, 'rows={tableRows} mount\'u bulunamadı').toBe(1);
    expect(span[0]).toMatch(/\bcompare=\{compare\}/);
  });

  it('receiver mount\'u compare GEÇMİYOR (RED sıfır, prior yok — karar şerhte)', () => {
    const recv = dbMounts.filter(w => /receiverRows/.test(w));
    expect(recv.length, 'receiver mount\'u bulunamadı').toBe(1);
    expect(recv[0]).not.toMatch(/\bcompare=/);
  });

  it('kutu URL\'den okunuyor ve sorguya gidiyor', () => {
    expect(CODE).toMatch(/const compare = sp\.get\('compare'\) === 'prior';/);
    expect(CODE).toMatch(/api\.databases\(from, to, compare \? 'prior' : undefined/);
    expect(CODE).toMatch(/queryKey: \['databases', from, to, compare, env\]/);
  });

  it('toRow prior alanlarını satıra kopyalıyor (toRow açık kopyalar — eksik alan tabloya ulaşmaz)', () => {
    for (const f of ['priorSpanCount', 'priorErrorCount', 'priorAvgMs', 'priorP50Ms', 'priorP99Ms']) {
      expect(CODE, `toRow ${f} kopyalamıyor`).toMatch(new RegExp(`\\b${f}:`));
    }
  });
});

describe('tagWindows — pencere etikete hapsediliyor', () => {
  it('iç JSX taşıyan propta erken kapanmaz', () => {
    const w = tagWindows('<DependenciesTable extraControls={<label><input /></label>} kind="db" compare={c} />', 'DependenciesTable');
    expect(w[0]).toContain('compare={c}');
  });
  it('komşu etikete taşmaz', () => {
    const w = tagWindows('<DependenciesTable kind="db" />\n<DependenciesTable kind="db" compare={c} />', 'DependenciesTable');
    expect(w.length).toBe(2);
    expect(w[0]).not.toContain('compare');
  });
});
