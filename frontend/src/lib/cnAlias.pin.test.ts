import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { cn } from 'cn';
import { cn as cnLocal } from './cn';

// cnAlias.pin — v0.10.1613 (shadcn Faz 1 R5). shadcn@4.21.5 kayıt defteri
// üretilen dosyalara `import { cn } from "cn"` yazıyor ve npm `cn` paketini
// kuruyor. Plan §5.5 bağımlılık tablosunda o paket YOK; `cn` çıplak kimliği
// üç yerde src/lib/cn.ts'e (twMerge(clsx)) takma adlı. Biri eksik kalırsa:
// vite → npm'den çözmeye çalışır (paket yok → build kırılır, iyi), vitest →
// aynı, tsc → `cn` tipi yok. Asıl risk SESSİZ olan: biri `npm i cn` derse
// takma ad yine kazanır ama package.json yalan söyler → paket yasak.
const FE = resolve(__dirname, '..', '..');
const read = (p: string) => readFileSync(resolve(FE, p), 'utf8');

describe('cn takma adı', () => {
  it('vite, vitest ve tsconfig `cn` → src/lib/cn.ts', () => {
    expect(read('vite.config.ts')).toContain("{ find: /^cn$/, replacement: path.resolve(__dirname, 'src/lib/cn.ts') },");
    expect(read('vitest.config.ts')).toContain("{ find: /^cn$/, replacement: fileURLToPath(new URL('./src/lib/cn.ts', import.meta.url)) },");
    expect(read('tsconfig.json')).toContain('"cn": ["./src/lib/cn.ts"]');
  });

  it('npm `cn` paketi bağımlılık değil; clsx + tailwind-merge + cva tam pinli', () => {
    const pkg = JSON.parse(read('package.json')) as { dependencies: Record<string, string>; devDependencies: Record<string, string> };
    expect(pkg.dependencies.cn).toBeUndefined();
    expect(pkg.devDependencies.cn).toBeUndefined();
    for (const n of ['clsx', 'tailwind-merge', 'class-variance-authority']) expect(pkg.dependencies[n], n).toMatch(/^\d+\.\d+\.\d+$/);
  });

  it('çözülen modül yerel uygulama: twMerge çakışanı sonuncuyla değiştirir', () => {
    expect(cn).toBe(cnLocal);
    expect(cn('px-2 py-1', false && 'hidden', 'px-4')).toBe('py-1 px-4');
  });

  it('üretilen shadcn dosyaları yalnız `cn` kimliğini kullanıyor (clsx/tailwind-merge doğrudan değil)', () => {
    const dir = resolve(FE, 'src/components/shadcn');
    for (const f of readdirSync(dir).filter(f => f.endsWith('.tsx') && !f.includes('.test.'))) {
      const s = readFileSync(resolve(dir, f), 'utf8');
      expect(s, f).not.toMatch(/from ["'](clsx|tailwind-merge)["']/);
      if (/\bcn\(/.test(s)) expect(s, f).toContain('import { cn } from "cn"');
    }
  });
});
