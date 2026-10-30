#!/usr/bin/env node
// bundle-budget — v0.10.1609 (shadcn Faz 1 R2, plan §1.3).
//
// Ölçülen şey TARAYICININ ilk yükü: dist/index.html'deki
// <script type="module" src> + her <link rel="modulepreload" href>.
// Kural (audit §11 "Bütçe"): ilk yükün JS gz toplamı ≤ BUNDLE_BUDGET_KB
// (varsayılan 347) ve bu kümedeki HİÇBİR chunk'ın sourcemap'inde
// Radix / cmdk / sonner / floating-ui / components/shadcn kaynağı yok.
// Neden sourcemap: Vite/rolldown manifest'i chunk'ları listeler, içindeki
// modülleri değil; zaten üretilen .map dosyalarının sources[]'ı listeler
// (build.sourcemap: true, vite.config.ts). Manifest üretilmez — go:embed
// all:frontend/dist onu da binary'ye gömerdi.
//
// gz = zlib.gzipSync varsayılan seviye (Vite'ın build raporuyla aynı
// yöntem) — tek yöntem çivili, sayı sürümden sürüme kıyaslanabilir.
// Çıktı stdout + dist/bundle-budget.txt; son satır migration-log.md'ye
// AYNEN yapıştırılır. Bağımlılık yok (Node ≥ 22).
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DIST = path.join(ROOT, 'dist');
const BUDGET_KB = Number(process.env.BUNDLE_BUDGET_KB || 347);

// Yasak kaynaklar (plan §1.3 adım 3). v0.10.1613 (R5, §12 Q8): paketleme
// `radix-ui` tek paketi (shadcn@4.21.5 çıktısı); liste vite.config.ts
// `radix` grubunun package-lock kapanışıyla aynı aile.
const FORBIDDEN_SOURCES = [
  /node_modules\/(radix-ui|@radix-ui|cmdk|sonner|@floating-ui|react-remove-scroll|aria-hidden|use-sidecar|use-callback-ref|react-style-singleton|get-nonce|detect-node-es)\//,
  /src\/components\/shadcn\//,
];

// Eski sınıf adıyla AYNI adı taşıyan Tailwind yardımcıları (R5). utilities
// katmanı legacy'nin üstünde → böyle bir yardımcı GEÇİRİLMEMİŞ sayfadaki
// eski işaretlemede de kazanır. İzinli olanların değeri globals.css'tekiyle
// birebir (tokens.css `--spacing: 4px`; .sr-only aynı gizleme). Listede
// olmayan bir çakışma = FAIL: ya üretilen sınıfı değiştir ya eski kuralı sil.
const ALLOWED_LEGACY_COLLISIONS = ['gap-1', 'gap-2', 'gap-4', 'sr-only'];

const kb = (n) => (n / 1024).toFixed(1);

function fail(msg) {
  console.error(`bundle-budget: ${msg}`);
  process.exit(1);
}

if (!existsSync(path.join(DIST, 'index.html'))) fail('dist/index.html yok — önce `vite build`');
const html = readFileSync(path.join(DIST, 'index.html'), 'utf8');

const attr = (tag, name) => {
  const m = tag.match(new RegExp(`\\s${name}=["']([^"']+)["']`));
  return m ? m[1] : null;
};
const initial = [];
for (const m of html.matchAll(/<script\b[^>]*>/g)) {
  if (attr(m[0], 'type') === 'module' && attr(m[0], 'src')) initial.push(attr(m[0], 'src'));
}
for (const m of html.matchAll(/<link\b[^>]*>/g)) {
  if (attr(m[0], 'rel') === 'modulepreload' && attr(m[0], 'href')) initial.push(attr(m[0], 'href'));
}
if (initial.length === 0) fail('index.html içinde module script / modulepreload bulunamadı');

const readSources = (file) => {
  const map = `${file}.map`;
  // Yalnız sanal modül taşıyan chunk'ların .map'i yok: rolldown çalışma
  // zamanı + \0-önekli preload/interop yardımcıları (vite.config.ts
  // priority 110 `vendor` grubu). Bugün 2; MAX_MAPLESS aşılırsa sourcemap
  // bozulmuş demektir ve tarama kör olur → FAIL.
  if (!existsSync(map)) { mapless.push(path.basename(file)); return []; }
  return JSON.parse(readFileSync(map, 'utf8')).sources || [];
};

