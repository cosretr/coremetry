// problemSubject.ts — v0.9.1339 (entity-model Faz 4b, frontend yarısı).
//
// ORİJİNAL SEMPTOM: `Problem.service` sorgusuz bir servis adı sayılıyordu.
// Backend'de v0.9.1338 bunu düzeltti (Problem.kind); burası o türü GÖRÜNÜR
// kılıyor. Öncesinde db_capacity.go'nun yazdığı `corebank-scan.prod` on
// küsur yüzeyde `<Link to={serviceHref(p.service)}>` olarak basılıyordu:
// link geçerli görünüyor, tıklanıyor, servis sayfası BOŞ açılıyor.
// Hata değil CEVAPSIZLIK — ve çalışmayan bir bağlantı, bağlantı
// olmamasından KÖTÜDÜR (aynı gerekçe: chstore selfHealthRunbooks yorumu).
//
// Bu dosya SAF: React yok, router yok, fetch yok. Tek işi bir özne
// dizgisini sınıflandırmak ve nasıl basılacağını söylemek.

/** Backend'in `problems.kind` evreni (chstore ProblemKind* sabitleri). */
export type SubjectKind = 'service' | 'db' | 'external';
export type ExternalSubject = { source: string; values: string[] };

/** v0.10.1017 — /inbox özne şeridi: URL → şerit. Kapalı sözlük, bilinmeyen =
 *  'service' (sunucunun normalizeInboxSubject'iyle birebir). */
export function parseSubjectLane(raw: string | null | undefined): SubjectKind {
  return raw === 'db' || raw === 'external' ? raw : 'service';
}

/** `db:<system>@<instance>` çözümü. chstore.ParseDBSubjectID'nin ikizi. */
export type DbSubject = { system: string; instance: string };

// DB_PREFIX — chstore.TopologyNodeIDPrefixes'in `db:` girdisiyle AYNI
// dizgi. Frontend'de sözlüğü ithal edemiyoruz (ayrı dil), o yüzden tek
// sabit + problemSubject.test.ts'te biçim pini: iki taraf ayrışırsa test
// kırılır, ürün sessizce yanlış sınıflandırmaz.
const DB_PREFIX = 'db:';
// v0.10.228 (Influx D3) — dış metrik kaynağı öznesi: `ext:<kaynak>/<v1>/<v2>`.
const EXT_PREFIX = 'ext:';

export function parseExternalSubject(subject: string): ExternalSubject | null {
  if (!subject.startsWith(EXT_PREFIX)) return null;
  const parts = subject.slice(EXT_PREFIX.length).split('/');
  if (parts[0] === '') return null;
  return { source: parts[0], values: parts.slice(1) };
}

/**
 * parseDbSubject — `db:<system>@<instance>` çözer, değilse null.
 *
 * Ayırıcı İLK '@': system asla '@' taşımaz, instance (bir host adı)
 * teorik olarak taşıyabilir. "'@' varsa böl" yazmak `checkout@v2` gibi
 * bir SERVİS adını db öznesi sanardı — önek ZORUNLU.
 */
export function parseDbSubject(subject: string): DbSubject | null {
  if (!subject.startsWith(DB_PREFIX)) return null;
  const rest = subject.slice(DB_PREFIX.length);
  const at = rest.indexOf('@');
  if (at <= 0 || at === rest.length - 1) return null;
  return { system: rest.slice(0, at), instance: rest.slice(at + 1) };
}

// ── Veritabanı sağlık kuralı (v0.10.1073, db-health) ───────────────────────
// Kural id veritabanı ÜÇLÜSÜNÜ taşır: `db-health:<system>@<instance>/<db>`
// (Go chstore.DBHealthRuleID / ParseDBHealthRuleID ikizi; problemSubject.test.ts
// Go kaynağına karşı pinler). Problem detayı Databases detay sayfasına ve
// trace listesine bu üçlüyle bağlanır — özne dizgisi (`db:<sys>@<X>`) X'in
// instance mı db.name mi olduğunu söylemediği için yetmez.

/** Go chstore.RuleDBHealthPrefix ikizi. */
export const DB_HEALTH_RULE_PREFIX = 'db-health:';

export type DbHealthRef = { system: string; instance: string; dbName: string };

/** `db-health:<system>@<instance>/<db>` çözer, değilse null. Ayırıcılar İLK
 *  '@' ve SON '/' (Go ile aynı; '/' içeren db.name instance'a kayar). */
