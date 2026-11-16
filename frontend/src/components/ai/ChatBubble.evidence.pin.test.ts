// ChatBubble.evidence.pin.test.ts — v0.10.558: kanıt kartı chart bloklarının
// hemen altında, yalnız tamamlanmış turda; kart bileşeni ayrı dosyada.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';

const bubble = readFileSync(resolve(__dirname, 'ChatBubble.tsx'), 'utf8');
const card = readFileSync(resolve(__dirname, 'EvidenceCard.tsx'), 'utf8');

describe('EvidenceCard yerleşimi', () => {
  it('chart bloklarından sonra, pending değilken', () => {
    const chart = bubble.indexOf('chartBlocks(turn.blocks).map(');
    const ev = bubble.indexOf('evidenceBlocks(turn.blocks).map(');
    expect(chart).toBeGreaterThan(0);
    expect(ev).toBeGreaterThan(chart);
    expect(bubble).toContain('!turn.pending && evidenceBlocks(turn.blocks)'); // v0.10.2213 — sıra kurucusu içinde
  });
  it('kart: verdict, RED tablosu, değişiklikler, desenler, problem linki', () => {
    // v0.10.2292 — metin arayüz dilinde: kart anahtarı okur, TR katalog değeri aynı.
    for (const w of ['ev-verdict', "t('chatEv.tableAria', lang)", "t('chatEv.changes', lang)", "t('chatEv.patterns', lang)", '/problems?problem=']) {
      expect(card).toContain(w);
    }
    expect([t('chatEv.tableAria', 'tr'), t('chatEv.changes', 'tr'), t('chatEv.patterns', 'tr')])
      .toEqual(['RED şimdi / taban', 'Değişiklikler (pencere)', 'Log desenleri']);
    expect(card).not.toContain('useDataTable'); // sabit ≤10 satır: ham tablo meşru
  });
});
