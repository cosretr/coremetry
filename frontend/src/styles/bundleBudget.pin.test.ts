import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// bundleBudget.pin — v0.10.1609 (shadcn Faz 1 R2, plan §1.3). İlk-yük
// kapısı üç parçadan oluşur ve biri kablosuz kalırsa kapı YEŞİL ama
// işe yaramaz ("test edilmiş ama ulaşılamaz"): vite.config.ts'teki
// radix/toast grupları (Radix eager vendor catch-all'ına düşmesin),
// sourcemap (betik sources[] tarar), betiğin yasak regex'i + bütçesi,
// CI adımı ve npm betiği, ve kabuğun no-restricted-imports bloğu.
const FE = resolve(__dirname, '..', '..');
const read = (p: string) => readFileSync(resolve(FE, p), 'utf8');

describe('bundle bütçe kapısı kablolu', () => {
  it('vite.config.ts: radix + toast grupları catch-all üstünde, sourcemap açık', () => {
    const cfg = read('vite.config.ts');
    expect(cfg).toContain("{ name: 'toast', test: /node_modules\\/sonner[\\/]/, priority: 86 },");
    expect(cfg).toContain(
      "{ name: 'radix', test: /node_modules\\/(radix-ui|@radix-ui|cmdk|@floating-ui|react-remove-scroll(-bar)?|react-style-singleton|use-callback-ref|use-sidecar|aria-hidden|get-nonce)[\\/]/, priority: 85 },",
    );
    expect(cfg).toMatch(/sourcemap:\s*true/);
    // Gruplar catch-all'dan ÖNCE tanımlı (sıra okunabilirlik; öncelik sayısı bağlayıcı).
    expect(cfg.indexOf("name: 'radix'")).toBeLessThan(cfg.indexOf("{ name: 'vendor', test: /node_modules/, priority: 10 }"));
  });

  it('scripts/bundle-budget.mjs: yasak kaynak regex\'i, 347 KB, zlib, index.html kümesi', () => {
    const s = read('scripts/bundle-budget.mjs');
    expect(s).toContain('/node_modules\\/(radix-ui|@radix-ui|cmdk|sonner|@floating-ui|react-remove-scroll|aria-hidden)\\//');
    expect(s).toContain('/src\\/components\\/shadcn\\//');
    expect(s).toContain('process.env.BUNDLE_BUDGET_KB || 347');
    expect(s).toContain('gzipSync(buf)');
    expect(s).toContain("'modulepreload'");
    expect(s).toContain("'bundle-budget.txt'");
  });

  it('CI frontend işi Vite build\'den hemen sonra betiği koşar', () => {
    const ci = readFileSync(resolve(FE, '..', '.github/workflows/ci.yml'), 'utf8');
    const build = ci.indexOf('run: npx vite build');
    const step = ci.indexOf('run: node scripts/bundle-budget.mjs');
    expect(build).toBeGreaterThan(0);
    expect(step).toBeGreaterThan(build);
    // Arada başka adım yok: build ile bütçe adımı arasında tek "- name:".
    expect(ci.slice(build, step).match(/- name:/g)?.length).toBe(1);
  });

  it('npm betiği bundle:check build + ölçüm', () => {
    const pkg = JSON.parse(read('package.json')) as { scripts: Record<string, string> };
    expect(pkg.scripts['bundle:check']).toBe('vite build && node scripts/bundle-budget.mjs');
  });

  it('eslint: kabuk dosyaları Radix/shadcn overlay import edemez', () => {
    const e = read('eslint.config.js');
    const block = e.slice(e.indexOf("'src/components/AppShell.tsx'"));
    for (const f of ['src/App.tsx', 'src/main.tsx', 'src/components/Topbar.tsx', 'src/components/Sidebar.tsx', 'src/components/Toaster.tsx', 'src/components/ContextBar/**']) {
      expect(e).toContain(`'${f}'`);
    }
    expect(block).toContain("'no-restricted-imports'");
    for (const p of ["'radix-ui'", "'@radix-ui/*'", "'cmdk'", "'sonner'", "'**/components/shadcn/**'", "'**/coremetry/overlay/**'"]) {
      expect(block).toContain(p);
    }
  });
});
