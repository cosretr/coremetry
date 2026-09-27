// singleMonoStack.pin — v0.10.973 (tablo standardı dilim 6, T4/T5).
//
// Operatör onayı: docs/DECISIONS.md "Tablo standardı". tableUnityRatchet
// yalnız TOPLAMI tavanlar; başka bir dosyadaki düşüş buradaki bir geri
// dönüşü örtebilir. Bu çivi dilim 6'nın bu altı dosyadaki göçünü tutar:
//   • T5 — satır içi monospace yığını yok; yüz `var(--font-mono)`dan.
//     CopilotExplain'in eski `var(--mono, monospace)`ı TANIMSIZ değişkene
//     düşüp genel `monospace`i basıyordu (undefinedCssRefs fallback'li
//     `var()`ı güvenli sayar, yakalamaz).
//   • T4 — sohbet tablolarının sayı hücreleri `num` (arayüz fontu +
//     tabular-nums); `num mono` geri gelmez.
//   • T5/T11 — ChatBubble adım tablosunun durum hücresi satır içi
//     `whiteSpace: nowrap` taşımıyor; tek satırlığı `tbody td`den gelir.
//     O CSS varsayımı da burada çivili: bir `.cm-steps-t td` kuralı sarmayı
//     açarsa rozetler alt alta düşer.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';

const SRC = resolve(__dirname, '..');
// Yorumları at: gerekçe yorumları eski yazımı (`var(--mono, monospace)`) anıyor.
// Satır yorumu yalnız `:`/tırnak sonrası değilse (URL dizgileri kalsın).
const read = (p: string) => readFileSync(resolve(SRC, p), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .split('\n').map(l => l.replace(/(^|[^:'"`])\/\/.*$/, '$1')).join('\n');

// tableUnityRatchet `inlineMonoStack` ile aynı desen.
const INLINE_MONO = /\bfont(?:Family)?:\s*(['"`])[^'"`\n]*monospace[^'"`\n]*\1/g;
const FONT_MONO = /fontFamily:\s*'var\(--font-mono\)'/g;

// dosya → dilim 6'da `var(--font-mono)`a taşınan yer sayısı (en az)
const TOKENISED: Record<string, number> = {
  'components/CommandPalette.tsx': 2, // eylem rozeti + sonuç türü rozeti (10px kalır)
  'components/CopilotExplain.tsx': 2, // ilk cevap + kod incelemesi dosya satırları
  'pages/Slos.tsx': 1,                // burn analizi modalı özet satırı (11px kalır)
  'components/ai/ChatBubble.tsx': 1,  // araç adımı çipi
};
const NUM_CELLS: Record<string, number> = {
  'components/ai/ChatTraceList.tsx': 2, // süre, span
  'components/ai/EvidenceCard.tsx': 2,  // şimdi, taban
};
const FILES = [...Object.keys(TOKENISED), ...Object.keys(NUM_CELLS)];

describe('T5 — tek monospace yığını (dilim 6 dosyaları)', () => {
  for (const f of FILES) {
    it(`${f}: satır içi monospace yığını ve tanımsız --mono yok`, () => {
      const s = read(f);
      expect(s.match(INLINE_MONO) ?? []).toEqual([]);
      expect(s).not.toMatch(/var\(\s*--mono\b/);
    });
  }
  for (const [f, n] of Object.entries(TOKENISED)) {
    it(`${f}: mono yüz silinmedi, token'a taşındı (≥${n})`, () => {
      expect(read(f).match(FONT_MONO)?.length ?? 0).toBeGreaterThanOrEqual(n);
    });
  }
});

describe('T4 — sohbet tablolarında sayı arayüz fontunda', () => {
  for (const [f, n] of Object.entries(NUM_CELLS)) {
    it(`${f}: \`num mono\` yok, sayı hücreleri \`num\``, () => {
      const s = read(f);
      expect(s).not.toMatch(/\b(num mono|mono num)\b/);
      const numTds = jsxOpenTags(s, 'td').filter(t => /\sclassName="num"/.test(t.tag));
      expect(numTds.length).toBe(n);
    });
  }
});

describe('T5/T11 — ChatBubble adım tablosu: tek satırlık CSS\'ten', () => {
  it('ChatBubble\'da satır içi stilli <td> yok', () => {
    const tds = jsxOpenTags(read('components/ai/ChatBubble.tsx'), 'td');
    expect(tds.length).toBeGreaterThan(0);
    expect(tds.filter(t => /\sstyle=\{/.test(t.tag)).map(t => t.line)).toEqual([]);
  });

  it('`tbody td` nowrap; hiçbir `.cm-steps*` hücre kuralı sarmayı açmıyor', () => {
    const css = readFileSync(resolve(SRC, 'styles/globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
    const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
      sels: m[1].split(',').map(x => x.trim().replace(/\s+/g, ' ')),
      body: m[2],
    }));
    const base = rules.find(r => r.sels.includes('tbody td'));
    expect(base?.body).toMatch(/white-space:\s*nowrap/);
    const opened = rules.filter(r =>
      r.sels.some(x => /\.cm-steps[\w-]*\b.*\btd\b/.test(x))
      && [...r.body.matchAll(/white-space:\s*([\w-]+)/g)].some(m => m[1] !== 'nowrap'));
    expect(opened.map(r => r.sels.join(', '))).toEqual([]);
  });
});
