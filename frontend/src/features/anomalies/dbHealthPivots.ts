// dbHealthPivots — v0.10.1073: veritabanı sağlık (db-health) Problem'inin
// pivotları. Operatör: "Dün akşam CRM database'inde sorun oldu ama
// problemlerde P1 gelmedi" — kural artık veritabanı öznesinde açılıyor;
// detay sayfası onu Databases detayına ve o veritabanının trace'lerine bağlar.
//
// Neden kural id'sinden: özne dizgisi (`db:<sys>@<X>`) X'in instance mı db.name
// mi olduğunu SÖYLEMEZ (databaseProblems.ts başlığı); Databases detayının
// kimliği ise ÜÇLÜ (system, instance, dbName — databaseParam.ts). db-health
// kural id'si üçlünün tamamını taşır (Go chstore.DBHealthRuleID), yani link
// boş bir sayfaya değil, kuralın ölçtüğü satıra gider. Diğer db problemleri
// (kapasite: receiver instance'ı, yavaş ifade: yalnız db.name) üçlüyü
// taşımadığı için burada YOK — bugünkü "pivot yok" cümlesi onlarda sürer.
//
// SAF: React yok, yalnız href.

import { parseDbHealthRuleId } from '@/lib/problemSubject';
import { databaseDetailHref } from '@/pages/databases/databaseParam';
import { dbTracesHref } from '@/lib/pivotHref';
import { windowRangeParam } from '@/lib/urlState';

export interface DbHealthPivots {
  /** Databases detay sayfası (problem penceresiyle). */
  databaseHref: string;
  /** O veritabanının trace'leri (problem penceresi, hata süzgeci yok). */
  tracesHref: string;
  /** Yalnız hatalı trace'ler. */
  errorTracesHref: string;
}

export function dbHealthPivots(
  ruleId: string | null | undefined,
  window: { fromNs: number; toNs: number },
): DbHealthPivots | null {
  const ref = parseDbHealthRuleId(ruleId);
  if (!ref) return null;
  // '' = pencere reddedildi → param yazılmaz (pivotHref sözleşmesi).
  const range = windowRangeParam(window) || undefined;
  // dbName AYNEN geçer ('default' dahil): Databases listesi de MV'nin nöbetçi
  // değerini satırın kimliği olarak taşır ve aynı linki kurar.
  const databaseHref = databaseDetailHref(
    { system: ref.system, instance: ref.instance, dbName: ref.dbName, source: 'spans' },
    { range },
  );
  const traces = (hasError: boolean) => dbTracesHref({
    window, system: ref.system, instance: ref.instance, dbName: ref.dbName, hasError,
  });
  return { databaseHref, tracesHref: traces(false), errorTracesHref: traces(true) };
}