export function parseDbHealthRuleId(ruleId: string | null | undefined): DbHealthRef | null {
  const id = ruleId ?? '';
  if (!id.startsWith(DB_HEALTH_RULE_PREFIX)) return null;
  const rest = id.slice(DB_HEALTH_RULE_PREFIX.length);
  const at = rest.indexOf('@');
  if (at <= 0) return null;
  const tail = rest.slice(at + 1);
  const slash = tail.lastIndexOf('/');
  if (slash <= 0 || slash === tail.length - 1) return null;
  return { system: rest.slice(0, at), instance: tail.slice(0, slash), dbName: tail.slice(slash + 1) };
}

/**
 * subjectKind — satırın özne türü.
 *
 * `kind` alanı BOŞ olabilir ve boş = service: (a) kolonu ekleyen boot'un
 * probe'u false okur (küme kipinde DDL ertelenir), (b) v0.9.1338 öncesi
 * yazılmış satırlar. Backend zaten normalize ediyor; burada TEKRAR
 * normalize ediyoruz çünkü bu fonksiyon eski bir tarayıcı sekmesinin
 * önbelleğindeki JSON'u da görebilir.
 *
 * Tanınmayan bir kind DEĞERİ 'service' sayılmaz — biçimden karar verilir.
 * Böylece backend yarın 'queue' eklerse bu dosya onu servis diye basmaz.
 *
 * ⚠️ AD ÇAKIŞMASI — parametreler NEDEN nesne değil, ayrı ayrı:
 * `InboxItem.kind` ZATEN VAR ve TAMAMEN BAŞKA bir şey söylüyor
 * (`problem | exception | anomaly` — satırın KAYNAĞI, öznenin türü
 * değil). Bu fonksiyon bir nesne alsaydı `<SubjectLink item={inboxItem}/>`
 * sessizce yanlış alanı okurdu ve TypeScript UYARMAZDI: iki alan da
 * `string`. identity.go'daki clusterExpr çakışmasının (v0.9.1318) birebir
 * ikizi. Ayrı parametreler çağıranı hangi alanı verdiğini YAZMAYA
 * zorluyor.
 */
export function subjectKind(service: string, kind?: string): SubjectKind {
  // v0.10.596 — BİLİNEN kind biçimi YENER. Eskiden `ext:` şekli kind'ı
  // ezerdi; oysa `ext:` öneki iki anlam taşıyor: dış metrik öznesi
  // (`ext:<kaynak>/…`, kind=external) ve topoloji dış peer düğümü
  // (`ext:api.example.com`, bir Problem'e özne olursa kind=service). Backend
  // kind'ı yazım anında normalize ediyor (v0.9.1338); onu yok sayıp şekle
  // bakmak, bir servis Problem'ini "dış metrik serisi" diye basar ve kanıt
  // panelini boş açardı. Şekil yalnız kind BOŞ/BİLİNMEYEN olduğunda karar
  // verir (eski sekme JSON'u, InboxItem.kind karışıklığı).
  if (kind === 'external' || kind === 'db' || kind === 'service') return kind;
  if (parseExternalSubject(service)) return 'external';
  if (parseDbSubject(service)) return 'db';
  return 'service';
}

/**
 * subjectLabel — operatöre gösterilecek metin.
 *
 * db öznesinde ham `db:oracle@corebank-scan.prod` yerine okunabilir
 * `oracle · corebank-scan.prod`. Ham biçim bir MAKİNE kimliği; onu
 * ekrana basmak v0.9.1029'un topoloji tarafında yaptığı hataydı
 * (düğüm ham `queue:kafka:api.usage` adıyla görünüyordu).
 */
export function subjectLabel(service: string): string {
  const ext = parseExternalSubject(service);
  if (ext) return [ext.source, ...ext.values].join(' · ');
  const db = parseDbSubject(service);
  if (db) return `${db.system} · ${db.instance}`;
  return service;
}

/**
 * subjectHref — öznenin detay linki, ya da null ("link YOK").
 *
 * null İKİ durumda döner ve ikisi de bilinçli:
 *   • kind='db' — ÖLÇÜLDÜ (2026-08-24): kapasite problemlerinin taşıdığı
 *     receiver instance'ı (`corebank-scan.prod`) ile span türevli
 *     `db_summary_5m.instance` (`oracle`) AYRI kimlik uzayları, kesişim
 *     0 satır. Yani /databases linki de boş bir satıra giderdi. Köprü
 *     kurulana dek (ayrı dilim) DOĞRU cevap "link yok".
 *   • özne boş — bağlanacak bir şey yok (global log-query kuralları).
 *
 * Servis öznesinde çağıran serviceHref'i kendi opsiyonlarıyla kurar;
 * bu fonksiyon onu ÇAĞIRMAZ, yalnız "kurulabilir mi" sorusunu
 * cevaplar. Böylece her çağrı yeri kendi range/tab/env'ini taşımaya
 * devam eder ve v0.9.860'ın pencere-taşıma sözleşmesi bozulmaz.
 */
