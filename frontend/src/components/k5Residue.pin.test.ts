// k5Residue.pin.test.ts — v0.10.929 (K5 artıkları, components/features/lib).
//
// Operatör kararı K5 (v0.10.920/922): sağlıklı/normal durum NÖTR; yeşil
// yalnız bir GEÇİŞ (resolved/cleared/completed, kullanıcı eyleminin başarı
// geri bildirimi) ve VERİ (grafik serisi) için. Renk başka yerde yalnız
// sapma (warn/err) ve seçim (accent). statusPalette.pin.test.ts sözlüğü
// çiviliyor; bu pin v0.10.929'da nötrleşen yüzeylerin geri yeşillenmemesini
// ve geçiş yeşillerinin (bilerek) KALDIĞINI çiviler.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { healthToken } from '@/lib/health';
import { EVENT_KIND_COLOUR } from '@/lib/eventRegions';
import { statusTone as rolloutStatusTone } from '@/lib/rolloutRow';

const read = (rel: string) => readFileSync(resolve(__dirname, '..', rel), 'utf8');
// Yorumlar eski tonları tarihçe olarak anıyor; pinler yorumsuz metinde koşar.
const code = (rel: string) => read(rel)
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/^\s*\/\/.*$/gm, '');

const GREEN = /var\(--ok\)|\bb-ok\b|46,\s*160,\s*67|63,\s*185,\s*80/;

