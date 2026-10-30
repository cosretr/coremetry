// aiSdkPin.test — v0.10.1618 (CoSRE v2 Faz 2a): AI SDK pini + yalıtım.
//   (1) `ai` 5.0.276 ve `@ai-sdk/react` 2.0.279 TAM pin (package.json +
//       kurulu sürüm); @ai-sdk/react'in kendi `ai` bağımlılığı aynı sürüm —
//       iki kopya `ai` olursa Chat sınıfı ile transport ayrı modüllerden gelir.
//       Davranış doğrulamaları (TextUIPart'ta id yok, transient data-reset
//       message.parts'a girmez, onFinish her yolda) bu sürüme karşı yapıldı.
//   (2) Faz 2a'da uygulamadan HİÇBİR modül `ai` / `@ai-sdk/*` ya da v2
//       çekirdeğini import etmiyordu; Faz 2b B1 (v0.10.1647) bunu tembel pine
//       çevirdi (aşağıda).
//   (3) scripts/bundle-budget.mjs `ai` / `@ai-sdk` kaynağını ilk yükte yasaklar.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

const FE = resolve(__dirname, '..', '..', '..');
const json = (p: string) => JSON.parse(readFileSync(join(FE, p), 'utf8')) as {
  version?: string; dependencies?: Record<string, string>;
};

describe('AI SDK pini', () => {
  it('package.json tam sürüm, kurulu sürüm aynı', () => {
    const deps = json('package.json').dependencies ?? {};
    expect(deps.ai).toBe('5.0.276');
    expect(deps['@ai-sdk/react']).toBe('2.0.279');
    expect(json('node_modules/ai/package.json').version).toBe('5.0.276');
    const react = json('node_modules/@ai-sdk/react/package.json');
    expect(react.version).toBe('2.0.279');
    expect(react.dependencies?.ai).toBe('5.0.276');
  });

  it('package-lock: tek `ai` kopyası', () => {
    const lock = JSON.parse(readFileSync(join(FE, 'package-lock.json'), 'utf8')) as { packages: Record<string, { version?: string }> };
    const copies = Object.keys(lock.packages).filter(k => /(^|\/)node_modules\/ai$/.test(k));
    expect(copies).toEqual(['node_modules/ai']);
    expect(lock.packages['node_modules/ai'].version).toBe('5.0.276');
  });

  // v0.10.1642: provider-utils 3.0.41 `undici ^5.29.0` ister → 5.29.0 +
  // @fastify/busboy 2.1.1 (Trivy/Dependabot). Kapsamlı override yalnız bu
  // yolu 6.29.1'e çeker (bağımlılığı yok → busboy ağaçtan çıkar); jsdom'un
  // undici 7'si dokunulmaz.
  it('package-lock: provider-utils undici override 6.29.1, busboy yok, jsdom undici 7', () => {
    const pkg = JSON.parse(readFileSync(join(FE, 'package.json'), 'utf8')) as { overrides?: Record<string, unknown> };
    expect(pkg.overrides?.['@ai-sdk/provider-utils']).toEqual({ undici: '6.29.1' });
    const lock = JSON.parse(readFileSync(join(FE, 'package-lock.json'), 'utf8')) as { packages: Record<string, { version?: string; dependencies?: Record<string, string> }> };
    const undici = Object.entries(lock.packages).filter(([k]) => /(^|\/)node_modules\/undici$/.test(k)).map(([k, v]) => `${k}@${v.version}`).sort();
    expect(undici).toEqual([
      'node_modules/@ai-sdk/provider-utils/node_modules/undici@6.29.1',
      'node_modules/undici@7.30.1',
    ]);
    expect(Object.keys(lock.packages).filter(k => /node_modules\/@fastify\/busboy$/.test(k))).toEqual([]);
    expect(json('node_modules/@ai-sdk/provider-utils/node_modules/undici/package.json').version).toBe('6.29.1');
  });
});

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
}

// v0.10.1647 (Faz 2b B1, plan §1.3): "Faz 2a yalıtımı" → tembel pin.
//   • `ai` / `@ai-sdk/*` / v2 çekirdeği STATİK yalnız lib/cosre, components/cosre,
//     components/ai-elements içinden.
//   • components/cosre / ai-elements STATİK yalnız o ağaçlardan + /design
//     kataloğundan (DEV, tembel rota); kabuk kapısı yalnız dinamik import().
const V2_HOME = /^(lib\/cosre|components\/cosre|components\/ai-elements)\//;
const STATIC = /(?:from\s+|import\s+)['"]([^'"]+)['"]/g;
const DYNAMIC = /import\s*\(\s*['"]([^'"]+)['"]\s*\)/g;
const CORE = /lib\/cosre\/(useCosreChat|textIdMap|resetReducer|cosrePersist)$|^\.\.?\/(useCosreChat|textIdMap|resetReducer|cosrePersist)$/;
const V2_TREE = /(^|\/)components\/(cosre|ai-elements)(\/|$)|^\.\.?\/(cosre|ai-elements)\//;

