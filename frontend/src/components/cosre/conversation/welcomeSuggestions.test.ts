// welcomeSuggestions.test — CoSRE v2 karşılaması (mockup B, 2026-10-31):
// sekme kuruluşu ve v1 ile aynı gönder/doldur/seç anlamları.
import { describe, it, expect } from 'vitest';
import { tCosre } from '@/components/ai/capabilityHints';
import { WELCOME_TAB_MAX, welcomeTabs } from './welcomeSuggestions';

const starter = { chip: "Bu trace'i açıkla", question: 'Bu trace neden yavaş?' };

describe('welcomeTabs', () => {
  it('takım biliniyor + wiki + bağlam: dört sekme sabit sırada, her biri ≤4', () => {
    const tabs = welcomeTabs({ team: 'payments-core', wiki: true, ctxStarter: starter });
    expect(tabs.map(t => t.id)).toEqual(['status', 'investigate', 'find', 'learn']);
    for (const t of tabs) expect(t.items.length).toBeLessThanOrEqual(WELCOME_TAB_MAX);
    const status = tabs[0].items;
    expect(status.map(s => s.mode)).toEqual(['send', 'send', 'send', 'navigate']);
    expect(status[0]).toMatchObject({ question: 'payments-core takımının servisleri nasıl?' });
    expect(status[3]).toMatchObject({ team: 'payments-core' });
    expect(tabs[1].items.map(s => s.mode)).toEqual(['send', 'pick', 'pick']);
    expect(tabs[1].items[0]).toMatchObject({ question: starter.question });
    expect(tabs[2].items.every(s => s.mode === 'fill')).toBe(true);
    expect(tabs[3].items).toEqual([{ id: 'fill-wiki', mode: 'fill', labelKey: 'cosre.chip.wiki', promptKey: 'cosre.chip.wiki.prompt' }]);
  });

  it('takım yok: takım kapsamlı Durum sekmesi gizli; wiki kapalı → Öğren yok', () => {
    const tabs = welcomeTabs({ team: '  ', wiki: false, ctxStarter: null });
    expect(tabs.map(t => t.id)).toEqual(['investigate', 'find']);
    expect(tabs[0].items.map(s => s.id)).toEqual(['pick-service', 'pick-operation']);
  });

  it('açıklama niyetli bağlam çipi explain kipinde', () => {
    const explainAsk = { kind: 'trace', id: 'a'.repeat(32) } as never;
    const tabs = welcomeTabs({ team: '', wiki: false, ctxStarter: { ...starter, explainAsk } });
    expect(tabs[0].items[0]).toMatchObject({ mode: 'explain', label: starter.chip });
  });

  it('yol şablonu v1 "…/yol" çipiyle aynı metni, imleç başta doldurur', () => {
    const tabs = welcomeTabs({ team: '', wiki: false, ctxStarter: null });
    const path = tabs.find(t => t.id === 'find')!.items[0];
    expect(path).toMatchObject({ mode: 'fill', promptKey: 'cosre.welcome.s.path.prompt' });
    expect(tCosre('cosre.welcome.s.path.prompt')).toBe("| hatalı trace'lerini getir");
  });
});
