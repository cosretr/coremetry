// @vitest-environment jsdom
//
// v0.10.1145 — panodaki HTML → sohbet markdown'ı. Güvenlik sözleşmesi
// (htmlToMarkdown.ts başı): ayrık DOMParser belgesi, canlı DOM'a yazım yok,
// yalnız http(s) bağlantı, betik/stil/görsel düşer, boyut tavanı. Adlar sentetik.
import { describe, it, expect, afterEach } from 'vitest';
import { htmlToMarkdown, safeHref, HTML_PASTE_MAX } from './htmlToMarkdown';

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).__pwned;
});

describe('biçim', () => {
  it('başlık, kalın, italik, satır içi kod, paragraf, satır sonu', () => {
    const md = htmlToMarkdown(
      '<h2>svc-orders  durumu</h2><p>Hata <b>arttı</b>, <em>p95</em> <code>480 ms</code>.<br>İkinci satır.</p><p>Son</p>',
    );
    expect(md).toBe('## svc-orders durumu\n\nHata **arttı**, *p95* `480 ms`.\nİkinci satır.\n\nSon');
  });

  it('Google Docs: font-weight:normal sarmalayıcısı kalın DEĞİL; stil span\'ları biçim', () => {
    const md = htmlToMarkdown(
      '<b style="font-weight:normal" id="docs-internal-guid-1"><p><span style="font-weight:700">Kalın</span> ve '
      + '<span style="font-style:italic">eğik</span></p></b>',
    );
    expect(md).toBe('**Kalın** ve *eğik*');
  });

  it('pre → çitli blok; dil sınıftan (language-*, Confluence brush)', () => {
    expect(htmlToMarkdown('<p>Sorgu:</p><pre><code class="language-sql">SELECT *\n  FROM spans</code></pre>'))
      .toBe('Sorgu:\n\n```sql\nSELECT *\n  FROM spans\n```');
    expect(htmlToMarkdown('<pre class="syntaxhighlighter-pre" data-syntaxhighlighter-params="brush: java; gutter: false">int x = 1;</pre><p><b>not</b></p>'))
      .toContain('```java\nint x = 1;\n```');
  });

  it('pre gövdesindeki işaretler KAÇIRILMAZ (kod literal)', () => {
    expect(htmlToMarkdown('<pre>a * b `c`</pre><b>x</b>')).toContain('a * b `c`');
  });

  it('düzyazıdaki * ve ` kaçırılır (yanlış vurgu olmasın)', () => {
    expect(htmlToMarkdown('<p>5 * 3 = <b>15</b> ve `x`</p>')).toBe('5 \\* 3 = **15** ve \\`x\\`');
  });

  it('alıntı ve yatay çizgi', () => {
    expect(htmlToMarkdown('<blockquote><p>a</p><p><b>b</b></p></blockquote><hr><p>c</p>'))
      .toBe('> a\n>\n> **b**\n\n---\n\nc');
  });

  it('biçimsiz HTML (VS Code / düz span\'lar) → null: çağıran düz metin yolunu kullanır', () => {
    expect(htmlToMarkdown('<div style="white-space: pre;"><div><span style="color:#569cd6;">func</span> main() {}</div></div>')).toBeNull();
  });
});

describe('bağlantılar — yalnız http(s)', () => {
  it('http(s) korunur, etiket = adres ise çıplak URL', () => {
    expect(htmlToMarkdown('<p><a href="https://wiki.example.test/runbook?x=1">Runbook</a></p>'))
      .toBe('[Runbook](https://wiki.example.test/runbook?x=1)');
    expect(htmlToMarkdown('<a href="https://wiki.example.test/a">https://wiki.example.test/a</a>'))
      .toBe('https://wiki.example.test/a');
  });

  it.each([
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    ' javascript:alert(1)',
    'java\tscript:alert(1)',
    'data:text/html;base64,PHNjcmlwdD4=',
    'vbscript:msgbox(1)',
    '/relative/path',
    '//evil.example.test/x',
    '#anchor',
    'mailto:op@example.test',
  ])('%j → yalnız etiket metni', href => {
    const md = htmlToMarkdown(`<p><a href="${href.replace(/"/g, '&quot;')}">tıkla</a> <b>x</b></p>`);
    expect(md).toBe('tıkla **x**');
    expect(md).not.toMatch(/\]\(/);
  });

  it('adresteki parantezler kodlanır (bağlantı kırılmaz)', () => {
    expect(safeHref('https://wiki.example.test/a_(b)')).toBe('https://wiki.example.test/a_%28b%29');
  });

  it('etiketteki köşeli ayraçlar kaçırılır', () => {
    expect(htmlToMarkdown('<a href="https://x.example.test/">[1] kaynak</a>')).toBe('[\\[1\\] kaynak](https://x.example.test/)');
  });
});