describe('Faz 2b tembel pini', () => {
  const SRC = join(FE, 'src');
  const files = walk(SRC)
    .map(f => ({ rel: relative(SRC, f), src: readFileSync(f, 'utf8') }))
    .filter(f => !/\.test\.tsx?$/.test(f.rel));

  it('`ai` / `@ai-sdk` / v2 çekirdeği statik importu yalnız v2 dizinlerinde', () => {
    const bad: string[] = [];
    for (const { rel, src } of files) {
      if (V2_HOME.test(rel)) continue;
      for (const m of src.matchAll(STATIC)) {
        const spec = m[1];
        if (spec === 'ai' || spec.startsWith('ai/') || spec.startsWith('@ai-sdk/') || CORE.test(spec)) bad.push(`${rel}: ${spec}`);
      }
    }
    expect(bad).toEqual([]);
  });

  it('components/cosre / ai-elements statik importu yalnız v2 ağacında + /design kataloğunda', () => {
    const bad: string[] = [];
    for (const { rel, src } of files) {
      if (V2_HOME.test(rel) || rel === 'pages/design/catalog.tsx') continue;
      for (const m of src.matchAll(STATIC)) if (V2_TREE.test(m[1])) bad.push(`${rel}: ${m[1]}`);
    }
    expect(bad).toEqual([]);
  });

  it('kabuk kapısı v2\'yi yalnız dinamik import() ile anar', () => {
    const gate = files.find(f => f.rel === 'components/CopilotChatGate.tsx')!.src;
    expect([...gate.matchAll(DYNAMIC)].map(m => m[1])).toContain('./cosre/conversation/CosreChatV2');
    expect([...gate.matchAll(STATIC)].map(m => m[1]).filter(s => V2_TREE.test(s) || s === 'ai' || s.startsWith('@ai-sdk/'))).toEqual([]);
  });

  it('ayrıştırıcı: statik / dinamik ayrımı', () => {
    const st = (t: string) => [...t.matchAll(STATIC)].map(m => m[1]);
    const dy = (t: string) => [...t.matchAll(DYNAMIC)].map(m => m[1]);
    expect(st("import { Chat } from 'ai';")).toEqual(['ai']);
    expect(st("import './cosre/x';")).toEqual(['./cosre/x']);
    expect(st("const C = lazy(() => import('./cosre/conversation/CosreChatV2'));")).toEqual([]);
    expect(dy("const C = lazy(() => import('./cosre/conversation/CosreChatV2'));")).toEqual(['./cosre/conversation/CosreChatV2']);
    expect(V2_TREE.test('@/components/cosre/conversation/CosreChatV2')).toBe(true);
    expect(V2_TREE.test('./cosre/conversation/CosreChatV2')).toBe(true);
    expect(V2_TREE.test('@/lib/cosre/pickChatImpl')).toBe(false);
  });

  it('bundle-budget: ai kapanışı + v2 ağacı ilk yükte yasak, shadcn indeksi [1] korunur', () => {
    const s = readFileSync(join(FE, 'scripts', 'bundle-budget.mjs'), 'utf8');
    expect(s).toContain('FORBIDDEN_SOURCES[1].test(s)');
    const list = /const FORBIDDEN_SOURCES = \[([\s\S]*?)\n\];/.exec(s)![1].split('\n').filter(l => /^\s*\/[^/]/.test(l)).map(l => l.trim()); // yorum satırları değil, regex literal'leri
    expect(list[1]).toBe('/src\\/components\\/shadcn\\//,');
    expect(list[2]).toBe('/node_modules\\/(ai|@ai-sdk|swr|throttleit|use-stick-to-bottom)\\//,');
    expect(list[3]).toBe('/src\\/components\\/(cosre|ai-elements)\\//,');
    const ai = /node_modules\/(ai|@ai-sdk|swr|throttleit|use-stick-to-bottom)\//;
    expect(ai.test('../node_modules/ai/dist/index.mjs')).toBe(true);
    expect(ai.test('../node_modules/@ai-sdk/react/dist/index.mjs')).toBe(true);
    expect(ai.test('../node_modules/swr/dist/index/index.mjs')).toBe(true);
    expect(ai.test('../node_modules/aria-hidden/dist/es2015/index.js')).toBe(false);
    const tree = /src\/components\/(cosre|ai-elements)\//;
    expect(tree.test('../../src/components/cosre/conversation/CosreChatV2.tsx')).toBe(true);
    expect(tree.test('../../src/components/coremetry/overlay/Sheet.tsx')).toBe(false);
    // Rapor satırı + ai → grafana statik import kapısı.
    expect(s).toContain("ai chunk: ${groupChunk('ai')}");
    expect(s).toContain("a.startsWith('ai-') && a.endsWith('.js')");
    expect(s).toContain('if (aiToGrafana.length) fail(');
  });
});
