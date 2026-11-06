// v0.10.417 — log arama denetimi B5: satır sarma anahtarı. Koşullu
// className undefinedCssRefs kapısından kaçar → CSS kuralı burada pinli.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('log line wrap toggle (B5)', () => {
  // v0.10.1830 (Log Search Faz 1 dilim 1.7) — "⤶ sar" Görünüm ▾'deki gövde
  // satır yüksekliğine katıldı (Otomatik = sarılı). Tarayıcı tercihi
  // (`logs.wrap`) URL'de `rh` yokken varsayılanı belirler; araç satırında
  // ayrı anahtar yok.
  it('Logs.tsx: kalıcı tercih rh varsayılanı + Görünüm ▾ + display prop', () => {
    const src = read('./Logs.tsx');
    expect(src).toContain("getRaw('logs.wrap') === '1'");
    expect(src).toContain("rowLines: wrapLines ? 'auto' : 1");
    expect(src).toContain('display={<DisplayOptions value={display} onChange={setDisplay} />}');
    const bar = read('./logs/LogsTableToolbar.tsx');
    expect(bar).not.toContain('onToggleWrap');
    expect(bar).toContain('{p.display}');
  });
  it('LogTable: mesaj hücresi sınıfı (kırpma / sarma) ve title yalnız tek satırda', () => {
    const src = read('../components/LogTable.tsx');
    expect(src).toContain("className={clamp ? 'lt-clamp' : wrap ? 'lt-wrap' : undefined}");
    expect(src).toContain('title={wrap ? undefined : l.body}');
    expect(src).toContain("const rowLines: LogsLines = display?.rowLines ?? (wrap ? 'auto' : 1);");
  });
  it('globals.css: kural tanımlı, .td-full ve telefon kuralı dokunulmamış', () => {
    const css = read('../styles/globals.css');
    expect(css).toContain('.logtbl-dense > tbody > tr > td.lt-wrap {');
    expect(css).toMatch(/tbody td\.td-full \{[^}]*word-break: break-all/);
    // v0.10.420 — telefon kuralı gerçekten pinli (başlık öyle diyordu, gövde bakmıyordu).
    expect(css).toMatch(/\.logtbl-dense > tbody > tr > td \{ white-space: normal/);
  });
});
