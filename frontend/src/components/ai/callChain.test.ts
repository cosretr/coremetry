// v0.10.2374 — operatör: CoSRE trace açıklamasında çağrı zinciri tek satırda
// akıyordu. splitChain ≥2 oklu TEK satırı adımlara böler; metin kaybolmaz.
import { describe, it, expect } from 'vitest';
import { splitChain, isErrStep, type Chain } from './callChain';

const join = (c: Chain) => c.label + c.lead + c.steps.map((s, i) => s + (c.seps[i] ?? '')).join('');

describe('splitChain — zincir olan satırlar', () => {
  it.each([
    ['orders-api-prod çağırdı → gRPC status error döndü → kayıt bulunamadı', '', 3],
    ['Kritik hata noktası: orders-api-prod → customer-rel-prod-grpc · BagService/callBagService → status error', 'Kritik hata noktası:', 3],
    ['a -> b -> c -> d', '', 4],
    ['Akış: ACT (Active) → RCV (Received) → DIP (Dispatched)', 'Akış:', 3],
    ['`a → b` sonucu → **orders-api** → [belge](https://example.com/x)', '', 3],
  ])('%s', (input, label, n) => {
    const c = splitChain(input);
    expect(c?.label).toBe(label);
    expect(c?.steps.length).toBe(n);
    expect(c && join(c)).toBe(input);
  });

  it('adımlar okları ve kenar boşluğunu taşımaz', () => {
    expect(splitChain('Akış: a → b  ->  c')?.steps).toEqual(['a', 'b', 'c']);
  });
});

describe('splitChain — zincir OLMAYAN metin', () => {
  it.each([
    '',
    'Operasyon: orders-api-prod · POST /WEB/orders/assessment/decision',
    'Window 19.11.2026 21:02:24.443 → 21:02:24.529',
    'tek oklu cümle: orders-api → checkout-api',
    'a→b→c boşluksuz oklar',
    'ilk satır a → b\nikinci satır → c',
    '`a → b → c` yalnız kod içinde',
    '**a → b → c** kalın ortasından bölünmez',
    '[a → b → c](https://example.com/x)',
    'a → → c',
    'sonu okla biten → satır → ',
  ])('%s', input => {
    expect(splitChain(input)).toBeNull();
  });
});

describe('isErrStep', () => {
  it.each([
    ['gRPC status error döndü', true],
    ['com.example.core.exception.AppException: "Herhangi bir kayıt bulunamadı"', true],
    ['HTTP 503 ile sonuçlandı', true],
    ['"MCA-00029 döndü" mesajı', true],
    ['Bağlantı hatası oluştu', true],
    ['Hata: zaman aşımı', true],
    ['orders-api-prod · SERVER · POST /WEB/orders · HTTP 200', false],
    ['hatasız tamamlandı', false],
    ['customer-rel-prod içinde RelationService.getRelations', false],
  ])('%s → %s', (step, want) => {
    expect(isErrStep(step)).toBe(want);
  });
});
