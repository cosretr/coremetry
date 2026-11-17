import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2313 — metinler arayüz dilinde (TR aynı)
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { CheckboxField } from '@/components/coremetry/Field';
import { api } from '@/lib/api';
import type { ArgoCDBound, ArgoCDCandidate, ArgoCDPin, ArgoCDSettings, ArgoCDSettingsResponse, ArgoCDTokenStatus } from '@/lib/types';
import { ConfigStatusBanner, FlashBox, SettingsLoadError, humanize, useSettingsLoad } from './shared';
import {
  advInputId, applyBuffer, blankBuffer, bufferDirty, bufferErrorsFromIssues, bufferFrom, diffPhrases, dirtyText, domKey,
  draftFromSettings, hubName, issueFromServer, mergeDraft, newKey, parseArgoHttpError, pruneBufferErrors, reloadText,
  remoteClustersFrom, savedAtText, toPutBody, trPossessive3, validateBuffer, validateDraft,
  type AdvNumKey, type BufferErrors, type Draft, type InstanceBuffer, type InstanceDraft, type Issue,
  type MetricsOnlyMode, type RemoteCluster,
} from './argocdForm';
import { ArgoCDSectionPanel, ArgoCDNote } from './ArgoCDSectionPanel';
import { ArgoCDHubsPanel } from './ArgoCDHubsPanel';
import { ArgoCDInstancesPanel } from './ArgoCDInstancesPanel';
import { ArgoCDDiscoveryPanel } from './ArgoCDDiscoveryPanel';
import { ArgoCDEnvPanel } from './ArgoCDEnvPanel';
import { ArgoCDAdvancedPanel } from './ArgoCDAdvancedPanel';
import { ArgoCDEmptyPanel } from './ArgoCDEmptyPanel';

// ArgoCDTab — v0.10.974 — Ayarlar › Argo CD (Rollouts v2 P1.4 arayüzü;
// operatör onayı 2026-09-27 "Onay rollout için önerin", mockup argocd-canvas
// Main / States; sunucu v0.10.957 + v0.10.974 PUT kuralları).
//
// Bu sürüm ayarları SAKLAR ve DOĞRULAR. v0.10.983 (P3.1): argocd-metrics
// işçisinin AYRI onay kutusu var (`metricsWorker.enabled`, varsayılan kapalı;
// yalnız entegrasyon açıkken seçilebilir). İnceleme: üst düzey `enabled` P1'den
// beri "yalnız bayrağı kaydeder" diye sunuldu — işçi anahtarı yapılsaydı onu
// açmış kurulumlarda işçi deploy'la başlardı. Kayıt yalnız admin PUT'u (audit
// settings.argocd.update + 409 bayat koruması aynen). Argo CD API bağlantısı
// askıda (karar 2026-09-27); başlık bandı işçinin kayıtlı durumunu dürüstçe
// söyler. Sayfanın durumu:
//   • Taslak GET `settings`ten (saklanan) kurulur; Kaydet TÜM blobu taslak
//     sırasıyla PUT eder, yanıt (settings + tokens) yeni taban olur.
//   • Remote Cluster kaynağı Thanos ayar anlık görüntüsü (devre dışılar
//     dahil); iki GET birlikte yüklenir, form yarım çizilmez
//     (useSettingsLoad kapısı: okunamadıysa form hiç çizilmez).
//   • Kaydet önce istemci denetimi (tüm sorunlar birden, sunucu yollarıyla;
//     argocdForm.validateDraft); sunucu 400'ü taslak dizinine eşlenir ve son
//     söz sunucunun.
//   • Tek canlı bölge (role=status): "— kaydedilmedi." iletileri ve keşif
//     ilerlemesi. Odak yalnız eylemlerde taşınır.
//   • URL'de yalnız sekme slug'ı: açık form ve keşif sonuçları kaydedilmemiş
//     satırlara atıf yapan taslak durumudur (ClustersTab satırları gibi).
//   • v0.10.974 — Kaydet uçuştayken düzenlenebilir alan KİLİTLİ (`<fieldset
//     disabled>`): başarıda taslak sunucu yanıtıyla değiştirilir; kilit
//     olmasa istek sürerken yapılan düzenleme sessizce silinir ve sayaç
//     "Kayıtlı ayarlarla aynı." derdi.
//   • v0.10.974 — hata kutusu (role=alert, assertive) Kaydet anının
//     ANLIK GÖRÜNTÜSÜ (mockup flash): yazdıkça yeniden yazılıp ekran
//     okuyucuyu kesmez; taslak düzenlenince kapanır (mockup `flash: null`).
//     Alan altı hatalar canlı kalır. Gidilecek yeri olmayan yol bağlantı değil.
//   • v0.10.978 — bayat yazma koruması: her PUT GET'te görülen `updatedAt`i
//     `expectedUpdatedAt` olarak taşır; 409 `stale` → ayrı role=alert kutusu
//     ("başka biri tarafından değiştirildi — yeniden yükle") ve tek çıkış
//     "Yeniden yükle": yeniden GET + argocdForm.mergeDraft (sunucuda
//     değişmemiş alan/satırlardaki düzenlemeler korunur, çakışanlar sunucunun
//     değeriyle gelir; sonuç kutusu kaçının korunduğunu/atıldığını söyler).
//     Bu kutu yazmaya devam edince KAPANMAZ (bir sonraki Kaydet yine 409
//     olurdu); başarılı yeniden yükleme ya da kayıt kapatır. Yeniden yükleme
//     sürerken de alan kilitli. Açık formda uygulanmamış düzenleme varken
//     yeniden yükleme Kaydet gibi engellenir (form birleştirmeye girmez,
//     sessizce silinirdi).

