import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve, relative } from 'node:path';

// v0.10.1338 (operatör) — her AI açıklama affordance'ı "Ask CoSRE" /
// "CoSRE’ye sor" (i18n ai.askCosre) adını taşır; trace (v0.10.944) ve
// exception (v0.10.1326) emsali. Kaynak sabitlemesi: hiçbir AIExplainButton /
// CopilotExplain çağrısının etiketi literal bir "Explain " metni basmaz
// (yorumlar sayılmaz; yalnız JSX öğesinin kendisi taranır).
const SRC = resolve(__dirname, '../..');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) { walk(p, out); continue; }
    if (p.endsWith('.tsx') && !/\.test\.tsx$/.test(p)) out.push(p);
  }
  return out;
}

// `<AIExplainButton …/>` / `<CopilotExplain …/>` öğesini kendinden kapanan
// `/>`'a kadar topla (etiketin kendi `<span>…</span>` kapanışları `/>` içermez).
function elements(src: string, tag: string): string[] {
  const out: string[] = [];
  const re = new RegExp(`<${tag}\\b`, 'g');
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    const end = src.indexOf('/>', m.index);
    let stop = end;
    // label={<>…</>} içindeki iç içe `<IconSparkles />` ilk `/>` olabilir:
    // süslü parantez dengesi sıfırlanana dek ilerle.
    for (;;) {
      const chunk = src.slice(m.index, stop + 2);
      const open = (chunk.match(/\{/g) ?? []).length;
      const close = (chunk.match(/\}/g) ?? []).length;
      if (open === close || stop < 0) break;
      stop = src.indexOf('/>', stop + 2);
    }
    out.push(src.slice(m.index, stop + 2));
  }
  return out;
}

describe('Ask CoSRE etiketleri (v0.10.1338)', () => {
  const files = walk(SRC);
  const hits: { file: string; el: string }[] = [];
  for (const f of files) {
    const src = readFileSync(f, 'utf8');
    for (const tag of ['AIExplainButton', 'CopilotExplain']) {
      for (const el of elements(src, tag)) hits.push({ file: relative(SRC, f), el });
    }
  }

  it('tarama çağrı yerlerini buluyor', () => {
    expect(hits.filter(h => h.el.startsWith('<AIExplainButton')).length).toBeGreaterThan(8);
  });

  it('hiçbir etiket literal "Explain " metni basmaz', () => {
    const bad = hits.filter(h => /label=[\s\S]*Explain /.test(h.el)).map(h => h.file);
    expect(bad).toEqual([]);
  });

  // Incidents ölü kod dilimi 2: incident düğmesi arşiv sayfasıyla silindi.
  it('span / problem / anomaly düğmeleri ai.askCosre kullanır', () => {
    for (const kind of ['span', 'problem']) {
      const els = hits.filter(h => h.el.includes(`kind: '${kind}'`));
      expect(els.length, kind).toBeGreaterThan(0);
      for (const h of els) expect(h.el, `${kind} @ ${h.file}`).toContain("tr('ai.askCosre')");
    }
    const anomaly = hits.filter(h => h.el.includes(`kind: 'anomaly'`) || h.el.includes('kind="anomaly"'));
    // streams.tsx satır-içi kompakt "AI" düğmesi bilerek kısa kalır.
    const named = anomaly.filter(h => !h.file.endsWith('streams.tsx'));
    expect(named.length).toBeGreaterThanOrEqual(2);
    for (const h of named) expect(h.el, h.file).toContain("tr('ai.askCosre')");
  });

  it('Trace sayfasında trace + span düğmeleri aynı adı ipucuyla ayırır', () => {
    const span = hits.find(h => h.file.endsWith('SpanDetail.tsx') && h.el.includes("kind: 'span'"));
    expect(span?.el).toContain("title={tr('ai.askCosreSpanHint')}");
    const trace = hits.find(h => h.file.endsWith('pages/Trace.tsx') && h.el.includes("kind: 'trace'"));
    expect(trace?.el).toContain("title={tr('ai.askCosreTraceHint')}");
  });
});