export function subjectIsLinkable(service: string, kind?: string): boolean {
  return service !== '' && subjectKind(service, kind) === 'service';
}

/**
 * subjectTitle — linksiz basılan öznenin `title` metni. Operatör neden
 * tıklayamadığını görebilmeli; sessiz bir düz-metin "bu satır bozuk"
 * gibi okunur.
 */
export function subjectTitle(service: string): string | undefined {
  const ext = parseExternalSubject(service);
  if (ext) {
    return `Dış metrik kaynağı (${ext.source}) serisi — bir servis değil, ` +
      `bu yüzden servis sayfası linki yok`;
  }
  const db = parseDbSubject(service);
  if (db) {
    return `${db.system} veritabanı örneği (${db.instance}) — bir servis değil, ` +
      `bu yüzden servis sayfası linki yok`;
  }
  return undefined;
}

/**
 * derivedTeamTitle — TÜRETİLMİŞ sahipliğin çekincesi (v0.9.1345).
 *
 * Bir db konusunun katalogda satırı yok, o yüzden takımı "bu veritabanını
 * EN ÇOK ÇAĞIRAN servisin takımı" kuralıyla türetiliyor (operatör kararı
 * 2026-08-24, backend chstore/db_ownership.go).
 *
 * Metin İKİ şeyi birden söylemek zorunda ve ikisi de gerekli:
 *
 *  1. KANIT — hangi servis üzerinden türetildi. Operatör cevabı tartabilsin.
 *  2. SINIR — çözüm veritabanı SİSTEMİ düzeyinde yapılıyor, tekil örnek
 *     düzeyinde DEĞİL (iki kimlik uzayı kesişmiyor; backend'deki ölçülmüş
 *     uyarı). Yani aynı sistemden iki küme farklı takımlara aitse ikisi de
 *     aynı takıma yazılır.
 *
 * (2) olmadan bu metin kesin bir atıf gibi okunur. Bu repoda kendinden
 * emin görünen yanlış cevap, çekinceli cevaptan kötüdür.
 */
export function derivedTeamTitle(via: string, service?: string): string {
  const db = service ? parseDbSubject(service) : null;
  const what = db ? `${db.system} veritabanını` : 'bu veritabanını';
  return `Türetilmiş sahiplik (kesin atıf değil) — ${what} en çok çağıran ` +
    `servis "${via}" olduğu için onun katalog takımı gösteriliyor. ` +
    `Çözüm veritabanı SİSTEMİ düzeyinde yapılır, tekil örnek düzeyinde değil: ` +
    `aynı sistemden birden çok küme varsa hepsi bu takıma yazılır.`;
}


// v0.10.598 — dış hattın ÖZET Problem'leri: tavan aşımı (587), kaynak düştü
// (588), küme (597). Bunların root_cause_hypotheses satırı HİÇ yazılmaz —
// OnEvidence yalnız seri Problem'lerinde koşar; kanıt gerekçe metnindedir.
// Kural öneki backend sözleşmesi (chstore.RuleExtCapPrefix / RuleExtDownPrefix,
// anomaly clusterRulePrefix); problemSubject.test.ts biçim pini taşır.
export type ExternalSummaryKind = 'cap' | 'down' | 'cluster';
const EXT_SUMMARY_PREFIXES: ReadonlyArray<[string, ExternalSummaryKind]> = [
  ['anomaly:ext-cap:', 'cap'],
  ['anomaly:ext-down:', 'down'],
  ['anomaly-cluster:ext:', 'cluster'],
];

export function externalSummaryKind(ruleId: string | null | undefined): ExternalSummaryKind | null {
  const id = ruleId ?? '';
  for (const [prefix, kind] of EXT_SUMMARY_PREFIXES) {
    if (id.startsWith(prefix)) return kind;
  }
  return null;
}

