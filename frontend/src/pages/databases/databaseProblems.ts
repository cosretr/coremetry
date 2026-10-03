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
//
// v0.10.1027 — BİÇİM FARKINDALIĞI (liste işaretiyle birlikte, Databases dilim 4).
// `db:<system>@<X>` dizgisi X'in instance mı veritabanı adı mı olduğunu
// SÖYLEMEZ; iki uzay aynı dizgide çakışır (span satırının instance'ı çoğu kez
// "oracle" / "postgres" gibi genel bir ad, aynı adlı bir veritabanı da olabilir).
// Biçim KURALDAN türer: kapasite kuralı (rule_id `db-capacity:` önekli) instance
// biçimi, gerisi dbName biçimi. Kural: instance biçimi YALNIZ kimliğin
// instance'ıyla, dbName biçimi YALNIZ db.name'iyle eşleşir — liste
// (rowProblemSummary) ve detay kartı (keepProblemForms) AYNI aday kümesini
// (dbProblemSubjectForms) ve AYNI biçim yüklemini (dbProblemForm) kullanır.
//
// Aday kümesini iki istisna DARALTIR (iki yüzeyde de):
//   • 'default' db.name — db_summary_5m / db_statement_summary_5m'nin "span
//     db.name taşımıyordu" nöbetçisi (coalesce(…, 'default')), gerçek bir ad
//     değil. `db:<system>@default` öznesi hiçbir satıra / detaya bağlanmaz;
//     yalnız Problems sekmesinin Veritabanları şeridinde görünür.
//   • receiver kimliği — metric_points'ten keşfedilir ve db.name TAŞIMAZ;
//     yalnız instance biçimi (kapasite) sorulur.

import type { DBProblemCount, DBProblemForms, DBProblemSeverity, Problem } from '@/lib/types';
import { parseDbHealthRuleId } from '@/lib/problemSubject';

/** chstore.DBSubjectID'nin ikizi: system küçük harf + kırpılmış; biri boşsa ''. */
export function dbSubjectId(system: string, instance: string): string {
  const sys = system.trim().toLowerCase();
  const inst = instance.trim();
  return sys && inst ? `db:${sys}@${inst}` : '';
}

/** MV'lerin "db.name yok" nöbetçisi — bkz. dosya başı. */
export const DB_NAME_SENTINEL = 'default';

/** Go chstore.RuleDBCapacityPrefix ikizi (databaseProblems.test.ts Go kaynağına karşı pinler). */
export const DB_CAPACITY_RULE_PREFIX = 'db-capacity:';

export type DBProblemForm = 'instance' | 'dbName';

/** Go chstore.DBProblemSubjectForm ikizi: kapasite kuralı → instance, gerisi → dbName.
 *  v0.10.1073 — sağlık kuralı (db-health:) gerçek db.name taşımıyorsa ('default'
 *  nöbetçisi) instance biçimi; Go DBHealthSubject ile aynı seçim. */
export function dbProblemForm(ruleId: string | undefined | null): DBProblemForm {
  if ((ruleId ?? '').startsWith(DB_CAPACITY_RULE_PREFIX)) return 'instance';
  const h = parseDbHealthRuleId(ruleId);
  if (h && (h.dbName.trim() === '' || h.dbName.trim() === DB_NAME_SENTINEL)) return 'instance';
  return 'dbName';
}

/** Bir özne kimliği ve o kimlikte SORULAN biçimler. instance == dbName iken tek
 *  kimlik iki biçimi birden taşır (bir problemin tek biçimi var — çift sayım yok). */
export interface DBSubjectQuery { id: string; forms: DBProblemForm[] }

export interface DBSubjectRef { system: string; instance?: string; dbName?: string; source?: string }

