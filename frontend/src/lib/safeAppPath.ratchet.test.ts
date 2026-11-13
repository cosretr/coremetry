// safeAppPath ratchet — v0.10.1314 (Dependabot: react-router < 7.18 açık
// yönlendirme, ters bölü).
//
// Ne çiviliyor: uygulama-içi gezinme kapısı tek yerde (lib/safeAppPath).
// Yalnız "//"ya bakan yeni bir kapı (`startsWith('/') && !startsWith('//')`)
// kırmızı: `<Link to="/\evil.com">` / `navigate('/\\evil.com')` v6'nın
// ABSOLUTE_URL_REGEX'ini ve removeDoubleSlashes'ını geçer, pushState
// kökenler-arası diye fırlatır, router window.location.assign'a düşer →
// kullanıcı başka siteye gider. "//" kontrolü olan satır, aynı satırda ters
// bölü kontrolü ya da isSafeAppPath anmıyorsa ihlal.
//
// tsc/eslint bunu göremez: zayıf kapı tip olarak kusursuz.
//
// v0.10.1315 (olay linki XSS) — iki ek: (1) Go aynası internal/safelink
// tablosu (safelink_test.go) ile bu dosyanın tabloları KİLİTLİ ADIMDA (keep in
// lockstep) — biri değişirse öteki de; (2) API/editör kaynaklı URL alanları
// href/window.open'a yalnız SafeHref/appLinkKind üzerinden ulaşır.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { appLinkKind, isSafeAppPath, safeAppPath } from './safeAppPath';

const SRC = resolve(__dirname, '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules') continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}

// Muafiyetler GEREKÇEYE göre anahtarlı (spaLinks.test.ts emsali): gerekçe
// ortadan kalkarsa girdi ÇIKARILMALI — bayat girdi = yanlış sebeple yeşil.
const ALLOWED: { file: string; why: string }[] = [
  {
    file: 'lib/postLoginRedirect.ts',
    why: 'sanitizeRedirect isSafeAppPath\'ten SIKI (nokta segmenti, /login|/public|/api '
      + 'öneki) ve Go sanitizeOIDCNext ile 1:1 aynalı; ters bölüyü `includes` ile '
      + 'reddediyor. Ortak yardımcıya bağlamak iki taraflı aynayı bozar.',
  },
  {
    file: 'pages/settings/clusterArgoFields.ts',
    why: 'Git URL ayrıştırma (scp/ssh/file:/// biçimleri) — gezinme kapısı DEĞİL, '
      + 'sonuç Link/navigate\'e gitmiyor.',
  },
];

describe('safeAppPath ratchet — yalnız "//"ya bakan gezinme kapısı yok', () => {
  it('tarama temiz', () => {
    // İmza parçalardan kuruluyor ki bu dosyanın KENDİ düzyazısı bir ihlal
    // sanılmasın (spaLinks emsali; test dosyaları zaten taranmıyor).
    const CALL = 'starts' + 'With\\(';
    const SLASH2 = '[\'"`]' + '//' + '[\'"`]\\)';
    const RE = new RegExp(CALL + SLASH2);
    const BACKSLASH_OK = new RegExp('\\\\\\\\|isSafe' + 'AppPath|safe' + 'AppPath');
    const bad: string[] = [];
    for (const file of walk(SRC)) {
      const rel = file.slice(SRC.length + 1).split('\\').join('/');
      if (ALLOWED.some(a => a.file === rel)) continue;
      readFileSync(file, 'utf8').split('\n').forEach((line, i) => {
        if (/^\s*(\/\/|\*)/.test(line)) return;
        if (RE.test(line) && !BACKSLASH_OK.test(line)) {
          bad.push(`${rel}:${i + 1} ${line.trim().slice(0, 100)}`);
        }
      });
    }
    expect(bad, 'lib/safeAppPath isSafeAppPath kullan').toEqual([]);
  });

  it('muafiyet dosyalarının hepsi hâlâ var', () => {
    for (const a of ALLOWED) {
      expect(() => readFileSync(join(SRC, a.file), 'utf8'), `${a.file} yok — muafiyeti ÇIKAR`).not.toThrow();
    }
  });

  it('muafiyet dosyaları hâlâ "//" kontrolü taşıyor (yoksa muafiyeti ÇIKAR)', () => {
    const RE = new RegExp('starts' + 'With\\([\'"`]//');
    for (const a of ALLOWED) {
      expect(RE.test(readFileSync(join(SRC, a.file), 'utf8')), `${a.file} — muafiyet bayat`).toBe(true);
    }
  });
});

