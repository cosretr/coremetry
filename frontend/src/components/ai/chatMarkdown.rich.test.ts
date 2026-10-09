// v0.10.1137 — sohbet çözücüsünün "Claude gibi" yapıları (SAF): iç içe liste,
// başlangıç numarası, görev listesi, alıntı, çizgi, uyarı kutuları (GFM + TR),
// özet, <details>, dosya bloğu bilgi dizesi, indirme adı süzgeci, katlama
// eşiği, diff satırları, tanım satırları, düz metin kopyası. Her yapıda XSS
// vektörü METİN olarak kalır (çizim React çocuğu; burada yalnız veri).
import { describe, expect, it } from 'vitest';
import {
  CODE_COLLAPSE_LINES, calloutVariant, chatPlainText, codeLineCount, definitionRows, diffLineKind,
  parseChatBlocks, parseFenceInfo, sanitizeFileName, type ChatBlock,
} from './chatMarkdown';

const XSS = '<img src=x onerror=alert(1)>';
type B<K extends ChatBlock['kind']> = Extract<ChatBlock, { kind: K }>;
const first = <K extends ChatBlock['kind']>(bs: ChatBlock[], k: K) => bs.find(b => b.kind === k) as B<K>;

describe('liste — iç içe, başlangıç, görev', () => {
  it('girintili madde alt listeye gider (iki kademe)', () => {
    const l = first(parseChatBlocks('- a\n  - a1\n  - a2\n- b\n'), 'list');
    expect(l.items).toEqual(['a', 'b']);
    expect(l.nested?.[0]).toEqual([{ kind: 'list', ordered: false, items: ['a1', 'a2'] }]);
    expect(l.nested?.[1]).toBeNull();
  });
  it('sıralı listede alt madde işaretli liste ve kod çiti', () => {
    const l = first(parseChatBlocks('1. kur\n   ```sh\n   make\n   ```\n2. çalıştır\n   - not\n'), 'list');
    expect(l.ordered).toBe(true);
    expect(l.items).toEqual(['kur', 'çalıştır']);
    expect(l.nested?.[0]?.[0]).toMatchObject({ kind: 'code', lang: 'sh', code: 'make', open: false });
    expect(l.nested?.[1]?.[0]).toMatchObject({ kind: 'list', ordered: false, items: ['not'] });
  });
  it('N ile başlayan sıralı liste start taşır; 1 ile başlayan taşımaz', () => {
    expect(first(parseChatBlocks('3. üç\n4. dört\n'), 'list').start).toBe(3);
    expect(first(parseChatBlocks('1. bir\n'), 'list').start).toBeUndefined();
  });
  it('boş satır gevşek listeyi kesmez', () => {
    expect(first(parseChatBlocks('- a\n\n- b\n'), 'list').items).toEqual(['a', 'b']);
  });
  it('görev listesi: [ ] / [x] → tasks, işaret metinden düşer', () => {
    const l = first(parseChatBlocks('- [x] bitti\n- [ ] kaldı\n- düz\n'), 'list');
    expect(l.items).toEqual(['bitti', 'kaldı', 'düz']);
    expect(l.tasks).toEqual([true, false, null]);
  });
});

describe('alıntı / çizgi / uyarı / özet', () => {
  it('> alıntı yeniden çözülür; --- çizgi', () => {
    const bs = parseChatBlocks('> **dikkat** et\n> - madde\n\n---\n');
    const q = first(bs, 'quote');
    expect(q.blocks.map(b => b.kind)).toEqual(['text', 'list']);
    expect(bs.some(b => b.kind === 'hr')).toBe(true);
  });
  it.each([
    ['NOTE', 'note'], ['TIP', 'tip'], ['WARNING', 'warning'], ['IMPORTANT', 'important'], ['CAUTION', 'caution'],
    ['NOT', 'note'], ['İPUCU', 'tip'], ['UYARI', 'warning'], ['ÖNEMLİ', 'important'], ['DİKKAT', 'caution'], ['ÖZET', 'summary'],
  ])('> [!%s] → %s uyarı kutusu', (tag, variant) => {
    const c = first(parseChatBlocks(`> [!${tag}]\n> gövde\n`), 'callout');
    expect(c).toMatchObject({ variant, title: '' });
    expect(c.blocks).toEqual([{ kind: 'text', text: 'gövde' }]);
  });
  it('tanınmayan tür düz alıntı kalır', () => {
    expect(parseChatBlocks('> [!FOO]\n> x\n')[0].kind).toBe('quote');
    expect(calloutVariant('xyz')).toBeNull();
  });
  it('uyarı başlığındaki HTML metin olarak kalır (çözülmez)', () => {
    const c = first(parseChatBlocks(`> [!WARNING] ${XSS}\n> x\n`), 'callout');
    expect(c.title).toBe(XSS);
  });
  it('en üstteki **Özet:** satırı özet kutusu olur; ortadaki olmaz', () => {
    const bs = parseChatBlocks('**Özet:** Pipeline Jenkins\'te.\n\nAyrıntı burada.\n');
    expect(bs[0]).toEqual({ kind: 'callout', variant: 'summary', title: '', blocks: [{ kind: 'text', text: "Pipeline Jenkins'te." }] });
    expect(bs[1]).toEqual({ kind: 'text', text: 'Ayrıntı burada.' });
    expect(parseChatBlocks('giriş\n\n**Özet:** x\n')[0].kind).toBe('text');
  });
});

