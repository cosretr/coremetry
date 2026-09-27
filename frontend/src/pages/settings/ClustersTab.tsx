import { useEffect, useId, useMemo, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { GitBranch } from 'lucide-react';
import { fmtDateTime } from '@/lib/utils';
import { SpanClusterValuesPanel } from './SpanClusterValuesPanel';
import { Combobox } from '@/components/Combobox';
import { Spinner } from '@/components/Spinner';
import { Badge, Button, Field, LinkButton, Row, Stack, TextareaField } from '@/components/ui';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, FlashBox, humanize } from './shared';
import { useClusters } from '@/lib/queries';
import type { ArgoCDSettingsResponse, ThanosAuthType, ThanosClusterSnapshot } from '@/lib/types';
import {
  API_URLS_NOTE, API_URLS_PREVIEW_TITLE, FIELD_LABEL, SUFFIX_HINT,
  argoHubInstances, checkArgoFields, clientCheckSummary, splitUrlList,
} from './clusterArgoFields';

// ClustersTab — remote OpenShift clusters whose Thanos Querier
// feeds the /clusters page (v0.8.577, audit: docs/audit/
// thanos-multicluster-metrics-audit.md §2/§7.5). Whole list is
// saved atomically; per-cluster tokens follow the Tempo secret
// contract (never echoed, empty input keeps the stored one).
//
// The cluster NAME is the APM join key: it must equal the
// k8s.cluster.name / openshift.cluster.name value the cluster's
// spans report, or the service→cluster pivot won't light up. The
// name field therefore suggests OBSERVED cluster names (from
// telemetry, last 24h) and warns — without blocking — when the
// typed name isn't among them (Thanos-first onboarding order is
// legitimate).
//
// v0.10.974 — "Argo CD eşlemesi" (onaylı mockup ClusterFields.dc.html,
// operatör "Onay" 2026-09-27): Argo CD ayarındaki hubs[] bu kaydı gösteriyorsa
// nötr "Argo hub" rozeti + instance sayılı not + Ayarlar › Argo CD bağlantısı
// (hub seçimi burada DEĞİŞMEZ; not uyarır, engellemez). API server URL'leri
// için "kaydedilecek biçim" önizlemesi, argoSuffix tekilliği ve pairGroup
// "Bu grupta" ipucu istemcide (saf kural: clusterArgoFields.ts); küme-içi
// adres ya da yinelenen ek Kaydet'te PUT gitmeden yakalanır. Sunucunun ilk
// hatası yedek metin kalır. Mevcut İngilizce etiketler değişmez (§4.9).

interface EditRow {
  id: string;       // server-owned; '' until first save
  name: string;
  url: string;
  thanosLabelName: string;
  thanosLabelValue: string;
  spanClusterValue: string;
  /** v0.10.139 — virgülle ayrılmış çoklu değer; kaydetmede listeye çevrilir. */
  spanClusterValues: string;
  thanosLabelSource?: 'auto' | 'manual';
  thanosLabelDetectedAt?: number;
  labelCheck?: { ok: boolean; series: number; checkedAt: string; error?: string };
  authType: ThanosAuthType;
  token: string;    // only ever holds a NEW token; '' = keep stored
  hasToken: boolean;
  // v0.10.272 — token referansı (Influx/Tempo sözleşmesi): görünür, secret değil.
  tokenRef: string;
  tokenResolved: boolean;
  tokenError: string;
  namespaceFilter: string;
  insecureSkipVerify: boolean;
  enabled: boolean;
  // v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2). Liste metin
  // olarak tutulur (satır/virgül); kaydetmede diziye çevrilir, sunucu normalise eder.
  apiServerUrls: string;
  argoSuffix: string;
  pairGroup: string;
  // v0.10.956 — inceleme (karışık sürüm): liste anahtarı PUT'a yalnız bu true
  // iken gider. Snapshot `apiServerUrls` dizisi taşıdıysa (yeni sunucu), satır
  // formda yeni eklendiyse ya da operatör listeyi düzenlediyse true. GET'i
  // ESKİ pod yanıtladıysa (rolling upgrade) form saklı değeri hiç görmedi;
  // `[]` göndermek yeni pod'da gövdeyi yetkili yapıp üç alanı silerdi.
  apiServerUrlsKnown: boolean;
  // v0.10.974 — GET'te gelen değerler: tekillik hatası DÜZENLENEN satıra
  // bağlanır (mockup), bunun için "düzenlendi mi" karşılaştırması.
  savedApiServerUrls: string;
  savedArgoSuffix: string;
}

