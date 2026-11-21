// legacyCss — v0.10.2427. `@layer legacy` kaynak CSS'i artık iki dosya:
// globals.css (eager, index.css) + cosreChat.css (sohbet kuralları, tembel
// sohbet chunk'ıyla yüklenir; ilk yük CSS bütçesi). Stil ratchet/pin
// testleri ikisini BİRLİKTE okur — taşınan kurallar kapıların dışına
// düşmesin. Sıra = tarayıcıdaki kaskad sırası (cosreChat globals'tan sonra).
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

export const LEGACY_CSS_FILES = ['globals.css', 'cosreChat.css'] as const;

export function readLegacyCss(): string {
  return LEGACY_CSS_FILES.map((f) => readFileSync(resolve(__dirname, f), 'utf8')).join('\n');
}