describe('<details> — yalnız iki etiket', () => {
  it('özet + gövde (markdown çözülür)', () => {
    const d = first(parseChatBlocks('<details>\n<summary>Komutlar</summary>\n\n- a\n</details>\nson\n'), 'details');
    expect(d).toMatchObject({ summary: 'Komutlar', closed: true, open: false });
    expect(d.blocks[0]).toMatchObject({ kind: 'list', items: ['a'] });
  });
  it('tek satır biçimi + open bayrağı', () => {
    expect(first(parseChatBlocks('<details open><summary>S</summary>\nx\n</details>\n'), 'details')).toMatchObject({ summary: 'S', open: true });
  });
  it('özetteki ve gövdedeki başka HTML METİN kalır', () => {
    const d = first(parseChatBlocks(`<details>\n<summary>${XSS}</summary>\n<script>alert(1)</script>\n</details>\n`), 'details');
    expect(d.summary).toBe(XSS);
    expect(d.blocks).toEqual([{ kind: 'text', text: '<script>alert(1)</script>' }]);
  });
  it('kapanmamış details içeriği yutmaz (closed:false)', () => {
    expect(first(parseChatBlocks('<details>\n<summary>S</summary>\nyarım'), 'details')).toMatchObject({ closed: false });
  });
  it('başka etiketler details sayılmaz', () => {
    expect(parseChatBlocks('<div>x</div>\n')).toEqual([{ kind: 'text', text: '<div>x</div>' }]);
  });
});

describe('dosya bloğu', () => {
  it.each([
    ['yaml title=deploy/app.yaml', { lang: 'yaml', title: 'deploy/app.yaml' }],
    ['yaml title="deploy/my app.yaml"', { lang: 'yaml', title: 'deploy/my app.yaml' }],
    ['yaml:deploy/app.yaml', { lang: 'yaml', title: 'deploy/app.yaml' }],
    ['sql', { lang: 'sql' }],
    ['', { lang: '' }],
  ])('bilgi dizesi %j', (info, want) => {
    expect(parseFenceInfo(info)).toEqual(want);
  });
  it('bilgi dizesindeki XSS: dil süzülür, başlık METİN kalır', () => {
    const r = parseFenceInfo(`"><script>alert(1)</script> title=${XSS}`);
    expect(r.lang).toBe('scriptalert1script');
    const c = first(parseChatBlocks('```yaml title="<img onerror=x>.yaml"\na: 1\n```\n'), 'code');
    expect(c.title).toBe('<img onerror=x>.yaml');
    expect(c.lang).toBe('yaml');
  });
  it.each([
    ['deploy/app.yaml', 'yaml', 'app.yaml'],
    ['../../etc/passwd', 'txt', 'passwd.txt'],
    ['C:\\Windows\\evil.bat', '', 'evil.bat.txt'],
    ['.bashrc', 'sh', 'bashrc.txt'],
    // v0.10.1137 inceleme — uzantı izin listesi; çalıştırılabilir HER ZAMAN .txt
    ['run.ps1', 'powershell', 'run.ps1.txt'],
    ['app.js', 'js', 'app.js.txt'],
    ['page.HTML', 'html', 'page.HTML.txt'],
    ['icon.svg', '', 'icon.svg.txt'],
    ['setup.exe', '', 'setup.exe.txt'],
    ['notes.log', '', 'notes.log'],
    ['data.csv', '', 'data.csv'],
    ['fix.patch', 'diff', 'fix.patch'],
    ['nginx.conf', '', 'nginx.conf'],
    ['main.tf', 'hcl', 'main.tf'],
    ['weird.xyz', '', 'weird.xyz.txt'],
    ['Dockerfile', 'dockerfile', 'Dockerfile'],
    ['', 'js', 'snippet.js.txt'],
    ['', 'html', 'snippet.html.txt'],
    ['<img onerror=x>.yaml', 'yaml', 'img_onerror_x_.yaml'],
    ['şema düzeni.sql', 'sql', 'sema_duzeni.sql'],
    ['..', 'yaml', 'snippet.yaml'],
    ['', 'go', 'snippet.go'],
    [undefined, 'weird', 'snippet.txt'],
    ['CON', 'sh', 'snippet.sh'],
    ['a'.repeat(300) + '.txt', '', 'a'.repeat(100) + '.txt'],
  ])('indirme adı süzgeci %j (%s) → %s', (title, lang, want) => {
    expect(sanitizeFileName(title as string | undefined, lang)).toBe(want);
  });
  it('katlama eşiği: 25 satır katlanmaz, 26 katlanır', () => {
    const n = (k: number) => Array.from({ length: k }, (_, i) => `l${i}`).join('\n');
    expect(codeLineCount(n(CODE_COLLAPSE_LINES))).toBe(25);
    expect(codeLineCount(n(26)) > CODE_COLLAPSE_LINES).toBe(true);
    expect(codeLineCount('')).toBe(0);
  });
  it('diff satırları', () => {
    expect(['+a', '-b', '@@ -1 +1 @@', '--- a/x', '+++ b/x', ' c'].map(diffLineKind))
      .toEqual(['add', 'del', 'hunk', 'meta', 'meta', null]);
  });
});

