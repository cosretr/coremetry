// inboxFilters — Problems (/inbox) süzgeç sözlükleri ve varsayılanları.
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — Inbox.tsx'ten
// taşındı; içerik ve değerler aynen (davranış değişmez).
import type { InboxKind, SubjectLane } from '@/lib/types';
import { z, zParam } from '@/lib/urlSchema';

// Facet vocab + defaults (v0.8.291) — both defaults are what the URL codec
// omits so a fresh link stays clean.
export const PRIO_ALL = ['P1', 'P2', 'P3'] as const;
// v0.9.487 (operatör kararı, prod) — varsayılan yalnız P1: "defaultta sadece
// P1'ler gözüksün". P2/P3 chip sayılarıyla görünür, tek tık uzakta.
//
// v0.9.659 (operatör kararı, prod) — P2 GERİ EKLENDİ: "Problems sayfasında
// P1 P2 exceptions default listelensin".
//
// Kararın arkasındaki olay kayıtlı: tek servisten 12 dakikada 11.260 olay
// üreten bir exception P2 görünüyordu ve varsayılan görünümde HİÇ
// çıkmıyordu (v0.9.627). O vaka artık P1'e terfi ediyor (patlama hızı),
// ama terfi kurallarının yakalayamadığı ciddi P2'ler için varsayılanın
// kendisi bir emniyet ağı.
//
// P3 hâlâ dışarıda: kronik/düşük-şiddet gürültüsü varsayılan görünümü
// doldurur ve v0.9.487'nin çözdüğü sorun buydu.
//
// v0.10.1014 (operatör kararı, prod) — VARSAYILAN ARTIK HER ŞEY: "Problems
// sekmesinde bütün hepsi gelsin, hangisi gerçek problem hangisi değil zamanla
// öğretelim". Üç önceki kararın tersi (487 yalnız P1, 659 P1+P2, 328 yalnız
// exception, 443 HTTP hataları kapalı): gürültüyü varsayılan süzgeçle
// GİZLEMEK yerine operatör neyin gerçek olduğunu işaretleyerek öğretecek
// (öğretme mekanizması ayrı dilim). Çipler ve sayıları aynen duruyor; daraltmak
// yine tek tık ve ?prio= / ?kind= paylaşılan linke biner.
//
// v0.10.1081 (operatör kararı, prod) — VARSAYILAN YENİDEN YALNIZ P1: "Problems
// sayfasında sadece P1'ler gözüksün ve first seen'e göre sıralı olsun".
// v0.10.1014'ün öncelik yarısının tersi (tür varsayılanı HER ŞEY kalıyor).
// Süzgeç sunucuda tavandan ÖNCE uygulanır (inboxSortAndCap), çip sayıları
// süzgeçten önceki kümeyi sayar; P2/P3 tek tıkla açılır ("tüm öncelikler")
// ve ?prio= linke biner. Parametresiz eski link artık yalnız P1 gösterir;
// açık ?prio=P1,P2,P3 bugünkü gibi hepsini.
//
// v0.10.2196 (operatör kararı 2026-11-15, plan S3) — VARSAYILAN YENİDEN HEPSİ:
// "P1, P2 ve P3 birlikte görünsün". v0.10.1081'in tersi. Sıralama öncelik
// önce kalır (P1 üstte, SORT_DEFAULT). Açık ?prio=P1 (veya alt küme) yine
// kazanır ve linke biner; parametresiz adres artık P1+P2+P3 gösterir.
// Rozet (inboxBadgeQuery) aynı kümeyi sayar. Bildirim kuralları DEĞİŞMEDİ.
export const PRIO_DEFAULT = ['P1', 'P2', 'P3'] as const;
// v0.10.1081 — varsayılan sıralama İLK GÖRÜLME, en yeni önce (aynı karar).
// URL'de `s_inbox` (DataTable sözleşmesi); parametresiz adres bu varsayılanı
// alır — kişisel localStorage sıralaması OKUNMAZ (persistSort:false), yoksa
// "parametresiz link = varsayılan" sözü tarayıcıya göre değişirdi.
// v0.10.2122 (Problems sadeleştirme S2, operatör onaylı mockup) — öncelik önce
// (P1 üstte), aynı öncelikte EN ESKİ önce (sunucu eşitlik kırıcısı). v0.10.1081'in
// "first seen'e göre sıralı" varsayılanının yerine; s_inbox=firstSeen linkleri aynen çalışır.
export const SORT_DEFAULT = { id: 'priority', dir: 'desc' as const };
// Sunucunun tanıdığı sıralama kimlikleri (normalizeInboxSort ile birebir);
// bayat bir `s_inbox` varsayılana düşer.
// v0.10.1084 — 'source' kolonu kalktı (kaynak ikinci satırın başında); bayat
// `s_inbox=source` varsayılana düşer (sunucu kimliği hâlâ tanır).
// v0.10.1529 — 'confidence' (Güven kolonu); 'service' kolonu kalktı ama kimlik
// tanınmaya devam eder (bayat `s_inbox=service` linki sunucuda aynen sıralar).
// v0.10.2395 — 'evidence' (Kanıt kolonu, sunucu sortInboxItems "evidence").
// v0.10.2482 — 'impact' (Etki kolonu, sunucu sortInboxItems "impact"). Varsayılan
// 'priority' aynı kalır; sunucu aynı öncelikte büyük etkiyi önce dizer.
export const INBOX_SORT_IDS = ['priority', 'service', 'detail', 'occurrences', 'firstSeen', 'lastSeen', 'assignee', 'confidence', 'evidence', 'impact'] as const;
export const KIND_ALL: readonly InboxKind[] = ['problem', 'exception', 'httperror', 'anomaly', 'incident'];
// v0.9.328 — operator: "Problems ilk açtığında exception görsün, kullanıcılar
// ona göre tasarlar." Exceptions are the signal operators trust: a thrown
// exception is a fact from the code, not an inference from a threshold.
//
// This is a DEFAULT, not a restriction. All four chips stay on screen with
// their full counts, so what's excluded is visible and one click away, and
// ?kind= carries the choice into every shared link. The P1 guard below makes
// sure a landing default can never hide something urgent.
// v0.10.1014 — tüm türler (yukarıdaki PRIO_DEFAULT notu). Dizi BİLEREK açık
// yazıldı (KIND_ALL'a atıf değil): inboxDefaults testi sabiti metinden okur.
// v0.10.1796 — operatör: "Problems'ta sürekli exception görüyorum; gerçek
// problemler ve anomaliler görünsün." Varsayılan tür = problem + anomaly +
// incident; exception ve httperror çipleri sayılarıyla ekranda kalır, tek
// tıkla açılır ve ?kind= ile linke biner (v0.9.417 görünürlük ruhu).
// v0.10.2399 (operatör: "HTTP gibi bazı problem tipleri varsayılan seçili değil, değiştirelim") —
// ardından "hepsi varsayılan açık olsun" — tüm türler varsayılan açık (v0.9.443 ve
// v0.10.1796 kararları geri alındı).
// v0.10.2431 (operatör: "regressed gözüken çok fazla var; exceptions kapalı
// olsun") — exception yine varsayılanda kapalı; çip sayısıyla kalır.
// v0.10.2479 (operatör: "Çok fazla problem ürüyor, gerçek problemler olsun
// isterim"; v0.10.1014 "bütün hepsi gelsin" kararının TERSİ) — varsayılan
// yalnız Problem (+ incident). Anomali / HTTP hatası çipleri sayılarıyla
// kalır, tek tıkla açılır; varsayılan URL'e yazılmaz. Sunucu ikizi
// inbox.go inboxKindsDefault (rozet aynı kümeyi sayar).
export const KIND_DEFAULT: readonly InboxKind[] = ['problem', 'incident'];
// v0.10.1827 — Incidents emekliyken (kabuk bayrağı user.features.incidents
// kapalı; operatör kararı 2026-11-05) `incident` türü sözlükten, çiplerden ve
// varsayılandan çıkar; eski ?kind=incident linki codec'te varsayılana düşer.
// Sunucu ikizi inbox_incidents_retired.go (rozet aynı kümeyi sayar).
export const KIND_ALL_RETIRED: readonly InboxKind[] = ['problem', 'exception', 'httperror', 'anomaly'];
export const KIND_DEFAULT_RETIRED: readonly InboxKind[] = ['problem']; // v0.10.2479
// v0.9.443 — 'httperror': error.type fallback'inin ürettiği çıplak
// durum-kodu grupları ("404"). Varsayılan facet'te KAPALI (KIND_DEFAULT
// değişmedi) — bankada beklenen istemci hataları triage'ı boğmasın;
// chip sayısı yine görünür, tek tıkla açılır (v0.9.417 görünürlük ruhu).
// v0.10.2486 dilim 2 — tek adlar (audit §1.2): exception = Errors (Hatalar),
// anomaly = Signals (Sinyaller), alarm kuralı = Rules (Kurallar). Facet etiketi
// katalogdan (KIND_LABEL_KEY); bu EN sabiti yalnız sayfa giriş paragrafında.
export const KIND_LABEL: Record<InboxKind, string> = {
  problem: 'Rules', exception: 'Errors', httperror: 'HTTP errors',
  anomaly: 'Signals', incident: 'Incidents',
};
export const KIND_LABEL_KEY: Record<InboxKind, string> = {
  problem: 'nav.alerts', exception: 'nav.problems', httperror: 'inbox.kindPl.httperror',
  anomaly: 'inbox.tab.signals', incident: 'nav.incidents',
};

