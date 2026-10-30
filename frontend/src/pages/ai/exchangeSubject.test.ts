// exchangeSubject.test.ts — v0.10.1645. Operator-reported: /ai "Açıklama
// turu" çekmecesinin SORU kutusu `Bu trace'i açıkla · eea6c7b0…` gösteriyordu
// (kısa kimlik kaydediliyordu); çekmece açıklanan öznenin TAM kimliğini
// gösterir — yeni satırda sorudan, eski satırda bağlam kimliğinden.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { explainTurnText, explainCodeTurnText } from '@/components/ai/explainTurn';
import { exchangeSubjectRef } from './exchangeView';

const TR = '0af7651916cd43dd8448eb211c80319c';

describe('v0.10.1645 — exchangeSubjectRef', () => {
  const cases: Array<{ name: string; ex: Parameters<typeof exchangeSubjectRef>[0]; want: ReturnType<typeof exchangeSubjectRef> }> = [
    { name: 'yeni satır: tam trace kimliği sorunun içinde',
      ex: { question: `Bu trace'i açıkla · ${TR}` },
      want: { kind: 'trace', id: TR, full: true, question: `Bu trace'i açıkla · ${TR}` } },
    { name: 'eski satır: kısa trace → traceId',
      ex: { question: "Bu trace'i açıkla · 0af76519…", traceId: TR },
      want: { kind: 'trace', id: TR, full: true, question: `Bu trace'i açıkla · ${TR}` } },
    { name: 'eski kod turu',
      ex: { question: "Bu trace'i kodla birlikte açıkla · 0af76519…", traceId: TR },
      want: { kind: 'trace', id: TR, full: true, question: `Bu trace'i kodla birlikte açıkla · ${TR}` } },
    { name: 'eski satır: bağlam kimliği başka trace → kısa kalır (uydurulmaz)',
      ex: { question: "Bu trace'i açıkla · deadbeef…", traceId: TR },
      want: { kind: 'trace', id: 'deadbeef…', full: false, question: "Bu trace'i açıkla · deadbeef…" } },
    { name: 'eski satır: bağlam kimliği yok',
      ex: { question: "Bu trace'i açıkla · 0af76519…" },
      want: { kind: 'trace', id: '0af76519…', full: false, question: "Bu trace'i açıkla · 0af76519…" } },
    { name: 'exception (ora: öneki)',
      ex: { question: 'ORA-00001 grubunu açıkla · ora:a1b2c3d4…', exceptionId: 'ora:a1b2c3d4e5f60718' },
      want: { kind: 'exception', id: 'ora:a1b2c3d4e5f60718', full: true, question: 'ORA-00001 grubunu açıkla · ora:a1b2c3d4e5f60718' } },
    { name: 'problem',
      ex: { question: 'Bu problemi açıkla · p-123456…', problemId: 'p-1234567890' },
      want: { kind: 'problem', id: 'p-1234567890', full: true, question: 'Bu problemi açıkla · p-1234567890' } },
    { name: 'runbook → problemId',
      ex: { question: 'Bu problem için runbook çıkar · p-123456…', problemId: 'p-1234567890' },
      want: { kind: 'runbook', id: 'p-1234567890', full: true, question: 'Bu problem için runbook çıkar · p-1234567890' } },
    { name: 'span eski satır: span kimliği saklanmamış',
      ex: { question: "Bu span'i açıkla · b7ad6b71…", traceId: TR },
      want: { kind: 'span', id: 'b7ad6b71…', full: false, question: "Bu span'i açıkla · b7ad6b71…" } },
    { name: 'serbest soru: özne yok',
      ex: { question: 'checkout neden yavaş?' }, want: null },
  ];
  for (const c of cases) it(c.name, () => expect(exchangeSubjectRef(c.ex)).toEqual(c.want));

  // Kalıplar istemcinin GÖRÜNEN metnini (explainTurnText) tanımalı — metin
  // değişirse eşleşme sessizce kopmasın.
  it('explainTurnText çıktısını tanır', () => {
    const texts: Array<[string, string]> = [
      [explainTurnText({ kind: 'trace', id: TR }), 'trace'],
      [explainCodeTurnText('trace', TR), 'trace'],
      [explainTurnText({ kind: 'exception', id: 'a1b2c3d4e5f60718' }, 'NullPointerException'), 'exception'],
      [explainTurnText({ kind: 'problem', id: 'p-1234567890' }), 'problem'],
      [explainTurnText({ kind: 'incident', id: 'inc-000123' }), 'incident'],
      [explainTurnText({ kind: 'anomaly', id: 'an-0000123' }), 'anomaly'],
      [explainTurnText({ kind: 'runbook', id: 'p-1234567890' }), 'runbook'],
    ];
    for (const [t, kind] of texts) expect(exchangeSubjectRef({ question: t })?.kind).toBe(kind);
  });

  it('çekmece genişletilmiş soruyu ve kimlik satırını çizer', () => {
    const src = readFileSync(resolve(__dirname, 'ExchangesPanel.tsx'), 'utf8');
    expect(src).toContain("{subj?.question || ex.question || '(boş)'}");
    expect(src).toContain('<Link to={traceHref(subj.id)}');
    expect(src).toContain('<CopyButton value={subj.id}');
  });
});
