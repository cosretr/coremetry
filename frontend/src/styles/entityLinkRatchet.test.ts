import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// entityLinkRatchet — v0.10.2488 (entity tutarlılık denetimi dilim 4,
// docs/frontend/entity-coherence-audit.md §3.5 + §5.3).
//
// Ölçü = denetimin M4 tanımı birebir: açılış etiketi `<Link …>` (çok satırlı)
// yedi bağlantı üreticisinden birinin ÇAĞRISINI içeriyorsa sayılır:
//   serviceHref, entityHref, podDetailPath, endpointDetailHref,
//   databaseDetailHref, messagingTopicHref, nodeDetailHref.
// Kural: entity adını çizen bağlantı `components/coremetry/entity/EntityLink`
// (`to={{ kind, … }}`; href'i tür kaydı `lib/entityKinds.ts` kurar).
// Ham `<Link to={serviceHref(…)}>` yazılmaz.
//
// Kapsam: src/**/*.{ts,tsx}; HARİÇ *.test.*, *.stories.*, *.d.ts,
// components/shadcn/** (üretilen), testdata/. Yorumlar ayıklanır.
//
// Hedef 0; kalan her kullanım GEREKÇELİ izin listesinde (ALLOW). Listede
// olmayan dosyanın tavanı 0. YALNIZ AŞAĞI: sayı düşünce tavan aynı commit'te
// iner. Yeniden ölçüm:
//   ENTITY_LINK_RATCHET_PRINT=1 npx vitest run src/styles/entityLinkRatchet.test.ts
const SRC = resolve(__dirname, '..');

const PRODUCERS = ['serviceHref', 'entityHref', 'podDetailPath', 'endpointDetailHref', 'databaseDetailHref', 'messagingTopicHref', 'nodeDetailHref'];
const PRODUCER_CALL = new RegExp(`\\b(?:${PRODUCERS.join('|')})\\(`);
const EXCLUDE = /^components\/shadcn\/|(^|\/)testdata\//;

/** Açılış etiketleri: `<Link` + süslü parantez derinliği 0'da ilk `>`. */
export function linkOpenTags(src: string): string[] {
  const out: string[] = [];
  const re = /<Link(?=[\s/>])/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    let i = m.index + 5; let d = 0; let q = '';
    for (; i < src.length; i++) {
      const c = src[i];
      if (q) { if (c === q && src[i - 1] !== '\\') q = ''; continue; }
      if (c === '"' || c === "'" || c === '`') { q = c; continue; }
      if (c === '{') d++;
      else if (c === '}') d--;
      else if (c === '>' && d === 0) break;
    }
    out.push(src.slice(m.index, i + 1));
  }
  return out;
}

export const producerLinks = (src: string): number =>
  linkOpenTags(stripTsComments(src)).filter(t => PRODUCER_CALL.test(t)).length;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(p) && !/\.(test|stories)\.|\.d\.ts$/.test(p)) out.push(p);
  }
  return out;
}

const counts = new Map<string, number>();
let subjectLinkJsx = 0;
for (const p of walk(SRC)) {
  const rel = relative(SRC, p).split(sep).join('/');
  if (EXCLUDE.test(rel)) continue;
  const src = readFileSync(p, 'utf8');
  const n = producerLinks(src);
  if (n) counts.set(rel, n);
  subjectLinkJsx += (stripTsComments(src).match(/<SubjectLink(?=[\s/>])/g) ?? []).length;
}
const total = [...counts.values()].reduce((a, b) => a + b, 0);

if (process.env.ENTITY_LINK_RATCHET_PRINT) {
  console.log(`ENTITY_LINK producerLinks=${total} files=${counts.size}\n${[...counts].sort().map(([f, n]) => `  '${f}': ${n},`).join('\n')}`);
}

const HINT = 'entity adı bağlantısı: <EntityLink to={{ kind: …, … }}> kullan '
  + '(components/coremetry/entity/EntityLink; href tür kaydından, lib/entityKinds.ts)';

// Taban: denetim (v0.10.2482) 71; aynı tanımla v0.10.2487'de 86 / 59 dosya
// (fark: dilim 1–3 arası yeni bağlantılar + çok satırlı etiketin tam
// taranması). Dilim 4 (v0.10.2488): 86 → 2, ikisi de gerekçeli.
const ALLOW: Record<string, { n: number; why: string }> = {
  'pages/settings/MLServiceProfiles.tsx': {
    n: 1,
    why: 'yönetim ekranı (Settings sekmesi, Karar A6) — bu programda dokunulmaz',
  },
  'pages/service/DetailsPropsStrip.tsx': {
    n: 1,
    why: 'etiket entity adı değil, göreli zaman ("Son deploy · 3 sa önce"); aynı servisin GitOps sekmesine sekme gezintisi',
  },
};
const TOTAL = 2;

describe('entityLinkRatchet (denetim M4, yalnız aşağı)', () => {
  it('dosya başına üreticili <Link> izin listesini aşmaz', () => {
    const over = [...counts].filter(([f, n]) => n > (ALLOW[f]?.n ?? 0)).map(([f, n]) => `${f}: ${n} > ${ALLOW[f]?.n ?? 0}`);
    expect(over, `${HINT}\n${over.join('\n')}`).toEqual([]);
  });

  it('TOTAL aşılmaz', () => {
    expect(total, HINT).toBeLessThanOrEqual(TOTAL);
  });

  it('izin listesi bayatlamaz, her girdi gerekçeli', () => {
    const stale = Object.entries(ALLOW).filter(([f, a]) => (counts.get(f) ?? 0) < a.n || !a.why).map(([f, a]) => `${f}: ölçülen ${counts.get(f) ?? 0} < ${a.n}`);
    if (total < TOTAL) stale.push(`TOTAL ${TOTAL} → ${total}`);
    expect(stale, 'sayı düştü — izin listesini aynı commit\'te indir').toEqual([]);
  });

  it('SubjectLink EntityLink\'e katlandı (ayrı bileşen yok)', () => {
    expect(subjectLinkJsx).toBe(0);
    expect(existsSync(join(SRC, 'components/SubjectLink.tsx'))).toBe(false);
  });

  it('desen kaymadı: M4', () => {
    expect(producerLinks('<Link to={serviceHref(a, { range })} className="mono">a</Link>')).toBe(1);
    expect(producerLinks('<Link\n  to={entityHref(r)}\n  style={{ x: 1 }}>r</Link>')).toBe(1);
    expect(producerLinks('<Link to="/services">x</Link>')).toBe(0);
    expect(producerLinks('<EntityLink to={{ kind: \'service\', name: a }}>a</EntityLink>')).toBe(0);
    expect(producerLinks('// <Link to={serviceHref(a)}>')).toBe(0);
    expect(producerLinks('<LinkButton onClick={() => nav(serviceHref(a))} />')).toBe(0);
  });
});
