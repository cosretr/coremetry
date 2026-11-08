import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { aiSubjectTitle, aiSubjectTopic, type AISubject } from '@/lib/aiSubject';
import { t } from '@/lib/i18n';

// v0.10.1338 (operatör) — v0.10.1326/1338 "Ask CoSRE" tutarlılığının devamı:
// log satırı ✨ (LogTable), log artışı ✨ (LogsHistogram) ve anomali akışının
// kompakt "AI" düğmesi (streams.tsx) da ai.askCosre adını taşır. Davranış /
// özneler değişmedi; çekmece başlığı "CoSRE’ye sor", modelin KONU satırı
// (aiSubjectTopic) eski semantik ifadeyi korur.
const SRC = resolve(__dirname, '../..');
const read = (p: string) => readFileSync(resolve(SRC, p), 'utf8');

// `<AIExplainButton …/>` öğesini süslü parantez dengesiyle topla.
function explainButtons(src: string): string[] {
  const out: string[] = [];
  const re = /<AIExplainButton\b/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    let stop = src.indexOf('/>', m.index);
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

describe('Ask CoSRE etiketleri — log / log-spike / kompakt anomali (v0.10.1338)', () => {
  it('LogTable log satırı düğmesi ai.askCosre + ai.askCosreLogHint', () => {
    const els = explainButtons(read('components/LogTable.tsx'));
    expect(els.length).toBe(1);
    expect(els[0]).toContain("tr('ai.askCosre')");
    expect(els[0]).toContain("title={tr('ai.askCosreLogHint')}");
    expect(els[0]).not.toMatch(/Açıkla/);
  });

  // v0.10.1967 (operatör: "Burada Ask CoSRE olmasına gerek yok") — log histogramında düğme yok.
  it('LogsHistogram artış düğmesi kaldırıldı', () => {
    expect(explainButtons(read('components/LogsHistogram.tsx')).length).toBe(0);
  });

  it('streams kompakt "AI" düğmesinin erişilebilir adı ai.askCosre · anomaly', () => {
    const src = read('features/anomalies/streams.tsx');
    expect(src).toContain("`${tr('ai.askCosre')} · anomaly`");
    const el = explainButtons(src).find(e => e.includes("kind: 'anomaly'"));
    expect(el).toBeDefined();
    // Görünür metin kompakt "AI" (ekran okuyucudan gizli) …
    expect(el).toContain('<span aria-hidden="true">AI</span>');
    // … erişilebilir ad + ipucu "Ask CoSRE · anomaly".
    expect(el).toContain('<span className="sr-only">{askCosreName}</span>');
    expect(el).toContain('title={askCosreName}');
  });

  // v0.10.1367 — Endpoint detayının tek tık "Ask CoSRE"ı: ProblemDetail ile aynı
  // biçim (dolu birincil, ✨ + ai.askCosre), ipucu ai.askCosreEndpointHint.
  it('EndpointDetail düğmesi ai.askCosre + ai.askCosreEndpointHint, emphasis strong', () => {
    const els = explainButtons(read('pages/EndpointDetail.tsx'));
    expect(els.length).toBe(1);
    expect(els[0]).toContain('subject={aiSubject}');
    expect(els[0]).toContain('emphasis="strong"');
    expect(els[0]).toContain("tr('ai.askCosre')");
    expect(els[0]).toContain("title={tr('ai.askCosreEndpointHint')}");
    expect(t('ai.askCosreEndpointHint', 'en')).toBe('Ask CoSRE · endpoint — what is happening, since when, where the time goes, which errors.');
    expect(t('ai.askCosreEndpointHint', 'tr')).toBe('CoSRE’ye sor · endpoint — ne oluyor, ne zamandan beri, süre nereye gidiyor, hangi hatalar.');
  });

  it('i18n ipuçları iki dilde', () => {
    expect(t('ai.askCosreLogHint', 'en')).toBe('Ask CoSRE · log — explain this log line.');
    expect(t('ai.askCosreLogHint', 'tr')).toBe('CoSRE’ye sor · log — bu log satırını açıkla.');
  });

  it('aiSubjectTitle log / log-spike = CoSRE’ye sor; aiSubjectTopic eski ifadeyi korur', () => {
    const spike: AISubject = { kind: 'log-spike', id: 'svc', fromNs: 5, toNs: 9, mode: 'auto', env: '', q: '', level: 'all' };
    const line: AISubject = { kind: 'log', id: 'svc', tsNs: '1', pod: '', traceId: '', hash: '0000abcd' };
    for (const s of [spike, line]) {
      expect(aiSubjectTitle(s, 'tr')).toBe('CoSRE’ye sor');
      expect(aiSubjectTitle(s, 'en')).toBe('Ask CoSRE');
    }
    expect(aiSubjectTopic(spike)).toBe('Log artışını açıkla');
    expect(aiSubjectTopic(line)).toBe('Log satırını açıkla');
  });
});

// v0.10.1512 (operatör) — trace bağlamındaki log tablosunda satır ✨'i yok;
// /logs ve servis log sekmesi (hideTraceColumn=false) düğmeyi korur.
describe('LogTable satır ✨ trace bağlamında gizli', () => {
  it('LogLineExplain yalnız hideTraceColumn=false iken çizilir', () => {
    expect(read('components/LogTable.tsx')).toContain('{!l.origin && !hideTraceColumn && <LogLineExplain l={l} />}');
  });
});
