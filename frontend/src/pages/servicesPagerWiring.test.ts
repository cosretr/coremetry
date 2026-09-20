import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// servicesPagerWiring.test.ts — v0.10.827 (operatör-bildirimli, 2026-09-20).
//
// SEMPTOM: /services'in ALTTAKİ şeridinde "Next" ve "Last" hiçbir şey
// yapmıyor; tıktan sonra operatör hâlâ 1. sayfada.
//
// KÖK NEDEN (davranışsal ölçüm: lib/useUrlPage.test.tsx): react-router 6.30'un
// `setSearchParams`ı her URL yazımında YENİ KİMLİK alıyor. Sayfa onu saran
// `setPage`i "filtre değişince sayfayı sıfırla" efektinin deps'ine koymuştu
// (v0.9.1111), yani tıkın KENDİ yazımı efekti yeniden tetikliyor ve
// `setPage(0)` sayfayı geri alıyordu.
//
// Bu dosya KABLOLAMAYI çiviliyor (davranışı test eden dosya ayrı): sayfa
// setter'ı kendi içinde yeniden kurmaz, kimliği sabit olan paylaşılan
// hook'tan alır. Sabitlik sözleşmesi orada test ediliyor; burada yalnız
// "sayfa o sözleşmeye BAĞLI mı" sorusu var — çünkü satır içi bir kopya
// bugünkü bug'ı sessizce geri getirirdi.
const src = readFileSync(resolve(__dirname, './Services.tsx'), 'utf8');

describe('/services sayfa şeridi kablolaması (v0.10.827)', () => {
  it('sayfa numarası paylaşılan useUrlPage hook\'undan gelir', () => {
    expect(src).toContain("import { useUrlPage } from '@/lib/useUrlPage'");
    expect(src).toContain('const [page, setPage] = useUrlPage();');
  });

  it('satır içi kimliği-kaypak setPage geri gelmedi', () => {
    // Eski hâl: `useCallback(…, [setSearchParams])` ile sarılmış bir setPage.
    expect(src).not.toMatch(/const setPage = useCallback/);
  });

  it('sayfa-sıfırlama efekti setPage\'i deps\'inde TAŞIYOR (sabitlik yükü taşır)', () => {
    // Bu satır bilerek burada: `setPage` deps'te DURUYOR (exhaustive-deps
    // bastırılmıyor). Zararsız olmasının TEK sebebi hook'un kimlik sözleşmesi
    // — o bozulursa bug aynen geri gelir, bu yüzden ikisi birlikte pinli.
    expect(src).toContain('minSpans, minP99, compare, setPage]);');
  });

  it('şeridin iki dalı da AYNI sayfa state\'ini sürüyor', () => {
    const pagers = src.match(/<Pager mode="offset"[\s\S]*?\/>/g) ?? [];
    expect(pagers.length).toBeGreaterThanOrEqual(2);
    for (const p of pagers) {
      expect(p).toContain('page={page}');
      expect(p).toContain('onPage={setPage}');
    }
  });
});
