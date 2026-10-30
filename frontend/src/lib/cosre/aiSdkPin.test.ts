// aiSdkPin.test — v0.10.1618 (CoSRE v2 Faz 2a): AI SDK pini + yalıtım.
//   (1) `ai` 5.0.276 ve `@ai-sdk/react` 2.0.279 TAM pin (package.json +
//       kurulu sürüm); @ai-sdk/react'in kendi `ai` bağımlılığı aynı sürüm —
//       iki kopya `ai` olursa Chat sınıfı ile transport ayrı modüllerden gelir.
//       Davranış doğrulamaları (TextUIPart'ta id yok, transient data-reset
//       message.parts'a girmez, onFinish her yolda) bu sürüme karşı yapıldı.
//   (2) Faz 2a: uygulamadan HİÇBİR modül `ai` / `@ai-sdk/*` ya da v2 çekirdeğini
//       import etmez — v1 ekranı ve giriş chunk'ı değişmez. Faz 2b bunu tembel
//       (lazy) import pinine çevirir.
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
});

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
}

describe('Faz 2a yalıtımı', () => {
  it('src/lib/cosre dışında `ai` / `@ai-sdk` / v2 çekirdeği importu yok', () => {
    const SRC = join(FE, 'src');
    const IMPORT = /(?:from\s+|import\s*\(\s*|import\s+)['"]([^'"]+)['"]/g;
    const CORE = /lib\/cosre\/(useCosreChat|textIdMap|resetReducer|cosrePersist)$|^\.\.?\/(useCosreChat|textIdMap|resetReducer|cosrePersist)$/;
    const bad: string[] = [];
    for (const f of walk(SRC)) {
      const rel = relative(SRC, f);
      if (rel.startsWith('lib/cosre/')) continue;
      for (const m of readFileSync(f, 'utf8').matchAll(IMPORT)) {
        const spec = m[1];
        if (spec === 'ai' || spec.startsWith('ai/') || spec.startsWith('@ai-sdk/') || CORE.test(spec)) bad.push(`${rel}: ${spec}`);
      }
    }
    expect(bad).toEqual([]);
  });

  it('bundle-budget: ai / @ai-sdk ilk yükte yasak, shadcn indeksi [1] korunur', () => {
    const s = readFileSync(join(FE, 'scripts', 'bundle-budget.mjs'), 'utf8');
    expect(s).toContain('/node_modules\\/(ai|@ai-sdk)\\//,\n];');
    expect(s).toContain('FORBIDDEN_SOURCES[1].test(s)');
    const list = /const FORBIDDEN_SOURCES = \[([\s\S]*?)\];/.exec(s)![1].split('\n').filter(l => /^\s*\//.test(l));
    expect(list[1].trim()).toBe('/src\\/components\\/shadcn\\//,');
    const re = /node_modules\/(ai|@ai-sdk)\//;
    expect(re.test('../node_modules/ai/dist/index.mjs')).toBe(true);
    expect(re.test('../node_modules/@ai-sdk/react/dist/index.mjs')).toBe(true);
    expect(re.test('../node_modules/aria-hidden/dist/es2015/index.js')).toBe(false);
  });
});
