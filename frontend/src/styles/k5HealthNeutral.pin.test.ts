// k5HealthNeutral.pin — v0.10.929 (K5 artıkları, CSS dilimi).
//
// Operatör kararı K5 (v0.10.920/922): sağlıklı/normal hâl NÖTR; yeşil
// (--ok / --ok-bg) yalnız bir GEÇİŞ (resolved/recovered/completed, az önce
// yapılan eylemin başarı geri bildirimi: kopyalandı, kaydedildi) ve VERİ
// (grafik serisi) için. Renk bunun dışında yalnız sapmada (warn/err).
//
// Ne çiviliyor:
//   1. globals.css'te var(--ok…) kullanan HER kural aşağıdaki izin
//      listesinde — yeni bir "sağlıklı = yeşil" kuralı sessizce giremez.
//   2. Sağlıklı hâl kuralları (status-*-operational, sağlık noktaları,
//      iyileşme deltaları) nötr; sapma kardeşleri renkli kalır.
//   3. Ölü kalan yeşil kurallar (.ins-cell--ok, düz .is-ok, .pb-tile.ok)
//      geri gelmedi.
//   4. Geçiş/geri bildirim yeşilleri fazla nötrleştirilmedi.
//   5. Açık KİP (Logs canlı kuyruk .live-on) bir seçim: accent, kırmızı değil.
//
// Neden ayrı kapı: bunların hiçbiri tsc/colorLeaks/undefinedCssRefs'e
// takılmaz — `var(--ok)` geçerli bir token, kural da geçerli CSS.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');

// Yorumları BOŞALT (satır korunur): gerekçe yorumlarında `var(--ok)` geçiyor.
const CLEAN = CSS.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string }
// paletteStep2.pin ile aynı ayrıştırma: `[^{}]+` @media başlığını kural
// sanmaz, medya bloklarının içindeki kurallar yakalanır.
const RULES: Rule[] = [...CLEAN.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
  selectors: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')),
  body: m[2],
}));
const bodyOf = (sel: string): string => {
  const r = RULES.find(x => x.selectors.includes(sel));
  if (!r) throw new Error(`kural yok: ${sel}`);
  return r.body;
};
const USES_OK = /var\(\s*--ok(-bg)?\s*[,)]/;

// Yeşil kalmasına İZİN verilen seçiciler — her biri geçiş ya da eylem geri
// bildirimi. Yeni satır = bilinçli karar.
const ALLOW_OK = new Map<string, string>([
  ['.b-ok',                               'geçiş rozeti (resolved) + Badge success; tüketiciler tek tek nötrlendi'],
  ['button.is-ok',                        'kopyalandı geri bildirimi (CopyButton, ChatBubble, Trace)'],
  ['button.is-ok:hover:not(:disabled)',   'kopyalandı geri bildirimi, hover'],
  ['button.accent.copied',                'ShareButton kopyalandı geri bildirimi'],
  ['.toast-success',                      'başarı tostu (eylem geri bildirimi)'],
  ['.toast-success .toast-icon',          'başarı tostu ikonu'],
  ['.pb-tl li.ok::after',                 'ProblemDetail zaman çizelgesi "Resolved" noktası (geçiş)'],
  // v0.10.929 (K5, lider kararı) — ölü .pb-tile.ok kuralı silindi, girdisi de.
]);

describe('K5 — globals.css yeşil yalnız geçiş/geri bildirim', () => {
  it('var(--ok…) kullanan her kural izin listesinde', () => {
    const offenders: string[] = [];
    for (const r of RULES) {
      if (!USES_OK.test(r.body)) continue;
      for (const s of r.selectors) if (!ALLOW_OK.has(s)) offenders.push(s);
    }
    expect(offenders, `Sağlıklı hâl yeşile boyanıyor olabilir (K5):\n${offenders.join('\n')}`).toEqual([]);
  });

  it('izin listesi bayat değil — her girdinin kuralı hâlâ var ve hâlâ yeşil', () => {
    // Geçiş/geri bildirim yeşilleri fazla nötrleştirilmedi (kopyalandı,
    // resolved, başarı tostu) — liste hem tavan hem taban.
    for (const sel of ALLOW_OK.keys()) {
      const r = RULES.find(x => x.selectors.includes(sel));
      if (!r) throw new Error(`izin listesinde bayat girdi (kural silinmiş): ${sel} — ALLOW_OK'tan çıkar`);
      expect(r.body, sel).toMatch(USES_OK);
    }
  });
});

