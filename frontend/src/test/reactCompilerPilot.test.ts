// React Compiler 1.0 pilotu — annotation modu (vite.config.ts + vitest.config.ts).
//
// İki şeyi sabitler:
//  1. 'use memo' taşıyan bileşenler GERÇEKTEN derleniyor (sessiz bail-out
//     olursa — ör. yeni bir eslint-disable react-hooks satırı — test kırmızı).
//  2. Vitest de derlenmiş çıktıyı çalıştırıyor: Logs/Traces/Service render
//     testleri derleyicili davranışı ölçer, derleyicisiz değil.
//
// TracesPageInner / TracesPage / LogsInner (2026-11-21): 14 kasıtlı
// `eslint-disable react-hooks/exhaustive-deps` useEffectEvent'e taşındı,
// render'da ref okumaları state / türetilmiş değer oldu; artık bail-out yok.
// Bu dosyalarda yeni bir react-hooks bastırması testi kırmızıya çevirir.
import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { transformSync } from '@babel/core';
import { LogsFilterRow } from '@/pages/logs/LogsFilterRow';

const root = fileURLToPath(new URL('../', import.meta.url));

const EXPECTED: Record<string, string[]> = {
  'pages/Traces.tsx': ['AggregateTable', 'HeaderStat', 'TracesEmptyDetail', 'TracesPage', 'TracesPageInner'],
  'pages/Logs.tsx': ['LogsInner', 'LogsPage'],
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

// Aşağı-yönlü cırcır: üretim kodundaki satır içi react-hooks bastırmaları
// (derleyici her birinde bileşeni atlar). 2026-11-21: 109 → 95 (Traces/Logs).
// Sayı yalnız AZALIR; düşürünce tabanı da düşür.
const REACT_HOOKS_SUPPRESSIONS_MAX = 95;
const SUPPRESSION = new RegExp('eslint-' + 'disable(?:-next-line|-line)? react-hooks/');
function countSuppressions(dir: string): number {
  let n = 0;
  for (const e of readdirSync(dir)) {
    const p = dir + '/' + e;
    if (statSync(p).isDirectory()) n += countSuppressions(p);
    else if (/\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e)) {
      n += readFileSync(p, 'utf8').split('\n').filter(l => SUPPRESSION.test(l)).length;
    }
  }
  return n;
}

describe('React Compiler pilotu', () => {
  it(`react-hooks bastırma cırcırı (≤ ${REACT_HOOKS_SUPPRESSIONS_MAX})`, () => {
    expect(countSuppressions(root.replace(/\/$/, ''))).toBeLessThanOrEqual(REACT_HOOKS_SUPPRESSIONS_MAX);
  });

  for (const [file, names] of Object.entries(EXPECTED)) {
    it(`${file}: 'use memo' bileşenleri derleniyor, bail-out yok`, () => {
      expect(compiled(file)).toEqual({ ok: names, failed: 0 });
    }, 30_000); // Logs.tsx tek başına ~1.5 sn derleniyor; tam koşu yükünde 5 sn varsayılanı aşıyordu
  }

  it('vitest derlenmiş modülü çalıştırıyor (memo cache)', () => {
    expect(String(LogsFilterRow)).toMatch(/const \$ = \(0, *[\w$]+\.c\)\(\d+\)/);
  });
});
