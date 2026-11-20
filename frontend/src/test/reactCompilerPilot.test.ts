// React Compiler 1.0 pilotu — annotation modu (vite.config.ts + vitest.config.ts).
//
// İki şeyi sabitler:
//  1. 'use memo' taşıyan bileşenler GERÇEKTEN derleniyor (sessiz bail-out
//     olursa — ör. yeni bir eslint-disable react-hooks satırı — test kırmızı).
//  2. Vitest de derlenmiş çıktıyı çalıştırıyor: Logs/Traces/Service render
//     testleri derleyicili davranışı ölçer, derleyicisiz değil.
//
// Bilinen bail-out'lar (bilerek opt-in DEĞİL): TracesPageInner ve LogsInner
// toplam 13 kasıtlı `eslint-disable react-hooks/exhaustive-deps` taşıyor;
// derleyici kural bastırılan bileşeni atlar. Önce useEffectEvent'e taşınmalı.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { transformSync } from '@babel/core';
import { LogsFilterRow } from '@/pages/logs/LogsFilterRow';

const root = fileURLToPath(new URL('../', import.meta.url));

const EXPECTED: Record<string, string[]> = {
  'pages/Traces.tsx': ['AggregateTable', 'HeaderStat', 'TracesEmptyDetail'],
  'pages/Logs.tsx': ['LogsPage'],
  'pages/Service.tsx': ['ServiceDetailInner', 'ServiceDetailPage', 'ServiceSLOChip', 'TabStrip'],
  'pages/logs/LogsQueryBar.tsx': ['LogsQueryBar'],
  'pages/logs/LogsFilterRow.tsx': ['LogsFilterRow'],
};

function compiled(file: string): { ok: string[]; failed: number } {
  const ok: string[] = [];
  let failed = 0;
  transformSync(readFileSync(root + file, 'utf8'), {
    filename: file, babelrc: false, configFile: false,
    parserOpts: { plugins: ['typescript', 'jsx'] },
    plugins: [['babel-plugin-react-compiler', {
      compilationMode: 'annotation',
      logger: { logEvent: (_f: string, e: { kind: string; fnName?: string | null }) => {
        if (e.kind === 'CompileSuccess' && e.fnName) ok.push(e.fnName);
        if (e.kind === 'CompileError' || e.kind === 'PipelineError') failed++;
      } },
    }]],
  });
  return { ok: ok.sort(), failed };
}

describe('React Compiler pilotu', () => {
  for (const [file, names] of Object.entries(EXPECTED)) {
    it(`${file}: 'use memo' bileşenleri derleniyor, bail-out yok`, () => {
      expect(compiled(file)).toEqual({ ok: names, failed: 0 });
    });
  }

  it('vitest derlenmiş modülü çalıştırıyor (memo cache)', () => {
    expect(String(LogsFilterRow)).toMatch(/const \$ = \(0, *[\w$]+\.c\)\(\d+\)/);
  });
});
