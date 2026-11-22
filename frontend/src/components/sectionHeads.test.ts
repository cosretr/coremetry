// v0.10.1538 — küçük yerel model (gemma4 / qwen3:8b) başlıkları düz yazınca
// çizim modelden bağımsız kalınlaştırır.
import { describe, it, expect } from 'vitest';
import { promoteHeadLine, promoteSectionHeads } from './sectionHeads';

describe('promoteHeadLine', () => {
  it.each([
    ['Hata zinciri:', '**Hata zinciri**'],
    ['Metod:', '**Metod**'],
    ['İşlem Akışı ve Veri Özeti', '**İşlem Akışı ve Veri Özeti**'],
    ['Hata Kaynağı', '**Hata Kaynağı**'], // v0.10.2456
    ['3. Önerilen Sonraki Adım', '**Önerilen Sonraki Adım**'],
    ['Olası Neden: ödeme servisi 503 döndü', '**Olası Neden:** ödeme servisi 503 döndü'],
    ['1. Kök Neden ve Sonraki Adım', '**Kök Neden ve Sonraki Adım**'],
    ['### Kök Neden', '### Kök Neden'],
    ['**Stacktrace Detayı:**', '**Stacktrace Detayı**'],
    ['Etkilenen servisler:', '**Etkilenen servisler:**'],
    ['Kök Neden: ödeme servisi 500 döndü', '**Kök Neden:** ödeme servisi 500 döndü'],
    ['2. Sonraki Adım: bağımlılığın loglarına bak', '**Sonraki Adım:** bağımlılığın loglarına bak'],
  ])('%s → başlık', (inp, want) => expect(promoteHeadLine(inp)).toBe(want));

  it.each([
    'Servis: checkout',
    '- Hata zinciri:',
    '> Not:',
    '| a | b |',
    '**Özet:** ödeme yavaş',
    'Bu çağrı yavaşladı ve sonra şunu yaptı:',
    'checkout servisi şunları çağırdı:',
    'Kök nedeni bilmiyoruz.',
    '`Metod`:',
  ])('%s → dokunulmaz', inp => expect(promoteHeadLine(inp)).toBe(inp));
});

describe('promoteSectionHeads', () => {
  it('çit içi korunur', () => {
    const t = 'Hata zinciri:\n```\nMetod:\n```\n';
    expect(promoteSectionHeads(t)).toBe('**Hata zinciri**\n```\nMetod:\n```\n');
  });
  it('akışta bitmemiş son satıra dokunmaz', () => {
    expect(promoteSectionHeads('a\nHata zinciri:', true)).toBe('a\nHata zinciri:');
    expect(promoteSectionHeads('a\nHata zinciri:\n', true)).toBe('a\n**Hata zinciri**\n');
  });
  it('değişiklik yoksa aynı dize', () => {
    const t = 'düz metin\n- madde';
    expect(promoteSectionHeads(t)).toBe(t);
  });
});