// v0.10.956 — apiServerUrls girdisi: virgül ya da satır sonu ayırır; kırpılır,
// boşlar atılır (splitUrlList, v0.10.974'den beri clusterArgoFields.ts'te).
// Kaydedilen değeri SUNUCU üretir (tek kural); v0.10.974 önizlemesi onun
// istemci kopyasıyla yalnız gösterir.

function fromSnapshot(c: ThanosClusterSnapshot): EditRow {
  return {
    id: c.id || '', name: c.name, url: c.url,
    thanosLabelName: c.thanosLabelName || '',
    thanosLabelValue: c.thanosLabelValue || '',
    spanClusterValue: c.spanClusterValue || '',
    spanClusterValues: (c.spanClusterValues && c.spanClusterValues.length ? c.spanClusterValues : [c.spanClusterValue || '']).filter(Boolean).join(', '),
    thanosLabelSource: c.thanosLabelSource, thanosLabelDetectedAt: c.thanosLabelDetectedAt, labelCheck: c.labelCheck,
    authType: (c.authType || 'none') as ThanosAuthType,
    token: '', hasToken: c.hasToken,
    tokenRef: c.tokenRef || '', tokenResolved: !!c.tokenResolved, tokenError: c.tokenError || '',
    namespaceFilter: c.namespaceFilter || '',
    insecureSkipVerify: !!c.insecureSkipVerify,
    enabled: c.enabled,
    // v0.10.956 — eski sunucu alanları göndermez → boş.
    apiServerUrls: (c.apiServerUrls ?? []).join('\n'),
    argoSuffix: c.argoSuffix || '',
    pairGroup: c.pairGroup || '',
    // Yeni sunucu diziyi HEP basar (boşsa []); yokluğu = eski pod yanıtladı.
    apiServerUrlsKnown: Array.isArray(c.apiServerUrls),
    savedApiServerUrls: (c.apiServerUrls ?? []).join('\n'), // v0.10.974
    savedArgoSuffix: c.argoSuffix || '',
  };
}

const EMPTY_ROW: EditRow = {
  id: '', name: '', url: '', authType: 'bearer', token: '', hasToken: false,
  tokenRef: '', tokenResolved: false, tokenError: '',
  thanosLabelName: '', thanosLabelValue: '', spanClusterValue: '', spanClusterValues: '',
  namespaceFilter: '', insecureSkipVerify: false, enabled: true,
  apiServerUrls: '', argoSuffix: '', pairGroup: '', apiServerUrlsKnown: true, // v0.10.956
  savedApiServerUrls: '', savedArgoSuffix: '', // v0.10.974 — yeni satır: her değer "düzenlenmiş"
};

// v0.10.974 — Combobox atomu aria-describedby geçirmiyor (atom bu değişikliğin
// dosyası değil): hub notu ad girdisine efektle bağlanır. React bu özniteliği
// yönetmediği için yeniden çizim onu silmez; not kalkınca öznitelik de kalkar.
function DescribedBy({ id, children }: { id?: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const input = ref.current?.querySelector('input');
    if (!input) return;
    if (id) input.setAttribute('aria-describedby', id);
    else input.removeAttribute('aria-describedby');
  }, [id]);
  return <div ref={ref}>{children}</div>;
}

// v0.10.974 — Kaydet öncesi denetimin özet maddesi: alan kimliği (bağlantı
// odağı oraya taşır), "<ad> · <alan>" ve metin.
interface CheckItem { id: string; label: string; text: string }

