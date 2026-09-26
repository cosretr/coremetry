// clusterSearch — v0.10.928 (Y1). Genel görünümdeki cluster kartının
// hedef sorgu dizesi. Eskiden `openCluster` bunu `setParams` ile tık
// anında yazıyordu (kart bir `div onClick` idi, klavyeyle açılamıyordu);
// kart artık `CardLink` ve hedefi RENDER anında bilmek zorunda, yani
// mantık saf bir fonksiyona taşındı.
//
// v0.9.17 — önceki cluster'ın sekmesi/drawer kimlikleri yeni cluster'a
// taşınmaz (filtre çipleri görünür+temizlenebilir olduklarından deep-link
// niyetine dokunulmaz). Diğer her parametre (pencere, env…) korunur.
const STALE_ON_SWITCH = ['tab', 'section', 'pod', 'ns'] as const;

export function clusterSearch(prev: URLSearchParams, name: string): string {
  const next = new URLSearchParams(prev);
  next.set('cluster', name);
  for (const k of STALE_ON_SWITCH) next.delete(k);
  return `?${next.toString()}`;
}
