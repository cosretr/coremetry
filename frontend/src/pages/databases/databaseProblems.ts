// databaseProblems — v0.10.1019 — /database sayfasındaki "bu veritabanının
// problemleri" bölümünün saf çekirdeği (operatör: Databases için Dynatrace'in
// Databases bölümü baz; "o database ile ilgili veriler gelebilir").
//
// DB özneli problemler (kind=db) zaten açılıyor ve Problems sekmesinin
// "Veritabanları" şeridinde listeleniyordu; veritabanının KENDİ sayfası
// onları göstermiyordu. Özne kimliği üreticiye göre İKİ biçimde:
//
//   db:<system>@<instance>   kapasite alarmı (evaluator/db_capacity.go)
//   db:<system>@<dbName>     yavaş ifade + hedefli kural (db_slow_statement.go,
//                            alert_target.go)
//
// Sayfanın kimliği üçlü (system, instance, dbName) olduğu için ikisi de sorulur.

import type { Problem } from '@/lib/types';

/** chstore.DBSubjectID'nin ikizi: system küçük harf + kırpılmış; biri boşsa ''. */
export function dbSubjectId(system: string, instance: string): string {
  const sys = system.trim().toLowerCase();
  const inst = instance.trim();
  return sys && inst ? `db:${sys}@${inst}` : '';
}

/** Bu sayfa kimliğine düşen özne kimlikleri (tekrarsız, boşlar atılmış). */
export function dbProblemSubjects(ref: { system: string; instance: string; dbName?: string }): string[] {
  const out: string[] = [];
  for (const id of [dbSubjectId(ref.system, ref.instance), dbSubjectId(ref.system, ref.dbName ?? '')]) {
    if (id && !out.includes(id)) out.push(id);
  }
  return out;
}

/** Problem pencereyle kesişiyor mu: pencere bitmeden başlamış VE (hâlâ açık YA DA
 *  pencere başladıktan sonra çözülmüş). Zamanlar unix ns. */
export function problemOverlapsWindow(p: Pick<Problem, 'status' | 'startedAt' | 'resolvedAt'>, fromNs: number, toNs: number): boolean {
  if (p.startedAt > toNs) return false;
  if (p.status !== 'resolved') return true;
  return (p.resolvedAt ?? p.startedAt) >= fromNs;
}

/** Listeleri birleştirir (kimlikle tekrarsız), pencereye süzer, sıralar:
 *  açıklar önce, sonra en yeni başlayan. */
export function dbProblemsForWindow(lists: (Problem[] | null | undefined)[], fromNs: number, toNs: number): Problem[] {
  const seen = new Set<string>();
  const out: Problem[] = [];
  for (const list of lists) {
    for (const p of list ?? []) {
      if (seen.has(p.id) || !problemOverlapsWindow(p, fromNs, toNs)) continue;
      seen.add(p.id);
      out.push(p);
    }
  }
  return out.sort((a, b) => {
    const ao = a.status === 'resolved' ? 1 : 0;
    const bo = b.status === 'resolved' ? 1 : 0;
    return ao !== bo ? ao - bo : b.startedAt - a.startedAt;
  });
}

/** Süre metni: açık problemde şimdiye, çözülmüşte çözülme anına dek. */
export function problemDurationText(p: Pick<Problem, 'status' | 'startedAt' | 'resolvedAt'>, nowNs: number): string {
  const end = p.status === 'resolved' ? (p.resolvedAt ?? p.startedAt) : nowNs;
  const sec = Math.max(0, Math.round((end - p.startedAt) / 1e9));
  if (sec < 60) return `${sec} sn`;
  if (sec < 3600) return `${Math.round(sec / 60)} dk`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} sa`;
  return `${(sec / 86400).toFixed(1)} gün`;
}
