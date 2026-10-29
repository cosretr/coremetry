// v0.10.1567 — CoSRE cevabında modelden bağımsız otomatik vurgu
// (autoHighlight.ts). Girdiler operatörün prod ekran görüntüsündeki
// qwen3.8-flash cevabından; yanlış-pozitif tablosu Türkçe düzyazıdan.
import { describe, it, expect } from 'vitest';
import { highlightParts } from './autoHighlight';

const hits = (s: string) => highlightParts(s).filter(p => p.kind).map(p => `${p.kind}:${p.v}`);

describe('highlightParts — prod cevabındaki kritik parçalar', () => {
  it.each([
    ['CreditCardDetailsService.java:151\'de NPE', ['code:CreditCardDetailsService.java:151']],
    ['com.akbank.juzdan.card.service.CreditCardDetails.CreditCardDetailsService (x)', ['code:com.akbank.juzdan.card.service.CreditCardDetails.CreditCardDetailsService']],
    ['DTOCardDetail.getTdrkLimArr() null olduğu için', ['code:DTOCardDetail.getTdrkLimArr()']],
    ['POST /api/card/crdCard/getCardLimitInfo çağrısı HTTP 500 döndü', ['code:POST /api/card/crdCard/getCardLimitInfo', 'err:HTTP 500']],
    ['NullPointerException üretildi; BSARuntimeException: "BSA_020"', ['err:NullPointerException', 'err:BSARuntimeException', 'err:BSA_020']],
    ['error_code=MCA-00029 (Oracle)', ['err:MCA-00029']],
    ['com.akbank.cards.util.util.OceanBackendFeignBase$FeignClientExceptionHandler.decode', ['code:com.akbank.cards.util.util.OceanBackendFeignBase$FeignClientExceptionHandler.decode']],
  ])('%s', (input, want) => {
    expect(hits(input)).toEqual(want);
  });
});

describe('highlightParts — düzyazıda yanlış çip yok', () => {
  it.each([
    'Window 28.10.2026 19:10:10.443 → 19:10:10.529',
    'sürüm 1.4.2 dağıtıldı, oran %3 arttı',
    'Kritik hata noktası: servis sahibi ile incele.',
    'Error oranı yükseldi ama HTTP 200 döndü',
    'Kanıt: 2 span · 1 Oracle satırı',
    'bsa-creditcard-ccmanagement-prod servisinden çağrı',
  ])('%s', input => {
    expect(hits(input)).toEqual([]);
  });

  it('metni kaybetmez: parçalar birleşince girdinin aynısı', () => {
    const s = 'x CreditCardDetails.java:151 y HTTP 503 z MCA-00029.';
    expect(highlightParts(s).map(p => p.v).join('')).toBe(s);
    expect(highlightParts('')).toEqual([{ v: '' }]);
  });
});