// Faz 2 dilim 1 R15 — CSSProperties sabitleri → Tailwind utility (aynı
// token'lar); ui Button/LinkButton → shadcn; üç ham onay kutusu →
// coremetry CheckboxField (ipucu ayrı `#…-h` öğesinde: aria-describedby
// sözleşmesi tek id). Kaydet kilidinin fieldset'i görünmez (kenarlık/dolgu
// yok, min-w-0: fieldset'in min-content varsayılanı tabloları taşırırdı).
const H2 = 'm-0 text-[length:var(--fs-lg)] font-semibold';
const DESC = 'm-0 max-w-[760px] text-[length:var(--fs-md)] leading-5 text-(--text2)';
const HINT = 'pl-6 text-[length:var(--fs-xs)] leading-snug text-(--text3)';

/** Onay kutusu + altında `id`'li ipucu (aria-describedby = yalnız o id). */
function Toggle({ id, label, checked, disabled, onChange, hint }: {
  id: string; label: string; checked: boolean; disabled: boolean; onChange: (v: boolean) => void; hint: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <CheckboxField id={id} label={label} checked={checked} disabled={disabled} aria-describedby={`${id}-h`} onCheckedChange={onChange} />
      <div id={`${id}-h`} className={HINT}>{hint}</div>
    </div>
  );
}

const EMPTY_SETTINGS: ArgoCDSettings = { enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} };
// v0.10.2313 — metin arayüz dilinde (acd.pending); TR aynı.
const pendingIssue = (lang: Lang): Issue => ({ path: '', message: t('acd.pending', lang), short: t('acd.pending', lang), target: { kind: 'form' } });

interface Snap {
  settings: ArgoCDSettings;
  tokens: Record<string, ArgoCDTokenStatus>;
  bounds: Record<string, ArgoCDBound>;
  pins: ArgoCDPin[];
}