/** Özet Problem'in kanıt paneli yerine geçen DÜRÜST açıklama — "toplanıyor" vaadi YOK. */
export function externalSummaryNote(kind: ExternalSummaryKind): { title: string; body: string } {
  switch (kind) {
    case 'cap':
      return { title: 'Tavan özeti — seri kanıtı yok', body: 'Bu Problem tik başına açılış tavanına takılan serilerin özetidir; hangi seriler olduğu ve sayısı gerekçede. Seri kanıtı (trace/pod/log) yalnız tek tek açılan seri Problem\'lerinde toplanır.' };
    case 'down':
      return { title: 'Kaynak erişilemiyor — seri kanıtı yok', body: 'Bu Problem dış kaynağın kendisine (ardışık başarısız poll) ait; son hata ve sayı gerekçede. Kaynak erişilemezken seri taraması koşmaz, dolayısıyla toplanacak seri kanıtı yoktur.' };
    case 'cluster':
      return { title: 'Küme özeti — seri kanıtı üyelerde', body: 'Aynı üst boyutta aynı anda patlayan seriler tek Problem\'de toplandı; üyeler gerekçede. Üyelerin bireysel Problem\'i açılmadığı için trace/pod/log kanıtı bu satırda değil, kaynağın seri Problem\'lerinde aranır.' };
  }
}

// ── Anomali Problem'i mi? (v0.10.1055) ─────────────────────────────────────
//
// Operatör ("Okdir"): anomaliden terfi etmiş Problem'de ANOMALY rozeti yoktu.
// Rozet iki yüzeyde (alarm detay sayfası, Alert rules listesi) elle
// `startsWith('anomaly:')` diye yazılmıştı; terfi öneki "anomaly-auto:" onu BİR
// karakterle ıskalıyor — backend'in v0.10.814'te bildirim türünde düzelttiği
// hatanın birebir ikizi. Önek listesi chstore.ProblemNotifyKind'in anomali
// motoru önekleri: "anomaly:" (metrik / service_silent / dış tarayıcı),
// "anomaly-cluster:" (kümeleme), PromotedAnomalyRulePrefix (terfi).
//
// Bilerek AYNALANMAYAN iki şey: (1) ProblemNotifyKind "anomaly:ext-down:"u
// "problem" türüne yönlendirir — bu bir BİLDİRİM yönlendirme istisnası (Anomali
// tikini kaldıran operatör kaynak-düştü alarmını kaybetmesin), kaynak iddiası
// değil; satır anomali motorunun yazdığı satır, rozeti bugünkü gibi kalır.
// (2) "exception-storm" / "exception:" de bildirimde anomali türüdür ama
// anomali kural ailesi değil; rozetleri hiç olmadı, olmayacak.

/** Terfi Problem'i kural öneki (chstore.PromotedAnomalyRulePrefix). */
export const PROMOTED_ANOMALY_RULE_PREFIX = 'anomaly-auto:';

const ANOMALY_RULE_PREFIXES: readonly string[] = ['anomaly:', 'anomaly-cluster:', PROMOTED_ANOMALY_RULE_PREFIX];

/** isAnomalyProblem — Problem'i anomali motoru mu açtı (ANOMALY tür rozeti). */
export function isAnomalyProblem(ruleId: string | null | undefined): boolean {
  const id = ruleId ?? '';
  return ANOMALY_RULE_PREFIXES.some(p => id.startsWith(p));
}

/**
 * isAnomalyDetectorRule — yalnız "anomaly:" ailesi (z-skor / service_silent /
 * dış seri dedektörü). isAnomalyProblem'in ALT kümesi ve ondan BİLEREK ayrı:
 * bu ailede `threshold` bir eşik değil olağan (medyan) değerdir ve açıklama
 * metni üstteki özet cümlesinin istatistik tekrarıdır (v0.10.1083 istisnası:
 * dış seride `anomaly:ext:` threshold = max(medyan, taban) — özet "eşik" der,
 * detailSummary.ts). Terfi Problem'inde `threshold` gerçek bir kapı
 * (MinPeakRatio); servis kümesinde üye sayısı alt sınırı, dış kümede
 * (v0.10.1083) ya üye sayısı alt sınırı (yayılım) ya da en güçlü üyenin
 * tabanlı eşiği — hangisi olduğu açıklamada yazar;
 * açıklamaları da sayfadaki TEK insan-okur metin (terfi: tür / desen / sayı,
 * küme: üye listesi). O yüzden "eşik" kelimesi ve Description bölümü bu
 * yüklemle karar verir, rozetle değil.
 */
export function isAnomalyDetectorRule(ruleId: string | null | undefined): boolean {
  return (ruleId ?? '').startsWith('anomaly:');
}
