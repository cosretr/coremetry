// inboxFilters — Problems (/inbox) süzgeç sözlükleri ve varsayılanları.
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — Inbox.tsx'ten
// taşındı; içerik ve değerler aynen (davranış değişmez).
import type { InboxKind, SubjectLane } from '@/lib/types';

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
export const INBOX_SORT_IDS = ['priority', 'service', 'detail', 'occurrences', 'firstSeen', 'lastSeen', 'assignee', 'confidence'] as const;
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
export const KIND_DEFAULT: readonly InboxKind[] = ['problem', 'anomaly', 'incident'];
// v0.10.1827 — Incidents emekliyken (kabuk bayrağı user.features.incidents
// kapalı; operatör kararı 2026-11-05) `incident` türü sözlükten, çiplerden ve
// varsayılandan çıkar; eski ?kind=incident linki codec'te varsayılana düşer.
// Sunucu ikizi inbox_incidents_retired.go (rozet aynı kümeyi sayar).
export const KIND_ALL_RETIRED: readonly InboxKind[] = ['problem', 'exception', 'httperror', 'anomaly'];
export const KIND_DEFAULT_RETIRED: readonly InboxKind[] = ['problem', 'anomaly'];
// v0.9.443 — 'httperror': error.type fallback'inin ürettiği çıplak
// durum-kodu grupları ("404"). Varsayılan facet'te KAPALI (KIND_DEFAULT
// değişmedi) — bankada beklenen istemci hataları triage'ı boğmasın;
// chip sayısı yine görünür, tek tıkla açılır (v0.9.417 görünürlük ruhu).
export const KIND_LABEL: Record<InboxKind, string> = {
  problem: 'Problems', exception: 'Exceptions', httperror: 'HTTP errors',
  anomaly: 'Anomalies', incident: 'Incidents',
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
export const SUBJECT_LABEL: Record<SubjectLane, string> = {
  service: 'Servisler', db: 'Veritabanları', external: 'Dış kaynaklar',
};
// v0.10.1026 (operatör kararı 2026-10-01: "14 girsin") — dış kaynak
// problemleri VARSAYILAN listede de görünür (sunucu: varsayılan şerit =
// servis + dış kaynak). Yeni şerit yok; çip ipuçları kümeyi söylüyor, yoksa
// "Servisler" çipi altında bir Oracle satırı yanlış şeritte sanılırdı.
export const SUBJECT_TITLE: Partial<Record<SubjectLane, string>> = {
  service: 'Servisler ve dış kaynak (ör. Oracle) problemleri',
  external: 'Yalnız dış kaynak problemleri — varsayılan listede de görünürler',
};
export type InboxStatus = 'open' | 'all' | 'ignored';
export const STATUS_PIVOTS: readonly InboxStatus[] = ['open', 'all', 'ignored'];

// v0.10.740 — varsayılan occurrence tabanı (sunucu inboxDefaultMinOcc ile aynı).
// v0.10.949 — 2 → 5, çoklu-servis istisnalı (aynı anda ≥2 serviste görülen
// grup 5'in altında da görünür); istisna sunucuda, istemci varsayılan kipte
// tabanı GÖNDERMEZ.
export const DEFAULT_MIN_OCC = 5;
