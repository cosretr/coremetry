// dashboardUrl (v0.9.779) — /dashboard adres çubuğunun SAF kuralları.
//
// Neden ayrı dosya: Dashboard.tsx'teki değişken aynası "rezerve
// OLMAYAN her paramı sil" mantığıyla çalışıyor (bir panonun Grafana
// tarzı değişkenleri ?<ad>=<değer> olarak yazılıyor, dolayısıyla
// beyaz liste değil KARA liste tutulabiliyor). Bu, yeni bir sayfa
// paramı ekleyen herkes için bir tuzak: listeye yazmayı unutursan
// paramın her render'da sessizce siliniyor ve özellik "çalışmıyor"
// görünüyor. Kural burada, testli ve tek yerde.
//
// v0.10.1430 — auto-refresh aralığı ayrıştırması (REFRESH_CHOICES /
// parseRefreshParam / refreshLabel) BURADAN ÇIKTI: `?refresh=` artık
// Topbar'daki global RefreshPicker'ın parametresi (lib/autoRefresh.ts;
// eski 30/60/300 tam sayıları orada hâlâ okunuyor). 'refresh' rezerve
// listede KALIYOR — aksi hâlde değişken aynası onu her yazımda silerdi.

// DASHBOARD_RESERVED_PARAMS — /dashboard'un KENDİ paramları. Bunların
// dışındaki her ?key=value bir pano değişkeni değeri sayılır.
export const DASHBOARD_RESERVED_PARAMS: ReadonlySet<string> = new Set([
  'id',      // hangi pano
  'edit',    // düzenleme modunda aç
  'range',   // zaman penceresi (useUrlRange)
  'refresh', // global auto-refresh aralığı (v0.9.779 → v0.10.1430 lib/autoRefresh.ts)
  'kiosk',   // TV/kiosk modu (v0.9.779)
  'panel',   // tek panel görünümü (v0.10.1998, Faz 3 dilim 3.4)
]);

// panelShareUrl — SAF: bu panonun tek panel bağlantısı. Geçerli adresin
// tüm paramları (aralık, değişkenler, kiosk) korunur; düzenleme kipi düşer
// (paylaşılan bağlantı alıcıyı düzenlemeye sokmaz).
export function panelShareUrl(href: string, panelId: string): string {
  const u = new URL(href);
  u.searchParams.set('panel', panelId);
  u.searchParams.delete('edit');
  return u.toString();
}

// isDashboardVariableParam — bu param bir pano değişkeninin değeri mi?
export function isDashboardVariableParam(key: string): boolean {
  return !DASHBOARD_RESERVED_PARAMS.has(key);
}
