import type { ExceptionSample, StackFramesResult } from '@/lib/types';
import { codeSourceRef } from '@/components/codeSourceRef';

// stackSource.ts — v0.10.1048 (operatör: "Exception sayfasındaki dosya
// bağlantıları hâlâ daldan açılıyor; kod incelemesi artık sürümden okuyor.
// İkisi aynı yere baksın.").
//
// Exception detayının stack'i ve frame linklerinin sürümü AYNI örnekten gelir:
// kod incelemesi (exceptionStackVersion) de sürümü yalnız stack'i veren
// olaydan seçer. En yeni örneğin sürümü, daha eski bir örneğin stack'ine asla
// yamanmaz — o sürüm başka bir kodu, satırlar başka bir dosyayı gösterirdi.

// representativeStack — SAF: stack taşıyan İLK örnek (sunucu sırası, en yeni
// önce) ve O örneğin çalışan sürümü. Sürüm sunucuda devops.RunningVersion ile
// seçilir (yer tutucu atlanır); burada yalnız kırpılır. Stack'li örnek yoksa
// ikisi de boş; sürüm yoksa '' → istek gövdesi bugünküyle bayt bayt aynı.
export function representativeStack(samples: readonly ExceptionSample[]): { stack: string; version: string } {
  const s = samples.find(x => x.stacktrace);
  if (!s) return { stack: '', version: '' };
  return { stack: s.stacktrace, version: (s.runningVersion ?? '').trim() };
}

// frameLinksRef — SAF: linklerin HANGİ ref'e gittiği, AI panelinin "Kaynak:"
// satırıyla aynı kelimelerle (codeSourceRef): sürüm commit'e bağlandıysa
// "· 1.4.2 (çalışan sürüm)", değilse "· release (dal)". Yalnız link
// ÇİZİLDİYSE (StackTrace'in hasLink'iyle aynı yüklem) — linksiz bir stack'e
// "şuraya gider" demek yanlış olurdu.
export function frameLinksRef(r: StackFramesResult | undefined): string {
  if (!r?.configured || !r.frames.some(f => f.isApp && !!f.url)) return '';
  return codeSourceRef({ version: r.revision?.verified ? r.revision.version : undefined, branch: r.branch });
}
