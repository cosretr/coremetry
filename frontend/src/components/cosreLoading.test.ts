// v0.10.152 (operatör) — CoSRE Explain yüklenirken gövde ortasında büyük,
// OTel işaretli yükleniyor durumu (küçük köşe spinner'ı değil). Kaynak taraması.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

describe('CoSRE explain loading state (v0.10.152)', () => {
  // v0.10.1384 (operatör: "CoSRE düşünüyor derken ve explain trace'teki dönme
  // efektleri aynı değil") — dönen OTel halkası (LoaderMark) yerine sohbetle
  // AYNI AiThinking göstergesi, `block` varyantı; yerleşim pinleri aynen.
  // v0.10.1407 — ✨ kartı (CopilotExplain) özne kipiyle silindi; açıklama bir
  // sohbet turu ve ilk token öncesi bekleyiş balonun ortalanmış AiThinking'i.
  it('uses AiThinking centered while waiting for the first token (sohbet balonu)', () => {
    const src = readFileSync(join(__dirname, 'ai/ChatBubble.tsx'), 'utf8').replace(/^\s*\/\/.*$/gm, '');
    expect(src).toContain('const thinkingOnly = !isUser && turn.pending && !turn.text && !turn.error;');
    expect(src).toContain('{thinkingOnly && <div className="cm-wait-center"><AiThinking variant="row" label={turn.statusLine} /></div>}');
    expect(src).not.toMatch(/<LoaderMark/);
  });
  it('PageLoader reuses LoaderMark (single mark, no drift)', () => {
    const src = readFileSync(join(__dirname, 'Spinner.tsx'), 'utf8').replace(/^\s*\/\/.*$/gm, '');
    expect((src.match(/opentelemetry\.svg/g) ?? []).length).toBe(1);
    expect(src).toMatch(/<LoaderMark label=\{label \?\? 'Loading'\} \/>/);
    // v0.10.155 — lg etiket büyük harfe ÇEVRİLMEZ ("CoSRE düşünüyor…").
    expect(src).toMatch(/textTransform: size === 'lg' \? 'none' : 'uppercase'/);
  });
});
