// aiEval.ts — v0.10.940 (Settings › CoSRE › Değerlendirme). AiEvalPanel'in
// SAF yarısı: yüzey seçimi, koşu başı geçme farkı, süre / ilerleme metni,
// varsayılan profil seçimi, karşılaştırma gruplaması, beklentinin okunur
// hâli ve URL yardımcıları.
//
// Neden bileşenin içinde değil: bu kararların her biri SESSİZ yanlışa
// açık — yanlış kıyaslanan bir "Fark" hücresi sahte bir gerileme alarmı
// (kırmızı), yanlış seçilen bir "Profil" satırı operatöre koşunun başka
// bir modelle yapıldığını söyler; ikisinde de ekranda hata görünmez.
// Tablo testli (aiEval.test.ts) ve react-refresh yalnız bileşen dışa
// aktaran dosya istediği için ayrı modülde (aiTuning.ts / aiBudget.ts
// emsali).
import type {
  EvalCaseResult, EvalCompare, EvalExpect, EvalRunStatus, EvalRunSummary, EvalsetCatalog, EvalsetCatalogSurface,
} from '@/lib/types';
import { fmtDateTime } from '@/lib/utils';

/** Değerlendirme sekmesinin URL'de sahip olduğu parametreler. v0.10.940 —
 *  sekmeden çıkınca AiTab bunları siler: dönüşte bayat bir `?case=` çekmeceyi
 *  kendiliğinden yeniden açmasın. `tab` AiTab'ındır, burada değil. */
export const EVAL_URL_PARAMS = ['run', 'case', 'cmp'] as const;

// ── URL ──────────────────────────────────────────────────────────────────

/** v0.10.940 — `prev`in KOPYASINA yamayı uygular (null = sil). Yabancı
 *  parametreler (tablo sıralaması `s_*`, `tab`) korunur — ev kuralı "prev'i
 *  kopyala, sorgu dizgesini sıfırdan kurma" (frontend-conventions §4). */
export function withEvalParams(prev: URLSearchParams, patch: Record<string, string | null>): URLSearchParams {
  const p = new URLSearchParams(prev);
  for (const [k, v] of Object.entries(patch)) {
    if (v) p.set(k, v);
    else p.delete(k);
  }
  return p;
}

/** v0.10.940 — satırın GERÇEK href'i (getRowHref / karşılaştırma linkleri):
 *  orta tık / ⌘-tık yeni sekmede aynı çekmeceyi açar (tablo standardı T7). */
export function evalHref(pathname: string, prev: URLSearchParams, patch: Record<string, string | null>): string {
  const qs = withEvalParams(prev, patch).toString();
  return qs ? `${pathname}?${qs}` : pathname;
}

// ── Yüzey seçimi ────────────────────────────────────────────────────────

/** v0.10.940 — çip tıkı. Boş seçim = TÜMÜ (sunucu sözleşmesi: surfaces []
 *  = hepsi). Tümü seçiliyken bir yüzeye basmak DARALTIR (yalnız o yüzey);
 *  seçim her yüzeyi kapsar hâle gelirse [] 'e döner — "hepsi seçili" ile
 *  "Tümü" iki ayrı hâl olmasın (aynı koşuyu iki farklı `surfaces` ile
 *  kaydetmek Fark sütununun kıyasını bozardı, bkz. passDeltas). Sıra
 *  kataloğun sırasıdır, tıklama sırası değil. */
export function toggleSurface(selected: readonly string[], surface: string, order: readonly string[]): string[] {
  const set = new Set(selected);
  if (set.has(surface)) set.delete(surface);
  else set.add(surface);
  const next = order.filter(s => set.has(s));
  return next.length === order.length ? [] : next;
}

/** Seçimin vaka sayısı (boş seçim = katalog toplamı). */
export function selectionCaseCount(surfaces: readonly EvalsetCatalogSurface[], selected: readonly string[]): number {
  const pick = selected.length ? surfaces.filter(s => selected.includes(s.surface)) : surfaces;
  return pick.reduce((a, s) => a + s.cases, 0);
}

// ── Profil başlığı ──────────────────────────────────────────────────────

export interface ProfileView {
  profileId: string;
  profileLabel: string;
  provider: string;
  model: string;
  baseUrl: string;
}

const viewOf = (s: EvalsetCatalogSurface): ProfileView =>
  ({ profileId: s.profileId, profileLabel: s.profileLabel, provider: s.provider, model: s.model, baseUrl: s.baseUrl });

