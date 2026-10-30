import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner } from './shared';
import { vmFloorToForm, vmFloorToWire, vmSelfModeToForm, vmSelfStatusLine } from './vmForm';
import type { VMAuthType, VMLabelMap, VMSelfMetricsMode, VMSettingsInput, VMSnapshot, VMTestResult } from '@/lib/types';

// v0.10.948 (CoSRE Faz B) — etiket eşlemesi formu. Rol sırası ve adlar Go'nun
// vmetrics.LabelRoles'ü; ad dilbilgisi vmetrics.ValidLabelName ile aynı (noktalı
// `k8s.pod.name` VM'de `k8s_pod_name` olarak yaşar). Form yalnız UYARIR —
// kararın tek yazımı sunucuda (LabelMapProblem → 400, metin aynen gösterilir).
const LABEL_ROLES: { key: keyof VMLabelMap; label: string; ph: string }[] = [
  { key: 'service', label: 'Servis', ph: 'boş = service_name konvansiyonu' },
  // v0.10.1377 — HTTP metrik süzgeci (metrik kipi + küme kıyası) boşken bu etiketi kullanır.
  { key: 'env', label: 'Ortam', ph: 'boş = deployment_environment_name' },
  { key: 'cluster', label: 'Cluster', ph: 'boş = keşif' },
  { key: 'namespace', label: 'Namespace', ph: 'boş = keşif' },
  { key: 'pod', label: 'Pod', ph: 'boş = keşif' },
  { key: 'version', label: 'Sürüm', ph: 'boş = keşif' },
];
// Faz 2 dilim 1 R8 — inline stil yerine Tailwind utility (token değişkenleri).
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-4 text-[length:var(--fs-md)] text-(--text2)';
const CARD = 'mt-[18px] flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4';
const SECTION = 'flex flex-col gap-3 border-t border-(--border) pt-3';
const ASIDE = 'ml-1.5 text-[length:var(--fs-xs)] italic text-(--text3)';
const LABEL_NAME = /^[a-zA-Z_][a-zA-Z0-9_]*$/;
// labelForm — snapshot eşlemesi → form (her rol dolu dize; eksik anahtar = '').
function labelForm(m: VMLabelMap | undefined): Required<VMLabelMap> {
  const out = { service: '', env: '', cluster: '', namespace: '', pod: '', version: '' };
  for (const r of LABEL_ROLES) out[r.key] = (m?.[r.key] ?? '').trim();
  return out;
}
function labelNameError(v: string): string | undefined {
  const t = v.trim();
  if (!t || LABEL_NAME.test(t)) return undefined;
  return `geçersiz etiket adı — [a-zA-Z_][a-zA-Z0-9_]* olmalı (ör. ${t.replace(/[^a-zA-Z0-9_]/g, '_')})`;
}