// v0.10.1314 — kapının tablo sözleşmesi.
describe('isSafeAppPath (v0.10.1314)', () => {
  const CASES: Array<[string, boolean]> = [
    ['/\\evil.com', false],          // react-router v6 açığı: "/\" protokol-göreli
    ['\\\\evil.com', false],         // "\\host" (kök-göreli değil)
    ['//evil.com', false],           // protokol-göreli
    ['/%5Cevil.com', false],         // kod çözülünce "/\"
    ['/%2F%2Fevil.com', false],      // kod çözülünce "//"
    ['/x\\y', false],                // yolda ters bölü
    ['/x%0Ay', false],               // kod çözülünce kontrol karakteri (LF)
    ['/ x', false],                  // boşluk
    ['https://a', false],            // mutlak URL
    ['', false],
    ['/%E0%A4%A', false],            // bozuk yüzde kodlaması (decodeURIComponent fırlatır)
    ['/', true],
    ['/traces?service=a#x', true],
    ['/problems?problem=%2Fx', true], // sorgu kısmı kod çözülmez; yol temiz
  ];
  for (const [u, want] of CASES) {
    it(`${JSON.stringify(u)} → ${want}`, () => {
      expect(isSafeAppPath(u)).toBe(want);
      expect(safeAppPath(u)).toBe(want ? u : null);
    });
  }
  it('safeAppPath: null/undefined → null', () => {
    expect(safeAppPath(null)).toBeNull();
    expect(safeAppPath(undefined)).toBeNull();
  });
});

// v0.10.1314 — iç/dış/çizilmez ayrımı (AnnotationLane olay linki, runbookTarget).
describe('appLinkKind (v0.10.1314)', () => {
  const CASES: Array<[string, 'internal' | 'external' | null]> = [
    ['/\\evil.com', null],
    ['//evil.com', null],
    ['/%2F%2Fevil.com', null],
    ['javascript:alert(1)', null],
    ['ftp://x', null],
    ['/problems?problem=p1', 'internal'],
    ['https://ci.example/build/1', 'external'],
    ['HTTP://ci.example', 'external'],
  ];
  for (const [href, want] of CASES) {
    it(`${JSON.stringify(href)} → ${want}`, () => expect(appLinkKind(href)).toBe(want));
  }
});

// v0.10.1315 — Go internal/safelink aynası. Keep in lockstep with
// internal/safelink/safelink_test.go TestClassify: Internal ⇔ 'internal',
// None ⇔ null. Bilinçli farklar (Go yalnız DAHA SIKI, saklama tarafı):
// Go TrimSpace yapar (" https://x" External; burada null — boşluklu href
// çizilmez), "http://" (host yok), userinfo'lu URL ve kontrol karakterli dış
// URL Go'da None. Bu satırlar Go tablosunda.
describe('appLinkKind — Go safelink aynası (v0.10.1315)', () => {
  const CASES: Array<[string, 'internal' | 'external' | null]> = [
    ['javascript:alert(1)', null],
    ['JavaScript:alert(1)', null],
    ['data:text/html,<script>alert(1)</script>', null],
    ['vbscript:msgbox(1)', null],
    ['http:evil', null],
    ['', null],
    ['/a\u00a0b', null],
    ['/%E0%A4', null],
    ['/runbook?id=abc&problemId=p1', 'internal'],
    ['HTTP://ci.example', 'external'],
    ['https://a', 'external'],
  ];
  for (const [href, want] of CASES) {
    it(`${JSON.stringify(href)} → ${want}`, () => expect(appLinkKind(href)).toBe(want));
  }
});