describe('v0.10.929 — sağlıklı durum nötr (yeşil yok)', () => {
  const neutralised = [
    'components/RootCauseRibbon.tsx',
    'components/ai/ChatTraceList.tsx',
    'components/ServiceMapNodeDrawer.tsx',
    'components/TrendDelta.tsx',
    'components/TopologyFlowGraph.tsx',
    'components/topology/FocusedNeighborhood.tsx',
    'components/SavedViewsBar.tsx',
    'components/RCAVerdictPanel.tsx',
    'components/AIAnalysisPanel.tsx',
    'components/StackTrace.tsx',
    'components/CorrelationContextDrawer.tsx',
    'components/DBQueriesPanel.tsx',
    'components/DeployHistoryPanel.tsx',
    'components/LogPatternsPanel.tsx',
    'components/LogFieldsPanel.tsx',
    'components/dashboard/PanelRenderer.tsx',
    'components/viz/TimeSeriesPanel.tsx',
    'features/anomalies/EvaluatorStatus.tsx',
    'features/anomalies/ProblemNotifyPanel.tsx',
    'features/anomalies/ExternalEvidencePanel.tsx',
    'features/dependencies/CallerSection.tsx',
    'features/dependencies/DetailDrawer.tsx',
    'features/dependencies/panels/OraclePanel.tsx',
    'lib/health.ts',
    'lib/serviceHealth.ts',
    'lib/eventRegions.ts',
    'lib/insightCard.ts',
  ];
  for (const f of neutralised) {
    it(`${f}: yeşil token/sınıf/literal yok`, () => {
      expect(code(f)).not.toMatch(GREEN);
    });
  }

  it('hata oranı %0 rozetleri gri (ok değil)', () => {
    for (const f of ['components/DependenciesTable.tsx', 'features/dependencies/CallerSection.tsx', 'features/dependencies/DetailDrawer.tsx']) {
      const s = code(f);
      expect(s).not.toMatch(/errCls = [^;]*'ok';/);
      expect(s).toMatch(/errCls = [^;]*'gray';/);
    }
    const t = code('components/DependenciesTable.tsx');
    // v0.10.929 — trend çipi satırın Err% eşikleriyle AYNI (>5 err, >0 warn);
    // >1'e geri kayarsa 0<err≤1 satırda amber, çipte gri olur.
    expect(t).toMatch(/trend\.curErrorRate > 5 \? 'err'\s*:\s*trend\.curErrorRate > 0 \? 'warn'\s*:\s*'gray'/);
    expect(t).not.toContain("'warn' : 'ok'");
    expect(t).not.toContain('var(--ok)');
  });

  it('Stat/GaugeStat/UP hapı: sağlıklı dal nötr (WaitClassesBar veri renkleri hariç)', () => {
    const s = code('features/dependencies/panels/shared.tsx');
    expect(s).not.toContain('var(--ok)');
    expect(s).toContain(": tone === 'warn' ? 'var(--warn)' : 'var(--text3)';");
    expect(s).toContain("color: status === 'up' ? 'var(--text2)' : 'var(--err)',");
  });

  it('sağlıklı trace/exemplar: yalnız ERROR rozeti, OK ekran okuyucuya', () => {
    for (const f of ['components/RootCausePanel.tsx', 'components/RootCauseRibbon.tsx', 'components/ai/ChatTraceList.tsx']) {
      expect(code(f)).toContain('<span className="sr-only">OK</span>');
    }
  });

  it('streams "aktif anomali yok" kutusu nötr', () => {
    const s = code('features/anomalies/streams.tsx');
    expect(s).not.toContain('var(--ok)');
    expect(s).toContain("<Check size={13} strokeWidth={2} style={{ color: 'var(--text3)', flexShrink: 0 }} />");
  });

  it('ChatBubble araç adımı ok → gri; AI güven "yüksek" → gri', () => {
    expect(code('components/ai/ChatBubble.tsx')).toContain('<span className="badge b-gray">ok</span>');
    expect(code('components/AIAnalysisPanel.tsx')).toContain("yuksek: 'b-gray'");
  });

  it('healthToken: eşikler aynı, sağlıklı dal nötr', () => {
    expect(healthToken(0)).toBe('var(--text3)');
    expect(healthToken(1)).toBe('var(--text3)');
    expect(healthToken(1.5)).toBe('var(--warn)');
    expect(healthToken(6)).toBe('var(--err)');
  });

  it('deploy işareti kategori: --text2 (AnnotationLane ile aynı)', () => {
    expect(EVENT_KIND_COLOUR.deploy).toBe('var(--text2)');
    expect(code('components/viz/TimeSeriesPanel.tsx')).toContain("deploy: 'var(--text2)',");
  });

  // v0.10.929 (K5) — canvas-güvenli: resolveVar yalnız tam `var(--x)`'i
  // çözer; color-mix/var karışımı canvas'ta geçersiz renk olur.
  it('olay türü renkleri canvas-güvenli: literal renk ya da tam var(--token)', () => {
    const literal = /^(#[0-9a-fA-F]{3,8}|rgba?\([\d.,\s]+\)|[a-z]+)$/;
    const token = /^var\(--[\w-]+\)$/;
    for (const [kind, c] of Object.entries(EVENT_KIND_COLOUR)) {
      expect(literal.test(c) || token.test(c), `${kind}: ${c}`).toBe(true);
      expect(c).not.toContain('color-mix');
    }
    expect(EVENT_KIND_COLOUR.config).toBe('var(--accent)');
  });

  it('topoloji: "yeni" kenar vurgu, ad alanı paleti seri paletinden', () => {
    const s = code('components/TopologyFlowGraph.tsx');
    // v0.10.929 (K5) — hata kırmızısı "yeni"den önce gelir; yeni kenar ayrıca
    // kendi kesik desenini alır (accent2 ≈ hover --accent, light/redhat).
    expect(s).toContain("const stroke = errorish ? 'var(--err)' : e.isNew ? 'var(--accent2)' : hot ? 'var(--accent)'");
    expect(s).toContain('const newDash = e.isNew ? NEW_EDGE_DASH : undefined;');
    expect(s).toContain('strokeDasharray: newDash }}');
    expect(s).toMatch(/const NEW_EDGE_DASH = '\d+ \d+';/);
    expect(s).not.toContain("NEW_EDGE_DASH = '4 6'"); // normal akış deseniyle aynı olamaz
    expect(s).toContain('const palette = seriesPalette();');
    expect(s).not.toMatch(/'--warn',\s*'--ok'/);
  });

  it('uygulanan kayıtlı görünüm bir SEÇİM: vurgu dili', () => {
    const s = code('components/SavedViewsBar.tsx');
    expect(s).toContain("? 'var(--accent-bg)'");
    expect(s).toContain("? '1px solid var(--accent)'");
  });
});

describe('v0.10.929 — normal durum için kırmızı/amber yok', () => {
  it('RootCausePanel açık problem rozeti STATUS_TONE open ile aynı (sözlükten)', () => {
    // v0.10.929 (K5) — elle 'b-gray' değil: tek sözlük (statusTone yaprağı).
    const s = code('components/RootCausePanel.tsx');
    expect(s).toContain('<TriageStatusBadge s="open" label="OPEN" />');
    expect(s).not.toMatch(/className="badge b-\w+">OPEN</);
  });

  it('RootCausePanel rollout durumu rolloutRow statusTone()\'dan türer (kayamaz)', () => {
    // v0.10.929 (K5) — in_progress sürüyor (info), sapma değil; stalled /
    // rolled_back sapma kalır; completed GEÇİŞ.
    const s = code('components/RootCausePanel.tsx');
    expect(s).toContain("statusTone as rolloutStatusTone } from '@/lib/rolloutRow';");
    expect(s).toContain('<Badge tone={rolloutStatusTone(ev.status)}>');
    expect(s).not.toMatch(/ev\.status === '\w+' \? 'b-/);
    expect(rolloutStatusTone('in_progress')).toBe('info');
    expect(rolloutStatusTone('completed')).toBe('success');
    expect(rolloutStatusTone('stalled')).toBe('warning');
    expect(rolloutStatusTone('rolled_back')).toBe('danger');
    expect(rolloutStatusTone('superseded')).toBe('neutral');
  });

  it('anomali ACTIVE nötr; CLEARED geçiş yeşil — üç yüzey tek kural', () => {
    expect(code('features/anomalies/streams.tsx')).toContain("e.status === 'active' ? 'b-gray' : 'b-ok'");
    expect(code('features/anomalies/AnomalyDetailDrawer.tsx')).toContain("event.status === 'active' ? 'neutral' : 'success'");
    const w = code('features/anomalies/AnomalyWindowTable.tsx');
    expect(w).toContain('<Badge tone="neutral">active</Badge> : <Badge tone="success">cleared</Badge>');
    expect(w).toContain(`{e.verdict === 'anomaly' && <Badge tone="neutral"`);
  });
});

describe('v0.10.929 — geçiş yeşilleri BİLEREK kalır', () => {
  it('rollout / runbook "completed" yeşil (geçiş)', () => {
    // RootCausePanel bunu kullanır (yukarıda). v0.10.929'da bu panelde YENİ
    // yeşil: eskiden b-gray'di; tamamlanan rollout bir GEÇİŞ (K5), rolloutRow
    // ve Rollouts sayfasıyla aynı dil.
    expect(rolloutStatusTone('completed')).toBe('success');
    expect(code('components/ProblemRunbookPanel.tsx')).toContain("completed: 'b-ok'");
    expect(code('lib/rolloutRow.ts')).toContain("case 'completed': return 'success';");
  });

  it('alarm çözüldü işareti yeşil (geçiş)', () => {
    expect(code('components/charts/AnnotationLane.tsx')).toContain("alert_resolved: 'var(--ok)'");
  });
});
