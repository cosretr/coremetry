// logsTeam — v0.10.1477 (operatör 2026-10-23). /logs takım süzgecinin (team=)
// çip etiketi yardımcısı. v0.10.1855 (Log Search Faz 2 dilim 2.4, §9-4): otomatik
// "Takımım" varsayılanı ve kişisel vazgeçme bayrağı KALDIRILDI — yerine
// varsayılan kapsam `scope=mine` (lib/logsScope.ts); team= elle süzgeç olarak kalır.

/** Aynı takım mı (çip etiketi "Takımım" mı "Takım" mı) — kırpılmış, harf duyarsız. */
export function isSameTeam(a: string, b: string): boolean {
  const x = a.trim().toLocaleLowerCase('tr');
  return x !== '' && x === b.trim().toLocaleLowerCase('tr');
}
