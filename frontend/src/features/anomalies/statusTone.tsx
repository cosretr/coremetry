// statusTone.tsx — v0.10.929 (K5) — TEK durum → ton sözlüğü, hafif yaprak modül.
//
// v0.10.922'de sözlük ProblemDetail.tsx içinde doğdu (ProblemsSection ve
// AnomaliesPage onu zaten içe aktarıyordu). v0.10.929 (K5) incelemesi:
// RootCausePanel, Incident, Incidents ve Watchers aynı kuralı elle
// kopyalıyordu — ProblemDetail'i içe aktarmak onların parçalarına ağır bir
// zincir (TimeChart, RootCausePanel, AI panelleri…) getirirdi, RootCausePanel
// için de döngü olurdu. Çözüm: sözlük + iki rozet buraya, bağımlılıksız bir
// yaprağa taşındı; ProblemDetail buradan içe aktarır ve eski içe aktaranlar
// kırılmasın diye yeniden dışa aktarır.
//
// KURAL — bu dosya HİÇBİR proje modülünü içe aktarmaz (yalnız React tipleri);
// statusPalette.pin.test.ts bunu çiviler. Ağır bir import eklemek sözlüğü
// kullanan her sayfa parçasına o zinciri geri getirir.
//
// Operatör kararı K5: normal/sağlıklı durum NÖTR, yeşil yalnız bir GEÇİŞ için.
//   open / active / acknowledged / ignored / muted / new → nötr (b-gray)
//   resolved              → b-ok  (geçiş: düzeldi)
//   regressed             → b-warn (dikkat, alarm değil)
// Aciliyetin rengi ÖNCELİK rozetinde (P1 kırmızı); durum onu tekrar etmez.
//
// v0.10.1037 (operatör: "Exceptions'ta NEW ile REGRESSED renkleri aynı,
// düzelt.") — v0.10.922/929'un "new/regressed amber" satırı değişti: new
// artık NÖTR. NEW, triaj görmemiş her grubun normal, varsayılan durumu —
// alarm problemlerindeki open gibi (o zaten nötr); K5 kuralıyla normal durum
// renk taşımaz. Listede neredeyse her satır amber NEW olunca amber bilgi
// taşımıyordu ve seyrek REGRESSED satırı (çözülmüştü, geri geldi) onunla
// aynı tonda kayboluyordu. Şimdi amber yalnız regressed'te: sapma o.
// Rozetin kelimesi ve NEW'in ipucu (StateBadge title) aynen kalıyor.
// Renk hiçbir yerde tek taşıyıcı değil — rozetin kelimesi aynen kalıyor.
// Bilinmeyen durum GİZLENMEZ, nötr tonda ham kelimesiyle basılır.
import type { CSSProperties } from 'react';

// Dışa aktarılmaz (react-refresh: bileşen dosyası yalnız bileşen dışa
// aktarır); içerik statusPalette.pin.test.ts'te kaynaktan çivilenir.
const STATUS_TONE: Record<string, string> = {
  open: 'b-gray', active: 'b-gray', acknowledged: 'b-gray', ignored: 'b-gray', muted: 'b-gray',
  new: 'b-gray', // v0.10.1037 — normal durum (open gibi) nötr; amber yalnız regressed
  resolved: 'b-ok',
  regressed: 'b-warn',
};

/** Durumun rozet sınıfı; bilinmeyen durum nötr (b-gray) düşer. */
function statusToneClass(s: string): string {
  return STATUS_TONE[s.toLowerCase()] ?? 'b-gray';
}

// v0.10.929 (K5) — `style` yalnız yerleşim içindir (ör. Watchers satırındaki
// marginLeft); ton her zaman sözlükten gelir.
export function TriageStatusBadge({ s, label, title, style }: {
  s: string; label: string; title?: string; style?: CSSProperties;
}) {
  return <span className={`badge ${statusToneClass(s)}`} title={title} style={style}>{label}</span>;
}

// Alarm problemlerinin (problems tablosu) üç durumu — liste satırı ve detay
// şeridi aynı kelimeyi basar; tanımadığı durumda eskisi gibi hiçbir şey.
const PROBLEM_STATUS_LABEL: Record<string, string> = {
  open: 'OPEN', acknowledged: 'ACK', resolved: 'RESOLVED',
};

export function ProblemStatusBadge({ status }: { status: string }) {
  const label = PROBLEM_STATUS_LABEL[status];
  return label ? <TriageStatusBadge s={status} label={label} /> : null;
}
