import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button, LinkButton } from '@/components/ui';
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

const EMPTY_ROOT: CSSProperties = { maxWidth: 660 };
const H2: CSSProperties = { margin: 0, fontSize: 'var(--fs-lg)', fontWeight: 600 };
const DESC: CSSProperties = { margin: 0, maxWidth: 760, fontSize: 'var(--fs-md)', lineHeight: '20px', color: 'var(--text2)' };
const ASIDE: CSSProperties = { marginLeft: 'auto', fontSize: 'var(--fs-xs)', color: 'var(--text3)' };
const CHECK: CSSProperties = { display: 'flex', alignItems: 'flex-start', gap: 'var(--sp-4)' };
const SAVEBAR: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--sp-4)', paddingTop: 'var(--sp-7)', borderTop: '1px solid var(--divider)' };
const BOLD: CSSProperties = { fontWeight: 600 };
const DIRTY: CSSProperties = { fontSize: 'var(--fs-sm)', color: 'var(--text2)' };
// v0.10.974 — Kaydet kilidinin fieldset'i görünmez: varsayılan kenarlık/dolgu
// yok; min-width 0 (fieldset'in min-content varsayılanı tabloları taşırırdı).
const LOCK: CSSProperties = { minWidth: 0, margin: 0, padding: 0, border: 0 };
// v0.10.978 — bayat kutusunun eylem satırı (kutu metninden ayrık).
const STALE_ACT: CSSProperties = { marginTop: 'var(--sp-3)' };

const EMPTY_SETTINGS: ArgoCDSettings = { enabled: false, apiWorker: {}, classification: {}, reader: {}, intervals: {}, mapping: {} };
const PENDING_TEXT = 'Açık düzenleme formunda uygulanmamış değişiklik var: önce “Tabloya uygula” ya da “Vazgeç”.';
const PENDING_ISSUE: Issue = { path: '', message: PENDING_TEXT, short: PENDING_TEXT, target: { kind: 'form' } };

interface Snap {
  settings: ArgoCDSettings;
  tokens: Record<string, ArgoCDTokenStatus>;
  bounds: Record<string, ArgoCDBound>;
  pins: ArgoCDPin[];
}

