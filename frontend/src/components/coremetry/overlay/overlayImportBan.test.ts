import { describe, it, expect } from 'vitest';
import { ESLint } from 'eslint';
import { resolve } from 'node:path';

// overlayImportBan — v0.10.1615 (shadcn Faz 1 R7, plan §7.2). Yasak
// eslint.config.js'te; burada GERÇEK ESLint çalıştırılıp dosya yoluna göre
// sonucu ölçülüyor (yapılandırma metnini değil): pin değil davranış.
const FE = resolve(__dirname, '../../../..');
const eslint = new ESLint({ cwd: FE });

async function restricted(code: string, file: string): Promise<boolean> {
  const [r] = await eslint.lintText(code, { filePath: resolve(FE, file) });
  return r.messages.some(m => m.ruleId === 'no-restricted-imports');
}

describe('Radix overlay importu yalnız components/coremetry içinden', () => {
  const OVERLAYS = ['dialog', 'alert-dialog', 'sheet', 'popover', 'dropdown-menu', 'context-menu', 'tooltip', 'command', 'select'];

  it('sayfa: shadcn overlay dosyası (takma ad + göreli) ve radix-ui yasak', async () => {
    for (const n of OVERLAYS) {
      expect(await restricted(`import { X } from '@/components/shadcn/${n}';\nexport const y = X;\n`, 'src/pages/Probe.tsx'), n).toBe(true);
    }
    expect(await restricted(`import { X } from '../components/shadcn/dialog';\nexport const y = X;\n`, 'src/pages/Probe.tsx')).toBe(true);
    expect(await restricted(`import { Dialog } from 'radix-ui';\nexport const y = Dialog;\n`, 'src/pages/Probe.tsx')).toBe(true);
    expect(await restricted(`import { Root } from '@radix-ui/react-dialog';\nexport const y = Root;\n`, 'src/features/x/Probe.ts')).toBe(true);
  });

  it('sayfa: overlay olmayan shadcn dosyası ve coremetry/overlay serbest', async () => {
    expect(await restricted(`import { Button } from '@/components/shadcn/button';\nexport const y = Button;\n`, 'src/pages/Probe.tsx')).toBe(false);
    expect(await restricted(`import { Dialog } from '@/components/coremetry/overlay';\nexport const y = Dialog;\n`, 'src/pages/Probe.tsx')).toBe(false);
  });

  it('coremetry/, shadcn/, ai-elements/ içinden serbest', async () => {
    for (const f of ['src/components/coremetry/overlay/Probe.tsx', 'src/components/shadcn/probe.tsx', 'src/components/ai-elements/Probe.tsx']) {
      expect(await restricted(`import { Dialog } from 'radix-ui';\nexport const y = Dialog;\n`, f), f).toBe(false);
    }
  });

  it('kabuk dosyası daha sıkı blokta: coremetry/overlay de yasak (ilk yük, plan §1.2)', async () => {
    expect(await restricted(`import { Dialog } from '@/components/coremetry/overlay';\nexport const y = Dialog;\n`, 'src/components/Topbar.tsx')).toBe(true);
    expect(await restricted(`import { Button } from '@/components/shadcn/button';\nexport const y = Button;\n`, 'src/components/Topbar.tsx')).toBe(true);
    // v0.10.1617 (R9): barrel overlay'i taşır → kabukta yasak; dosya yolu serbest.
    expect(await restricted(`import { PageShell } from '@/components/coremetry';\nexport const y = PageShell;\n`, 'src/components/AppShell.tsx')).toBe(true);
    expect(await restricted(`import { PageShell } from '@/components/coremetry/PageShell';\nexport const y = PageShell;\n`, 'src/components/AppShell.tsx')).toBe(false);
  });
}, 60_000);