/** v0.10.940 — başlıktaki "Profil / Model / Uç" satırlarının profili: koşunun
 *  VARSAYILAN yönlenen profili. Öncelik: (1) kataloğun `defaultProfileId`si
 *  (sunucunun ŞU AN varsayılan dediği) bir yüzeye yönleniyorsa o; (2) yoksa
 *  (eski sunucu alanı göndermiyor ya da her yüzey eşlemeyle başka profile
 *  gidiyor) en ÇOK yüzeyin yönlendiği profil (eşitlikte vaka toplamı, sonra
 *  katalog sırası).
 *  Son koşunun `profileId`si BİLEREK girdi değil: o, koşunun BAŞLADIĞI
 *  anın varsayılanı — operatör kardeş sekmede varsayılanı değiştirdikten
 *  sonra başlık eski profili "bir sonraki koşu bununla" diye gösterirdi.
 *  Parametre bu yüzden katalog biçiminde: koşu alanı yanlışlıkla verilemez.
 *  Neden çoğunlukta "ilk / en çok vakalı yüzey" DEĞİL: katalog vaka
 *  sayısına göre sıralı ve en kalabalık yüzey IntentClassify (20 vaka) —
 *  tam da chat-intent eşlemesiyle KÜÇÜK modele yönlenebilen yüzey. Onu
 *  başlığa koymak diğer on bir yüzeyin varsayılan modelini "istisna" gibi
 *  gösterirdi. */
export function pickDefaultProfile(
  catalog: Pick<EvalsetCatalog, 'surfaces' | 'defaultProfileId'> | null | undefined,
): ProfileView | null {
  const surfaces = catalog?.surfaces ?? [];
  if (!surfaces.length) return null;
  const want = catalog?.defaultProfileId;
  if (want) {
    const hit = surfaces.find(s => s.profileId === want);
    if (hit) return viewOf(hit);
  }
  const tally = new Map<string, { n: number; cases: number; first: number; s: EvalsetCatalogSurface }>();
  surfaces.forEach((s, i) => {
    const t = tally.get(s.profileId);
    if (t) { t.n++; t.cases += s.cases; } else tally.set(s.profileId, { n: 1, cases: s.cases, first: i, s });
  });
  const best = [...tally.values()].sort((a, b) => b.n - a.n || b.cases - a.cases || a.first - b.first)[0];
  return viewOf(best.s);
}

/** v0.10.940 — varsayılandan AYRILAN yüzeyler tek satırda:
 *  "IntentClassify → Küçük (qwen3.5-2b)". Hiçbiri yoksa ''. */
export function routingHint(surfaces: readonly EvalsetCatalogSurface[], defaultProfileId: string): string {
  return surfaces
    .filter(s => s.profileId !== defaultProfileId)
    .map(s => `${s.surface} → ${s.profileLabel || s.profileId} (${s.model || '—'})`)
    .join(' · ');
}

// ── Koşular ─────────────────────────────────────────────────────────────

/** Karşılaştırılabilir (bitmiş) koşu: sunucunun compare ucu da yalnız
 *  done / cancelled kabul eder. */
export const isFinished = (s: EvalRunStatus): boolean => s === 'done' || s === 'cancelled';

/** Listede sürmekte olan koşu (liste en yeni önce; sunucu en çok birini
 *  `running` bırakır — çapraz pod kapısı). */
export function activeRun(runs: readonly EvalRunSummary[]): EvalRunSummary | null {
  return runs.find(r => r.status === 'running') ?? null;
}

/** v0.10.940 — `?run=` yokken seçili koşu: en yeni sürmekte / bitmiş koşu.
 *  failed / abandoned atlanır — vakası olmayan ya da yarım bir kaydı
 *  "son koşu" diye açmak, bir önceki sağlam koşunun özetini saklardı.
 *  Hepsi öyleyse en yenisi (boş ekran yerine dürüst bir "Başarısız"). */
export function pickDefaultRunId(runs: readonly EvalRunSummary[]): string | null {
  return (runs.find(r => r.status === 'running' || isFinished(r.status)) ?? runs[0])?.id ?? null;
}

const sameSurfaceSet = (a: readonly string[], b: readonly string[]): boolean => {
  if (a.length !== b.length) return false;
  const s = new Set(a);
  return b.every(x => s.has(x));
};