// v0.10.1137 inceleme — derinlik tavanı: kötü niyetli / bozuk iç içe yapı
// özyinelemeyi taşırmaz, düz metne düşer.
describe('derinlik tavanı', () => {
  it("'>'.repeat(3000) atmaz, blok üretir", () => {
    const bs = parseChatBlocks('>'.repeat(3000) + ' x\n');
    expect(bs.length).toBe(1);
    let d = 0; let b: ChatBlock | undefined = bs[0];
    while (b && b.kind === 'quote') { d++; b = b.blocks[0]; }
    expect(d).toBeLessThanOrEqual(8);
    expect(b?.kind).toBe('text');
  });
  it('2000 kademeli liste atmaz', () => {
    const md = Array.from({ length: 2000 }, (_, i) => `${'  '.repeat(i)}- m${i}`).join('\n') + '\n';
    const bs = parseChatBlocks(md);
    expect(bs[0].kind).toBe('list');
    expect(chatPlainText(md)).toContain('m1999');
  });
  it("'<details>\\n'.repeat(5000) atmaz", () => {
    const bs = parseChatBlocks('<details>\n'.repeat(5000));
    expect(bs[0].kind).toBe('details');
  });
});

describe('başlık ve tanım satırları', () => {
  it('h1–h4 kademesi', () => {
    expect(parseChatBlocks('#### d\n')).toEqual([{ kind: 'heading', level: 4, text: 'd' }]);
  });
  it('"**Anahtar:** değer" paragrafı tanım satırları; karışık paragraf değil', () => {
    expect(definitionRows('**Servis:** checkout\n**Durum**: sağlıklı')).toEqual([
      { key: 'Servis', value: 'checkout' }, { key: 'Durum', value: 'sağlıklı' },
    ]);
    expect(definitionRows('**Servis:** checkout\nserbest cümle')).toBeNull();
  });
});

describe('chatPlainText (kopyala)', () => {
  it('işaretler düşer, link adresiyle, liste/görev/tablo düz', () => {
    const md = '## Başlık\n**kalın** ve [Jenkins](https://ci.example.test/job/a) [1]\n\n- [x] bitti\n1. bir\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```sh\nmake\n```\n';
    expect(chatPlainText(md)).toBe('Başlık\n\nkalın ve Jenkins (https://ci.example.test/job/a) [1]\n\n[x] bitti\n\n1. bir\n\na\tb\n1\t2\n\nmake');
  });
  it('görsel yalnız alt metin; javascript: link etiketi + adres düz metin', () => {
    expect(chatPlainText('![logo](https://x.example.test/a.png) yazı')).toBe('logo yazı');
  });
});