describe('K5 — sağlıklı hâl nötr, sapma renkli', () => {
  it('status-*-operational (Monitors, admin/stats; ayarlar -neutral takma adı) nötr', () => {
    for (const sel of ['.status-banner-operational', '.status-pill-operational', '.status-dot-operational']) {
      const b = bodyOf(sel);
      expect(b, sel).not.toMatch(/--ok\b/);
      expect(b, `${sel} başka bir durum rengine kaydırılmamalı`).not.toMatch(/--(warn|err)/);
    }
    expect(bodyOf('.status-pill-operational')).toMatch(/background:\s*var\(--bg3\)/);
    expect(bodyOf('.status-dot-operational')).toMatch(/var\(--border-strong\)/);
    // Sapma kardeşleri renk taşımaya devam eder.
    for (const k of ['banner', 'pill', 'dot']) {
      expect(bodyOf(`.status-${k}-degraded`)).toMatch(/--warn/);
      expect(bodyOf(`.status-${k}-outage`)).toMatch(/--err/);
    }
  });

  // v0.10.929 (K5, lider kararı) — ayar bandının (ConfigStatusBanner) semantik
  // takma adları -operational ile AYNI kuralda: değerler ayrışamaz.
  it('.status-banner-neutral / .status-pill-neutral = -operational (tek kural)', () => {
    for (const k of ['banner', 'pill']) {
      const r = RULES.find(x => x.selectors.includes(`.status-${k}-neutral`));
      expect(r, `.status-${k}-neutral`).toBeDefined();
      expect(r?.selectors, k).toContain(`.status-${k}-operational`);
    }
  });

  it('sağlık noktaları: .green nötr halka, amber/red renkli', () => {
    for (const dot of ['.ov-dot', '.topo-dot']) {
      const g = bodyOf(`${dot}.green`);
      expect(g, dot).not.toMatch(/--ok\b/);
      expect(g, dot).toMatch(/background:\s*transparent/);
      expect(g, dot).toMatch(/inset 0 0 0 1px var\(--border-strong\)/);
      expect(bodyOf(`${dot}.amber`)).toMatch(/--warn-solid/);
      expect(bodyOf(`${dot}.red`)).toMatch(/--err/);
    }
  });

  // .ov-dot.green (F3 dilimi yeniden kullanıyor) ve .ov-delta.up.good
  // (KpiTile goodWhenUp hâlâ basıyor) CANLI — ölü sanılıp silinmesin.
  it('iyileşme deltası nötr (--text2), kötüleşme kırmızı', () => {
    for (const sel of ['.ov-delta.down', '.ov-delta.up.good', '.ev-delta--down'])
      expect(bodyOf(sel), sel).toMatch(/color:\s*var\(--text2\)/);
    for (const sel of ['.ov-delta.up', '.ov-delta.down.bad', '.ev-delta--up'])
      expect(bodyOf(sel), sel).toMatch(/color:\s*var\(--err\)/);
  });

  it('ölü yeşiller geri gelmedi: .ins-cell--ok, düz .is-ok, .pb-tile.ok', () => {
    const all = RULES.flatMap(r => r.selectors);
    expect(all.filter(s => s.includes('.ins-cell--ok'))).toEqual([]);
    expect(all.filter(s => s.includes('.pb-tile.ok'))).toEqual([]);
    // Geçiş noktası (Resolved) yaşıyor — silme .pb-tile ile sınırlı.
    expect(bodyOf('.pb-tl li.ok::after')).toMatch(USES_OK);
    // Düz `.is-ok` (öğe niteleyicisiz) OracleTab'ın sağlıklı durum metnini
    // boyuyordu; kopyalandı tonu yalnız `button.is-ok`ta yaşar.
    expect(all.filter(s => /(^|[\s>+~,])\.is-ok\b/.test(s))).toEqual([]);
    expect(all).toContain('button.is-ok');
  });
});

// ── Açık kip = seçim (v0.10.929, K5, lider kararı) ─────────────────────────
// Logs "Live tail" açıkken .live-on dolgusu --err-solid idi: bir KİP seçimi
// sapma diliyle (kırmızı) boyanıyordu. Artık accent + --on-accent; açık hâl
// dolgu (kapalıyken ikincil/dolgusuz) + nabız noktası + etiketle net kalır.
// Okunurluk contrastTokens.test.ts ile aynı yöntemle burada da çivili:
// --on-accent / --accent ≥ 4.5 (normal metin, WCAG 1.4.3) üç temada.
const NO_COMMENTS = CSS.replace(/\/\*[\s\S]*?\*\//g, '');
function hexBlock(re: RegExp): Map<string, string> {
  const m = re.exec(NO_COMMENTS);
  if (!m) throw new Error(`blok yok: ${re}`);
  const out = new Map<string, string>();
  for (const d of m[1].matchAll(/(--[\w-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) out.set(d[1], d[2].toLowerCase());
  return out;
}
const THEME_BLOCKS: Record<string, Map<string, string>> = {
  dark: hexBlock(/:root\s*\{([\s\S]*?)\n\}/),
  light: hexBlock(/\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/),
  redhat: hexBlock(/\[data-theme="redhat"\]\s*\{([\s\S]*?)\n\}/),
};
function themeTok(theme: string, name: string): string {
  const v = THEME_BLOCKS[theme].get(name) ?? THEME_BLOCKS.dark.get(name);
  if (!v) throw new Error(`${theme} ${name} hex değil ya da yok`);
  return v;
}
function lum(hex: string): number {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function ratio(a: string, b: string): number {
  const x = lum(a), y = lum(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

describe('K5 — açık kip seçim dilinde (.live-on)', () => {
  it('.live-on dolgusu accent, metin --on-accent; sapma rengi yok', () => {
    const b = bodyOf('.live-on');
    expect(b).toMatch(/background:\s*var\(--accent\)/);
    expect(b).toMatch(/color:\s*var\(--on-accent\)/);
    expect(b).not.toMatch(/--(err|warn|ok)(-solid|-bg)?\b/);
    // Açık hâl ipucu (nabız noktası) duruyor ve dolguyla aynı okunur renkte.
    const dot = bodyOf('.live-on::before');
    expect(dot).toMatch(/background:\s*var\(--on-accent\)/);
    expect(dot).toMatch(/animation:\s*pulse\b/);
  });

  it('okunurluk: --on-accent / --accent ≥ 4.5 her temada', () => {
    const out: string[] = [];
    for (const theme of Object.keys(THEME_BLOCKS)) {
      const r = ratio(themeTok(theme, '--on-accent'), themeTok(theme, '--accent'));
      if (r < 4.5) out.push(`${theme}: --on-accent / --accent = ${r.toFixed(2)} (< 4.5)`);
    }
    expect(out).toEqual([]);
  });
});