/** v0.10.940 — "Fark" sütunu: koşunun geçen vaka sayısı eksi, kendisinden
 *  ESKİ ilk karşılaştırılabilir koşununki. Karşılaştırılabilir = ikisi de
 *  `done`, AYNI model, AYNI yüzey seçimi, AYNI vaka sayısı. null = kıyas
 *  yok (soluk "—"). Spec yalnız "aynı model" diyor; üç daraltma bilinçli:
 *   • `cancelled` ve `running` satırı kıyaslanmaz: yarıda kalan koşunun
 *     geçme sayısı kısmi — 10 vakada durdurulmuş bir koşu, önceki tam
 *     koşuya karşı −37 gibi SAHTE bir kırmızı gerileme basardı.
 *   • yüzey seçimi farklıysa kıyaslanmaz: yalnız IntentClassify'ı (20)
 *     koşturmak tam koşuya (51) karşı yine sahte bir düşüş olurdu.
 *   • v0.10.940 — vaka sayısı (`total`) farklıysa kıyaslanmaz: aynı seçimde
 *     fikstür seti değişmiş (sürümler arası vaka eklendi / silindi); 48'de
 *     45 ile 51'de 46 arasındaki "−1" başka vakaları sayar. Soluk "—";
 *     daha eskiye bakılmaz — en yakın kıyaslanabilir koşu tabandır.
 *  Vaka düzeyinde dürüst kıyas zaten aşağıdaki karşılaştırma aracında. */
export function passDeltas(runs: readonly EvalRunSummary[]): Record<string, number | null> {
  const out: Record<string, number | null> = {};
  runs.forEach((r, i) => {
    out[r.id] = null;
    if (r.status !== 'done') return;
    const prev = runs.slice(i + 1).find(o => o.status === 'done' && o.model === r.model
      && sameSurfaceSet(o.surfaces ?? [], r.surfaces ?? []));
    if (prev && prev.total === r.total) out[r.id] = r.pass - prev.pass;
  });
  return out;
}

/** v0.10.940 — koşu durumunun görünümü (tablo standardı T9: nokta + metin,
 *  renk yalnız SAPMADA). done / running / cancelled nötr — biri beklenen
 *  son, biri işleyen iş, biri operatörün kendi eylemi. failed kırmızı,
 *  abandoned amber (sahibi düşmüş koşu: dikkat ister ama hata değil).
 *  Sınıflar globals.css `.status-dot*` (Monitors / StatusSection ailesi);
 *  `evs-dot` satır içi hizayı verir. */
export const RUN_STATUS_VIEW: Record<EvalRunStatus, { label: string; dot: string; tone?: 'err' | 'warn' }> = {
  running:   { label: 'Koşuyor',     dot: 'evs-dot status-dot status-dot-operational pulse-dot' },
  done:      { label: 'Bitti',       dot: 'evs-dot status-dot status-dot-operational' },
  cancelled: { label: 'Durduruldu',  dot: 'evs-dot status-dot status-dot-operational' },
  failed:    { label: 'Başarısız',   dot: 'evs-dot status-dot status-dot-outage', tone: 'err' },
  abandoned: { label: 'Yarım kaldı', dot: 'evs-dot status-dot status-dot-degraded', tone: 'warn' },
};

const isRunStatus = (s: string): s is EvalRunStatus => Object.prototype.hasOwnProperty.call(RUN_STATUS_VIEW, s);

/** Bilinmeyen bir durum (yeni sunucu, eski istemci) çökertmesin: nötr, ham ad. */
export function runStatusView(s: string): { label: string; dot: string; tone?: 'err' | 'warn' } {
  return isRunStatus(s) ? RUN_STATUS_VIEW[s] : { label: s || '—', dot: 'evs-dot status-dot status-dot-operational' };
}

/** Karşılaştırma seçicisinin adayları: bitmiş, seçiliden farklı koşular. */
export function compareCandidates(runs: readonly EvalRunSummary[], headId: string | null): EvalRunSummary[] {
  return runs.filter(r => r.id !== headId && isFinished(r.status));
}

// ── Süre / sayı biçimi ──────────────────────────────────────────────────