const MAX_MAPLESS = 2;
const mapless = [];
const rows = [];
const offenders = [];
let initialGz = 0;
let mainGz = 0;
for (const href of [...new Set(initial)]) {
  const file = path.join(DIST, href.replace(/^\//, ''));
  if (!existsSync(file)) fail(`${href} index.html'de var ama dist'te yok`);
  const buf = readFileSync(file);
  const gz = gzipSync(buf).length;
  initialGz += gz;
  const base = path.basename(file);
  if (/^index-[^/]*\.js$/.test(base)) mainGz = gz;
  rows.push({ base, gz });
  const bad = readSources(file).filter((s) => FORBIDDEN_SOURCES.some((re) => re.test(s)));
  if (bad.length) offenders.push({ base, bad });
}

// Rapor (kapı değil, plan §1.3 adım 5): radix/toast grup chunk'ları ve
// shadcn kaynağı taşıyan chunk sayısı (Faz 1'de 0, ilk dilimden sonra > 0).
const assets = readdirSync(path.join(DIST, 'assets'));
const groupChunk = (name) => {
  const f = assets.find((a) => a.startsWith(`${name}-`) && a.endsWith('.js'));
  return f ? `${f} ${kb(gzipSync(readFileSync(path.join(DIST, 'assets', f))).length)} KB gz` : 'absent';
};
let shadcnChunks = 0;
for (const a of assets) {
  if (!a.endsWith('.js.map')) continue;
  const src = JSON.parse(readFileSync(path.join(DIST, 'assets', a), 'utf8')).sources || [];
  if (src.some((s) => FORBIDDEN_SOURCES[1].test(s))) shadcnChunks++;
}

// KAPI (operatör, R5): radix chunk'ı grafana chunk'ını STATİK import
// edemez — ederse Radix kullanan her sayfa 180 KB gz'lik tembel grafana'yı
// da çeker (v0.9.708 sınıfı: paylaşılan bağımlılık yanlış gruba yutulur).
// Minified ESM: `import{…}from"./grafana-X.js"` / `import"./grafana-X.js"`.
// Dinamik `import("./grafana-…")` sayılmaz (tembel kenar, zararsız).
const STATIC_IMPORT = /(?:^|[;}\s])import\s*(?:[\w$*{][^;'"]*?from\s*)?["']\.\/([^"']+\.js)["']/g;
const radixFile = assets.find((a) => a.startsWith('radix-') && a.endsWith('.js'));
const radixToGrafana = [];
if (radixFile) {
  const src = readFileSync(path.join(DIST, 'assets', radixFile), 'utf8');
  for (const m of src.matchAll(STATIC_IMPORT)) if (/^grafana-/.test(m[1])) radixToGrafana.push(m[1]);
}

// KAPI (R5): yardımcı ↔ eski sınıf adı çakışması (ALLOWED_LEGACY_COLLISIONS).
const utilityNames = new Set();
const legacyNames = new Set();
for (const a of assets) {
  if (!/^index-.*\.css$/.test(a)) continue;
  const css = readFileSync(path.join(DIST, 'assets', a), 'utf8');
  let i = css.indexOf('@layer utilities{');
  if (i < 0) continue;
  let depth = 0;
  let j = i + '@layer utilities'.length;
  for (; j < css.length; j++) {
    if (css[j] === '{') depth++;
    else if (css[j] === '}' && --depth === 0) break;
  }
  for (const m of css.slice(i, j).matchAll(/\.((?:\\.|[\w-])+)/g)) utilityNames.add(m[1].replace(/\\/g, ''));
}
for (const f of ['src/styles/globals.css', 'src/styles/chart.css']) {
  const css = readFileSync(path.join(ROOT, f), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
  for (const m of css.matchAll(/\.([A-Za-z_][\w-]*)/g)) legacyNames.add(m[1]);
}
const collisions = [...utilityNames].filter((n) => legacyNames.has(n)).sort();
const badCollisions = collisions.filter((n) => !ALLOWED_LEGACY_COLLISIONS.includes(n));

if (mapless.length > MAX_MAPLESS) fail(`${mapless.length} ilk-yük chunk'ının .map'i yok (${mapless.join(', ')}) — build.sourcemap kapalı mı?`);

const over = initialGz > BUDGET_KB * 1024;
const out = [];
out.push(`bundle-budget  dist/index.html  ${rows.length} preloaded JS`);
for (const r of [...rows].sort((a, b) => b.gz - a.gz)) out.push(`  ${kb(r.gz).padStart(6)} KB gz  ${r.base}`);
out.push(`initial-load JS gz: ${kb(initialGz)} KB   budget ${BUDGET_KB} KB   main (index-*.js) ${kb(mainGz)} KB gz   ${over ? 'OVER BUDGET' : 'OK'}`);
out.push(`forbidden in entry: ${offenders.length ? offenders.map((o) => o.base).join(', ') : 'none'} (radix/cmdk/sonner/shadcn)   radix chunk: ${groupChunk('radix')}   toast chunk: ${groupChunk('toast')}   shadcn sources in output: ${shadcnChunks} chunks`);
for (const o of offenders) {
  out.push(`  ${o.base}:`);
  for (const s of o.bad.slice(0, 5)) out.push(`    ${s}`);
}
out.push(`radix → grafana static import: ${radixFile ? (radixToGrafana.length ? radixToGrafana.join(', ') : 'none') : 'n/a (no radix chunk)'}   utility∩legacy class names: ${collisions.length} (${collisions.join(' ') || '—'}; not allowed: ${badCollisions.join(' ') || 'none'})`);
out.push(`no sourcemap (virtual-only chunks, max ${MAX_MAPLESS}): ${mapless.join(', ') || 'none'}`);
out.push(`| initial ${kb(initialGz)} KB gz | main ${kb(mainGz)} KB gz | budget ${BUDGET_KB} | radix-in-entry ${offenders.length} |`);

const text = out.join('\n') + '\n';
process.stdout.write(text);
writeFileSync(path.join(DIST, 'bundle-budget.txt'), text);

if (radixToGrafana.length) fail(`${radixFile} grafana chunk'ını statik import ediyor: ${radixToGrafana.join(', ')} — vite.config.ts radix grubu önceliği/regex'i`);
if (badCollisions.length) fail(`Tailwind yardımcısı eski sınıf adıyla çakışıyor: ${badCollisions.join(' ')} (scripts/bundle-budget.mjs ALLOWED_LEGACY_COLLISIONS)`);
if (offenders.length) fail(`ilk yükte yasak kaynak: ${offenders.map((o) => o.base).join(', ')}`);
if (over) fail(`ilk yük ${kb(initialGz)} KB gz > bütçe ${BUDGET_KB} KB`);
