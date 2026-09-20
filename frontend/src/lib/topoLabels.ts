// topoLabels — pure label helpers for TopologyFlowGraph dependency pills
// (v0.8.297, operator-reported: the db pill only said "oracle"; the operator
// wants the instance name or db.name visible where calls land in
// postgres/mssql/oracle/redis).

// Bir bağımlılık pilinin ihtiyaç duyduğu düğüm alanları. ServiceMapNode bu
// şekle yapısal olarak uyar; testler sentetik nesne geçebilsin diye burada
// dar tutuldu.
export type DepPillNode = {
  service: string;
  kind?: string;
  subkind?: string;
  dbName?: string;
};

export type DepPillLines = { title: string; sub: string };

// depInstanceLabel — the concrete identity for a dependency pill's sub-line:
// db.name when known ("COREBANK"), else the @instance suffix of the node id
// ("db:oracle@oracle-prod" → "oracle-prod") unless it merely repeats the
// system name ("db:redis@redis" adds nothing), else null so the caller falls
// back to the generic kind label.
export function depInstanceLabel(n: { service: string; subkind?: string; dbName?: string }): string | null {
  if (n.dbName) return n.dbName;
  const at = n.service.indexOf('@');
  if (at >= 0) {
    const inst = n.service.slice(at + 1);
    if (inst && inst !== n.subkind) return inst;
  }
  return null;
}

// depPillLines — v0.10.517 (operatör, prod topoloji: "oracle altta, db name
// pgts02 üstte yazsa daha iyi olur"): bağımlılık pilinin İKİ satırı.
// Somut kimlik (db.name / @instance) varsa BAŞLIK odur, sistem adı
// ("oracle") alt satıra iner — operatör hangi veritabanına gittiğini
// başlıkta okur; sistem türü zaten renk/ikon ve alt satırda. Kimlik yoksa
// eski düzen: başlık sistem adı, alt satır tür etiketi (kindLabel).
//
// DİKKAT (v0.10.837): bu gövde TEK düğüme bakar, dolayısıyla "bu db.name
// gerçekten ayırt edici mi?" sorusunu CEVAPLAYAMAZ. Küme-duyarlı karar
// depPillLinesMap'te; grafik onu çağırır.
export function depPillLines(
  n: { service: string; subkind?: string; dbName?: string },
  kindLabel: string,
): DepPillLines {
  const system = n.subkind || n.service.replace(/^(db|queue|ext):/, '');
  const inst = depInstanceLabel(n);
  if (inst) return { title: inst, sub: system };
  return { title: system, sub: kindLabel };
}

// ── v0.10.837 — küme-duyarlı ayrım ───────────────────────────────────
// (Operatör-bildirimli, prod: bir servisin Topology sekmesinde ALTI ayrı
// Oracle düğümü vardı ve hepsinin başlığı aynı db.name'i yazıyordu. Yükleri
// tamamen farklıydı — 103 çağrı %0 hata / 2.8K %1.3 / 67.5K %5.5 — yani
// ayırt edememek estetik bir kusur değil, YANLIŞ düğüme bakma sorunuydu.)
//
// Kök neden: depPillLines tek düğüme bakar ve `db.name` varsa onu KOŞULSUZ
// başlık yapar. Bir db.name birçok sunucuda aynı olabilir; düğümü ayıran şey
// id'deki `@<instance>` parçasıdır (düğüm zaten (system, instance) düzeyinde
// toplanır — bkz. topology/nodeDetailHref.ts). Bu karar tek düğüme bakarak
// VERİLEMEZ: aynı db.name'in başka bir instance'ta da görünüp görünmediği
// ancak RENDER EDİLEN KÜMEYE bakınca bilinir.
//
// v0.10.517 BOZULMUYOR: o karar "somut kimlik BAŞLIKTA, motor adı altta"
// diyordu. Çakışmayan her düğüm bugünkü etiketi bayt bayt korur. Çakışan
// düğümde db.name zaten SOMUT DEĞİLDİR (altı düğümde aynı yazı) — orada
// somut kimlik ana makinedir, o yüzden başlığa o çıkar; db.name kaybolmaz,
// motor adının yanında alt satırda okunur. İlke aynı, uygulandığı alan
// düzeltildi.
//
// Neden belirteç BAŞLIKTA, alt satırda değil: pil `max-width:170px` ve
// `.topo-name` ellipsis'li — "shop_prd · db-core-a" gibi birleşik bir başlık
// tam da AYIRT EDİCİ KUYRUĞUNDAN kırpılırdı (kırpılma olayı sınıfı: fixed
// tablo + nowrap, docs/INCIDENTS.md). Kısa ana makine adı tek başına başlığa
// sığar ve operatör altı pili hover'sız, tek bakışta ayırır.

const DEP_ID_PREFIX = /^(db|queue|ext):/;

// depSystem — pilin motor adı ("oracle"): subkind, yoksa ön-eki soyulmuş id.
function depSystem(n: DepPillNode): string {
  return n.subkind || n.service.replace(DEP_ID_PREFIX, '');
}

