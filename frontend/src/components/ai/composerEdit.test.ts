// v0.10.1145 — composer markdown düzenleme yardımcıları (SAF). Her araç çubuğu
// düğmesi / kısayol bir TextEdit üretir; burada metin + seçim sonucu çivilenir.
// Seçim gösterimi: `[` `]` seçimin başı/sonu, `|` imleç (boş seçim).
import { describe, it, expect } from 'vitest';
import {
  applyEdit, continueList, fenceAt, inCodeFence, indentList, inlineActive, isHttpUrl, isNoopEdit,
  linkEdit, listItemAt, toggleCodeBlock, toggleInline, toggleLinePrefix, type EditState, type TextEdit,
} from './composerEdit';

/** "a[bc]d" / "ab|cd" → EditState. */
function st(marked: string): EditState {
  const caret = marked.indexOf('|');
  if (caret >= 0) {
    const value = marked.slice(0, caret) + marked.slice(caret + 1);
    return { value, start: caret, end: caret };
  }
  const a = marked.indexOf('[');
  const b = marked.indexOf(']', a);
  const value = marked.slice(0, a) + marked.slice(a + 1, b) + marked.slice(b + 1);
  return { value, start: a, end: b - 1 };
}

/** EditState → "a[bc]d" / "ab|cd". */
function show(s: EditState): string {
  if (s.start === s.end) return s.value.slice(0, s.start) + '|' + s.value.slice(s.start);
  return s.value.slice(0, s.start) + '[' + s.value.slice(s.start, s.end) + ']' + s.value.slice(s.end);
}

const run = (marked: string, f: (s: EditState) => TextEdit | null) => {
  const s = st(marked);
  const e = f(s);
  return e ? show(applyEdit(s.value, e)) : null;
};

describe('toggleInline — aç/kapa, çift sarma yok', () => {
  it.each([
    // [başlangıç, işaret, sonuç]
    ['a [foo] b', 'bold', 'a **[foo]** b'],
    ['a **[foo]** b', 'bold', 'a [foo] b'], // işaret seçimin DIŞINDA → kaldır
    ['a [**foo**] b', 'bold', 'a [foo] b'], // işaret seçimin İÇİNDE → kaldır
    ['a [foo] b', 'italic', 'a *[foo]* b'],
    ['a *[foo]* b', 'italic', 'a [foo] b'],
    ['a _[foo]_ b', 'italic', 'a [foo] b'],
    ['a [foo] b', 'code', 'a `[foo]` b'],
    ['a `[foo]` b', 'code', 'a [foo] b'],
    // kalın + italik birlikte; biri kaldırılınca diğeri kalır
    ['**[foo]**', 'italic', '***[foo]***'],
    ['***[foo]***', 'bold', '*[foo]*'],
    ['***[foo]***', 'italic', '**[foo]**'],
    ['*[foo]*', 'bold', '***[foo]***'],
    // boşluk işaretin DIŞINDA kalır
    ['a[ foo ]b', 'bold', 'a **[foo]** b'],
  ] as const)('%s + %s → %s', (from, mark, want) => {
    expect(run(from, s => toggleInline(s, mark))).toBe(want);
  });

  it('iki kez uygulamak başa döndürür (çift sarma yok)', () => {
    for (const mark of ['bold', 'italic', 'code'] as const) {
      const s0 = st('x [svc-orders] y');
      const once = applyEdit(s0.value, toggleInline(s0, mark));
      const twice = applyEdit(once.value, toggleInline(once, mark));
      expect(show(twice), mark).toBe('x [svc-orders] y');
    }
  });

  it('imleç: boş çift eklenir, imleç ortada; tekrar basınca çift kalkar', () => {
    expect(run('ab|', s => toggleInline(s, 'bold'))).toBe('ab**|**');
    expect(run('ab**|**', s => toggleInline(s, 'bold'))).toBe('ab|');
    expect(run('ab*|*', s => toggleInline(s, 'italic'))).toBe('ab|');
    expect(run('ab`|`', s => toggleInline(s, 'code'))).toBe('ab|');
    // boş kalın çiftinin içinde italik: kalını silmez, italik ekler
    expect(run('**|**', s => toggleInline(s, 'italic'))).toBe('***|***');
  });

  it('çok satırlı seçim: her dolu satır ayrı, liste öneki dışarıda; hepsi sarılıysa kaldırır', () => {
    expect(run('[- a\n- b\n\nc]', s => toggleInline(s, 'bold'))).toBe('[- **a**\n- **b**\n\n**c**]');
    expect(run('[- **a**\n- **b**]', s => toggleInline(s, 'bold'))).toBe('[- a\n- b]');
    // karışık: yalnız sarılı olmayan sarılır (çift sarma yok)
    expect(run('[**a**\nb]', s => toggleInline(s, 'bold'))).toBe('[**a**\n**b**]');
  });

  it('inlineActive: seçim/imleç işaretin içindeyken true', () => {
    expect(inlineActive(st('a **[foo]** b'), 'bold')).toBe(true);
    expect(inlineActive(st('a **fo|o** b'), 'bold')).toBe(true);
    expect(inlineActive(st('a [foo] b'), 'bold')).toBe(false);
    expect(inlineActive(st('a **[foo]** b'), 'italic')).toBe(false);
    expect(inlineActive(st('a `x|y` b'), 'code')).toBe(true);
  });
});