// v0.10.1315 — API/editör kaynaklı URL alanı (olay `link`, katalog
// runbook/oncall/repo, duyuru `linkUrl`, DevOps `url`) `href={x.link}` ya da
// `window.open(` ile çizilen her kaynak dosya kapıyı (lib/safeAppPath ya da
// components/ui/SafeHref) import etmeli. React 18 `javascript:` href'lerini
// engellemez — kapısız yeni bir sink = saklanan XSS.
const URL_SINK_ALLOWED: { file: string; why: string; pattern: RegExp }[] = [
  {
    file: 'lib/utils.ts',
    why: 'openNewTab: rowClickHandlers\'ın Ctrl/orta tık yedeği; href satırın '
      + 'KENDİ hesaplanmış uygulama yolu (getRowHref), API alanı değil.',
    pattern: new RegExp('window\\.' + 'open\\('),
  },
];

describe('v0.10.1315 — API-supplied URL fields reach href/window.open only through SafeHref/appLinkKind', () => {
  // İmza parçalardan kuruluyor ki bu dosyanın KENDİ düzyazısı ihlal sanılmasın.
  const HREF_FIELD = new RegExp('href=' + '\\{[^}]*\\.(link|runbookUrl|oncallUrl|repository|linkUrl|url)\\b');
  const WIN_OPEN = new RegExp('window\\.' + 'open\\(');
  const GATE_IMPORT = new RegExp('from [\'"](@/|\\.{1,2}/)+(lib/)?safe' + 'AppPath[\'"]'
    + '|from [\'"](@/|\\.{1,2}/)+(components/)?(ui/)?Safe' + 'Href[\'"]');

  it('tarama temiz', () => {
    const bad: string[] = [];
    for (const file of walk(SRC)) {
      const rel = file.slice(SRC.length + 1).split('\\').join('/');
      if (URL_SINK_ALLOWED.some(a => a.file === rel)) continue;
      // Kapının kendisi (openSafeLink'in window.open'ı, SafeHref'in <a>'sı).
      if (rel === 'lib/safeAppPath.ts' || rel === 'components/ui/SafeHref.tsx') continue;
      const src = readFileSync(file, 'utf8');
      if (GATE_IMPORT.test(src)) continue;
      src.split('\n').forEach((line, i) => {
        if (/^\s*(\/\/|\*)/.test(line)) return;
        if (HREF_FIELD.test(line) || WIN_OPEN.test(line)) {
          bad.push(`${rel}:${i + 1} ${line.trim().slice(0, 100)}`);
        }
      });
    }
    expect(bad, 'components/ui/SafeHref ya da lib/safeAppPath (appLinkKind / openSafeLink) kullan').toEqual([]);
  });

  it('muafiyet dosyaları hâlâ var ve hâlâ deseni taşıyor (yoksa muafiyeti ÇIKAR)', () => {
    for (const a of URL_SINK_ALLOWED) {
      let src = '';
      expect(() => { src = readFileSync(join(SRC, a.file), 'utf8'); }, `${a.file} yok — muafiyeti ÇIKAR`).not.toThrow();
      expect(a.pattern.test(src), `${a.file} — muafiyet bayat (${a.why})`).toBe(true);
      expect(GATE_IMPORT.test(src), `${a.file} artık kapıyı import ediyor — muafiyeti ÇIKAR`).toBe(false);
    }
  });

  it('tarayıcı gerçekten yakalıyor (kendi kendini doğrulama)', () => {
    expect(HREF_FIELD.test('<a href={ev.link} target="_blank">')).toBe(true);
    expect(HREF_FIELD.test('<a href={p.runbookUrl}>')).toBe(true);
    expect(HREF_FIELD.test('<a href={f.url}>')).toBe(true);
    expect(HREF_FIELD.test('<a href={href}>')).toBe(false);
    expect(WIN_OPEN.test("window.open(e.link, '_blank')")).toBe(true);
    expect(GATE_IMPORT.test("import { SafeHref } from '@/components/ui/SafeHref';")).toBe(true);
    expect(GATE_IMPORT.test("import { appLinkKind } from '@/lib/safeAppPath';")).toBe(true);
    expect(GATE_IMPORT.test("import { appLinkKind } from './safeAppPath';")).toBe(true);
  });
});