// v0.10.974 — "Argo CD eşlemesi" alt başlığı mockup ClusterFields.dc.html'deki
// gibi GERÇEK bir h3 (ekran okuyucu başlık gezinmesi) ve cümle düzeninde: 12px
// / 600 / --text, yanında 11px --text3 açıklama, taban çizgisinde hizalı.
// SectionHead'in büyük harfli .dtl-sech dili detay sayfası ayırıcısıdır.
const ARGO_HEAD_ROW: CSSProperties = { alignItems: 'baseline' };
const ARGO_H3: CSSProperties = { margin: 0, fontSize: 'var(--fs-sm)', fontWeight: 600, color: 'var(--text)' };
const ARGO_H3_META: CSSProperties = { fontSize: 'var(--fs-xs)', color: 'var(--text3)' };

export function ClustersTab() {
  const [rows, setRows] = useState<EditRow[]>([]);
  const [busy, setBusy] = useState(false);
  // v0.10.974 — `items`: istemci denetiminin bağlantılı maddeleri (yalnız err).
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string; items?: CheckItem[] } | null>(null);
  const baseId = useId();
  // v0.10.974 — Argo CD ayarı: rozet + not için EN-İYİ-ÇABA okuma. Formun
  // yükleme kapısına (useSettingsLoad) karışmaz; okunamazsa (503, yetki, eski
  // pod) rozet ve not yok, hata metni de yok — Remote Cluster formu bundan
  // bağımsız çalışır.
  const [argo, setArgo] = useState<ArgoCDSettingsResponse | null>(null);
  useEffect(() => {
    let alive = true;
    api.getArgoCDSettings()
      .then(r => { if (alive) setArgo(r); })
      .catch(() => { if (alive) setArgo(null); });
    return () => { alive = false; };
  }, []);
  const hubInstances = useMemo(() => argoHubInstances(argo), [argo]);
  // v0.10.128 — per-row "Test label" result (probe endpoint, on click only).
  const [probe, setProbe] = useState<Record<string, string>>({});
  // v0.10.140 — "Algıla": sunucu enjeksiyonsuz sorguyla etiketi bulur ve
  // (belirsiz değilse) kaydeder; belirsizlikte adaylar satırın altında.
  const [detect, setDetect] = useState<Record<string, { text: string; candidates?: Record<string, string[]> }>>({});
  const runDetect = async (r: EditRow) => {
    const ref = r.id || r.name.trim();
    if (!ref) return;
    setDetect(p => ({ ...p, [ref]: { text: '…' } }));
    try {
      const res = await api.thanosClusterDetect(ref, true);
      const d = res.detection;
      if (res.error) setDetect(p => ({ ...p, [ref]: { text: `✗ ${res.error}`, candidates: d?.candidates } }));
      else if (res.applied) {
        setDetect(p => ({ ...p, [ref]: { text: d?.label ? `✓ ${d.label}="${d.value}" (auto, saved)` : '✓ aday etiket yok — matcher gerekmiyor (saved)' } }));
        const next = await api.getThanosSettings();
        setRows((next.clusters ?? []).map(fromSnapshot));
      } else setDetect(p => ({ ...p, [ref]: { text: d?.ambiguous ? `? ${d.label}: birden çok değer — birini seçin` : 'algılanamadı', candidates: d?.candidates } }));
    } catch (err) {
      setDetect(p => ({ ...p, [ref]: { text: `✗ ${err instanceof Error ? err.message : 'detect failed'}` } }));
    }
  };
  const runProbe = async (r: EditRow) => {
    const ref = r.id || r.name.trim();
    if (!ref) return;
    setProbe(p => ({ ...p, [ref]: '…' }));
    try {
      const res = await api.thanosClusterProbe(ref);
      setProbe(p => ({ ...p, [ref]: res.ok
        ? `✓ ${res.series} node series${res.label ? ` with ${res.label}="${res.value}"` : ''}`
        : `✗ ${res.error || 'no series'}` }));
    } catch (err) {
      setProbe(p => ({ ...p, [ref]: `✗ ${err instanceof Error ? err.message : 'probe failed'}` }));
    }
  };

  // Observed cluster names from telemetry (last 24h) — the
  // suggestion source for the join-key warning. timeRange math
  // stays inside useMemo (v0.5.184 rule).
  const [fromNs, toNs] = useMemo(() => {
    const now = Date.now() * 1e6;
    return [now - 24 * 3600 * 1e9, now];
  }, []);
  const observedQ = useClusters(fromNs, toNs);
  const observed = useMemo(() => new Set(observedQ.data ?? []), [observedQ.data]);
  // Same list, array-shaped + sorted — the Combobox suggestion source.
  // Sorting stays here (not in render) so the array identity is stable
  // across re-renders and the dropdown's filter memo doesn't rebuild.
  const observedNames = useMemo(() => [...observed].sort(), [observed]);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getThanosSettings(),
    s => {
      setRows((s.clusters ?? []).map(fromSnapshot));
    },
  );

  const patch = (i: number, p: Partial<EditRow>) =>
    setRows(rs => rs.map((r, j) => (j === i ? { ...r, ...p } : r)));

  // v0.10.974 — önizleme + tekillik + "Bu grupta" (saf; clusterArgoFields.ts).
  const checks = useMemo(() => checkArgoFields(rows), [rows]);
  const fieldId = (field: 'api' | 'suffix', i: number) => `${baseId}-${field}-${i}`;
  const focusField = (id: string) => document.getElementById(id)?.focus();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    // The name field carried `required` while it was a native <input>.
    // The Combobox atom renders its own input and takes no `required`
    // prop, so the invariant moves to the submit path — where it always
    // belonged for a repeated-row form: the browser bubble points at a
    // row that may be scrolled out of view and says only "fill this in",
    // never WHY an empty join key is fatal.
    const nameless = rows.findIndex(r => !r.name.trim());
    if (nameless >= 0) {
      setMsg({ kind: 'err', text: `Cluster #${nameless + 1} has no name — the name is the join key and cannot be empty.` });
      return;
    }
    // v0.10.974 — istemci denetimi: sunucunun reddedeceği küme-içi adres,
    // bozuk/yinelenen adres ya da ek varsa istek GİTMEZ; tüm sorunlar
    // bağlantılı maddelerle listelenir (sunucu yalnız ilkini, alan yolu
    // olmadan döndürür).
    const items = checks.flatMap((c, i) => c.issues.map(it => ({
      id: fieldId(it.field, i),
      label: `${rows[i].name.trim() || `#${i + 1}`} · ${FIELD_LABEL[it.field]}`,
      text: it.text,
    })));
    if (items.length > 0) {
      setMsg({ kind: 'err', text: clientCheckSummary(items.length), items });
      return;
    }
    setBusy(true); setMsg(null);
    try {
      const next = await api.putThanosSettings({
        clusters: rows.map(r => ({
          id: r.id || undefined, // server-owned: lets a rename keep its id + token
          name: r.name.trim(), url: r.url.trim(), authType: r.authType,
          token: r.token, // '' keeps stored (server contract, id/name-matched)
          tokenRef: r.tokenRef.trim() || undefined,
          thanosLabelName: r.thanosLabelName.trim() || undefined,
          thanosLabelValue: r.thanosLabelValue.trim() || undefined,
          spanClusterValue: r.spanClusterValue.trim() || undefined,
          spanClusterValues: r.spanClusterValues.split(',').map(v => v.trim()).filter(Boolean),
          namespaceFilter: r.namespaceFilter.trim() || undefined,
          insecureSkipVerify: r.insecureSkipVerify, enabled: r.enabled,
          // v0.10.956 — apiServerUrls dizi (boşsa []): sunucu anahtarın
          // varlığını "alanları bilen istemci" sayar; boş liste/metin = temizle.
          // Anahtarı göndermeyen eski bundle'da sunucu saklı değerleri korur.
          // inceleme: satırı eski pod yüklediyse ve liste elle değişmediyse
          // anahtar GİTMEZ (undefined) — sunucu saklı listeyi ve boş metinlerin
          // saklı değerini taşır, dolu metni uygular.
          apiServerUrls: r.apiServerUrlsKnown ? splitUrlList(r.apiServerUrls) : undefined,
          argoSuffix: r.argoSuffix.trim(),
          pairGroup: r.pairGroup.trim(),
        })),
      });
      setRows((next.clusters ?? []).map(fromSnapshot));
      const on = (next.clusters ?? []).filter(c => c.enabled).length;
      setMsg({ kind: 'ok', text: `Saved — ${on} cluster(s) enabled.` });
    } catch (err) {
      // v0.10.974 — sunucunun ilk hatası yedek metin: "HTTP 400:" öneki ve
      // {error} zarfı ayıklanır (humanize).
      setMsg({ kind: 'err', text: humanize(err) || 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  return (
    <div style={{ maxWidth: 760 }}>
      <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 6 }}>Remote clusters (Thanos)</h2>
      <p style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 16 }}>
        Each entry points at an OpenShift cluster's Thanos Querier route.
        The <strong>/clusters</strong> page pulls per-pod CPU + memory from
        every enabled entry. Service pages pivot into a cluster when its <strong>name</strong> matches the
        <code style={{ background: 'var(--bg0)', padding: '1px 5px', borderRadius: 3, margin: '0 4px' }}>k8s.cluster.name</code>
        value the telemetry reports — <em>or</em> when the span values are mapped to it (Detect label / assign values below, v0.10.139-141).
        Typical auth: a ServiceAccount token bound to the
        <code style={{ background: 'var(--bg0)', padding: '1px 5px', borderRadius: 3, margin: '0 4px' }}>cluster-monitoring-view</code>
        ClusterRole. Read-only — Coremetry never writes to Thanos.
      </p>

      <form onSubmit={save}>
        {rows.length === 0 && (
          <div style={{ padding: 14, fontSize: 12, color: 'var(--text3)',
            border: '1px dashed var(--border)', borderRadius: 8, marginBottom: 12 }}>
            No clusters yet — add the first one below.
          </div>
        )}
        {rows.map((r, i) => {
          // v0.10.187 (F12) — ad telemetride yoksa bile EŞLENMİŞ span değerlerinden biri görülüyorsa «not in telemetry» yanlış olurdu (139-141 otomatik eşleme).
          const mapped = r.spanClusterValues.split(',').map(v => v.trim()).filter(Boolean);
          const nameKnown = r.name.trim() === '' || observed.size === 0 || observed.has(r.name.trim()) || mapped.some(v => observed.has(v));
          // v0.10.974 — Argo hub: kayıt Argo CD ayarının hubs[] listesinde mi
          // (yalnız kayıtlı id'ler); not ad, Etkin ve Remove'dan aria ile bağlı.
          const hubCount = r.id ? hubInstances.get(r.id) : undefined;
          const hubNoteId = hubCount !== undefined ? `${baseId}-hub-${i}` : undefined;
          const chk = checks[i];
          const apiId = fieldId('api', i);
          const apiDesc = chk.preview.length > 0 ? `${apiId}-n ${apiId}-p` : `${apiId}-n`;
          return (
            <div key={i} role="group" aria-label={`${r.name.trim() || `#${i + 1}`} kaydı`} style={{
              marginBottom: 12, padding: 14, borderRadius: 8,
              background: 'var(--bg2)', border: '1px solid var(--border)',
              opacity: r.enabled ? 1 : 0.65,
            }}>
              <div style={{ display: 'flex', gap: 10, marginBottom: hubNoteId ? 6 : 10, alignItems: 'flex-end' }}>
                <label style={{ flex: 1 }}>
                  <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>
                    Cluster name (join key)
                    {r.id && (
                      <span className="badge mono" style={{ marginLeft: 8 }}
                        title="Opaque, immutable cluster id — the root of the entity hierarchy. Renaming the cluster keeps it.">
                        {r.id}
                      </span>
                    )}
                    {hubNoteId && (
                      <>{' '}<Badge tone="neutral"><GitBranch size={11} aria-hidden="true" />Argo hub</Badge></>
                    )}
                    {!nameKnown && (
                      <span className="badge b-warn" style={{ marginLeft: 8 }}
                        title="Name not seen in the last 24h of telemetry — Thanos data will not match service pages. The warning clears once the cluster starts reporting.">
                        not in telemetry
                      </span>
                    )}
                  </div>
                  <DescribedBy id={hubNoteId}>
                    <Combobox value={r.name} onChange={v => patch(i, { name: v })}
                      options={observedNames} placeholder="prod-ist" width="100%" />
                  </DescribedBy>
                </label>
                <label style={{ display: 'flex', alignItems: 'center', gap: 6, paddingBottom: 6 }}>
                  <input type="checkbox" checked={r.enabled} aria-describedby={hubNoteId}
                    onChange={e => patch(i, { enabled: e.target.checked })} />
                  <span style={{ fontSize: 12 }}>Enabled</span>
                </label>
                <Button type="button" variant="ghost" size="sm" aria-describedby={hubNoteId}
                  onClick={() => setRows(rs => rs.filter((_, j) => j !== i))}>
                  Remove
                </Button>
              </div>
              {hubNoteId && (
                <div id={hubNoteId} className="field-hint">
                  Argo CD hub'ı ({hubCount} instance): kaldırır ya da kapatırsanız Argo CD ayarı bir sonraki kayıtta reddedilir. Hub seçimi ve küme etiketi kararı <Link to="/settings/argocd">Ayarlar › Argo CD</Link> sekmesinde; burada değiştirilemez.
                </div>
              )}
              <label style={{ display: 'block', marginBottom: 10, marginTop: hubNoteId ? 10 : undefined }}>
                <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>Thanos Querier URL</div>
                <input value={r.url} required={r.enabled}
                  onChange={e => patch(i, { url: e.target.value })}
                  placeholder="https://thanos-querier-openshift-monitoring.apps.prod-ist.example.com"
                  style={{ width: '100%' }} />
              </label>
              <div style={{ display: 'flex', gap: 10, marginBottom: 10 }}>
                <label style={{ width: 180 }}>
                  <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>Auth</div>
                  <select value={r.authType}
                    onChange={e => patch(i, { authType: e.target.value as ThanosAuthType })}
                    style={{ width: '100%' }}>
                    <option value="bearer">Bearer token</option>
                    <option value="none">None (in-mesh / mTLS)</option>
                  </select>
                </label>
                {r.authType === 'bearer' && (
                  <label style={{ flex: 1 }}>
                    <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>
                      Token
                      {r.hasToken && <span style={{ color: 'var(--text3)', marginLeft: 8 }}>· stored</span>}
                    </div>
                    <input type="password" value={r.token}
                      onChange={e => patch(i, { token: e.target.value })}
                      placeholder={r.hasToken ? '(leave empty to keep stored value)' : 'paste ServiceAccount token…'}
                      style={{ width: '100%' }} />
                  </label>
                )}
                {r.authType === 'bearer' && (
                  /* v0.10.272 — Influx/Tempo deseni: referans doluysa saklı token yerine kullanılır. */
                  <div style={{ flex: 1 }}>
                    <Field label="…ya da token referansı" value={r.tokenRef}
                      onChange={e => patch(i, { tokenRef: e.target.value })}
                      placeholder="env:COREMETRY_THANOS_TOKEN_PROD  ·  file:/var/run/secrets/thanos/token"
                      autoComplete="off"
                      error={r.tokenError || undefined}
                      hint={r.tokenResolved ? 'Çözüldü ✓' : 'env: Helm extraEnv + existingSecret; file: mount edilmiş Secret (≤30 s)'} />
                  </div>
                )}
              </div>
              {/* v0.10.128 — identity mapping (entity layer). Label name empty = no
                  matcher = one Thanos URL per cluster (the classic model). */}
              <div style={{ display: 'flex', gap: 10, marginBottom: 10 }}>
                <label style={{ width: 200 }}>
                  <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>Thanos label name</div>
                  <input value={r.thanosLabelName}
                    onChange={e => patch(i, { thanosLabelName: e.target.value })}
                    placeholder='cluster  ·  empty = per-cluster URL'
                    style={{ width: '100%' }} />
                </label>
                <label style={{ flex: 1 }}>
                  <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>Thanos label value</div>
                  <input value={r.thanosLabelValue}
                    onChange={e => patch(i, { thanosLabelValue: e.target.value })}
                    placeholder={r.name.trim() ? `empty = ${r.name.trim()}` : 'empty = cluster name'}
                    disabled={!r.thanosLabelName.trim()}
                    style={{ width: '100%' }} />
                </label>
                <label style={{ flex: 1 }}>
                  <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>
                    Span cluster values
                    {r.thanosLabelSource && (
                      <span className={`badge ${r.thanosLabelSource === 'auto' ? 'b-info' : 'b-gray'}`} style={{ marginLeft: 6 }}
                        title={r.thanosLabelSource === 'auto' && r.thanosLabelDetectedAt ? `Thanos etiketi otomatik algılandı · ${fmtDateTime(new Date(r.thanosLabelDetectedAt))}` : 'Thanos etiketi elle girildi'}>
                        label: {r.thanosLabelSource}
                      </span>
                    )}
                  </div>
                  {/* v0.10.139 — bir kayıt birden çok span değeri taşır (virgülle); bir
                      değer aynı anda TEK kayda — çakışmayı sunucu reddeder ve bağlı kaydı söyler. */}
                  <input value={r.spanClusterValues}
                    onChange={e => patch(i, { spanClusterValues: e.target.value, spanClusterValue: e.target.value.split(',')[0]?.trim() ?? '' })}
                    placeholder={r.name.trim() ? `empty = ${r.name.trim()} · comma-separated` : 'empty = cluster name · comma-separated'}
                    style={{ width: '100%' }} />
                </label>
                <div style={{ display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', gap: 4, minWidth: 140 }}>
                  <Button type="button" variant="ghost" size="sm" disabled={!r.id}
                    title={r.id ? 'Query count(kube_node_info) with this cluster\'s label matcher (saved settings)' : 'Save first'}
                    onClick={() => runProbe(r)}>
                    Test label
                  </Button>
                  <Button type="button" variant="secondary" size="sm" disabled={!r.id}
                    title={r.id ? 'Thanos external label\'ını enjeksiyonsuz sorguyla algıla ve kaydet (auto)' : 'Save first'}
                    onClick={() => runDetect(r)}>
                    Detect label
                  </Button>
                  {r.labelCheck && !r.labelCheck.ok && (
                <div style={{ fontSize: 12, color: 'var(--err)', marginTop: 4 }} title={r.labelCheck.checkedAt}>
                  ⚠ periyodik doğrulama: {r.labelCheck.error || 'etiket eşleşmiyor'} · {fmtDateTime(new Date(r.labelCheck.checkedAt))}
                </div>
              )}
              {detect[r.id || r.name.trim()] && (
                <div style={{ fontSize: 12, color: 'var(--text2)', marginTop: 4 }}>
                  {detect[r.id || r.name.trim()].text}
                  {detect[r.id || r.name.trim()].candidates && Object.entries(detect[r.id || r.name.trim()].candidates!).map(([label, vals]) => (
                    <span key={label} style={{ marginLeft: 8 }}>
                      {label}: {vals.slice(0, 8).map(v => (
                        <Button key={v} type="button" variant="ghost" size="xs" title={`Bu değeri seç: ${label}="${v}" (kaydetmeyi unutma)`}
                          onClick={() => patch(i, { thanosLabelName: label, thanosLabelValue: v })}>{v}</Button>
                      ))}
                    </span>
                  ))}
                </div>
              )}
              {probe[r.id || r.name.trim()] && (
                    <span style={{ fontSize: 11, color: 'var(--text2)' }}>{probe[r.id || r.name.trim()]}</span>
                  )}
                </div>
              </div>
              <Stack gap={3}>
                <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end' }}>
                  <label style={{ flex: 1 }}>
                    <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 4 }}>
                      Namespace filter (PromQL regex — cardinality shield)
                    </div>
                    <input value={r.namespaceFilter}
                      onChange={e => patch(i, { namespaceFilter: e.target.value })}
                      placeholder='^(app-|payments-)  ·  empty = all namespaces (top 500 pods)'
                      style={{ width: '100%' }} />
                  </label>
                  <label style={{ display: 'flex', alignItems: 'center', gap: 6, paddingBottom: 6, whiteSpace: 'nowrap' }}>
                    <input type="checkbox" checked={r.insecureSkipVerify}
                      onChange={e => patch(i, { insecureSkipVerify: e.target.checked })} />
                    <span style={{ fontSize: 12 }}>Skip TLS verify</span>
                  </label>
                </div>
                {/* v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2, karar 7):
                    Argo CD eşlemesinin cluster tarafı. P1'de okuyan yok; doğrulama +
                    normalise + tekillik sunucuda (thanos.ReconcileClusterSettings).
                    v0.10.974 — alt başlık (h3) + önizleme + istemci denetimi (mockup
                    ClusterFields.dc.html). */}
                <div lang="tr" className="row gap-2 row-wrap" style={ARGO_HEAD_ROW}>
                  <h3 style={ARGO_H3}>Argo CD eşlemesi</h3>
                  <span style={ARGO_H3_META}>Argo'nun <code>dest_server</code> ve uygulama adı eki bu kümeye nasıl çözülür</span>
                </div>
                <div>
                  <TextareaField id={apiId} label="API server URLs (Argo CD dest_server)" rows={2}
                    value={r.apiServerUrls}
                    onChange={e => patch(i, { apiServerUrls: e.target.value, apiServerUrlsKnown: true })}
                    placeholder="https://api.cluster-a.example.invalid:6443"
                    autoComplete="off" spellCheck={false}
                    aria-invalid={chk.apiInvalid ? 'true' : undefined}
                    aria-describedby={apiDesc}
                    style={chk.apiInvalid ? { borderColor: 'var(--err)' } : undefined} />
                  <div id={`${apiId}-n`} className="field-hint">{API_URLS_NOTE}</div>
                  {chk.preview.length > 0 && (
                    <div id={`${apiId}-p`} className="field-hint">
                      <div>{API_URLS_PREVIEW_TITLE}</div>
                      {chk.preview.map((l, k) => (
                        <Row key={k} gap={2}>
                          <span className={l.bad ? 'mono is-err' : 'mono'}>{l.text}</span>
                          <span className={l.bad ? 'is-err' : undefined}>{l.note}</span>
                        </Row>
                      ))}
                    </div>
                  )}
                </div>
                <Row gap={3} wrap>
                  <div className="row-grow">
                    <Field id={fieldId('suffix', i)} label="Argo app suffix" value={r.argoSuffix}
                      onChange={e => patch(i, { argoSuffix: e.target.value })}
                      placeholder="e.g. ca" autoComplete="off" spellCheck={false}
                      error={chk.suffixError} hint={SUFFIX_HINT} />
                  </div>
                  <div className="row-grow">
                    <Field label="Pair group" value={r.pairGroup}
                      onChange={e => patch(i, { pairGroup: e.target.value })}
                      placeholder="e.g. prod-pair-1" autoComplete="off"
                      hint={chk.pairHint} />
                  </div>
                </Row>
              </Stack>
            </div>
          );
        })}

        {/* v0.10.974 — FlashBox: hata role=alert, başarı role=status. İstemci
            denetiminin maddeleri alana götüren bağlantılar (odak eylemle taşınır). */}
        <Stack gap={3}>
          {msg && (
            <FlashBox kind={msg.kind}>
              {msg.items ? <strong>{msg.text}</strong> : msg.text}
              {msg.items?.map((it, k) => (
                <div key={k}>
                  · <LinkButton onClick={() => focusField(it.id)}>{it.label}</LinkButton>: {it.text}
                </div>
              ))}
            </FlashBox>
          )}
          <Row gap={2}>
            <Button type="button" variant="secondary"
              onClick={() => setRows(rs => [...rs, { ...EMPTY_ROW }])}>
              + Add cluster
            </Button>
            <Button type="submit" variant="primary" loading={busy}>
              Save all
            </Button>
          </Row>
        </Stack>
      </form>
      {/* v0.10.141 — span cluster değerleri: eşleşmemişleri bir kayda ata (teklik sunucuda). */}
      <SpanClusterValuesPanel clusters={rows.map(r => ({ id: r.id || undefined, name: r.name }))}
        onAssigned={(clusterId, values) => setRows(rs => rs.map(r => r.id === clusterId
          ? { ...r, spanClusterValues: values.join(', '), spanClusterValue: values[0] ?? '' }
          : r))} />
    </div>
  );
}