// v0.9.254 — 'ignored' yalnızca exception gruplarını gösterir (problem'ler
// MUTE'lanır, anomaliler SILENCE'lanır — farklı fiiller, farklı state).
// Ayrı bir pivot olması bilinçli: susturmak kasıtlı bir eylem, o satırları
// günlük 'all' görünümüne geri dökmek operatörün sustuğu gürültüyü geri
// getirirdi.
// v0.9.1342 (operatör kararı) — ÖZNE ŞERİDİ. DB problemleri servis
// problemleriyle AYNI listede yarışmasın: kendi şeritleri olsun.
//
// `SubjectLane` ile `InboxKind` KARIŞTIRILMAZ. InboxKind satırın KAYNAĞI
// (problem/exception/anomaly), SubjectLane satırın NEYİ anlattığı. İkisi
// de string olsaydı derleyici karışıklığı yakalayamazdı — v0.9.1339 tam
// olarak o çakışmadan bir bug üretti ve çözümü ayrı ada taşımaktı.
// v0.10.1017 (operatör: "Oracle'dan gelenler Problems'te gözükmüyor") —
// üçüncü şerit: dış metrik kaynağı özneleri (Oracle / Influx, kind=external).
// O satırlar ne servis ne db şeridine giriyordu; hiçbir yerde görünmüyorlardı.
export const SUBJECT_LANES: readonly SubjectLane[] = ['service', 'db', 'external'];
// v0.10.2283 — değerler lib/i18n anahtarı (inbox.lane.*; TR "Servisler", "Veritabanları", "Dış kaynaklar").
export const SUBJECT_LABEL: Record<SubjectLane, string> = {
  service: 'inbox.lane.service', db: 'inbox.lane.db', external: 'inbox.lane.external',
};
// v0.10.1026 (operatör kararı 2026-10-01: "14 girsin") — dış kaynak
// problemleri VARSAYILAN listede de görünür (sunucu: varsayılan şerit =
// servis + dış kaynak). Yeni şerit yok; çip ipuçları kümeyi söylüyor, yoksa
// "Servisler" çipi altında bir Oracle satırı yanlış şeritte sanılırdı.
// v0.10.2283 — değerler lib/i18n anahtarı (inbox.lane.*.title; TR metin aynı).
export const SUBJECT_TITLE: Partial<Record<SubjectLane, string>> = {
  service: 'inbox.lane.service.title',
  external: 'inbox.lane.external.title',
};
// v0.10.2476 (operatör: "problemler kapanınca ayrıca gözüksün aktifler
// arasında kalmasın") — 'closed' (Kapanan): seçili pencerede kapanmış
// Problem'ler, en son kapanan önce (sunucu inbox_closed.go). Sıra operatörün:
// Açık · Kapanan · Susturulan · Tümü. Etiketler lib/i18n inbox.st.*.
export type InboxStatus = 'open' | 'closed' | 'ignored' | 'all';
export const STATUS_PIVOTS: readonly InboxStatus[] = ['open', 'closed', 'ignored', 'all'];
export const STATUS_LABEL: Record<InboxStatus, string> = {
  open: 'inbox.st.open', closed: 'inbox.st.closed', ignored: 'inbox.st.ignored', all: 'inbox.st.all',
};
// ?status= codec'i (lib/urlSchema): bilinmeyen / eksik değer → 'open';
// varsayılan yazılmaz (param silinir), küme dışı değer URL'e asla yazılmaz.
export const INBOX_STATUS_CODEC = zParam<InboxStatus>(z.enum(['open', 'closed', 'ignored', 'all']), 'open');

// v0.10.740 — varsayılan occurrence tabanı (sunucu inboxDefaultMinOcc ile aynı).
// v0.10.949 — 2 → 5, çoklu-servis istisnalı (aynı anda ≥2 serviste görülen
// grup 5'in altında da görünür); istisna sunucuda, istemci varsayılan kipte
// tabanı GÖNDERMEZ.
export const DEFAULT_MIN_OCC = 5;
