import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';

// settingsPalette.pin.test.ts — v0.10.929 (K5 artıkları, operatör kararı
// v0.10.920/922).
//
// KURAL: bir ayarın hâli (kurulu / kurulmamış / kapalı / aktif / saklı /
// çözüldü) NÖTR — ne sağlık ne sapma. Yeşil yalnız kullanıcının AZ ÖNCE
// bastığı düğmenin doğrudan geri bildirimi (kaydet/test flash'ı, test
// sonucu, dry-run, "✓ Seçildi", provision mesajı).
//   • Üst durum bantları ConfigStatusBanner'dan geçer ve sağlık
//     değiştiricilerini (-operational / -degraded / -outage) DEĞİL, semantik
//     nötr sınıfları (.status-banner-neutral / -pill-neutral) taşır; inline
//     renk yok.
//   • b-ok / 'success' tonu hiçbir ayar dosyasında yok (DevOps dry-run'ın iki
//     eylem sonucu hariç); "· stored/saklı/kayıtlı" --text3.
//   • var(--ok) yalnız bir ok/err KOŞULUNUN yeşil kolu (msg.kind === 'ok',
//     test.ok, tr.ok) ya da iki listelenmiş eylem geri bildirimi.
//   • Maintenance DISABLED/PAST nötr (eskiden kırmızı/yeşil); ACTIVE amber.
//   • Backup "+N new" vurgu (--accent2), yeşil değil.
function stripComments(src: string): string {
  // JSX yorumları + satır başı blok/satır yorumları; string içindeki '/*'
  // (ör. glob metinleri) koda dokunmasın diye genel /*…*/ taraması yok.
  return src
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\*[\s\S]*?\*\//gm, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}
const read = (f: string) => stripComments(readFileSync(resolve(__dirname, f), 'utf8'));
const files = readdirSync(__dirname)
  .filter(f => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f));

// Koşulsuz yeşil — ikisi de kullanıcı eyleminin doğrudan geri bildirimi.
const UNCONDITIONAL_OK: Record<string, RegExp> = {
  'LdapTab.tsx': /color: 'var\(--ok\)', marginBottom: 6/,        // "✓ Seçildi" (kullanıcı az önce seçti)
  'LdapUserPicker.tsx': /color: 'var\(--ok\)' \}\}>\{provisionMsg\}/, // provision sonucu
};
const OK_BRANCH = /(kind === 'ok'|\btest\.ok|\btr\.ok) \?/;

describe('Settings sade palet — K5 artıkları (v0.10.929)', () => {
  it('ayar durum bantları nötr: status-banner/-pill modifier yok, ConfigStatusBanner var', () => {
    for (const f of files) {
      expect(read(f), f).not.toMatch(/status-(banner|pill)-(operational|degraded|outage)/);
    }
    const banner = read('shared.tsx').match(/export function ConfigStatusBanner[\s\S]*?\n}\n/)?.[0] ?? '';
    expect(banner).not.toMatch(/--(ok|warn|err)\b/);
    // v0.10.929 (K5, lider kararı) — inline renk yerine semantik nötr sınıflar;
    // değerlerin -operational ile aynı kalması k5HealthNeutral.pin'de çivili.
    expect(banner).toContain('<div className="status-banner status-banner-neutral">');
    expect(banner).toContain('<span className="status-pill status-pill-neutral">{label}</span>');
    expect(banner).not.toMatch(/\b(background|borderColor|color):/);
    for (const f of ['MetricsBackendTab.tsx', 'KibanaTab.tsx', 'TempoTab.tsx', 'LogBridgeTab.tsx',
      'ElasticTab.tsx', 'AiTab.tsx', 'McpServersTab.tsx', 'DevOpsTab.tsx']) {
      expect(read(f), f).toMatch(/<ConfigStatusBanner label=/);
    }
  });

  it('hiçbir ayar dosyası b-ok / success tonu (durum = yeşil) basmaz', () => {
    for (const f of files) {
      const src = read(f);
      expect(src, f).not.toMatch(/\bb-ok\b/);
      expect(src, f).not.toMatch(/tone="success"/);
      // v0.10.929 (K5, lider kararı) — JSX özniteliği dışındaki yollar da:
      // `return 'success'` (EntitiesTab statusTone'un eski 'ok' kolu),
      // `tone={x ? 'success' : …}`. Tek muafiyet DevOps (aşağıda sayılı).
      if (f !== 'DevOpsTab.tsx') expect(src, f).not.toMatch(/['"`]success['"`]/);
    }
    // Tek 'success': DevOps dry-run sonucu (kullanıcının az önce koşturduğu).
    const devops = read('DevOpsTab.tsx');
    expect(devops.match(/['"`]success['"`]/g)?.length).toBe(2);
    expect(devops.match(/'success'/g)?.length).toBe(2);
    expect(devops).toContain("<Badge tone={dry.ok ? 'success' : 'danger'}>");
  });

  it("EntitiesTab senkron koşusu: 'ok' nötr, yalnız partial/failed renkli", () => {
    const fn = read('EntitiesTab.tsx').match(/function statusTone\([\s\S]*?\n}\n/)?.[0] ?? '';
    expect(fn).toContain("if (s === 'failed') return 'danger';");
    expect(fn).toContain("if (s === 'partial') return 'warning';");
    expect(fn).toContain("return 'neutral';");
    expect(fn).not.toMatch(/'ok'/);
  });

  it('var(--ok) yalnız eylem geri bildiriminin yeşil kolu', () => {
    for (const f of files) {
      const lines = read(f).split('\n').filter(l => l.includes('var(--ok)'));
      for (const l of lines) {
        if (OK_BRANCH.test(l)) continue; // FlashBox dahil: kind === 'ok' ? 'var(--ok)'
        expect(UNCONDITIONAL_OK[f]?.test(l), `${f}: ${l.trim()}`).toBe(true);
      }
    }
  });

  it('"· stored / saklı / kayıtlı" işaretleri --text3', () => {
    for (const f of ['MetricsBackendTab.tsx', 'TempoTab.tsx', 'ElasticTab.tsx', 'ClustersTab.tsx', 'DevOpsTab.tsx', 'OracleTab.tsx']) {
      const markers = read(f).split('\n').filter(l => /· (stored|saklı|kayıtlı)</.test(l));
      expect(markers.length, f).toBeGreaterThan(0);
      for (const l of markers) expect(l, f).toContain("color: 'var(--text3)'");
    }
    expect(read('OracleTab.tsx')).not.toMatch(/\bis-ok\b/);
  });

  it('Oracle hüküm tonu yeşil içermez (iyi yapılandırma = nötr; değerler oracleProbe.test.ts)', () => {
    expect(read('oracleProbe.ts')).toContain("export type ScanTone = 'b-warn' | 'b-err' | 'b-gray';");
  });

  it('Maintenance: DISABLED/PAST nötr, ACTIVE amber; Backup "+N new" --accent2', () => {
    const mt = read('MaintenanceTab.tsx');
    expect(mt).toContain('<span className="badge b-gray" style={{ fontSize: 9 }}>DISABLED</span>');
    expect(mt).toContain('<span className="badge b-gray" style={{ fontSize: 9 }}>PAST</span>');
    expect(mt).toContain('<span className="badge b-warn" style={{ fontSize: 9 }}>ACTIVE</span>');
    expect(read('BackupTab.tsx')).toContain("color: d.willAdd.length > 0 ? 'var(--accent2)' : 'var(--text3)'");
  });
});
