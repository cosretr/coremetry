// statusPalette.pin.test.ts — v0.10.922 (sade palet adım 1).
//
// Operatör kararları: K5 — normal/sağlıklı durum NÖTR, yeşil yalnız bir
// GEÇİŞ (resolved) için; "bir olgu = bir sinyal". Dört yüzey (Inbox,
// Problems satırı, Exceptions listesi, Problem/Exception detayı) aynı durumu
// dört ayrı tonda basıyordu ("open" amber/kırmızı, "acknowledged" mavi/amber,
// "regressed" kırmızı/amber, "new" kırmızı/amber/mavi). Tek sözlük
// (STATUS_TONE → TriageStatusBadge); bu pin hem sözlüğün İÇERİĞİNİ hem de
// tüketicilerin ondan başka ton seçmediğini çiviler.
//
// v0.10.929 (K5) — sözlük ProblemDetail.tsx'ten hafif yaprak modüle
// (./statusTone.tsx) taşındı. İçerik YALNIZ burada çivilenir (tek yer);
// diğer pinler (k5PagesAM, k5Leftovers, k5Residue) tüketicilerin yaprağı
// içe aktardığını çiviler, sözlüğü kopyalamaz.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');
const detail = read('./ProblemDetail.tsx');
const leaf = read('./statusTone.tsx');
const section = read('./ProblemsSection.tsx');
const anomalies = read('./AnomaliesPage.tsx');
const inbox = read('../../pages/Inbox.tsx');
const drawer = read('../../components/InboxTriageDrawer.tsx');

// Yorum satırları eski tonları tarihçe olarak anıyor; kod pinleri yorumsuz
// metin üzerinde koşar.
const code = (src: string) => src
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/^\s*\/\/.*$/gm, '');

// v0.10.929 (K5) — sözlük yaprakta, dışa aktarılmıyor (react-refresh):
// içerik kaynaktan okunur. TEK içerik pini burası.
function statusTone(): Record<string, string> {
  const m = leaf.match(/const STATUS_TONE: Record<string, string> = \{([\s\S]*?)\};/);
  expect(m, 'STATUS_TONE statusTone.tsx içinde tanımlı').not.toBeNull();
  const out: Record<string, string> = {};
  for (const [, k, v] of m![1].matchAll(/(\w+): '(b-\w+)'/g)) out[k] = v;
  return out;
}