export function ArgoCDTab() {
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
    announce('Açık formda tabloya uygulanmamış değişiklik var: önce “Tabloya uygula” ya da “Vazgeç”.');
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
    announce('Yeni instance satırı açıldı.');
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
    if (n) { setBufErr(err); announce(`${n} alan düzeltilmeli.`); return; }
    const row = applyBuffer(buffer);
    const isNew = buffer.isNew;
    change(d => ({ ...d, instances: isNew ? [...d.instances, row] : d.instances.map(i => (i.key === row.key ? row : i)) }));
    setBuffer(null); setBufErr({}); setPending(false);
    announce(`${row.id} tabloya uygulandı — kaydedilmedi.`);
    focus(`acd-inst-btn-${domKey(row.key)}`);
  };
  const removeInstance = (key: string, f: { ok: string; blocked?: string }) => {
    const row = draft.instances.find(i => i.key === key);
    if (!row) return;
    const n = snap.pins.filter(p => p.instanceId === row.id).length;
    if (n) {
      setInstMsg(`${row.id} kaldırılamaz: bu instance'a ${n} pin bağlı; önce API'den pin'leri kaldırın (PUT /api/settings/argocd, pins[]).`);
      announce(`${row.id} kaldırılamadı: ${n} pin bağlı.`);
      if (f.blocked) focus(f.blocked);
      return;
    }
    change(d => ({ ...d, instances: d.instances.filter(i => i.key !== key) }));
    if (buffer?.key === key) { setBuffer(null); setBufErr({}); setPending(false); }
    setInstMsg('');
    announce(`${row.id} tablodan kaldırıldı — kaydedilmedi.`);
    focus(f.ok);
  };
  const removeBuffer = () => {
    if (!buffer) return;
    if (buffer.isNew) {
      setBuffer(null); setBufErr({}); setPending(false);
      announce('Yeni instance tablodan kaldırıldı — kaydedilmedi.');
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
    announce(`${row.id} ${row.enabled ? 'devre dışı bırakıldı' : 'etkinleştirildi'} — kaydedilmedi.`);
    focus(`acd-inst-menu-${domKey(key)}`);
  };
  const addCandidate = (hubClusterId: string, c: ArgoCDCandidate, id: string) => {
    const row: InstanceDraft = {
      key: newKey('n:'), origin: 'new', savedId: '', id, hubClusterId, name: '', hubNamespace: c.hubNamespace,
      metricsJob: c.metricsJob, apiUrl: '', storedRef: '', tokenInput: '', clearToken: false, insecureSkipVerify: false,
      enabled: true, discovered: true, appsAnyNamespace: c.appsAnyNamespace || c.namespaceCase === 'C',
    };
    change(d => ({ ...d, instances: [...d.instances, row] }));
    announce(`${id} tabloya eklendi — kaydedilmedi. API URL, tokenRef ya da kimlik için satırı açın.`);
  };

  // ── Hub'lar ─────────────────────────────────────────────────────────────
  const addHub = (clusterId: string) => change(d => (d.hubs.some(h => h.clusterId === clusterId) ? d
    : { ...d, hubs: [...d.hubs, { key: `h:${clusterId}`, clusterId, inject: true }] }));

  // ── Kaydet ──────────────────────────────────────────────────────────────
  const vctx = { clusters, bounds: snap.bounds, saved: base.instances };
  const formDirty = bufferDirty(buffer, draft.instances);
  const clientIssues: Issue[] = validated
    ? [...(formDirty ? [PENDING_ISSUE] : []), ...validateDraft(draft, vctx)]
    : [];
  const issues: Issue[] = serverIssue ? [...clientIssues, serverIssue] : clientIssues;

  const save = async () => {
    if (busy) return;
    setOkText(''); setSaveError(''); setServerIssue(null); setNote(null);
    if (buffer && !formDirty) { setBuffer(null); setBufErr({}); setPending(false); }
    const found = validateDraft(draft, vctx);
    if (formDirty || found.length) {
      setValidated(true);
      setShownIssues([...(formDirty ? [PENDING_ISSUE] : []), ...found]);
      if (formDirty) { setPending(true); focus('acd-ed-apply'); }
      announce('Kaydedilmedi.');
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
      setOkText(`Kaydedildi — ${s.hubs?.length ?? 0} hub, ${s.instances?.length ?? 0} instance; ${s.pins?.length ?? 0} pin olduğu gibi geri gönderildi. Denetim kaydı yazıldı (settings.argocd.update); ${s.enabled && s.metricsWorker?.enabled ? 'metrik işçisi açık — worker lideri en geç 30 sn içinde okumaya başlar.' : 'metrik işçisi kapalı — hub sorgusu atılmaz.'}`);
      announce('Kaydedildi.');
    } catch (e) {
      const p = parseArgoHttpError(e);
      if (p.status === 409 && p.errorType === 'stale') {
        setStale({ updatedAt: p.updatedAt });
        announce('Kaydedilmedi — ayarlar başka biri tarafından değiştirildi.');
        return;
      }
      if (p.status === 400) setServerIssue(issueFromServer(p, draft));
      else setSaveError(p.status ? `HTTP ${p.status}: ${p.error}` : humanize(e));
      announce('Kaydedilmedi.');
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
    if (formDirty) { setNote({ kind: 'err', text: PENDING_TEXT }); blockPending(); return; }
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
      announce('Yeniden yüklendi.');
      setFocusAfter('acd-save');
    } catch (e) {
      const p = parseArgoHttpError(e);
      setNote({ kind: 'err', text: `Yeniden yüklenemedi — ${p.status ? `HTTP ${p.status}: ${p.error}` : humanize(e)}` });
      announce('Yeniden yüklenemedi.');
    } finally {
      busyRef.current = false;
      setBusy(false); setReloading(false);
    }
  };
  const revert = () => {
    setDraft(base);
    setBuffer(null); setBufErr({}); setPending(false); setValidated(false); setShownIssues([]);
    setServerIssue(null); setSaveError(''); setOkText(''); setInstMsg(''); setHubMsg(''); setNote(null);
    announce('Değişiklikler geri alındı.');
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
  const at = savedNow ? 'son kayıt şimdi' : savedAtText(snap.settings.updatedAt);
  const emptyMode = base.hubs.length === 0 && base.instances.length === 0 && draft.hubs.length === 0 && draft.instances.length === 0;
  const liveRegion = <div role="status" aria-live="polite" className="sr-only">{live}</div>;
  const banner = (text: string) => (
    <ConfigStatusBanner label={savedEnabled ? 'Açık' : 'Kapalı'} aside={at ? <span style={ASIDE}>{at}</span> : undefined}>{text}</ConfigStatusBanner>
  );
  const workerOn = !emptyMode && draft.enabled && draft.metricsWorker;
  const enabledBox = (
    <div className="stack gap-3">
      <div style={CHECK}>
        <input id="acd-enabled" type="checkbox" checked={emptyMode ? false : draft.enabled} disabled={emptyMode}
          aria-describedby="acd-enabled-h"
          onChange={e => { const v = e.target.checked; change(d => ({ ...d, enabled: v, metricsWorker: v ? d.metricsWorker : false })); }} />
        <div>
          <label htmlFor="acd-enabled">Argo CD entegrasyonu açık</label>
          <div id="acd-enabled-h" className="field-hint">
            {emptyMode
              ? 'Açmak için önce bir hub ekleyin: açıkken en az bir etkin hub zorunlu.'
              : 'Açıkken en az bir etkin hub zorunlu. Tek başına hiçbir işçi başlatmaz: metrik işçisi aşağıdaki ayrı anahtarla açılır, Argo CD API bağlantısı askıda.'}
          </div>
        </div>
      </div>
      <div style={CHECK}>
        <input id="acd-metrics" type="checkbox" checked={workerOn} disabled={emptyMode || !draft.enabled}
          aria-describedby="acd-metrics-h" onChange={e => { const v = e.target.checked; change(d => ({ ...d, metricsWorker: v })); }} />
        <div>
          <label htmlFor="acd-metrics">Metrik işçisini çalıştır (argocd-metrics)</label>
          <div id="acd-metrics-h" className="field-hint">
            {!emptyMode && !draft.enabled
              ? 'Önce Argo CD entegrasyonunu açın.'
              : <>Açıkken worker lideri her tik aralığında (Gelişmiş › <code>intervals.metricsS</code>, varsayılan 60 sn) hub Thanos'undaki <code>argocd_*</code> metriklerini okur ve Application durum değişimleriyle tamamlanan senkronları kaydeder. Argo CD API'sine bağlanılmaz; kapalıyken hiçbir hub sorgusu atılmaz.</>}
          </div>
        </div>
      </div>
    </div>
  );

  if (emptyMode) {
    return (
      <div className="stack gap-4" style={EMPTY_ROOT}>
        {liveRegion}
        <div className="stack gap-2">
          <h2 style={H2}>Argo CD</h2>
          <p style={DESC}>Hub kümelerindeki Argo CD instance'larını tanımlar. Metrik işçisi açıkken Application durumu hub Thanos'undaki <code>argocd_*</code> metriklerinden izlenir; Argo CD API'sine bağlanılmaz.</p>
        </div>
        {banner(clusters.length ? 'Ayarlanmadı — hub ve instance yok' : 'Ayarlanmadı — Remote Cluster kaydı yok')}
        {enabledBox}
        <ArgoCDEmptyPanel clusters={clusters} onAddHub={id => {
          addHub(id);
          announce(`${hubName(id, clusters)} hub olarak eklendi — kaydedilmedi.`);
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
    ? 'Kaydedilmedi — sunucu reddetti. Hiçbir değişiklik yazılmadı; kayıtlı ayar olduğu gibi duruyor.'
    : `Kaydedilmedi — ${boxIssues.length} ${allPaths ? 'alan' : 'sorun'} düzeltilmeli. Hiçbir değişiklik yazılmadı; kayıtlı ayar olduğu gibi duruyor.`;
  // States (c): "4 instance · 2'si hatalı" — sorunlu satır sayısı (canlı).
  const badRows = new Set(issues.flatMap(i => (i.target.kind === 'instance' ? [i.target.key] : []))).size;
  const instMeta = `${draft.instances.length} instance · ${draft.hubs.length} hub${badRows ? ` · ${badRows}${trPossessive3(badRows)} hatalı` : ''}`;

  return (
    <div className="stack gap-6">
      {liveRegion}
      <div className="stack gap-2">
        <h2 style={H2}>Argo CD</h2>
        <p style={DESC}>
          Hub kümelerindeki Argo CD instance'larını tanımlar. Metrik işçisi açıkken Application durum değişimleri ve tamamlanan senkronlar
          hub'ın <code>argocd_*</code> metriklerinden kaydedilir; rollout'larla eşleme sonraki adımda. Hub, o kümenin <code>argocd_*</code>
          serilerini tutan Thanos'u gösteren sıradan bir Remote Cluster kaydıdır. Salt okuma: Coremetry Argo CD'ye yazmaz, refresh tetiklemez;
          Argo CD API bağlantısı şimdilik askıda.
        </p>
      </div>
      {banner(`${savedWorker ? 'Metrik işçisi açık — hub metrikleri okunuyor' : savedEnabled ? 'Ayarlar saklanıyor ve doğrulanıyor — metrik işçisi kapalı' : 'Kapalı — ayarlar saklanıyor, metrik işçisi çalışmıyor'} · Argo CD API bağlantısı askıda`)}
      <fieldset disabled={busy} aria-busy={busy || undefined} className="stack gap-6" style={LOCK}>
        {enabledBox}

        <ArgoCDHubsPanel hubs={draft.hubs} instances={draft.instances} clusters={clusters} enabled={draft.enabled} issues={issues}
          msg={hubMsg} onMsg={setHubMsg}
          onAdd={addHub}
          onRemove={key => change(d => ({ ...d, hubs: d.hubs.filter(h => h.key !== key) }))}
          onInject={(key, v) => change(d => ({ ...d, hubs: d.hubs.map(h => (h.key === key ? { ...h, inject: v } : h)) }))}
          announce={announce} focus={focus} />

        <ArgoCDSectionPanel id="acd-inst-h" title="Instance'lar" meta={instMeta}
          desc={<>Her instance, bir hub üzerindeki bir Argo CD namespace'idir. Kimlik (id) hub'lar arasında da tekil olmalı (ClickHouse <code>argocd_app_status.instance_id</code>). Argo CD API erişimi yalnız token referansıyla (<code>tokenRef</code>) verilir; referansı olmayan instance yalnız metriklerle izlenir.</>}>
          <ArgoCDInstancesPanel instances={draft.instances} base={base} hubs={draft.hubs} clusters={clusters} tokens={snap.tokens}
            pins={snap.pins} buffer={buffer} bufErr={bufErr} pending={pending} issues={issues} msg={instMsg}
            onOpen={openEdit} onEdit={editInstance} onToggle={toggleInstance} onRemove={removeInstance} onBufferChange={changeBuffer}
            onApply={applyEdit} onCancel={closeEdit} onRemoveBuffer={removeBuffer} />
          <ArgoCDDiscoveryPanel hubs={draft.hubs} clusters={clusters} instances={draft.instances}
            leading={(
              <Button variant="secondary" size="sm" id="acd-inst-add" disabled={draft.hubs.length === 0}
                title={draft.hubs.length === 0 ? 'Önce bir hub ekleyin' : undefined} onClick={newInstance}>
                + Instance ekle
              </Button>
            )}
            onAdd={addCandidate} onUndo={key => removeInstance(key, { ok: '' })} announce={announce} focus={focus} />
        </ArgoCDSectionPanel>

        <ArgoCDEnvPanel envList={draft.envList} issues={issues} onChange={next => change(d => ({ ...d, envList: next }))} />

        <ArgoCDAdvancedPanel draft={draft} bounds={snap.bounds} pins={snap.pins} issues={issues} open={advOpen} onOpen={setAdvOpen}
          pinsOpen={pinsOpen} onPinsOpen={setPinsOpen}
          onAdv={(k: AdvNumKey, v: string) => change(d => ({ ...d, adv: { ...d.adv, [k]: v } }))}
          onMode={(m: MetricsOnlyMode) => change(d => ({ ...d, metricsOnlyMode: m }))} />
      </fieldset>

      <div style={SAVEBAR}>
        {boxIssues.length > 0 && (
          <FlashBox kind="err">
            <div style={BOLD}>{header}</div>
            {boxIssues.map((is, ix) => (
              <div key={ix}>
                {'· '}
                {!is.path ? is.message
                  : is.target.kind === 'other'
                    ? <><span className="mono">{is.path}</span> — {is.short}</>
                    : <><LinkButton tone="muted" underline="dotted" onClick={() => goTo(is)}><span className="mono">{is.path}</span></LinkButton> — {is.short}</>}
              </div>
            ))}
          </FlashBox>
        )}
        {saveError && <FlashBox kind="err">Kaydedilmedi — {saveError}. Hiçbir değişiklik yazılmadı; kayıtlı ayar olduğu gibi duruyor.</FlashBox>}
        {stale && (
          <FlashBox kind="err">
            <div style={BOLD}>
              Kaydedilmedi — ayarlar başka biri tarafından değiştirildi ({savedAtText(stale.updatedAt) || 'kayıt zamanı bilinmiyor'}) — yeniden yükle.
            </div>
            <div>
              Sunucuda değişmemiş alan ya da satırlardaki düzenlemeleriniz korunur; sunucuda da değişen alanlar ve satırlar bütünüyle sunucunun değeriyle gelir.
              Hiçbir değişiklik yazılmadı; kayıtlı ayar olduğu gibi duruyor.
            </div>
            <div style={STALE_ACT}>
              <Button variant="secondary" size="sm" loading={reloading} onClick={() => { void reload(); }}>Yeniden yükle</Button>
            </div>
          </FlashBox>
        )}
        {note && <FlashBox kind={note.kind}>{note.text}</FlashBox>}
        {okText && <FlashBox kind="ok">{okText}</FlashBox>}
        <div className="row gap-4 row-wrap">
          <Button variant="secondary" disabled={busy || (phrases.length === 0 && !formDirty)} onClick={revert}>Değişiklikleri geri al</Button>
          <Button variant="primary" id="acd-save" loading={busy && !reloading} disabled={reloading} onClick={() => { void save(); }}>Kaydet</Button>
          <span style={DIRTY}>{dirtyText(phrases, formDirty)}</span>
        </div>
        <ArgoCDNote>
          Kaydet önce doğrular, sonra <code>settings.argocd.update</code> denetim kaydını yazar; tüm pod'lar 30 sn içinde yeni ayarı alır.
        </ArgoCDNote>
      </div>
    </div>
  );
}