export function ArgoCDTab() {
  const lang = useLang();
  const [snap, setSnap] = useState<Snap>({ settings: EMPTY_SETTINGS, tokens: {}, bounds: {}, pins: [] });
  const [clusters, setClusters] = useState<RemoteCluster[]>([]);
  const [base, setBase] = useState<Draft>(() => draftFromSettings(EMPTY_SETTINGS));
  const [draft, setDraft] = useState<Draft>(base);
  const [buffer, setBuffer] = useState<InstanceBuffer | null>(null);
  const [bufErr, setBufErr] = useState<BufferErrors>({});
  const [pending, setPending] = useState(false);
  const [instMsg, setInstMsg] = useState('');
  const [hubMsg, setHubMsg] = useState('');
  const [validated, setValidated] = useState(false);
  /** Kaydet anında bulunan istemci sorunları (hata kutusunun içeriği). */
  const [shownIssues, setShownIssues] = useState<Issue[]>([]);
  const [serverIssue, setServerIssue] = useState<Issue | null>(null);
  const [saveError, setSaveError] = useState('');
  const [okText, setOkText] = useState('');
  /** v0.10.978 — PUT 409 stale: sunucudaki damga (gövdede yoksa undefined). */
  const [stale, setStale] = useState<{ updatedAt?: number } | null>(null);
  /** v0.10.978 — yeniden yükleme sonucu (korunan/atılan sayısı ya da hata). */
  const [note, setNote] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [reloading, setReloading] = useState(false);
  /** v0.10.978 — yeniden yükleme sonrası odak hedefi: kilit AÇILDIKTAN (commit) sonra; `focus()`'un
   *  setTimeout(0)'ı React'in toplu güncellemesinden önce koşabilir ve devre dışı Kaydet odak almazdı. */
  const [focusAfter, setFocusAfter] = useState('');
  const [busy, setBusy] = useState(false);
  const [live, setLive] = useState('');
  const [savedNow, setSavedNow] = useState(false);
  const [advOpen, setAdvOpen] = useState(false);
  const [pinsOpen, setPinsOpen] = useState(false);
  /** PUT uçuşta — fieldset'e ek ikinci kilit (ör. Kaydet'ten önce açılmış menü). */
  const busyRef = useRef(false);

  const applyResponse = (r: ArgoCDSettingsResponse) => {
    const d = draftFromSettings(r.settings);
    setSnap({ settings: r.settings, tokens: r.tokens ?? {}, bounds: r.bounds ?? {}, pins: r.settings.pins ?? [] });
    setBase(d);
    setDraft(d);
  };
  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => Promise.all([api.getArgoCDSettings(), api.getThanosSettings()]),
    ([a, t]) => { applyResponse(a); setClusters(remoteClustersFrom(t.clusters)); },
  );

  // Aynı ileti iki kez gelirse de okunsun: sıfır genişlikli boşlukla fark.
  const announce = useCallback((text: string) => setLive(prev => (prev === text ? `${text}\u200b` : text)), []);
  const focus = useCallback((id: string) => {
    if (!id) return;
    setTimeout(() => document.getElementById(id)?.focus(), 0);
  }, []);
  useEffect(() => {
    if (!focusAfter) return;
    document.getElementById(focusAfter)?.focus();
    setFocusAfter('');
  }, [focusAfter]);
  const change = useCallback((f: (d: Draft) => Draft) => {
    if (busyRef.current) return;
    setDraft(f);
    setServerIssue(null); setSaveError(''); setOkText(''); setShownIssues([]); setNote(null); // `stale` kalır (v0.10.978)
  }, []);

  // ── Satır içi form ──────────────────────────────────────────────────────
  const blockPending = () => {
    setPending(true);
    announce(t('acd.pendingForm', lang));
    focus('acd-ed-apply');
  };
  const closeEdit = () => {
    const b = buffer;
    setBuffer(null); setBufErr({}); setPending(false);
    focus(b && !b.isNew ? `acd-inst-btn-${domKey(b.key)}` : 'acd-inst-add');
  };
  const openEdit = (key: string) => {
    if (bufferDirty(buffer, draft.instances)) { blockPending(); return; }
    if (buffer && buffer.key === key) { closeEdit(); return; }
    const row = draft.instances.find(i => i.key === key);
    if (!row) return;
    setBuffer(bufferFrom(row)); setBufErr(bufferErrorsFromIssues(issues, key)); setPending(false); setInstMsg('');
    focus('acd-ed-name');
  };
  // v0.10.974 — menünün "Düzenle"si: form o satırda açıksa hiçbir şey yapmaz
  // (mockup onEdit: yalnız menü kapanır, odak Popover'la ⋯'ye döner); kirli
  // form da o satırınsa "uygulanmamış değişiklik" engeli çıkmaz.
  const editInstance = (key: string) => {
    if (buffer && buffer.key === key) return;
    openEdit(key);
  };
  const newInstance = () => {
    if (bufferDirty(buffer, draft.instances)) { blockPending(); return; }
    if (draft.hubs.length === 0) return;
    setBuffer(blankBuffer(draft.hubs[0].clusterId)); setBufErr({}); setPending(false); setInstMsg('');
    announce(t('acd.tab.newRow', lang));
    focus('acd-ed-id');
  };
  const changeBuffer = (next: InstanceBuffer) => {
    if (buffer) setBufErr(e => pruneBufferErrors(e, buffer, next));
    setBuffer(next);
  };
  const applyEdit = () => {
    if (!buffer) return;
    const err = validateBuffer(buffer, draft.instances, draft.hubs, clusters);
    const n = Object.keys(err).length;
    if (n) { setBufErr(err); announce(tf('acd.tab.nFix', lang, { n })); return; }
    const row = applyBuffer(buffer);
    const isNew = buffer.isNew;
    change(d => ({ ...d, instances: isNew ? [...d.instances, row] : d.instances.map(i => (i.key === row.key ? row : i)) }));
    setBuffer(null); setBufErr({}); setPending(false);
    announce(tf('acd.tab.applied', lang, { id: row.id }));
    focus(`acd-inst-btn-${domKey(row.key)}`);
  };
  const removeInstance = (key: string, f: { ok: string; blocked?: string }) => {
    const row = draft.instances.find(i => i.key === key);
    if (!row) return;
    const n = snap.pins.filter(p => p.instanceId === row.id).length;
    if (n) {
      setInstMsg(tf('acd.tab.pinBlocked', lang, { id: row.id, n }));
      announce(tf('acd.tab.pinBlockedLive', lang, { id: row.id, n }));
      if (f.blocked) focus(f.blocked);
      return;
    }
    change(d => ({ ...d, instances: d.instances.filter(i => i.key !== key) }));
    if (buffer?.key === key) { setBuffer(null); setBufErr({}); setPending(false); }
    setInstMsg('');
    announce(tf('acd.tab.removed', lang, { id: row.id }));
    focus(f.ok);
  };
  const removeBuffer = () => {
    if (!buffer) return;
    if (buffer.isNew) {
      setBuffer(null); setBufErr({}); setPending(false);
      announce(t('acd.tab.newRemoved', lang));
      focus('acd-inst-add');
      return;
    }
    removeInstance(buffer.key, { ok: 'acd-inst-add' });
  };
  const toggleInstance = (key: string) => {
    const row = draft.instances.find(i => i.key === key);
    if (!row) return;
    change(d => ({ ...d, instances: d.instances.map(i => (i.key === key ? { ...i, enabled: !i.enabled } : i)) }));
    if (buffer?.key === key) setBuffer({ ...buffer, enabled: !row.enabled });
    announce(tf(row.enabled ? 'acd.tab.disabled' : 'acd.tab.enabled', lang, { id: row.id }));
    focus(`acd-inst-menu-${domKey(key)}`);
  };
  const candidateRow = (hubClusterId: string, c: ArgoCDCandidate, id: string): InstanceDraft => ({
    key: newKey('n:'), origin: 'new', savedId: '', id, hubClusterId, name: '', hubNamespace: c.hubNamespace,
    metricsJob: c.metricsJob, apiUrl: '', storedRef: '', tokenInput: '', clearToken: false, insecureSkipVerify: false,
    enabled: true, discovered: true, appsAnyNamespace: c.appsAnyNamespace || c.namespaceCase === 'C',
  });
  const addCandidate = (hubClusterId: string, c: ArgoCDCandidate, id: string) => {
    const row = candidateRow(hubClusterId, c, id);
    change(d => ({ ...d, instances: [...d.instances, row] }));
    announce(tf('acd.tab.candAdded', lang, { id }));
  };
  // v0.10.990 (operatör: "tek tek ekle diyorum") — keşif panelinin "Tümünü
  // ekle"si: N aday TEK taslak güncellemesiyle (tek "kaydedilmedi" geçişi).
  const addCandidates = (hubClusterId: string, items: { cand: ArgoCDCandidate; id: string }[]) => {
    if (items.length === 0) return;
    const rows = items.map(it => candidateRow(hubClusterId, it.cand, it.id));
    change(d => ({ ...d, instances: [...d.instances, ...rows] }));
    announce(tf('acd.tab.candsAdded', lang, { n: rows.length }));
  };

  // ── Hub'lar ─────────────────────────────────────────────────────────────
  // v0.10.997 (operatör: "Hub kaldıramıyorum instance varsa") — hub'ı, ona
  // bağlı tüm instance'larla birlikte TEK taslak değişikliğiyle kaldırır.
  // Pin denetimi panelde (pin'li hub'da bu yol sunulmaz); açık düzenleme
  // formu kaldırılan hub'a aitse kapanır.
  const removeHubWithInstances = (key: string) => {
    const hub = draft.hubs.find(h => h.key === key);
    if (!hub) return;
    change(d => ({
      ...d,
      hubs: d.hubs.filter(h => h.key !== key),
      instances: d.instances.filter(i => i.hubClusterId !== hub.clusterId),
    }));
    if (buffer?.hubClusterId === hub.clusterId) { setBuffer(null); setBufErr({}); setPending(false); }
    setInstMsg('');
  };
  const addHub = (clusterId: string) => change(d => (d.hubs.some(h => h.clusterId === clusterId) ? d
    : { ...d, hubs: [...d.hubs, { key: `h:${clusterId}`, clusterId, inject: true }] }));

  // ── Kaydet ──────────────────────────────────────────────────────────────
  const vctx = { clusters, bounds: snap.bounds, saved: base.instances };
  const formDirty = bufferDirty(buffer, draft.instances);
  const clientIssues: Issue[] = validated
    ? [...(formDirty ? [pendingIssue(lang)] : []), ...validateDraft(draft, vctx)]
    : [];
  const issues: Issue[] = serverIssue ? [...clientIssues, serverIssue] : clientIssues;

  const save = async () => {
    if (busy) return;
    setOkText(''); setSaveError(''); setServerIssue(null); setNote(null);
    if (buffer && !formDirty) { setBuffer(null); setBufErr({}); setPending(false); }
    const found = validateDraft(draft, vctx);
    if (formDirty || found.length) {
      setValidated(true);
      setShownIssues([...(formDirty ? [pendingIssue(lang)] : []), ...found]);
      if (formDirty) { setPending(true); focus('acd-ed-apply'); }
      announce(t('acd.tab.notSaved', lang));
      return;
    }
    setBusy(true);
    busyRef.current = true;
    try {
      // v0.10.978 — GET'te görülen damga geri gider (kayıtsız blob 0); sunucu tutmazsa 409 stale.
      const r = await api.putArgoCDSettings(toPutBody(draft, snap.pins, snap.settings.updatedAt ?? 0));
      applyResponse(r);
      setBuffer(null); setBufErr({}); setPending(false); setValidated(false); setShownIssues([]); setSavedNow(true); setInstMsg(''); setHubMsg(''); setStale(null);
      const s = r.settings;
      setOkText(tf('acd.tab.ok', lang, { hubs: s.hubs?.length ?? 0, inst: s.instances?.length ?? 0, pins: s.pins?.length ?? 0,
        tail: t(s.enabled && s.metricsWorker?.enabled ? 'acd.tab.okWorkerOn' : 'acd.tab.okWorkerOff', lang) }));
      announce(t('acd.tab.savedLive', lang));
    } catch (e) {
      const p = parseArgoHttpError(e);
      if (p.status === 409 && p.errorType === 'stale') {
        setStale({ updatedAt: p.updatedAt });
        announce(t('acd.tab.staleLive', lang));
        return;
      }
      if (p.status === 400) setServerIssue(issueFromServer(p, draft));
      else setSaveError(p.status ? `HTTP ${p.status}: ${p.error}` : humanize(e));
      announce(t('acd.tab.notSaved', lang));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };
  // v0.10.978 — 409 sonrası "Yeniden yükle": yeniden GET, üç yönlü birleştirme
  // (argocdForm.mergeDraft); yanıt yeni taban, birleşmiş taslak yerinde kalır.
  // Hata: bayat kutusu KALIR (çıkış yolu hâlâ o), hata ayrı kutuda, taslak dokunulmaz.
  const reload = async () => {
    if (busy) return;
    // v0.10.978 — Kaydet'teki kural burada da geçerli: açık formdaki uygulanmamış
    // düzenleme birleştirmeye GİRMEZ (yalnız taslak birleşir) ve sessizce silinirdi;
    // sonuç kutusu bile "düzenleme yoktu" derdi. Önce "Tabloya uygula" ya da "Vazgeç".
    if (formDirty) { setNote({ kind: 'err', text: t('acd.pending', lang) }); blockPending(); return; }
    setBusy(true); setReloading(true);
    busyRef.current = true;
    setNote(null);
    try {
      const r = await api.getArgoCDSettings();
      const fresh = draftFromSettings(r.settings);
      const m = mergeDraft(base, draft, fresh);
      setSnap({ settings: r.settings, tokens: r.tokens ?? {}, bounds: r.bounds ?? {}, pins: r.settings.pins ?? [] });
      setBase(fresh); setDraft(m.draft);
      setBuffer(null); setBufErr({}); setPending(false); setValidated(false); setShownIssues([]);
      setServerIssue(null); setSaveError(''); setOkText(''); setStale(null); setSavedNow(false); setInstMsg(''); setHubMsg('');
      setNote({ kind: m.dropped ? 'err' : 'ok', text: reloadText(m) });
      announce(t('acd.tab.reloaded', lang));
      setFocusAfter('acd-save');
    } catch (e) {
      const p = parseArgoHttpError(e);
      setNote({ kind: 'err', text: tf('acd.tab.reloadFail', lang, { e: p.status ? `HTTP ${p.status}: ${p.error}` : humanize(e) }) });
      announce(t('acd.tab.reloadFailLive', lang));
    } finally {
      busyRef.current = false;
      setBusy(false); setReloading(false);
    }
  };
  const revert = () => {
    setDraft(base);
    setBuffer(null); setBufErr({}); setPending(false); setValidated(false); setShownIssues([]);
    setServerIssue(null); setSaveError(''); setOkText(''); setInstMsg(''); setHubMsg(''); setNote(null);
    announce(t('acd.tab.reverted', lang));
  };
  const goTo = (is: Issue) => {
    const t = is.target;
    switch (t.kind) {
      case 'instance': focus(`acd-inst-btn-${domKey(t.key)}`); break;
      case 'hub': focus(`acd-hub-rm-${domKey(t.key)}`); break;
      case 'hubs': focus('acd-hub-add'); break;
      case 'env': focus('acd-env-in'); break;
      case 'adv': setAdvOpen(true); focus(advInputId(t.key)); break;
      case 'advMode': setAdvOpen(true); focus('acd-adv-mode'); break;
      case 'pins': setPinsOpen(true); focus('acd-pins-btn'); break;
      case 'form': focus('acd-ed-apply'); break;
      default: break;
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const savedEnabled = !!snap.settings.enabled;
  const savedWorker = savedEnabled && !!snap.settings.metricsWorker?.enabled;
  const at = savedNow ? t('acd.tab.savedNow', lang) : savedAtText(snap.settings.updatedAt);
  const emptyMode = base.hubs.length === 0 && base.instances.length === 0 && draft.hubs.length === 0 && draft.instances.length === 0;
  const liveRegion = <div role="status" aria-live="polite" className="sr-only">{live}</div>;
  const banner = (text: string) => (
    <ConfigStatusBanner label={t(savedEnabled ? 'acd.tab.on' : 'acd.tab.off', lang)} aside={at ? <span className="ml-auto text-[length:var(--fs-xs)] text-(--text3)">{at}</span> : undefined}>{text}</ConfigStatusBanner>
  );
  const workerOn = !emptyMode && draft.enabled && draft.metricsWorker;
  const enabledBox = (
    <div className="flex flex-col gap-3">
      <Toggle id="acd-enabled" label={t('acd.tab.enable', lang)} checked={emptyMode ? false : draft.enabled} disabled={emptyMode}
        onChange={v => change(d => ({ ...d, enabled: v, metricsWorker: v ? d.metricsWorker : false, autoRegister: v ? d.autoRegister : false }))}
        hint={emptyMode
          ? t('acd.tab.enableHintEmpty', lang)
          : t('acd.tab.enableHint', lang)} />
      <Toggle id="acd-metrics" label={t('acd.tab.worker', lang)} checked={workerOn} disabled={emptyMode || !draft.enabled}
        onChange={v => change(d => ({ ...d, metricsWorker: v }))}
        hint={!emptyMode && !draft.enabled
          ? t('acd.tab.enableFirst', lang)
          : lang === 'tr'
            ? <>{t('acd.tab.workerHint.a', lang)}<code>intervals.metricsS</code>{t('acd.tab.workerHint.b', lang)}<code>argocd_*</code>{t('acd.tab.workerHint.c', lang)}</>
            : <>{t('acd.tab.workerHint.a', lang)}<code>argocd_*</code>{t('acd.tab.workerHint.b', lang)}<code>intervals.metricsS</code>{t('acd.tab.workerHint.c', lang)}</>} />
      {/* v0.10.1013 (operatör: "autodiscover etse daha iyi olacak, tek tek ekle
          diyorum") — keşfedilen instance'ların kendiliğinden kaydı. Varsayılan
          kapalı; yalnız EKLER. Silinen instance'ın geri geleceği açıkça yazılır. */}
      <Toggle id="acd-autoreg" label={t('acd.tab.autoreg', lang)} checked={!emptyMode && draft.enabled && draft.autoRegister}
        disabled={emptyMode || !draft.enabled} onChange={v => change(d => ({ ...d, autoRegister: v }))}
        hint={!emptyMode && !draft.enabled
          ? t('acd.tab.enableFirst', lang)
          : <>{t('acd.tab.autoregHint.a', lang)}<b>{t('acd.tab.autoregHint.b', lang)}</b>{t('acd.tab.autoregHint.c', lang)}<code>settings.argocd.autoregister</code>{t('acd.tab.autoregHint.d', lang)}</>} />
    </div>
  );

  if (emptyMode) {
    return (
      <div className="flex max-w-[660px] flex-col gap-4">
        {liveRegion}
        <div className="flex flex-col gap-2">
          <h2 className={H2}>Argo CD</h2>
          <p className={DESC}>{t('acd.tab.descEmpty.a', lang)}<code>argocd_*</code>{t('acd.tab.descEmpty.b', lang)}</p>
        </div>
        {banner(t(clusters.length ? 'acd.tab.bannerNoHub' : 'acd.tab.bannerNoRc', lang))}
        {enabledBox}
        <ArgoCDEmptyPanel clusters={clusters} onAddHub={id => {
          addHub(id);
          announce(tf('acd.hubs.added', lang, { name: hubName(id, clusters) }));
          focus('acd-enabled');
        }} />
      </div>
    );
  }

  const phrases = diffPhrases(draft, base, clusters);
  // Hata kutusu Kaydet anının görüntüsü (+ sunucu 400'ü); alan altı hatalar `issues`le canlı.
  const boxIssues: Issue[] = serverIssue ? [...shownIssues, serverIssue] : shownIssues;
  const allPaths = boxIssues.every(i => i.path);
  const header = serverIssue && shownIssues.length === 0
    ? t('acd.tab.hdrServer', lang)
    : tf(allPaths ? 'acd.tab.hdrFields' : 'acd.tab.hdrIssues', lang, { n: boxIssues.length });
  // States (c): "4 instance · 2'si hatalı" — sorunlu satır sayısı (canlı).
  const badRows = new Set(issues.flatMap(i => (i.target.kind === 'instance' ? [i.target.key] : []))).size;
  const instMeta = tf('acd.tab.instMeta', lang, { n: draft.instances.length, h: draft.hubs.length })
    + (badRows ? tf('acd.tab.instBad', lang, { n: badRows, poss: trPossessive3(badRows) }) : '');

  return (
    <div className="flex flex-col gap-6">
      {liveRegion}
      <div className="flex flex-col gap-2">
        <h2 className={H2}>Argo CD</h2>
        <p className={DESC}>
          {t('acd.tab.desc.a', lang)}<code>argocd_*</code>{t('acd.tab.desc.b', lang)}<code>argocd_*</code>{t('acd.tab.desc.c', lang)}
        </p>
      </div>
      {banner(t(savedWorker ? 'acd.tab.bannerWorker' : savedEnabled ? 'acd.tab.bannerStored' : 'acd.tab.bannerOff', lang) + t('acd.tab.bannerApi', lang))}
      <fieldset disabled={busy} aria-busy={busy || undefined} className="m-0 flex min-w-0 flex-col gap-6 border-0 p-0">
        {enabledBox}

        <ArgoCDHubsPanel hubs={draft.hubs} instances={draft.instances} pins={snap.pins} clusters={clusters} issues={issues}
          msg={hubMsg} onMsg={setHubMsg}
          onAdd={addHub}
          onRemove={key => change(d => ({ ...d, hubs: d.hubs.filter(h => h.key !== key) }))}
          onRemoveWithInstances={removeHubWithInstances}
          onInject={(key, v) => change(d => ({ ...d, hubs: d.hubs.map(h => (h.key === key ? { ...h, inject: v } : h)) }))}
          announce={announce} focus={focus} />

        <ArgoCDSectionPanel id="acd-inst-h" title={t('acd.tab.instTitle', lang)} meta={instMeta}
          desc={<>{t('acd.tab.instDesc.a', lang)}<code>argocd_app_status.instance_id</code>{t('acd.tab.instDesc.b', lang)}<code>tokenRef</code>{t('acd.tab.instDesc.c', lang)}</>}>
          <ArgoCDInstancesPanel instances={draft.instances} base={base} hubs={draft.hubs} clusters={clusters} tokens={snap.tokens}
            buffer={buffer} bufErr={bufErr} pending={pending} issues={issues} msg={instMsg}
            onOpen={openEdit} onEdit={editInstance} onToggle={toggleInstance} onRemove={removeInstance} onBufferChange={changeBuffer}
            onApply={applyEdit} onCancel={closeEdit} onRemoveBuffer={removeBuffer} />
          <ArgoCDDiscoveryPanel hubs={draft.hubs} clusters={clusters} instances={draft.instances}
            leading={(
              <Button variant="outline" size="sm" id="acd-inst-add" disabled={draft.hubs.length === 0}
                title={draft.hubs.length === 0 ? t('acd.addHubFirst', lang) : undefined} onClick={newInstance}>
                {t('acd.tab.addInst', lang)}
              </Button>
            )}
            onAdd={addCandidate} onAddAll={addCandidates} onUndo={key => removeInstance(key, { ok: '' })} announce={announce} focus={focus} />
        </ArgoCDSectionPanel>

        <ArgoCDEnvPanel envList={draft.envList} issues={issues} onChange={next => change(d => ({ ...d, envList: next }))} />

        <ArgoCDAdvancedPanel draft={draft} bounds={snap.bounds} pins={snap.pins} issues={issues} open={advOpen} onOpen={setAdvOpen}
          pinsOpen={pinsOpen} onPinsOpen={setPinsOpen}
          onAdv={(k: AdvNumKey, v: string) => change(d => ({ ...d, adv: { ...d.adv, [k]: v } }))}
          onMode={(m: MetricsOnlyMode) => change(d => ({ ...d, metricsOnlyMode: m }))} />
      </fieldset>

      <div className="flex flex-col gap-2 border-t border-(--divider) pt-4">
        {boxIssues.length > 0 && (
          <FlashBox kind="err">
            <div className="font-semibold">{header}</div>
            {boxIssues.map((is, ix) => (
              <div key={ix}>
                {'· '}
                {!is.path ? is.message
                  : is.target.kind === 'other'
                    ? <><span className="font-mono">{is.path}</span> — {is.short}</>
                    : <><Button variant="link" size="xs" className="h-auto p-0 text-[length:inherit] text-inherit underline decoration-dotted" onClick={() => goTo(is)}><span className="font-mono">{is.path}</span></Button> — {is.short}</>}
              </div>
            ))}
          </FlashBox>
        )}
        {saveError && <FlashBox kind="err">{tf('acd.tab.saveErr', lang, { e: saveError })}</FlashBox>}
        {stale && (
          <FlashBox kind="err">
            <div className="font-semibold">
              {tf('acd.tab.stale', lang, { at: savedAtText(stale.updatedAt) || t('acd.tab.staleUnknown', lang) })}
            </div>
            <div>
              {t('acd.tab.staleBody', lang)}
            </div>
            <div className="mt-1.5">
              <Button variant="outline" size="sm" loading={reloading} onClick={() => { void reload(); }}>{t('acd.tab.reload', lang)}</Button>
            </div>
          </FlashBox>
        )}
        {note && <FlashBox kind={note.kind}>{note.text}</FlashBox>}
        {okText && <FlashBox kind="ok">{okText}</FlashBox>}
        <div className="flex flex-wrap items-center gap-4">
          <Button variant="outline" disabled={busy || (phrases.length === 0 && !formDirty)} onClick={revert}>{t('acd.tab.revert', lang)}</Button>
          <Button id="acd-save" loading={busy && !reloading} disabled={reloading} onClick={() => { void save(); }}>{t('acd.tab.save', lang)}</Button>
          <span className="text-[length:var(--fs-sm)] text-(--text2)">{dirtyText(phrases, formDirty)}</span>
        </div>
        <ArgoCDNote>
          {t('acd.tab.note.a', lang)}<code>settings.argocd.update</code>{t('acd.tab.note.b', lang)}
        </ArgoCDNote>
      </div>
    </div>
  );
}