// depInstanceOf — id'nin İLK '@'inden sonrası. Sunucu tarafı adı
// `db:<system>@<instance>` kuruyor; instance adı '@' içerebilir, sistem adı
// içeremez (internal/api/servicegraph.go decodeNodeName) — bu yüzden
// indexOf, lastIndexOf değil.
function depInstanceOf(n: DepPillNode): string {
  const at = n.service.indexOf('@');
  return at >= 0 ? n.service.slice(at + 1) : '';
}

// Ayırt edici belirteç merdiveni. Pil dar, o yüzden ilk basamak ana makine
// adının İLK NOKTAYA KADARKİ parçası: "db-core-a.internal.example.com" →
// "db-core-a" (tam alan adı basılmaz). Pratikte ayrım burada biter; bitmezse
// (aynı kısa ad, farklı alan adlarında) tam instance'a, o da yetmezse düğüm
// kimliğine çıkılır — merdivenin son basamağı düğüm id'si olduğu için ayrım
// GARANTİ edilir, "genelde yeter" değil.
const DEP_DISCRIMINATOR_RUNGS: ReadonlyArray<(n: DepPillNode) => string> = [
  n => depInstanceOf(n).split('.')[0],
  n => depInstanceOf(n),
  n => n.service.replace(DEP_ID_PREFIX, ''),
];

// depDisambiguation — render edilecek düğüm listesini alır ve YALNIZ etiketi
// bir başkasınınkiyle çakışan düğümler için service → ayırt edici belirteç
// eşlemesi döner. Çakışma tek bir alana değil ÜRETİLEN (başlık, alt satır)
// ikilisine bakılarak saptanır: sözleşme "iki farklı düğüm aynı ikiliyi
// üretemez" olduğu için ölçülen şey de tam olarak o ikilidir.
//
// Aynı db.name AYNI instance üzerinde birden çok kez görünüyorsa bu çakışma
// DEĞİLDİR: ya aynı düğümdür, ya da alt satırı zaten farklı olan başka bir
// motordur (oracle vs mysql) — ikili benzersiz kalır, ayrım eklenmez.
export function depDisambiguation(
  nodes: readonly DepPillNode[],
  kindLabel: (n: DepPillNode) => string,
): Map<string, string> {
  const out = new Map<string, string>();

  // 1) Bugünkü (yamasız) etiketine göre grupla. Anahtar ikilinin KENDİSİ —
  //    ayrı ayrı db.name/instance karşılaştırmak "aynı db.name ama farklı
  //    motor" gibi zaten ayrışmış hâlleri boşuna bozardı.
  const byPair = new Map<string, DepPillNode[]>();
  const seen = new Set<string>();
  for (const n of nodes) {
    if (!n.kind) continue;          // servis düğümü: bağımlılık pili değil
    if (seen.has(n.service)) continue; // aynı id = aynı düğüm, çakışma değil
    seen.add(n.service);
    const b = depPillLines(n, kindLabel(n));
    // Anahtar AYIRICISIZ kodlanıyor (JSON dizisi): etiket metni herhangi bir
    // ayırıcı karakteri içerebilir ve "a|b"+"c" ile "a"+"b|c" aynı anahtara
    // düşerdi. Kontrol karakteri (NUL) de kullanılmıyor — kaynağa kaçan bir
    // NUL tüm kapılardan geçip dosyayı ikili yapıyor (binary-poisoned source).
    const key = JSON.stringify([b.title, b.sub]);
    const g = byPair.get(key);
    if (g) g.push(n); else byPair.set(key, [n]);
  }

  // 2) Yalnız gerçekten çakışan grupta, ayrımı SAĞLAYAN ilk basamağı seç.
  for (const group of byPair.values()) {
    if (group.length < 2) continue;
    for (const rung of DEP_DISCRIMINATOR_RUNGS) {
      const tokens = group.map(rung);
      if (tokens.some(t => !t)) continue;                 // boş belirteç ayırmaz
      if (new Set(tokens).size !== group.length) continue; // hâlâ çakışıyor
      group.forEach((n, i) => out.set(n.service, tokens[i]));
      break;
    }
  }
  return out;
}

// depPillLinesMap — grafiğin çağırdığı KÜME-DUYARLI giriş: render edilecek
// düğümlerin tamamını alır, service → (başlık, alt satır) döner. Bağımlılık
// olmayan düğümler (kind yok) eşlemeye girmez.
export function depPillLinesMap(
  nodes: readonly DepPillNode[],
  kindLabel: (n: DepPillNode) => string,
): Map<string, DepPillLines> {
  const disc = depDisambiguation(nodes, kindLabel);
  const out = new Map<string, DepPillLines>();
  for (const n of nodes) {
    if (!n.kind) continue;
    const base = depPillLines(n, kindLabel(n));
    const token = disc.get(n.service);
    if (!token) { out.set(n.service, base); continue; }
    // Çakışma: ayırt edici belirteç BAŞLIĞA çıkar, db.name motor adının
    // yanında alt satırda kalır (v0.10.517'nin "motor adı altta" kararı
    // korunur, db.name kaybolmaz).
    out.set(n.service, {
      title: token,
      sub: n.dbName ? `${depSystem(n)} · ${n.dbName}` : base.sub,
    });
  }
  return out;
}
