// logsScope — v0.10.1855 (Log Search Faz 2 dilim 2.4; audit §5 risk #2, §9-4
// kararı: varsayılan kapsam `scope=mine`). /logs'un servis odaklı başlangıcı.
// SAF; logsScope.test.ts.
//
// Karar tablosu (sıra önemli):
//   1. açık servis seçili            → off   (servis kazanır; scope gönderilmez)
//   2. URL `scope=all`               → off   (operatör "tüm servisler" dedi)
//   3. URL'de scope yok + süzgeçli derin link → off (paylaşılan link aynen açılır)
//   4. /api/logs/scope bekleniyor    → pending (okuma ATILMAZ — kapsamsız istek
//                                       atıp hemen kapsamlısıyla değiştirmek ES
//                                       maliyetini ikiye katlar)
//   5. uç hata verdi / yetenek yok (CH log yolu) → off (bugünkü davranış)
//   6. kapsam çözüldü (≥1 servis)   → URL'de mine varsa active; yoksa apply
//                                       (URL'ye BİR KEZ replace ile yazılır; istek
//                                       aynı render'da kapsamlı gider)
//   7. kapsam çözülmedi (takımsız / servissiz / kimliksiz) → fallback-all:
//      okuma KAPSAMSIZ gider (tüm servisler, sayfanın örnek boyutu) ve sayfa
//      nedenini engellemeyen bir notla söyler.
//      v0.10.1868 (operatör, prod): 1855'te bu durumda sorgu atılmıyor, "Bir
//      servis seçin" boş ekranı çıkıyordu; takımı tanımlı olmayan kullanıcı
//      sayfayı boş gördü. "En azından son 500 satır gelse iyi olur, seçilmese
//      de." Kapsamsız okuma 1855 öncesinin varsayılanıyla aynı maliyettir.
import type { LogsScope, LogsScopeService } from '@/lib/types';

export type LogsScopeMode = 'mine' | 'all' | '';

export function parseLogsScope(v: string | null | undefined): LogsScopeMode {
  return v === 'mine' || v === 'all' ? v : '';
}

export type LogsScopeDecision =
  | { kind: 'off' }
  | { kind: 'pending' }
  | { kind: 'apply' }
  | { kind: 'active' }
  | { kind: 'fallback-all'; reason: string };

export function decideLogsScope(i: {
  urlScope: LogsScopeMode;
  /** Sayfa açılırken URL'de süzgeç vardı mı (derin link). */
  urlHadFilters: boolean;
  service: string;
  /** undefined = yükleniyor · null = hata · veri. */
  scope: LogsScope | null | undefined;
}): LogsScopeDecision {
  if (i.service.trim() !== '') return { kind: 'off' };
  if (i.urlScope === 'all') return { kind: 'off' };
  if (i.urlScope === '' && i.urlHadFilters) return { kind: 'off' };
  if (i.scope === undefined) return { kind: 'pending' };
  if (i.scope === null || !i.scope.available) return { kind: 'off' };
  if (i.scope.scoped && i.scope.services.length > 0) return { kind: i.urlScope === 'mine' ? 'active' : 'apply' };
  return { kind: 'fallback-all', reason: i.scope.reason };
}

/** İstek parametresi: kapsam etkinse 'mine'. */
export function logsScopeRequestParam(d: LogsScopeDecision): 'mine' | undefined {
  return d.kind === 'active' || d.kind === 'apply' ? 'mine' : undefined;
}

/** Okumalar bekletilsin mi — YALNIZ kapsam kararı beklenirken (kapsamsız istek
 *  atıp hemen kapsamlısıyla değiştirmemek için). Kapsam çözülemezse beklenmez. */
export function logsScopeGate(d: LogsScopeDecision): boolean {
  return d.kind === 'pending';
}

const pct = (v: number) => `%${v.toLocaleString('tr-TR', { maximumFractionDigits: 1, minimumFractionDigits: v > 0 && v < 10 ? 1 : 0 })}`;

/** Seçicideki satır sonu etiketi (mockup "sağlıklı · %0,2 hata"). */
export function scopeHealthLabel(s: LogsScopeService): string {
  const problems = s.openProblems ? ` · ${s.openProblems} açık Problem` : '';
  switch (s.health) {
    case 'green': return `sağlıklı · ${pct(s.errorRate ?? 0)} hata`;
    case 'yellow': return `bozulmuş · ${pct(s.errorRate ?? 0)} hata${problems}`;
    case 'red': return `sorunlu · ${pct(s.errorRate ?? 0)} hata${problems}`;
    default: return `son 5 dk span yok${problems}`;
  }
}

/** Kapsam gerekçesinin okunur hâli (fallback-all notu). */
export function scopeReasonText(reason: string): string {
  switch (reason) {
    case 'no_team': return 'hesabınızda takım tanımlı değil';
    case 'no_services': return 'takımınıza katalogda servis atanmamış';
    case 'no_identity': return 'oturum kimliği yok';
    case 'error': return 'kapsam şu an okunamadı';
    default: return 'kapsam çözülemedi';
  }
}
