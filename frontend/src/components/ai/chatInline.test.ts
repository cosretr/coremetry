// v0.10.1137 — satır içi çözücü (SAF) + link güvenlik kararı (chatLinks).
import { describe, expect, it } from 'vitest';
import { inlinePlain, parseInline, type InlineNode } from './chatInline';
import { buildLinkPolicy, classifyLink, normalizeUrl, toAppPath, trimUrlPunct } from './chatLinks';

const links = (s: string) => parseInline(s).filter((n): n is Extract<InlineNode, { t: 'link' }> => n.t === 'link');

describe('parseInline', () => {
  it('çıplak URL: sondaki noktalama ve dengesiz parantez dışarıda', () => {
    expect(links('bkz. https://jenkins.example.test/job/a/.').map(l => l.href)).toEqual(['https://jenkins.example.test/job/a/']);
    expect(links('(https://ci.example.test/x)').map(l => l.href)).toEqual(['https://ci.example.test/x']);
    expect(links('https://w.example.test/a_(b)').map(l => l.href)).toEqual(['https://w.example.test/a_(b)']);
    expect(links('https://a.example.test/x').every(l => l.label === null)).toBe(true);
  });
  it('markdown link: etiket düğümleri, iç içe vurgu', () => {
    const [l] = links('[**Jenkins** işi](https://ci.example.test/job/a "başlık")');
    expect(l.href).toBe('https://ci.example.test/job/a');
    expect(l.label).toEqual([{ t: 'strong', c: [{ t: 'text', v: 'Jenkins' }] }, { t: 'text', v: ' işi' }]);
  });
  it('javascript: / data: hedefleri yine düğüm — karar çizimde (blocked)', () => {
    expect(links('[tık](javascript:alert(1))')[0]?.href).toBe('javascript:alert(1');
    expect(links('[x](data:text/html,<b>)')[0]?.href.startsWith('data:')).toBe(true);
    // çıplak yalnız http(s): javascript: hiç link olmaz
    expect(links('javascript:alert(1)')).toEqual([]);
  });
  it('görseller ASLA düğüm değil: yalnız alt metin', () => {
    expect(parseInline('![logo](https://x.example.test/a.png) son')).toEqual([{ t: 'text', v: 'logo son' }]);
    expect(parseInline('![](https://x.example.test/a.png)')).toEqual([]);
  });
  it('ham HTML metin kalır', () => {
    expect(parseInline('<script>alert(1)</script>')).toEqual([{ t: 'text', v: '<script>alert(1)</script>' }]);
  });
  it('[n] atıf düğümü; [1, 2] iki atıf; [n](url) link', () => {
    expect(parseInline('a [2] b')).toEqual([{ t: 'text', v: 'a ' }, { t: 'cite', n: 2 }, { t: 'text', v: ' b' }]);
    expect(parseInline('[1, 3]').filter(n => n.t === 'cite')).toHaveLength(2);
    expect(parseInline('[1](https://a.example.test)')[0].t).toBe('link');
  });
  it('italik / üstü çizili / tuş kapağı; snake_case italik değil', () => {
    expect(parseInline('*eğik* ~~eski~~ [[Ctrl+C]]')).toEqual([
      { t: 'em', c: [{ t: 'text', v: 'eğik' }] }, { t: 'text', v: ' ' },
      { t: 'del', c: [{ t: 'text', v: 'eski' }] }, { t: 'text', v: ' ' }, { t: 'kbd', v: 'Ctrl+C' },
    ]);
    expect(parseInline('my_var_name ve 2 * 3 * 4')).toEqual([{ t: 'text', v: 'my_var_name ve 2 * 3 * 4' }]);
    expect(parseInline('[[1], [2]]').some(n => n.t === 'kbd')).toBe(false);
  });
  it('kod içinde işaret çözülmez; kaçış literal', () => {
    expect(parseInline('`**a** https://x.example.test`')).toEqual([{ t: 'code', v: '**a** https://x.example.test' }]);
    expect(parseInline('\\*yıldız\\*')).toEqual([{ t: 'text', v: '*yıldız*' }]);
  });
  it('düz metin: link etiket (adres)', () => {
    expect(inlinePlain(parseInline('[Jenkins](https://ci.example.test) ve https://b.example.test'))).toBe('Jenkins (https://ci.example.test) ve https://b.example.test');
  });
});