// MetricsBackendTab — external VictoriaMetrics READ backend (v0.9.1150,
// Faz 1). TempoTab is the template; the Test button follows DevOpsTab.
//
// The copy is deliberate about SCOPE, because "metrics come from
// VictoriaMetrics now" is not what this switch does. It repoints the
// surfaces an operator drives by hand (catalogue + picker, Explore,
// dashboard metric panels, MCP query_metric, filter suggestions). Every
// span-derived surface and the fixed-name infra panels stay on
// ClickHouse. An operator who reads "metrics backend" as "all metrics"
// will file a bug the first time a JVM panel disagrees, so the form says
// it up front.
//
// Admin-only (the whole Settings area is), and the saved token reads the
// operator's entire VM.
export function MetricsBackendTab() {
  const [enabled, setEnabled] = useState(false);
  const [baseUrl, setBaseUrl] = useState('');
  const [authType, setAuthType] = useState<VMAuthType>('none');
  const [token, setToken] = useState('');
  const [hasToken, setHasToken] = useState(false);
  // v0.10.273 — token referansı (Influx/Tempo/Thanos sözleşmesi): görünür, secret değil.
  const [tokenRef, setTokenRef] = useState('');
  const [tokenResolved, setTokenResolved] = useState(false);
  const [tokenError, setTokenError] = useState('');
  const [insecureSkipVerify, setInsecureSkipVerify] = useState(false);
  // DİZİ state, sayı değil — '' tek dürüst "üstüne yazma yok" gösterimi.
  // Çeviri vmForm.ts'te ve tablo-testli; gerekçesi orada.
  const [rateWindowFloor, setRateWindowFloor] = useState('');
  const [allowUnfilteredPercentiles, setAllowUnfilteredPercentiles] = useState(false);
  // v0.10.292 — çift yazım (VM tek metrik deposu Dilim 1a): OTLP metrik
  // gövdesi VM'e de yazılır; boş URL = baseUrl.
  const [writeUrl, setWriteUrl] = useState('');
  const [writeEnabled, setWriteEnabled] = useState(false);
  // v0.10.948 — rol → VM etiket adı (boş = konvansiyon/keşif). Saklı değer
  // okunur ve HER kayıtta tam nesne gider: sunucunun işaretçi sözleşmesinde
  // alan yoksa saklı korunur, boş alan temizler (vmetrics_handlers.go).
  const [labelMap, setLabelMap] = useState<Required<VMLabelMap>>(() => labelForm(undefined));
  // v0.10.1422 — Coremetry'nin kendi metrikleri → bu VM (auto | on | off) +
  // cevabı veren pod'un durum satırı (sunucudan; form yalnız gösterir).
  const [selfMetrics, setSelfMetrics] = useState<VMSelfMetricsMode>('auto');
  const [selfSnap, setSelfSnap] = useState<Pick<VMSnapshot, 'selfMetrics' | 'selfMetricsActive' | 'selfMetricsReason' | 'selfMetricsStatus'>>({});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [test, setTest] = useState<VMTestResult | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getVMSettings(),
    s => {
      setEnabled(s.enabled);
      setBaseUrl(s.baseUrl || '');
      setAuthType((s.authType || 'none') as VMAuthType);
      setHasToken(s.hasToken);
      setTokenRef(s.tokenRef ?? '');
      setTokenResolved(!!s.tokenResolved);
      setTokenError(s.tokenError ?? '');
      setInsecureSkipVerify(!!s.insecureSkipVerify);
      setRateWindowFloor(vmFloorToForm(s.rateWindowFloorS));
      setAllowUnfilteredPercentiles(!!s.allowUnfilteredPercentiles);
      setWriteUrl(s.writeUrl ?? '');
      setWriteEnabled(!!s.writeEnabled);
      setLabelMap(labelForm(s.labelMap));
      setSelfMetrics(vmSelfModeToForm(s.selfMetrics));
      setSelfSnap(s);
    },
  );

  const buildInput = (): VMSettingsInput => ({
    enabled, baseUrl, authType,
    token, // empty preserved on the server side
    tokenRef: tokenRef.trim() || undefined,
    insecureSkipVerify,
    // Bu ikisinde "boş = sakla" kuralı YOK: 0 ve false burada anlamlı
    // değerler ("varsayılan taban", "koruma açık"), token'ın tersine.
    rateWindowFloorS: vmFloorToWire(rateWindowFloor),
    allowUnfilteredPercentiles,
    writeUrl: writeUrl.trim() || undefined,
    writeEnabled,
    labelMap: labelForm(labelMap), // kırpılmış, altı rolün tamamı
    selfMetrics,
  });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null); setTest(null);
    try {
      const next = await api.putVMSettings(buildInput());
      setHasToken(next.hasToken);
      setTokenResolved(!!next.tokenResolved);
      setTokenError(next.tokenError ?? '');
      setToken('');
      // Sunucu NE SAKLADIĞININ tek yetkilisi: dönen snapshot'tan yeniden
      // tohumla. Aralık dışı bir değer 400 atar (buraya hiç gelmez) ve kutu
      // operatörün yazdığını hata mesajıyla birlikte tutar — doğru olan bu.
      setRateWindowFloor(vmFloorToForm(next.rateWindowFloorS));
      setAllowUnfilteredPercentiles(!!next.allowUnfilteredPercentiles);
      // v0.10.948 — eşleme de sunucunun kırpıp sakladığıyla tohumlanır.
      if (next.labelMap) setLabelMap(labelForm(next.labelMap));
      setSelfMetrics(vmSelfModeToForm(next.selfMetrics));
      setSelfSnap(next);
      setMsg({
        kind: 'ok',
        text: next.enabled
          ? 'Kaydedildi — metrik keşif ve sorgu yüzeyleri artık VictoriaMetrics’ten okuyor.'
          : 'Kaydedildi — metrik okumaları ClickHouse’ta.',
      });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Kaydetme başarısız' });
    } finally {
      setBusy(false);
    }
  };

  // Test probes the values IN THE FORM without saving — the operator
  // pastes a URL, verifies it, and only then ticks Enabled. The server
  // treats the submitted form as enabled for the probe's duration.
  const runTest = async () => {
    setBusy(true); setMsg(null); setTest(null);
    try {
      setTest(await api.testVMSettings(buildInput()));
    } catch (err) {
      setTest({ ok: false, error: err instanceof Error ? err.message : 'Test başarısız' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const ready = enabled && baseUrl.trim().length > 0;
  const selfLine = vmSelfStatusLine(selfSnap);

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>Metrik okuma backend’i</h2>
      <p className={LEAD}>
        Zaten VictoriaMetrics çalıştıran kurulumlar için: aynı serilerin bir
        kopyasını Coremetry’nin ClickHouse’unda tutmak yerine metrikleri
        doğrudan VM’den okuyun (Prometheus uyumlu HTTP API).
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={ready ? 'VICTORIAMETRICS' : 'CLICKHOUSE'}>
        {ready
          ? `Metrik okumaları ${baseUrl} adresinden.`
          : 'Kapalı — metrik okumaları Coremetry’nin ClickHouse’undan.'}
      </ConfigStatusBanner>

      {/* KAPSAM. Bu satır olmadan "metrik backend'i" ifadesi "TÜM
          metrikler" diye okunur ve ilk JVM paneli uyuşmadığında bug
          olarak açılır. */}
      <div className="mt-3 rounded-md border border-(--border) bg-(--bg2) px-3 py-2.5 text-[length:var(--fs-sm)] leading-relaxed text-(--text2)">
        <b className="text-(--text)">Neyi kapsar:</b> metrik kataloğu ve
        adı seçiciler, Explore, dashboard <code>metric</code> panelleri, filtre
        anahtarı/değeri önerileri, MCP <code>query_metric</code> ve{' '}
        <code>list_metric_names</code>. Histogram ısı haritaları,
        yüzdelikler (p50/p95/p99) ve PromQL vekili de dahil (Faz 2).
        <br />
        <b className="text-(--text)">Neyi kapsamaz (ClickHouse’ta kalır):</b>{' '}
        span türevli her şey (servisler, operasyonlar, topoloji, trace’ler,
        exception’lar) ve sabit adlı iç okuyucular — hosts, altyapı, JVM
        panelleri, veritabanı kapasitesi.
        <br />
        <b className="text-(--text)">Yedeğe düşmez:</b> VM açık ve
        erişilemez durumdaysa metrik uçları <b>502</b> ile VM’nin kendi
        hatasını döner. Sessizce ClickHouse’a düşmek, sormadığınız bir
        kaynaktan gelen sayıları göstermek olurdu.
      </div>

      {/* Faz 2 dilim 1 R8: alanlar coremetry/Field ailesi (D1), kart aynı (D3). */}
      <form onSubmit={save} className={CARD}>
        <CheckboxField label="VictoriaMetrics’ten oku" checked={enabled} onCheckedChange={setEnabled} />

        <Field label="Base URL" value={baseUrl}
          onChange={e => setBaseUrl(e.target.value)}
          placeholder="http://victoria-metrics:8428  ·  cluster: http://vmselect:8481/select/0/prometheus"
          hint={<>
            Sondaki eğik çizgi opsiyonel. Çağrılan uçlar:{' '}
            <code>/api/v1/query_range</code>, <code>/api/v1/labels</code>,{' '}
            <code>/api/v1/label/&#123;name&#125;/values</code>. Tek düğüm için
            vmsingle adresi, cluster için <b>vmselect</b>’in{' '}
            <code>/select/&lt;accountID&gt;/prometheus</code> ön eki.
          </>} />

        <SelectField label="Kimlik doğrulama" value={authType}
          onChange={e => setAuthType(e.target.value as VMAuthType)}>
          <option value="none">Yok (VM’in kendisinde kimlik doğrulama yoktur)</option>
          <option value="bearer">Bearer token (vmauth / JWT’li ingress)</option>
        </SelectField>

        {authType === 'bearer' && (
          <Field label="Bearer token" type="password" value={token}
            labelAside={hasToken ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· saklı</span> : undefined}
            onChange={e => setToken(e.target.value)}
            placeholder={hasToken ? '(saklı değeri korumak için boş bırakın)' : 'token’ı yapıştırın…'} />
        )}

        {authType === 'bearer' && (
          /* v0.10.273 — Influx/Tempo/Thanos deseni: referans doluysa saklı token yerine kullanılır. */
          <Field label="…ya da token referansı" value={tokenRef}
            onChange={e => setTokenRef(e.target.value)}
            placeholder="env:COREMETRY_VM_TOKEN  ·  file:/var/run/secrets/vm/token"
            autoComplete="off"
            error={tokenError || undefined}
            hint={tokenResolved
              ? 'Çözüldü ✓ — saklı token yerine bu kullanılıyor'
              : 'Doluysa saklı token yerine bu kullanılır. env: Helm extraEnv + existingSecret (pod restart); file: mount edilmiş Secret. Test de bu referansı çözer.'} />
        )}

        {/* v0.10.292 — çift yazım (Aşama 1): OTLP metrik gövdesi VM'e de yazılır.
            Okuma backend'inden BAĞIMSIZ: yazımı açıp okumayı ClickHouse'ta
            bırakmak kıyas dönemidir (/api/metrics/compare, Dilim 1c). */}
        <Field label="Yazma URL'i (vminsert / vmagent; boşsa temel URL)" value={writeUrl}
          onChange={e => setWriteUrl(e.target.value)}
          placeholder="http://vminsert:8480/insert/0"
          autoComplete="off"
          hint="POST <url>/opentelemetry/v1/metrics — protobuf + gzip. Test, yazım açıkken bu ucu da yoklar." />
        <CheckboxField checked={writeEnabled} onCheckedChange={setWriteEnabled}
          label={<>
            OTLP metriklerini VictoriaMetrics'e de yaz (çift yazım)
            <span className={ASIDE}>(ClickHouse yazımı sürer; kaynak seçimi yukarıdaki anahtarla)</span>
          </>} />

        {/* v0.10.1422 — Coremetry'nin KENDİ metrikleri (api / ingest / worker /
            sre pod'ları) bu VM'e: yazım hedefi yukarıdaki Yazma URL'i (boşsa
            Base URL). Gelen OTLP iletiminden (çift yazım kutusu) BAĞIMSIZ. */}
        <SelectField label="Coremetry’nin kendi metrikleri" value={selfMetrics}
          onChange={e => setSelfMetrics(e.target.value as VMSelfMetricsMode)}
          hint={<>
            <span className={cn('block', selfLine.kind === 'ok' ? 'text-(--ok)' : selfLine.kind === 'err' ? 'text-(--err)' : 'text-(--text3)')}>
              {selfLine.text}
            </span>
            <span className="mt-1 block">
              HTTP RED (<code>http_server_request_duration_seconds</code>, api + ingest OTLP/HTTP),
              OTLP/gRPC (<code>rpc_server_duration_milliseconds</code>), worker işleri
              (<code>worker_job_duration_seconds</code>), SRE araştırmaları ve Go runtime metrikleri;
              etiket <code>service_name=coremetry-&lt;rol&gt;</code>. 30 sn’de bir, istek yolunun dışında.
              Cluster VM’de Yazma URL’ine vminsert’i (<code>…/insert/&lt;tenant&gt;</code>) girin.
            </span>
          </>}>
          <option value="auto">Otomatik — VM’den okuma açıkken bu VM’e yaz</option>
          <option value="on">Açık — okuma kapalı olsa da yaz</option>
          <option value="off">Kapalı</option>
        </SelectField>

        <CheckboxField checked={insecureSkipVerify} onCheckedChange={setInsecureSkipVerify}
          label={<>TLS doğrulamayı atla<span className={ASIDE}>(yalnız kendinden imzalı sertifika / POC)</span></>} />

        {/* SORGU AYARLARI — bağlantı değil, gönderilen ifadenin ŞEKLİ.
            Ayrı bir bölüm, çünkü ikisi de "VM'e nasıl bağlanılır"
            sorusuna değil "VM'e ne sorulur" sorusuna cevap veriyor ve
            biri (koruma) kapatıldığında etkisi tüm kurulumda görülür. */}
        <div className={SECTION}>
          <div className="mb-1 text-[length:var(--fs-sm)] font-semibold">Sorgu ayarları</div>

          <Field label="Rate penceresi tabanı (s)" value={rateWindowFloor}
            onChange={e => setRateWindowFloor(e.target.value)}
            inputMode="numeric"
            placeholder="300 (varsayılan)"
            hint={<>
              <code>rate</code> ve <code>last</code> çevirilerinin en dar
              lookbehind penceresi. VM, yazılmamış bir pencereyi{' '}
              <code>max(step, scrape_interval)</code>’a genişletir; Coremetry
              pencereyi hep açıkça yazdığı için o genişletme bize kalıyor ve
              scrape aralığı VM’den okunamıyor — bu taban onun yerine geçer.
              Export aralığı bilinen bir kurulumda 300s gereğinden geniş
              olabilir (her rate grafiğinden beş dakikalık detayı düzler).
              Boş bırakın = varsayılan; aksi hâlde <b>10–3600</b> sn.
              <br />
              <b className="text-(--text2)">Etkilemediği yerler:</b>{' '}
              <code>increase</code> ve histogram ısı haritası. Onların
              penceresi bir TOPLAM: genişletmek grafik hâlâ “bir kova” derken
              sayıyı katlar.
            </>} />

          <CheckboxField checked={allowUnfilteredPercentiles} onCheckedChange={setAllowUnfilteredPercentiles}
            label="Filtresiz yüzdeliklere izin ver"
            hint={<>
              Varsayılan olarak <b>kapalı</b>: yüzdelik (p50/p95/p99),
              histogram ısı haritası ve histogram ailesindeki{' '}
              <code>avg</code> sorguları <b>ne servis ne de etiket filtresi</b>{' '}
              taşıdığında <b>400</b> ile reddedilir — böyle bir sorgu kova
              serilerinin tamamını taratır ve milyonlarca serili bir VM’de
              paylaşılan <code>vmselect</code>’i herkes için yavaşlatır.
              Reddedilen sorgu ekranda ne yapılacağını söyler. Açmak eski
              davranışa döner; <code>rate</code>/<code>increase</code>{' '}
              zaten korumaya girmez (kolları <code>_count</code> okur, kova
              değil).
            </>} />
        </div>

        {/* v0.10.948 — BAĞLAM ETİKETLERİ: CoSRE'nin metrik araçları (query_metric,
            list_metric_labels) servis/ortam/cluster/namespace/pod/sürüm süzgecini
            hangi VM etiketine uygulayacak. Sıra: burası → konvansiyon → canlı keşif;
            araç sonucu hangisini kullandığını ve eşlenemeyen süzgeci "kısmi"
            diye raporlar. Trace kimliği bir metrik etiketi DEĞİLDİR. */}
        <div className={SECTION}>
          <div className="text-[length:var(--fs-sm)] font-semibold">Bağlam etiketleri (CoSRE)</div>
          <div className="mb-1 text-[length:var(--fs-xs)] leading-normal text-(--text3)">
            CoSRE bir trace’i metriklerle eşlerken servis, ortam, cluster,
            namespace, pod ve sürüm süzgecini bu etiketlere uygular. Boş alan:
            servis için <code>service_name</code> konvansiyonu, diğerleri için
            metriğin gerçek etiketlerinden keşif. Eşlenemeyen süzgeç sessizce
            düşmez, sonuçta “kısmi” olarak raporlanır.
          </div>
          <div className="grid-2 gap-3">
            {LABEL_ROLES.map(r => (
              <Field key={r.key} label={r.label} value={labelMap[r.key]}
                onChange={e => { const v = e.target.value; setLabelMap(m => ({ ...m, [r.key]: v })); }}
                placeholder={r.ph} autoComplete="off" spellCheck={false}
                error={labelNameError(labelMap[r.key])} />
            ))}
          </div>
        </div>

        {test && (
          <div className={cn('text-[length:var(--fs-sm)]', test.ok ? 'text-(--ok)' : 'text-(--err)')}>
            {test.ok
              ? <>✓ Bağlantı kuruldu — VM Prometheus API’siyle cevap veriyor.</>
              : <>✗ {test.error || 'Bağlantı kurulamadı'}</>}
          </div>
        )}

        {msg && (
          <div className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        {/* D4: eylem satırı sağa yaslı, birincil en sağda (eskiden Kaydet solda). */}
        <div>
          <ActionRow
            secondary={<Button variant="secondary" disabled={busy || !baseUrl.trim()}
              onClick={() => void runTest()}>
              Bağlantıyı test et
            </Button>}
            confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
          <div className="mt-2 text-[length:var(--fs-xs)] leading-normal text-(--text3)">
            Test kaydetmez — formdaki değerlerle <code>up</code> sorgusunu dener
            (5 sn). Token alanı boşsa saklı token kullanılır. Boş sonuç da
            BAŞARIDIR: VM erişilebilir ve API’yi konuşuyor demektir.
          </div>
        </div>
      </form>
    </div>
  );
}