describe('toggleLinePrefix — tam satırlar', () => {
  it.each([
    ['fo|o', 'bullet', '- fo|o'],
    ['- fo|o', 'bullet', 'fo|o'],
    ['[a\nb\nc]', 'ordered', '[1. a\n2. b\n3. c]'],
    ['[1. a\n2. b]', 'ordered', '[a\nb]'],
    ['[- a\n- b]', 'ordered', '[1. a\n2. b]'], // madde → numaralı dönüşümü
    ['[1. a\n2. b]', 'bullet', '[- a\n- b]'],
    ['[a\nb]', 'quote', '[> a\n> b]'],
    ['[> a\n> b]', 'quote', '[a\nb]'],
    ['|', 'bullet', '- |'],
    ['  - x|', 'bullet', '  x|'], // girinti korunur
  ] as const)('%j + %s → %j', (from, kind, want) => {
    expect(run(from, s => toggleLinePrefix(s, kind))).toBe(want);
  });

  it('seçim satırın ortasında başlasa da tüm satır', () => {
    expect(run('ab[c\nd]e', s => toggleLinePrefix(s, 'bullet'))).toBe('[- abc\n- de]');
  });

  it('boş satırlar öneksiz kalır', () => {
    expect(run('[a\n\nb]', s => toggleLinePrefix(s, 'bullet'))).toBe('[- a\n\n- b]');
  });
});

describe('toggleCodeBlock', () => {
  it('satırları çitle sarar, seçim gövdede', () => {
    expect(run('x\n[SELECT 1\nFROM t]\ny', s => toggleCodeBlock(s))).toBe('x\n```\n[SELECT 1\nFROM t]\n```\ny');
  });
  it('boş satırda boş blok, imleç içinde', () => {
    expect(run('|', s => toggleCodeBlock(s))).toBe('```\n|\n```');
  });
  it('dil etiketi', () => {
    expect(run('[{}]', s => toggleCodeBlock(s, 'json'))).toBe('```json\n[{}]\n```');
  });
  it('imleç çitin içindeyse çitler kalkar (aç/kapa)', () => {
    expect(run('x\n```sql\nSELE|CT 1\n```\ny', s => toggleCodeBlock(s))).toBe('x\nSELE|CT 1\ny');
    // kapanmamış çit
    expect(run('```\na|b', s => toggleCodeBlock(s))).toBe('a|b');
  });
});

describe('linkEdit (Ctrl/Cmd+K)', () => {
  it('seçim URL ise <url> (istemsiz)', () => {
    expect(run('bkz [https://wiki.example.test/a?b=1] x', linkEdit)).toBe('bkz <[https://wiki.example.test/a?b=1]> x');
  });
  it('seçim metin ise [metin](|) — imleç adres yerinde', () => {
    expect(run('bkz [runbook] x', linkEdit)).toBe('bkz [runbook](|) x');
  });
  it('boş seçim: [|]() — önce etiket', () => {
    expect(run('a |', linkEdit)).toBe('a [|]()');
  });
  it('mevcut bağlantının içinde: bağlantı kalkar (aç/kapa)', () => {
    expect(run('a [run|book](https://wiki.example.test/r) b', linkEdit)).toBe('a [runbook] b');
    expect(run('a <https://wiki.exa|mple.test> b', linkEdit)).toBe('a [https://wiki.example.test] b');
  });
  it('isHttpUrl yalnız http(s)', () => {
    expect(isHttpUrl('https://example.test/x')).toBe(true);
    expect(isHttpUrl('javascript:alert(1)')).toBe(false);
    expect(isHttpUrl('https://example.test/a b')).toBe(false);
    expect(isHttpUrl('ftp://example.test')).toBe(false);
  });
});

