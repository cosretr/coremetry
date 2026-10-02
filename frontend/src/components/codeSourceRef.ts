import type { AICodeContext } from '@/lib/types';

// codeSourceRef.ts — v0.10.1044 (operatör: "Kod, dalın ucundan değil çalışan
// sürümden okunsun"). "Kaynak:" satırının ref parçası — kodun HANGİ ref'ten
// okunduğu dürüstçe: çalışan sürümün commit'i mi, dalın ucu mu. İki çizim
// yeri (CopilotExplain) aynı metni bu tek fonksiyondan alır.
export function codeSourceRef(code: Pick<AICodeContext, 'version' | 'branch'>): string {
  if (code.version) return ` · ${code.version} (çalışan sürüm)`;
  if (code.branch) return ` · ${code.branch} (dal)`;
  return '';
}
