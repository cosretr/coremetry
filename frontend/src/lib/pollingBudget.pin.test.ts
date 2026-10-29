// pollingBudget.pin.test.ts — v0.10.1430 yoklama bütçesi RATCHET'i.
//
// CLAUDE.md: "polling ≥ 10 s (except /api/health 5 s); every poller pauses
// on document.hidden". v0.10.1430 global auto-refresh'i (lib/autoRefresh.ts)
// getirirken envanter iki şeyi gösterdi: (1) göreli pencere mount'ta donuyordu,
// yani pencere anahtarlı `refetchInterval`ler AYNI geçmiş pencereyi
// istiyordu (endpoints 30 s, service map 30 s — kaldırıldı); (2) saat sayısı
// dağınıktı (Dashboard refreshTick, Clusters liveTick/?live=1 — kaldırıldı).
// Bu pin ikisinin geri sızmasını yakalar (defaultRange.pin.test.ts şablonu):
//   • her sayısal `refetchInterval` literali ≥ 10_000, tek istisna
//     lib/queries/health.ts (5 s);
//   • `refetchInterval:` ya da `setInterval(` taşıyan dosyalar KAPALI liste —
//     yeni bir yoklama döngüsü = bu listeye BİLEREK eklenir (ve önce "global
//     tik + LIVE_META yeterli mi?" sorulur).
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

const SRC = resolve(__dirname, '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !p.includes('.test.')) out.push(p);
  }
  return out;
}

// Muhafazakâr yorum temizliği: satır başı `//` ve `/*`, JSX `{/* */}`. Satır
// içi blok yorumları BİLEREK dokunulmaz — bir SQL dizgesindeki `/*` yüzlerce
// satır kodu yutabilir ve pin yalan söyler (AdminClickhouse.tsx'te ölçüldü).
const code = (s: string) => s
  .replace(/\{\/\*[\s\S]*?\*\/\}/g, '')
  .replace(/^\s*\/\*[\s\S]*?\*\//gm, '')
  .replace(/^\s*\/\/.*$/gm, '');

const REFETCH_FILES = [
  'components/ServiceAttentionStrip.tsx',
  'features/anomalies/ProblemsSection.tsx',
  'lib/queries/admin.ts',
  'lib/queries/aiEval.ts',
  'lib/queries/alerts.ts',
  'lib/queries/anomalies.ts',
  'lib/queries/entities.ts',
  'lib/queries/events.ts',
  'lib/queries/health.ts',
  'lib/queries/inbox.ts',
  'lib/queries/incidents.ts',
  // v0.10.1557 — lib/queries/messaging.ts çıktı (Kafka istemcileri sekmesi sorgusu kaldırıldı).
  'lib/queries/monitors.ts',
  'lib/queries/problems.ts',
  'lib/queries/rollouts.ts',
  'lib/queries/runbooks.ts',
  'lib/queries/slos.ts',
  'lib/queries/sreEval.ts',
  'lib/queries/watchers.ts',
  'pages/AdminClickhouse.tsx',
  'pages/AdminStats.tsx',
  'pages/Dashboards.tsx',
  'pages/PublicStatus.tsx',
].sort();

const SET_INTERVAL_FILES = [
  'components/RefreshPicker.tsx',        // 5 s altyazı re-render'ı (istek yok, hidden guard)
  'lib/useAutoRefresh.ts',               // TEK global saat
  'pages/AIObservability.tsx',
  'pages/AdminClickhouse.tsx',
  'pages/Profiling.tsx',
  'pages/Rollouts.tsx',
  'pages/settings/KnowledgeTab.tsx',
  'pages/settings/WikiKnowledgeSection.tsx',
].sort();

describe('v0.10.1430 — yoklama bütçesi', () => {
  const files = walk(SRC).map(f => ({ rel: relative(SRC, f), src: code(readFileSync(f, 'utf8')) }));

  it('her sayısal refetchInterval ≥ 10_000; tek istisna /api/health (5 s)', () => {
    const bad: string[] = [];
    for (const { rel, src } of files) {
      for (const m of src.matchAll(/refetchInterval:\s*(\d[\d_]*)/g)) {
        const ms = Number(m[1].replace(/_/g, ''));
        if (ms < 10_000 && rel !== 'lib/queries/health.ts') bad.push(`${rel}: ${m[1]}`);
      }
    }
    expect(bad).toEqual([]);
    const health = files.find(f => f.rel === 'lib/queries/health.ts')!.src;
    expect(health).toMatch(/refetchInterval:\s*5_000/);
  });

  it('refetchInterval taşıyan dosyalar kapalı liste', () => {
    const got = files.filter(f => /refetchInterval\s*:/.test(f.src)).map(f => f.rel).sort();
    expect(got).toEqual(REFETCH_FILES);
  });

  it('setInterval taşıyan dosyalar kapalı liste; Dashboard/Clusters kendi saatini taşımaz', () => {
    const got = files.filter(f => /\bsetInterval\s*\(/.test(f.src)).map(f => f.rel).sort();
    expect(got).toEqual(SET_INTERVAL_FILES);
    expect(got).not.toContain('pages/Dashboard.tsx');
    expect(got).not.toContain('pages/Clusters.tsx');
  });

  it('her ham setInterval gövdesi document.hidden\'ı sorar (RQ dışı poller kuralı)', () => {
    for (const rel of SET_INTERVAL_FILES) {
      const src = files.find(f => f.rel === rel)!.src;
      // Profiling.tsx: setInterval bir şablon dizgesinin İÇİNDE (operatöre
      // gösterilen sunucu-tarafı ajan kodu), tarayıcı poller'ı değil.
      if (rel === 'pages/Profiling.tsx') continue;
      expect(src, `${rel} setInterval without document.hidden guard`).toMatch(/document\.hidden/);
    }
  });
});