describe('XSS vektörleri — hiçbiri çalışmaz, hiçbiri çıktıya girmez', () => {
  it('script / style / noscript / template içerikleriyle düşer', () => {
    const md = htmlToMarkdown(
      '<p><b>ok</b></p><script>window.__pwned = 1</script><style>p{color:red}</style>'
      + '<noscript>ns</noscript><template><b>tpl</b></template>',
    );
    expect(md).toBe('**ok**');
    expect((window as unknown as Record<string, unknown>).__pwned).toBeUndefined();
  });

  it('img onerror / svg onload tetiklenmez; görsel düşer, canlı DOM değişmez', () => {
    const before = document.body.innerHTML;
    const md = htmlToMarkdown(
      '<p><b>a</b><img src="x" onerror="window.__pwned=1"><svg onload="window.__pwned=2"><text>s</text></svg>'
      + '<iframe src="https://evil.example.test"></iframe></p>',
    );
    expect(md).toBe('**a**');
    expect((window as unknown as Record<string, unknown>).__pwned).toBeUndefined();
    expect(document.body.innerHTML).toBe(before);
    expect(document.querySelector('img, iframe, svg')).toBeNull();
  });

  it('on* / style nitelikleri çıktıya girmez; ham HTML metin olarak kaçırılmaz ama etiket üretmez', () => {
    const md = htmlToMarkdown('<p onclick="x()"><b onmouseover="y()">kalın</b> &lt;img src=x onerror=alert(1)&gt;</p>');
    expect(md).toBe('**kalın** <img src=x onerror=alert(1)>');
    expect(md).not.toContain('onclick');
    expect(md).not.toContain('onmouseover');
  });

  it('gizli öğeler düşer', () => {
    expect(htmlToMarkdown('<p><b>görünür</b><span style="display:none">gizli</span><span hidden>h</span></p>')).toBe('**görünür**');
  });
});

describe('listeler', () => {
  it('iç içe madde + numaralı (start), görev kutusu', () => {
    const md = htmlToMarkdown(
      '<ul><li>a<ul><li>a1</li><li>a2</li></ul></li><li>b</li></ul>'
      + '<ol start="3"><li>üç</li><li><input type="checkbox" checked>dört</li></ol>',
    );
    expect(md).toBe('- a\n  - a1\n  - a2\n- b\n\n3. üç\n4. [x] dört');
  });

  it('geçersiz ama yaygın: <ul> doğrudan <ul> içinde → önceki maddenin altı', () => {
    expect(htmlToMarkdown('<ul><li>a</li><ul><li>b</li></ul></ul>')).toBe('- a\n  - b');
  });

  it('madde içindeki paragraflar sıkı liste', () => {
    expect(htmlToMarkdown('<ol><li><p>bir</p><p>devam</p></li><li><p>iki</p></li></ol>'))
      .toBe('1. bir\n   devam\n2. iki');
  });
});

describe('tablolar → GFM', () => {
  it('başlık + ayraç + satırlar; hücrede | kaçırılır, colspan doldurulur, hizalama', () => {
    const md = htmlToMarkdown(
      '<table><thead><tr><th>Servis</th><th align="right">p95</th><th>Not</th></tr></thead>'
      + '<tbody><tr><td>svc-orders</td><td>480</td><td>a | b</td></tr>'
      + '<tr><td colspan="2"><b>toplam</b></td><td>x<br>y</td></tr></tbody></table>',
    );
    expect(md).toBe(
      '| Servis | p95 | Not |\n| --- | ---: | --- |\n| svc-orders | 480 | a \\| b |\n| **toplam** | | x y |',
    );
  });

  it('başlıksız tablo: ilk satır başlık olur; kısa satır doldurulur', () => {
    expect(htmlToMarkdown('<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>'))
      .toBe('| a | b |\n| --- | --- |\n| c | |');
  });

  it('iç içe tablo hücre metnine düzleşir; tek hücreli düzen tablosu yalnız içerik', () => {
    expect(htmlToMarkdown('<table><tr><td>x</td><td><table><tr><td>i1</td><td>i2</td></tr></table></td></tr></table>'))
      .toBe('| x | i1 i2 |\n| --- | --- |');
    expect(htmlToMarkdown('<table><tr><td><p><b>tek</b> hücre</p></td></tr></table>')).toBe('**tek** hücre');
  });
});

describe('tavanlar', () => {
  it(`${HTML_PASTE_MAX} bayt üstü → null (düz metne düşer)`, () => {
    const big = '<p><b>x</b></p>' + '<p>' + 'a'.repeat(HTML_PASTE_MAX) + '</p>';
    expect(htmlToMarkdown(big)).toBeNull();
  });
  it('derin iç içe yapı yığın taşırmaz', () => {
    const deep = '<div>'.repeat(5000) + '<b>dip</b>' + '</div>'.repeat(5000);
    expect(htmlToMarkdown(deep)).toContain('dip');
  });
  it('boş / yalnız görsel → null', () => {
    expect(htmlToMarkdown('')).toBeNull();
    expect(htmlToMarkdown('<p><img src="x"></p>')).toBeNull();
  });
});
