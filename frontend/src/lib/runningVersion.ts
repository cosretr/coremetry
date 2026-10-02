// runningVersion.ts — v0.10.590. SAF: span resource attribute'larından
// "olay anında koşan sürüm". Öncelik zinciri chstore.effectiveVersionExpr
// (deploys.go:87) ile AYNI — image tag deployment gerçeğinin kendisidir
// (tag değişimi ⇔ rollout), service.version filoda sabit kalabiliyor:
//   container.image.tag → k8s.container.image.tag → service.version
// Yer tutucu değerler (boş, SNAPSHOT, ${version}, unknown, latest) sürüm
// SAYILMAZ: yanlış bir tag'e bağlanmak bağlanmamaktan kötüdür.
// v0.10.1044 — chstore placeholderVersionList ile eşitlendi (main/master/HEAD/
// null/n/a/NULL); Go ikizi devops.RunningVersion, üçü running_version_test.go'da pinli.
const PLACEHOLDERS = new Set([
  '', '0.0.1', '0.0.1-SNAPSHOT', '0.1.0-SNAPSHOT', '1.0-SNAPSHOT', '1.0.0-SNAPSHOT',
  '${project.version}', '${version}', 'unknown', 'latest', 'dev', 'none',
  'main', 'master', 'HEAD', 'null', 'n/a', 'NULL',
]);

const KEYS = ['container.image.tag', 'k8s.container.image.tag', 'service.version'] as const;

export function runningVersion(res: Record<string, string> | null | undefined): string {
  if (!res) return '';
  for (const k of KEYS) {
    const v = (res[k] ?? '').trim();
    if (v && !PLACEHOLDERS.has(v) && !v.toUpperCase().includes('SNAPSHOT')) return v;
  }
  return '';
}
