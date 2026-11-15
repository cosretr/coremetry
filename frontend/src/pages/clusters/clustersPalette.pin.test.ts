// clustersPalette.pin.test.ts — v0.10.922 (sade palet adım 1). Kaynak pini.
//
// K5: sağlıklı/normal durum NÖTR — Clusters sayfasında geçiş (resolved/
// recovered) yok, dolayısıyla hiç yeşil (b-ok / var(--ok)) kalmamalı.
// Kategoriler (rol, süzgeç çipleri) b-info değil b-gray. "Live" noktası bir
// etkinlik göstergesi: nabız kalır, renk --text3. Sapma renkleri (b-err/
// b-warn, CPU/Mem >85 --err) KALIR — kelimeler de (healthy/reachable).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, '../Clusters.tsx'), 'utf8');

describe('Clusters — sade palet adım 1', () => {
  it('normal durum için yeşil yok (b-ok / var(--ok))', () => {
    expect(src).not.toMatch(/\bb-ok\b/);
    expect(src).not.toContain('var(--ok)');
  });
  it('kategoriler nötr: b-info yok, rol ve süzgeç çipleri b-gray', () => {
    expect(src).not.toMatch(/\bb-info\b/);
    // Clusters S6 (D3) — Nodes rolü rozet değil nötr düz metin (mockup).
    expect(src).toContain("case 'role': return dash(r.role ?? '');");
    // Clusters S5 — süzgeç çipleri coremetry/FilterBar çipi (nötr, ✕ ile
    // kaldırılır); tıklanınca silinen b-gray rozetleri kalktı.
    expect(src).toContain('<FilterBar chips={filterChips}');
    expect(src).not.toMatch(/className="badge b-gray" style=\{\{ cursor: 'pointer' \}\}/);
  });
  it('healthy/reachable kelimeleri --text3 metin olarak kalır', () => {
    // Clusters S5 — satır içi --text3 metni nötr .cl-status sınıfına taşındı.
    expect(src).toContain('<span className="cl-status">reachable</span>');
    // Clusters S4 — liste kartının "healthy" rozeti düz sözcük "sağlıklı" oldu
    // (D1); kalan tek "healthy" Namespaces tablosunun Health hücresi.
    // Clusters S12 — Health hücresi trend düğmesi: sağlıklı nötr (.cl-health), failing --err.
    expect(src).toContain("className={(r.failing ?? 0) > 0 ? 'cl-health cl-health--bad' : 'cl-health'}");
    // Liste: sağlıklı NÖTR sınıf (renk değiştiricisi yok); renk yalnız sapmada.
    expect(src).toContain('{state === \'healthy\' && <span className="cl-status">{t(\'clusters.state.healthy\', lang)}</span>}');
    expect(src).toContain('<span className="cl-status cl-status--crit">');
    expect(src).toContain('<span className="cl-status cl-status--down">{t(\'clusters.state.unreachable\', lang)}</span>');
  });
  it('sapma renkleri korunur (unreachable/failing b-err, >85 --err)', () => {
    expect(src).toContain('<span className="badge b-err">unreachable</span>');
    expect(src).toContain("`${r.failing} failing`");
    // v0.10.943 (tablo standardı dilim 3) — eşik rengi satır içi üçlüden kolon
    // tonuna taşındı (`pctTone` → .cell-err/.cell-warn/.cell-faint); eşikler aynı.
    expect(src).toContain("(p ?? 0) > 85 ? 'err' : (p ?? 0) > 60 ? 'warn' : 'faint'");
    // Clusters S6 — Nodes kolonları nodeColumns.ts'e taşındı (kendi eşik tonu);
    // burada yalnız Pods tablosunun iki % kolonu kalır.
    expect(src.match(/tone: r => pctTone\(r\.(cpuPct|memPct)\)/g)?.length).toBe(2);
  });
  // v0.10.1430 — LiveToggle (ve ?live=1) emekli: auto-refresh global Topbar
  // RefreshPicker'ında. Sayfada kendi canlı anahtarı / 10 s poll'u kalmadı.
  it('kendi Live anahtarı yok; anlık-durum sorguları global tik etiketini taşır', () => {
    expect(src).not.toMatch(/function LiveToggle|<LiveToggle|params\.get\('live'\)|const liveMs|setLiveTick/);
    expect(src).toContain("import { LIVE_META } from '@/lib/autoRefresh';");
    expect(src.match(/meta: LIVE_META,/g)?.length).toBe(10); // fleet-summary (v0.10.2185), summary, net, cpu, mem, alerts, pods, nodes, ns, deployments
  });
});