describe('continueList (Enter listede)', () => {
  it.each([
    ['- a|', '- a\n- |'],
    ['* a|', '* a\n* |'],
    ['1. a|', '1. a\n2. |'],
    ['9) a|', '9) a\n10) |'],
    ['  - a|', '  - a\n  - |'],
    ['- [x] bitti|', '- [x] bitti\n- [ ] |'],
    ['- ab|cd', '- ab\n- |cd'],
  ])('%j → %j', (from, want) => {
    expect(run(from, continueList)).toBe(want);
  });
  it('boş madde listeyi bitirir (işaret silinir)', () => {
    expect(run('- a\n- |', continueList)).toBe('- a\n|');
    expect(run('1. a\n2. |', continueList)).toBe('1. a\n|');
  });
  it('liste dışında / seçim varken null', () => {
    expect(continueList(st('düz metin|'))).toBeNull();
    expect(continueList(st('- [a]b'))).toBeNull();
    expect(continueList(st('-|'))).toBeNull(); // "-" tek başına madde değil
  });
  it('listItemAt numara + görev', () => {
    const it0 = listItemAt('x\n  3. [ ] iş', 6);
    expect(it0).toMatchObject({ ordered: true, num: 3, delim: '.', task: true, indent: '  ', content: 'iş' });
  });
});

describe('indentList (Tab / Shift+Tab)', () => {
  it('madde: kardeşin işaret genişliği kadar içeri', () => {
    expect(run('- a\n- b|', s => indentList(s, 1))).toBe('- a\n  - b|');
  });
  it('numaralı: 3 boşluk ve alt liste 1\'den başlar', () => {
    expect(run('1. a\n2. b|', s => indentList(s, 1))).toBe('1. a\n   1. b|');
  });
  it('dışarı: ebeveyn girintisine', () => {
    expect(run('1. a\n   - b|', s => indentList(s, -1))).toBe('1. a\n- b|');
    expect(run('- a\n  - b|', s => indentList(s, -1))).toBe('- a\n- b|');
  });
  it('çok satır: seçilen tüm maddeler', () => {
    expect(run('- a\n[- b\n- c]', s => indentList(s, 1))).toBe('- a\n[  - b\n  - c]');
  });
  it('en dışta Shift+Tab: değişmeyen düzenleme (odak kalır)', () => {
    const e = indentList(st('- a|'), -1);
    expect(e).not.toBeNull();
    expect(isNoopEdit(e!)).toBe(true);
  });
  it('liste dışında null (Tab odak gezinmesine kalır)', () => {
    expect(indentList(st('düz|'), 1)).toBeNull();
  });
});

describe('fenceAt / inCodeFence — imleç kod çitinde mi', () => {
  const doc = 'önce\n```json\n{"a": 1}\n```\nsonra';
  it.each([
    [0, false], // "önce"
    [doc.indexOf('```json') + 7, true], // açılış satırı sonu: blok açılıyor
    [doc.indexOf('{') + 3, true],
    [doc.lastIndexOf('```'), true], // kapanış çitinden önce
    [doc.lastIndexOf('```') + 3, false], // kapanış çitinden sonra
    [doc.length, false],
  ])('konum %i → %s', (pos, want) => {
    expect(inCodeFence(doc, pos)).toBe(want);
  });
  it('kapanmamış çit sona dek içeride', () => {
    const v = 'a\n```\nkod\nhâlâ kod';
    expect(inCodeFence(v, v.length)).toBe(true);
    expect(fenceAt(v, v.length)).toMatchObject({ inside: true, closed: false });
  });
  it('iki blok: aradaki metin dışarıda', () => {
    const v = '```\na\n```\narada\n```\nb\n```';
    expect(inCodeFence(v, v.indexOf('arada') + 2)).toBe(false);
    expect(inCodeFence(v, v.indexOf('b'))).toBe(true);
  });
});
