// v0.10.787 — genel varsayılan pencere TEK sabitten: DEFAULT_RANGE_PRESET.
//
// Operatör (2026-09-19): "coremetry default last 3 hour interval göstersin".
// Öncesi 23 sayfa kendi '30m' / '1h' literalini taşıyordu; tek sabit
// olmadan "varsayılanı değiştir" 23 dosyaya dokunmak demekti ve biri
// kaçınca sayfalar arası tutarsızlık sessiz kalırdı. Bu pin:
//   • sabit '3h' ve PRESET_SECONDS anahtarı (seçici gösterebilmeli),
//   • sahip sayfalarda useUrlRange / usePageZoomRange'e '30m' ya da '1h'
//     literali KALMADI,
//   • bilinçli istisnalar kapalı liste: Hosts/Clusters 15m (canlı altyapı),
//     Events/Rollouts/AIObservability 24h (olay listeleri). Yeni bir
//     istisna bu listeye BİLEREK eklenir.
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { DEFAULT_RANGE_PRESET } from './useUrlRange';
import { PRESET_SECONDS } from './utils';

const SRC = resolve(__dirname, '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.includes('.test.')) out.push(p);
  }
  return out;
}

const OWNER_RE = /use(?:UrlRange|PageZoomRange)\('([^']+)'/g;

const EXCEPTIONS: Record<string, string> = {
  'pages/Clusters.tsx': '15m',
  'pages/Hosts.tsx': '15m',
  'pages/AIObservability.tsx': '24h',
  'pages/Events.tsx': '24h',
  'pages/Rollouts.tsx': '24h',
};

describe('v0.10.787 — DEFAULT_RANGE_PRESET', () => {
  it("is '3h' and a picker preset", () => {
    expect(DEFAULT_RANGE_PRESET).toBe('3h');
    expect(PRESET_SECONDS[DEFAULT_RANGE_PRESET]).toBe(3 * 3600);
  });

  it('owning pages pass no 30m/1h literal; exceptions are the closed list', () => {
    const files = [...walk(join(SRC, 'pages')), ...walk(join(SRC, 'features')), ...walk(join(SRC, 'components'))];
    const literals: Record<string, string[]> = {};
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      const rel = relative(SRC, f);
      for (const m of src.matchAll(OWNER_RE)) {
        (literals[rel] ??= []).push(m[1]);
      }
    }
    for (const [rel, presets] of Object.entries(literals)) {
      for (const p of presets) {
        expect(['30m', '1h'], `${rel} passes '${p}' — use DEFAULT_RANGE_PRESET`).not.toContain(p);
        expect(EXCEPTIONS[rel], `${rel} passes '${p}' but is not a listed exception`).toBe(p);
      }
    }
    // Listenin her üyesi hâlâ o literali taşıyor (bayat istisna kalmasın).
    for (const [rel, p] of Object.entries(EXCEPTIONS)) {
      expect(literals[rel], `${rel} no longer passes '${p}' — drop it from EXCEPTIONS`).toContain(p);
    }
  });
});