/** "4 dk 10 sn" / "45 sn" / "1 sa 2 dk" — ilerleme satırının süresi. */
export function fmtDurationMs(ms: number): string {
  const s = Math.max(0, Math.floor((Number.isFinite(ms) ? ms : 0) / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h} sa ${m} dk`;
  if (m > 0) return `${m} dk ${s % 60} sn`;
  return `${s} sn`;
}

/** v0.10.940 — saniye, tek ondalık, tr-TR ("1,4"). Tablo standardı T4:
 *  sayı arayüz fontunda; birim başlıkta ("Ort. süre sn"). */
export function fmtSec1(ms: number): string {
  return ((Number.isFinite(ms) ? ms : 0) / 1000).toLocaleString('tr-TR', { minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

/** İki ondalık tr-TR (rubrik, atıf oranı). */
export function fmtDec2(x: number): string {
  return (Number.isFinite(x) ? x : 0).toLocaleString('tr-TR', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** İşaretli tam sayı: +2 / −3 / 0 (gerçek eksi işareti, tire değil). */
export function fmtSignedInt(n: number): string {
  if (n > 0) return `+${n}`;
  if (n < 0) return `−${Math.abs(n)}`;
  return '0';
}

/** İşaretli iki ondalık: +0,02 / −0,10 / 0,00. */
export function fmtSigned2(x: number): string {
  const v = Number.isFinite(x) ? x : 0;
  const abs = fmtDec2(Math.abs(v));
  if (abs === fmtDec2(0)) return abs;
  return v > 0 ? `+${abs}` : `−${abs}`;
}

/** Koşu zamanı "dd.mm.yyyy HH:mm" (fmtDateTime ailesi, yerelden bağımsız). */
export function fmtRunTime(iso: string): string {
  const ms = Date.parse(iso);
  if (!iso || !Number.isFinite(ms)) return '—';
  return fmtDateTime(ms).slice(0, -3);
}

/** Koşunun süresi: bitmişse finishedAt − startedAt, sürüyorsa now − startedAt. */
export function runElapsedMs(run: Pick<EvalRunSummary, 'startedAt' | 'finishedAt'>, nowMs: number): number {
  const start = Date.parse(run.startedAt);
  const end = run.finishedAt ? Date.parse(run.finishedAt) : nowMs;
  if (!Number.isFinite(start) || !Number.isFinite(end)) return 0;
  return Math.max(0, end - start);
}

/** İlerleme çubuğunun yüzdesi (0..100). */
export function progressPct(run: Pick<EvalRunSummary, 'done' | 'total'>): number {
  if (!run.total || run.total <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round((run.done / run.total) * 100)));
}

/** v0.10.940 — koşu sürerken tek satır. Son cümle bilinçli: koşu sunucuda
 *  (context.Background'a bağlı iş) sürer, sekmeyi kapatmak onu durdurmaz —
 *  operatörün sayfada beklemesi için bir sebep yok. */
export function progressText(run: Pick<EvalRunSummary, 'done' | 'total' | 'startedAt' | 'finishedAt'>, nowMs: number): string {
  return `Koşuyor · ${run.done} / ${run.total} vaka · ${fmtDurationMs(runElapsedMs(run, nowMs))} · sayfadan ayrılabilirsin, koşu sunucuda sürer`;
}

// ── Vakalar ─────────────────────────────────────────────────────────────

/** Kalan (başarısız) vakalar: atlanan vaka kalmış sayılmaz. */
export function failingCases(cases: readonly EvalCaseResult[]): EvalCaseResult[] {
  return cases.filter(c => !c.ok && !c.skipped);
}

/** "Neden" hücresi: ilk kalma gerekçesi, yoksa taşıma hatası. */
export function caseReason(c: Pick<EvalCaseResult, 'fails' | 'error'>): string {
  return (c.fails ?? [])[0] || c.error || '';
}

const quoteList = (xs: readonly string[]) => xs.map(x => `«${x}»`).join(', ');
const KNOWN_ENTITY_PREVIEW = 8;

/** v0.10.940 — fikstür beklentisinin okunur satırları (çekmecenin
 *  "Beklenti" bölümü; ham JSON ayrıca <pre>'de). Sıra puanlamanın sırası:
 *  önce kaldıran koşullar, sonra bağlam (bilinen varlıklar), en sonda ölçü
 *  (süre hedefi — kaldırmaz, sunucu onu yalnız ölçer). */
export function expectLines(e: EvalExpect | null | undefined): string[] {
  if (!e) return [];
  const out: string[] = [];
  if (e.mustContain?.length) out.push(`İçermeli: ${quoteList(e.mustContain)}`);
  if (e.mustNotContain?.length) out.push(`İçermemeli: ${quoteList(e.mustNotContain)}`);
  if (e.intent) out.push(`Niyet: ${e.intent}${e.intentService ? ` · servis: ${e.intentService}` : ''}`);
  if (e.verdicts?.length) out.push(`Kabul edilen hüküm: ${e.verdicts.join(' / ')}`);
  if (e.minEvidenceCitationRate !== undefined && e.minEvidenceCitationRate !== null) {
    out.push(`Asgari kanıt atıf oranı: ${fmtDec2(e.minEvidenceCitationRate)}`);
  }
  if (e.maxUnknownEntities !== undefined && e.maxUnknownEntities !== null) {
    out.push(`Uydurma ad üst sınırı: ${e.maxUnknownEntities}`);
  }
  if (e.knownEntities?.length) {
    const head = e.knownEntities.slice(0, KNOWN_ENTITY_PREVIEW).join(', ');
    const more = e.knownEntities.length > KNOWN_ENTITY_PREVIEW ? ` … (+${e.knownEntities.length - KNOWN_ENTITY_PREVIEW})` : '';
    out.push(`Bilinen varlıklar (${e.knownEntities.length}): ${head}${more}`);
  }
  if (e.knownTeams?.length) out.push(`Bilinen takımlar: ${e.knownTeams.join(', ')}`);
  if (e.maxLatencyMs) out.push(`Süre hedefi: ${fmtSec1(e.maxLatencyMs)} sn (ölçü — aşılması vakayı kaldırmaz)`);
  return out;
}

// ── Karşılaştırma ───────────────────────────────────────────────────────

export interface CompareGroup {
  key: 'newlyFailing' | 'regressed' | 'newlyPassing' | 'improved';
  title: string;
  /** Başlık tonu — yalnız kötüleşme gruplarında (T9). */
  tone?: 'err';
  items: Array<{ id: string; surface: string; detail?: string }>;
}

/** v0.10.940 — EvalCompare → boş olmayan gruplar. Sıra operatörün
 *  sorusunun sırası: önce ne BOZULDU (yeni kalanlar, rubriği düşenler),
 *  sonra ne düzeldi. Listeler null gelse de (eski sunucu) çökmez. */
export function compareGroups(cmp: EvalCompare): CompareGroup[] {
  const delta = (d: { before: number; after: number }) => `${fmtDec2(d.before)} → ${fmtDec2(d.after)}`;
  const groups: CompareGroup[] = [
    { key: 'newlyFailing', title: 'Yeni kalanlar', tone: 'err', items: (cmp.newlyFailing ?? []).map(c => ({ id: c.id, surface: c.surface })) },
    { key: 'regressed', title: 'Rubriği düşenler', tone: 'err', items: (cmp.regressed ?? []).map(c => ({ id: c.id, surface: c.surface, detail: delta(c) })) },
    { key: 'newlyPassing', title: 'Yeni geçenler', items: (cmp.newlyPassing ?? []).map(c => ({ id: c.id, surface: c.surface })) },
    { key: 'improved', title: 'Rubriği yükselenler', items: (cmp.improved ?? []).map(c => ({ id: c.id, surface: c.surface, detail: delta(c) })) },
  ];
  return groups.filter(g => g.items.length > 0);
}

/** v0.10.940 — karşılaştırmanın tek satırlık özeti:
 *  "26.09.2026 18:40 · v0.10.939 · 46 / 51 → 26.09.2026 19:10 · v0.10.940 ·
 *  48 / 51 · rubrik ortalaması +0,02" (+ yalnız bir tarafta olan vakalar). */
export function compareSummary(cmp: EvalCompare): string {
  const side = (s: EvalCompare['base']) => `${fmtRunTime(s.startedAt)} · ${s.appVersion || '—'} · ${s.pass} / ${s.total}`;
  const parts = [`${side(cmp.base)} → ${side(cmp.head)}`, `rubrik ortalaması ${fmtSigned2(cmp.rubricMeanDelta)}`];
  const onlyBase = (cmp.onlyInBase ?? []).length;
  const onlyHead = (cmp.onlyInHead ?? []).length;
  if (onlyBase) parts.push(`yalnız tabanda ${onlyBase} vaka`);
  if (onlyHead) parts.push(`yalnız seçilide ${onlyHead} vaka`);
  return parts.join(' · ');
}