describe('v0.10.922 — tek durum → ton sözlüğü', () => {
  it('eşleme: open/ack/ignored/muted nötr, new/regressed amber, resolved yeşil', () => {
    expect(statusTone()).toEqual({
      open: 'b-gray', active: 'b-gray', acknowledged: 'b-gray', ignored: 'b-gray', muted: 'b-gray',
      resolved: 'b-ok',
      regressed: 'b-warn', new: 'b-warn',
    });
  });

  it('bilinmeyen durum nötr düşer (gizlenmez)', () => {
    expect(leaf).toContain("STATUS_TONE[s.toLowerCase()] ?? 'b-gray'");
  });

  it('dört yüzey de sözlükten okur, kendi tonunu seçmez', () => {
    expect(inbox).toContain('<TriageStatusBadge s={k} label={k} title={`Durum: ${s}`} />');
    expect(anomalies).toContain('<TriageStatusBadge s={s} label={label}');
    expect(section).toContain('<ProblemStatusBadge status={p.status} />');
    expect(detail).toContain('<ProblemStatusBadge status={problem.status} />');
    expect(detail).toContain('<TriageStatusBadge s={state} label={STATE_LABEL[state]} />');
    // Eski el-yapımı durum rozetleri geri gelmesin.
    for (const src of [inbox, anomalies, section, detail].map(code)) {
      expect(src).not.toMatch(/className="badge b-(err|warn|ok)">(OPEN|ACK|RESOLVED)</);
      expect(src).not.toMatch(/=== 'acknowledged'\s*\?\s*'b-/);
    }
  });

  it('exception detayı new → NEW (liste ile aynı kelime)', () => {
    expect(detail).toContain("new: 'NEW', regressed: 'REGRESSED'");
  });
});

// v0.10.929 (K5) — sözlük hafif yaprakta; ProblemDetail yalnız içe/yeniden
// dışa aktarır. Yaprak hiçbir proje modülünü içe aktarmaz: RootCausePanel,
// Incident, Incidents, Watchers onu ProblemDetail'in ağır zinciri (TimeChart,
// AI panelleri, RootCausePanel → döngü) olmadan kullanır.
describe('v0.10.929 — durum sözlüğü hafif yaprak modülde', () => {
  it('yaprak yalnız React TİPİ içe aktarır (ProblemDetail/ağır zincir yok)', () => {
    const imports = leaf.match(/^import .*$/gm) ?? [];
    expect(imports).toEqual(["import type { CSSProperties } from 'react';"]);
    expect(code(leaf)).not.toContain('ProblemDetail');
    // Yeniden dışa aktarma (`export … from`) ya da dinamik import da ağır
    // modülü yaprağın parçasına çekerdi: tek `from` 'react', `import(` yok.
    expect(code(leaf).match(/\bfrom\s+'[^']+'/g) ?? []).toEqual(["from 'react'"]);
    expect(code(leaf)).not.toMatch(/\bimport\s*\(/);
  });

  it('ProblemDetail sözlüğü TANIMLAMAZ; yapraktan içe ve yeniden dışa aktarır', () => {
    const d = code(detail);
    expect(d).not.toMatch(/const STATUS_TONE\b/);
    expect(d).not.toMatch(/function (TriageStatusBadge|ProblemStatusBadge)\(/);
    expect(d).toContain("import { TriageStatusBadge, ProblemStatusBadge } from './statusTone';");
    expect(d).toContain("export { TriageStatusBadge, ProblemStatusBadge } from './statusTone';");
  });

  // Elle kopyalanmış kurallar sözlükten kayıyordu (v0.10.929 incelemesi).
  const consumers = [
    '../../components/RootCausePanel.tsx',
    '../../pages/Incident.tsx',
    '../../pages/Incidents.tsx',
    '../../pages/Watchers.tsx',
  ];
  for (const f of consumers) {
    it(`${f}: yapraktan içe aktarır, ProblemDetail'den değil`, () => {
      const src = code(read(f));
      expect(src).toContain("import { TriageStatusBadge } from '@/features/anomalies/statusTone';");
      expect(src).not.toMatch(/from '@\/features\/anomalies\/ProblemDetail'/);
      expect(src).toMatch(/<TriageStatusBadge s=/);
      // El-yapımı durum rozeti / yerel ton kuralı geri gelmesin.
      expect(src).not.toMatch(/className="badge b-\w+"[^>]*>(OPEN|ACK|RESOLVED)</);
      expect(src).not.toMatch(/s === 'resolved' \? 'b-ok'/);
    });
  }
});

describe('v0.10.922 — bir olgu = bir sinyal (renk yalnız öncelikte)', () => {
  it('Problems satırı: zemin tonu yok, kırmızı değer yok, şiddet nötr, P atomu', () => {
    const s = code(section);
    expect(s).not.toContain('color-mix(in srgb, var(--err)');
    expect(s).not.toContain("<b style={{ color: 'var(--err)' }}>");
    expect(s).toContain("<b style={{ color: 'var(--text)', fontWeight: 600 }}>{fmtFixed(p.value, 2)}</b>");
    expect(s).toContain('return <span className="badge b-gray">{s.toUpperCase()}</span>;');
    expect(s).toContain("import { PriorityBadge } from '@/components/ui/PriorityBadge';");
    expect(s).not.toMatch(/function PriorityBadge\(/);
  });

  it('Problem detay şeridi: şiddet nötr, öncelik paylaşılan atomdan', () => {
    const d = code(detail);
    expect(d).toContain('<span className="badge b-gray">{problem.severity.toUpperCase()}</span>');
    expect(d).toContain('{problem.priority && <PriorityBadge p={problem.priority} reason={problem.priorityReason} />}');
    expect(d).not.toContain('sevCls');
  });

  it('örnek trace satırlarında sabit ERROR rozeti yok', () => {
    expect(code(detail)).not.toContain('ERROR</span>');
  });

  it('üst veri (ANOMALY, runbook, atanan, env, AI, blast) mavi değil', () => {
    for (const src of [inbox, section, detail].map(code)) {
      expect(src).not.toContain('b-info');
    }
  });

  it('Inbox problem değeri kırmızı değil', () => {
    expect(code(inbox)).toContain("<b style={{ color: 'var(--text)', fontWeight: 600 }}>{fmtFixed(it.problem.value, 2)}</b>");
  });
});

// İnceleme bulgusu (v0.10.922): Inbox satırından açılan drawer atananı
// satırla aynı (nötr) tonda basar — tıklayınca renk değişmez.
describe('v0.10.922 — Inbox drawer satırla aynı', () => {
  it('atanan kişi rozeti nötr (mavi değil)', () => {
    const d = code(drawer);
    expect(d).not.toContain('badge b-info');
    expect(d).toMatch(/item\?\.assignee && \(\s*<span className="badge b-gray"/);
  });
});