describe('chatLinks — güvenlik kararı', () => {
  const policy = buildLinkPolicy({
    allowedLinks: ['https://jenkins.example.test/job/orders/', '/service?service=a'],
    sources: [{ doc: 'Wiki · Runbook', ref: 'https://devops.example.test/wiki?pagePath=%2FA', chunk: 1, score: 1 }],
    links: [{ label: 'Servis', href: '/service?service=a' }],
    origin: 'https://coremetry.example.test',
  });
  it('allowedLinks tam eşleşmesi (host harf-duyarsız, sondaki noktalama, parça yok sayılır)', () => {
    expect(classifyLink('https://JENKINS.example.test/job/orders/', policy)).toBe('allowed');
    expect(classifyLink('https://jenkins.example.test/job/orders/.', policy)).toBe('allowed');
    expect(classifyLink('https://jenkins.example.test/job/orders/#x', policy)).toBe('allowed');
  });
  it('kaynak host\'u allowlist: aynı host başka yol "allowed-host" (tam eşleşme değil)', () => {
    expect(classifyLink('https://devops.example.test/other/page', policy)).toBe('allowed-host');
  });
  it('çok kiracılı host: ilk yol parçası da tutmalı', () => {
    const mt = buildLinkPolicy({ sources: [{ doc: 'Wiki', ref: 'https://dev.azure.com/acme/Platform/_wiki/wikis/x', chunk: 1, score: 1 }] });
    expect(classifyLink('https://dev.azure.com/acme/Other/_git/repo', mt)).toBe('allowed-host');
    expect(classifyLink('https://dev.azure.com/attacker/x?d=secret', mt)).toBe('unverified');
    // aynı host'ta farklı ilk parçalı kaynaklar → yine parça şartı
    const two = buildLinkPolicy({ links: [
      { label: 'a', href: 'https://git.example.test/team-a/r' }, { label: 'b', href: 'https://git.example.test/team-b/r' },
    ] });
    expect(classifyLink('https://git.example.test/team-a/other', two)).toBe('allowed-host');
    expect(classifyLink('https://git.example.test/evil/x', two)).toBe('unverified');
  });
  it('bağlamdaki URL\'nin host\'u allowlist DEĞİL — exfil sorgu dizesi doğrulanmamış', () => {
    expect(classifyLink('https://jenkins.example.test/job/orders/?q=gizli', policy)).toBe('unverified');
    expect(classifyLink('https://attacker.example.test/?d=secret', policy)).toBe('unverified');
  });
  it('göreli ve aynı köken yol → relative', () => {
    expect(classifyLink('/traces?service=a', policy)).toBe('relative');
    expect(classifyLink('https://coremetry.example.test/problems', policy)).toBe('relative');
    expect(toAppPath('https://coremetry.example.test/problems?x=1', 'https://coremetry.example.test')).toBe('/problems?x=1');
  });
  // v0.10.1137 inceleme (HIGH) — aynı-köken kılığında dış adres.
  it.each([
    'https://coremetry.example.test//evil.example.com/?q=s',
    'https://COREMETRY.EXAMPLE.TEST//evil.example.com/?q=s',
    'https://coremetry.example.test/\\evil.example.com',
  ])('aynı köken ama yol dışarı açılır: %s → SPA linki DEĞİL', u => {
    expect(classifyLink(u, policy)).not.toBe('relative');
    expect(toAppPath(u, 'https://coremetry.example.test')).toBeNull();
  });
  it.each(['/%5Cevil.example.com', '/%2F%2Fevil.example.com', '/%2f%2fevil.example.com', '//evil.example.com', '/\\evil.example.com', '/a\u0000b', '/a\tb'])(
    'göreli kılığında dış adres: %s → blocked', u => {
      expect(classifyLink(u, policy)).toBe('blocked');
      expect(toAppPath(u, 'https://coremetry.example.test')).toBeNull();
    });
  it('büyük harfli aynı köken güvenli yolda relative; yol aynen', () => {
    expect(classifyLink('https://COREMETRY.example.test/problems?x=1#a', policy)).toBe('relative');
    expect(toAppPath('https://COREMETRY.example.test/problems?x=1#a', 'https://coremetry.example.test')).toBe('/problems?x=1#a');
  });

  it.each(['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,x', 'vbscript:x', '//evil.example.test/x', 'file:///etc/passwd', ' /a b'])(
    '%s → blocked', u => expect(classifyLink(u, policy)).toBe('blocked'));
  // v0.10.1137 inceleme — performans: 20 KB başıboş `*` / `~~` / `_` doğrusal kalır.
  it('20 KB başıboş işaret < 30 ms', () => {
    const junk = ('*a ~~b _c ' ).repeat(2000).slice(0, 20_000);
    parseInline(junk); // ısınma (JIT)
    const t0 = performance.now();
    parseInline(junk);
    parseInline('*x '.repeat(7000));
    expect(performance.now() - t0).toBeLessThan(30);
  });
  it('iç içe vurgu derinliği tavanı', () => {
    expect(() => parseInline('**'.repeat(5000) + 'x' + '**'.repeat(5000))).not.toThrow();
  });

  it('politikasız bağlam: hiçbir dış link doğrulanmış değil', () => {
    expect(classifyLink('https://jenkins.example.test/job/orders/', buildLinkPolicy({}))).toBe('unverified');
  });
  it('normalize / kırpma', () => {
    expect(normalizeUrl('HTTPS://A.example.test/')).toBe('https://a.example.test');
    expect(normalizeUrl('ftp://a')).toBeNull();
    expect(trimUrlPunct('https://a.example.test/x).')).toBe('https://a.example.test/x');
  });
});
