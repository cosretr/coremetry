// subjectModeGone.pin.test.ts — v0.10.1407 (Tek CoSRE adım 4). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// v0.10.1400–1407 her ✨ türünü CoSRE sohbetinin sıradan bir turu yaptı ve
// çekmecenin ÖZNE KİPİ (AIDrawerBody: CopilotExplain + ikinci useChatThread +
// ikinci composer) silindi. Bu kaynak pinleri kipin geri dönmesini engeller
// (CLAUDE.md: geriye dönük uyum kalıntısı yok):
//   - silinen modüller yok ve hiçbir dosya onları import etmiyor;
//   - CopilotChat'te özne-kipi dalı / açıklama-kipi bağlamı yok, ?ai= niyeti
//     her türde tek seferlik sohbet turu;
//   - silinen ✨ istemci yöntemleri api.ts'e geri gelmedi.
// v0.10.1413 — kipten kalan ölü kod da silindi (useChatThread seed / title /
// persistContext / adopt, kayıtlı-bağlam şeridi, ölü yardımcılar); ikinci describe.
// Davranış yarısı: explainTurn.test.ts ("her ?ai= türü bir sohbet turu"),
// CopilotChat.explainTurn.test.tsx, CopilotChat.explainCards.test.tsx.
import { describe, it, expect } from 'vitest';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';

const SRC = resolve(__dirname, '../..');

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules') continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
}

const FILES = walk(SRC).map(p => ({ rel: relative(SRC, p), src: readFileSync(p, 'utf8') }));
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8');

const GONE = [
  'components/ai/AIDrawerBody.tsx',
  'components/ai/AIDrawer.tsx',
  'components/ai/aiDrawerContext.ts',
  'components/CopilotExplain.tsx',
  'components/codeAsk.ts',
  'components/ai/ServiceChartsExplainBody.tsx',
  'components/ai/LogSpikeExplainBody.tsx',
  'features/anomalies/OracleExplainContextLine.tsx',
];

describe('çekmece özne kipi silindi (v0.10.1407)', () => {
  it('silinen modüller yok', () => {
    for (const rel of GONE) expect(existsSync(join(SRC, rel)), rel).toBe(false);
  });

  it('hiçbir dosya onları import etmiyor', () => {
    const IMPORT = /from\s+['"][^'"]*\/(AIDrawerBody|AIDrawer|aiDrawerContext|CopilotExplain|codeAsk|ServiceChartsExplainBody|LogSpikeExplainBody|OracleExplainContextLine)['"]/;
    const hits = FILES.filter(f => IMPORT.test(f.src)).map(f => f.rel);
    expect(hits).toEqual([]);
  });

  it('CopilotChat: özne-kipi dalı ve açıklama-kipi bağlamı yok; ?ai= her türde tek seferlik tur', () => {
    const c = read('components/CopilotChat.tsx');
    expect(c).not.toMatch(/<AIDrawerBody|InAiDrawerContext|resumeFor|isExplainTurnKind/);
    expect(c).toContain('const drawerOpen = open;');
    const eff = c.slice(c.indexOf('const explainAskSentRef'), c.indexOf('const prefillRef'));
    expect(eff).toContain("if (!rawSubject) { explainAskSentRef.current = ''; return; }");
    expect(eff).toContain('setSubject(null);');
    expect(eff).toContain('void send(ask.userText, explainTurnSendOpts(ask))');
  });

  it('AppShell yalnız CopilotChat mount eder (ayrı Explain çekmecesi yok)', () => {
    const shell = read('components/AppShell.tsx');
    expect(shell).toContain('<CopilotChat');
    expect(shell).not.toMatch(/<AIDrawer\b/);
  });

  it('AIExplainButton her türde sohbet turu (çekmece dalı yok)', () => {
    const b = read('components/ai/AIExplainButton.tsx');
    expect(b).not.toContain('isExplainTurnKind');
    expect(b).not.toContain('sağ çekmecede aç');
  });

  it('?aicode / ?aisrc tek seferlik: uygulama içi her özne yazımı siler', () => {
    const s = read('components/ai/useAiSubject.ts');
    expect(s).toContain('next.delete(AI_CODE_PARAM)');
    expect(s).toContain('else next.delete(AI_SRC_PARAM)');
  });

  it('silinen ✨ istemci yöntemleri api.ts\'e geri gelmedi', () => {
    const api = read('lib/api.ts');
    for (const m of ['copilotExplainTrace', 'copilotExplainSpan', 'copilotExplainProblem', 'copilotExplainException',
      'copilotExplainIncident', 'copilotExplainAnomaly', 'copilotExplainLog', 'copilotExplainServiceHealth',
      'copilotExplainCharts', 'copilotExplainLogSpike', 'copilotRunbook']) {
      expect(api, m).not.toMatch(new RegExp(`^\\s+${m}:`, 'm'));
    }
  });

  it('satır ✨ kapısı (useInAiDrawer) kalktı: sohbet içinde de görünür', () => {
    const hits = FILES.filter(f => !/\.test\.tsx?$/.test(f.rel) && /\b(useInAiDrawer|useRowExplain)\(/.test(f.src)).map(f => f.rel);
    expect(hits).toEqual([]);
  });
});

// v0.10.1413 — özne kipinin geride bıraktığı ölü kod. Yalnız üretim dosyaları
// taranır (pin dosyaları adları anmak zorunda).
describe('özne kipinden kalan ölü kod silindi (v0.10.1413)', () => {
  const PROD = FILES.filter(f => !/\.test\.tsx?$/.test(f.rel));

  it('useChatThread: seed / title / persistContext / adopt yok', () => {
    const h = read('components/ai/useChatThread.ts');
    expect(h).not.toMatch(/\bseed\?:|\btitle\?:|persistContext|\badopt\b/);
    expect(h).not.toMatch(/o\.seed|optsRef\.current\.(title|subject|persistContext)/);
  });

  it('silinen yardımcılar hiçbir üretim dosyasında yok', () => {
    expect(existsSync(join(SRC, 'lib/aiCodePref.ts'))).toBe(false);
    const GONE_NAMES = /\b(capPageContext|traceContextFromPage|CONTEXT_SNAPSHOT_MAX_BYTES|aiSubjectSubtitle|oracleExplainLine|tzBodyFields|emitAiFocus|AI_FOCUS_EVENT|aiIncludeCode|readIncludeCodePref|writeIncludeCodePref)\b/;
    expect(PROD.filter(f => GONE_NAMES.test(f.src)).map(f => f.rel)).toEqual([]);
  });

  it('kayıtlı-bağlam şeridi (saved / ai.ctx.saved) yok', () => {
    expect(read('components/ai/TraceContextStrip.tsx')).not.toMatch(/\acmved\b/);
    expect(read('lib/i18n.ts')).not.toMatch(/'ai\.ctx\.saved'|'ai\.subject\.trace'/);
  });
});