/** Kimliğe düşen (özne, biçim) adayları — liste ve detayın TEK aday kümesi. */
export function dbProblemSubjectForms(ref: DBSubjectRef): DBSubjectQuery[] {
  const out: DBSubjectQuery[] = [];
  const add = (id: string, form: DBProblemForm) => {
    if (!id) return;
    const hit = out.find(q => q.id === id);
    if (!hit) out.push({ id, forms: [form] });
    else if (!hit.forms.includes(form)) hit.forms.push(form);
  };
  add(dbSubjectId(ref.system, ref.instance ?? ''), 'instance');
  const name = (ref.dbName ?? '').trim();
  if (ref.source !== 'receiver' && name && name !== DB_NAME_SENTINEL) {
    add(dbSubjectId(ref.system, name), 'dbName');
  }
  return out;
}

/** Bu kimliğe düşen özne kimlikleri (tekrarsız, boşlar ve nöbetçi atılmış). */
export function dbProblemSubjects(ref: DBSubjectRef): string[] {
  return dbProblemSubjectForms(ref).map(q => q.id);
}

/** Bir özne kimliği için çekilen problemlerden yalnız SORULAN biçimdekiler
 *  (detay kartı: instance kimliğinden yalnız kapasite, dbName kimliğinden
 *  yalnız kapasite dışı; iki biçim birden sorulduysa hepsi). */
export function keepProblemForms<P extends Pick<Problem, 'ruleId'>>(
  list: P[] | null | undefined, forms: DBProblemForm[] | undefined,
): P[] {
  if (!list || !forms || forms.length === 0) return [];
  return list.filter(p => forms.includes(dbProblemForm(p.ruleId)));
}

// ── Önem (öncelik DEĞİL) ──────────────────────────────────────────────────
// v0.10.1027 — /inbox exception dışı her satırı P3'e çiviliyor
// (forceNonExceptionP3, operatör kararı v0.9.487): veritabanı yüzeylerinde
// kırmızı bir "P1", Problems sekmesinde aynı satırın P3'üyle çelişirdi. Önem
// saklanan kolon; renk yalnız sapan değerde: critical err, warning warn, gerisi nötr.

const SEVERITY_RANK: Record<DBProblemSeverity, number> = { critical: 3, warning: 2, info: 1 };

/** Saklanan önemin kanonik hâli: critical / warning; gerisi info (Go dbSeverity ikizi). */
export function dbSeverity(s: string | undefined | null): DBProblemSeverity {
  return s === 'critical' || s === 'warning' ? s : 'info';
}

export function dbSeverityRank(s: string | undefined | null): number {
  return SEVERITY_RANK[dbSeverity(s)];
}

/** Önem rozetinin sınıfı (literal sınıf adları — undefinedCssRefs görür). */
export function dbSeverityBadgeClass(s: string | undefined | null): 'badge b-err' | 'badge b-warn' | 'badge b-gray' {
  const sev = dbSeverity(s);
  return sev === 'critical' ? 'badge b-err' : sev === 'warning' ? 'badge b-warn' : 'badge b-gray';
}

/** Satırın açık problem özeti (liste işareti): instance biçimi yalnız satırın
 *  instance'ıyla, dbName biçimi yalnız db.name'iyle (ve yalnız span satırında)
 *  eşleşir; toplam = ikisinin toplamı, önem en ağırı. Hiçbiri yoksa null.
 *  subjects yok (yükleniyor / hata) → null: işaret yok, yer tutucu da yok. */
export function rowProblemSummary(
  row: DBSubjectRef,
  subjects: Record<string, DBProblemForms> | null | undefined,
): DBProblemCount | null {
  if (!subjects) return null;
  let open = 0;
  let top: DBProblemSeverity | null = null;
  for (const q of dbProblemSubjectForms(row)) {
    for (const form of q.forms) {
      const c = subjects[q.id]?.[form];
      if (!c || !(c.open > 0)) continue;
      open += c.open;
      const sev = dbSeverity(c.topSeverity);
      if (!top || SEVERITY_RANK[sev] > SEVERITY_RANK[top]) top = sev;
    }
  }
  return open > 0 && top ? { open, topSeverity: top } : null;
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
