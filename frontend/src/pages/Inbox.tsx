import { useEffect, useMemo, useRef, useState } from 'react';
import { stopRowClick } from '@/lib/a11y'; // v0.10.455 (dış denetim D3 dilim 3); v0.10.1414 satır linki
import { PriorityBadge } from '@/components/ui/PriorityBadge';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from '@/components/coremetry/overlay';
import { Button, Chip, SearchField } from '@/components/ui';
import { Link, useLocation, useNavigate, useSearchParams, Navigate } from 'react-router-dom';
import { anomalyEventHref } from '@/lib/anomalyHref';
import { Topbar } from '@/components/Topbar';
import { useInbox, useServicesMetadata, useProblemGraphSummary } from '@/lib/queries';
import { tsCompact, tsLong, fmtAgoNs, rowClickHandlers } from '@/lib/utils';
// v0.10.1084 — Exceptions listesiyle ortak başlık hücresi + satır metni (saf).
import { TriageTitleCell } from '@/features/anomalies/TriageTitleCell';
import { inboxRowHeadline, inboxRowLabel, inboxKindLabel, inboxCountSentence } from '@/lib/inboxRowText';
import { teamOptionsCI } from '@/lib/teamOptions';
import { parseSubjectLane } from '@/lib/problemSubject';
import { decodeCsvSet, encodeCsvSet, readInboxTeam, INBOX_TEAM_PARAM, INBOX_CAT_PARAM, INBOX_CAT_ALL, INBOX_CAT_LABEL, inboxMoreFiltersActive, inboxStatusHref } from '@/lib/inboxUrl';
import { MoreFilters } from '@/components/coremetry/MoreFilters';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface'; // v0.10.2143 — alan zemini (--field-bg)
import { cn } from 'cn';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, resolveInitialSort, type DataTableStateProps } from '@/components/ui/DataTable';
import { FacetMultiSelect } from '@/components/ui/FacetMultiSelect';
import { SavedViewsBar } from '@/components/SavedViewsBar';
// v0.10.1081 — triyaj çekmecesi (InboxTriageDrawer) KALDIRILDI: her satır tam
// sayfa detayına açılır; eski ?item= linkleri oraya yönlendirilir.
import {
  isInboxExcFamily, inboxRowOpen, inboxRowLink, inboxItemIdTarget, incidentBackState, readInboxDetail, withInboxDetail,
  type InboxDetailParam,
} from '@/lib/inboxHref';
// v0.9.837 (operator-reported) — "Alert rules" bölümü Exceptions
// sayfasından buraya taşındı: alert kuralları triage kuyruğunun
// parçası, exception kuyruğunun değil. Modül yolundan import (barrel
// değil): barrel AnomaliesPage'i de /inbox chunk'ına sürüklerdi.
import { AlertProblemHost } from '@/features/anomalies/ProblemsSection';
import { AssigneeCell } from '@/features/anomalies/AssigneeCell'; // v0.10.2125 (S5) — Take it İlgilenen hücresinde
import { useAuth } from '@/components/AuthProvider';
import type { InboxItem, InboxKind } from '@/lib/types';
import { stripMarkdown } from '@/components/Markdown';
import { serviceHref, inboxItemWindow } from '@/lib/serviceHref';
import { SubjectLink } from '../components/SubjectLink';
import { spreadOf, spreadTitle, defaultFloorTitle, spreadOffFloorTitle } from '@/features/anomalies/spread'; // v0.10.949
import { isOracleGroup, isSyntheticOracleService, oracleInboxDetail } from '@/features/anomalies/oracleGroup'; // v0.10.1109
import { useBranding } from '@/lib/branding'; // v0.10.1109 — Oracle grubu görünen adı
import type { SubjectLane } from '@/lib/types';
import { ProblemVerdictPolicyBar } from '@/components/ProblemVerdictPolicyBar';
import { ProblemVerdictRow } from '@/components/ProblemVerdictActions';
import { useVerdictRowContext } from '@/components/useVerdictRowContext'; // v0.10.1529 — satır Karar hücresi (D1); v0.10.1626 tek dizin
import { NoiseReasonButton, type NoiseReasonChoice } from '@/components/NoiseReasonButton'; // v0.10.1531 — toplu triyaj
import {
  splitStdHeadline, stdSourceChip, stdSourceName, stdSamplingNote, stdSinceLine, stdDurationLine,
  stdHeadlineView, stdEvidenceView, prioReasonView, stdRequestImpact,
} from '@/lib/problemStandardView'; // v0.10.1529 — Problem standardı R2; v0.10.2299 EN anahtardan
import { OrigLang } from '@/features/anomalies/OrigLang'; // v0.10.2299 — çizilemeyen sunucu metni (EN)
import { verdictOf, countNoise, filterByVerdictView, parseVerdictView, inboxVerdictSubject, type VerdictView } from '@/lib/problemVerdict';
import { rowPassesKindFacet } from '@/lib/inboxBuckets'; // v0.10.1803 — Oracle P1 problem kovasında

// v0.10.2170 (Problems S10) — süzgeç sözlükleri/varsayılanları ve kolonlar +
// satır hücreleri kendi dosyalarında (davranış değişmez).
import {
  PRIO_ALL, PRIO_DEFAULT, SORT_DEFAULT, INBOX_SORT_IDS, KIND_ALL, KIND_DEFAULT, KIND_ALL_RETIRED, KIND_DEFAULT_RETIRED,
  KIND_LABEL, SUBJECT_LANES, SUBJECT_LABEL, SUBJECT_TITLE, STATUS_PIVOTS, DEFAULT_MIN_OCC, type InboxStatus,
} from './inbox/inboxFilters';
import { inboxCols, capSortLabel, occLine } from './inbox/inboxColumns';
import { t as tl, tf, useLang } from '@/lib/i18n'; // v0.10.2283 — Türkçe metinler lib/i18n inbox.* (TR aynı)
import { TeamCell, AssigneePill, RowChips, DetailLine, GraphRowSub, GraphFrequentChip, GraphClusterMembers } from './inbox/InboxCells';


// exception ailesi — httperror satırları da exception payload'u taşır
// (aynı store, aynı fingerprint yolu); drawer/drill-in/toplu-ack ortak.
// v0.10.784 — exception-ailesi yüklemi ve "kaynağa git" adresleri
// lib/inboxHref'te (servis dikkat şeridiyle tek kaynak).
const isExcFamily = isInboxExcFamily;


// /inbox — unified triage view (v0.5.211). Merges Problems +
// Exception groups + Anomaly events server-side with a normalised
// P1/P2/P3 priority blend so operators get "everything needing a
// human" in one place instead of tab-hopping between three pages.
//
// A row click opens an in-place triage drawer (v0.8.292, Option B
// slice 3): root-cause ribbon + inline ack/assign/mute, without
// leaving /inbox. The drawer keeps an "Open source →" deep-link
// escape hatch to the per-source workspaces, which still exist.
// v0.10.1032 — bugün çekmece yalnız incident satırları ve eski ?item=
// linkleri için; exception / alarm kuralı / anomali satırı TAM SAYFA detay
// açar (karar lib/inboxHref inboxRowOpen'da, aşağıdaki openRow notu).
// v0.10.1081 — çekmece TAMAMEN kalktı: incident satırı da tam sayfaya
// (/incident?id=) gider; eski ?item= linki tam sayfaya yönlendirilir.


export default function InboxPage() {
  const navigate = useNavigate();
  // v0.10.1827 — tür sözlüğü + varsayılan Incidents ana anahtarına bağlı
  // (kabuk bayrağı user.features.incidents; yoksa emekli).
  const { user } = useAuth();
  const lang = useLang();
  const incidentsOn = user?.features?.incidents === true;
  const kindAll = incidentsOn ? KIND_ALL : KIND_ALL_RETIRED;
  const kindDefault = incidentsOn ? KIND_DEFAULT : KIND_DEFAULT_RETIRED;
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  // "/" focuses this filter via the shared table keyboard-nav.
  const searchRef = useRef<HTMLInputElement>(null);

  // URL = source of truth (v0.8.291, Option B Slice 2). Every facet/filter is
  // derived straight from the query string — no useState mirror, so the
  // one-way-read bug class (v0.8.256/265/267) can't exist here, and Copy link
  // reproduces the exact triage view. Defaults (P1+P2, all kinds) are the codec
  // fallback for an absent param, so a bare /inbox lands on the intended view.
  // v0.9.254 — 'ignored' pivotu eklendi. Susturulmuş exception grupları
  // BAŞKA hiçbir pivotta görünmüyordu (store varsayılanı da 'all' da onları
  // eliyor), ve bunları geri açan tek yüzey /problems'ın Ignored sekmesiydi.
  // O sayfa kalkarsa susturma kalıcı ve geri alınamaz hale gelirdi.
  const statusFilter: InboxStatus =
    searchParams.get('status') === 'all' ? 'all'
      : searchParams.get('status') === 'ignored' ? 'ignored'
        : 'open';
  const rawPrio = searchParams.get('prio');
  const rawKind = searchParams.get('kind');
  const rawCat = searchParams.get(INBOX_CAT_PARAM); // v0.10.706
  const catSet = useMemo(() => new Set(decodeCsvSet(rawCat, INBOX_CAT_ALL, INBOX_CAT_ALL)), [rawCat]);
  const prioSet = useMemo(() => new Set(decodeCsvSet(rawPrio, PRIO_ALL, PRIO_DEFAULT)), [rawPrio]);
  const kindSet = useMemo(
    () => new Set(decodeCsvSet(rawKind, kindAll, kindDefault) as InboxKind[]),
    [rawKind, kindAll, kindDefault]);
  const serviceFilter = searchParams.get('service') ?? '';
  // v0.9.251 — the search box drives `q` (service + title + source),
  // not `service`. `service` is still read so an older shared link
  // keeps narrowing exactly as it did; the two AND together
  // server-side.
  const searchFilter = searchParams.get('q') ?? '';
  // v0.9.252 — bulk triage selection. Deliberately NOT in the URL: a
  // shared link should reproduce the VIEW, not a half-finished
  // multi-select someone else is about to act on. Named `picked` to
  // stay clear of `selected`, which is the DRAWER's row (?item=).
  const [delOpen, setDelOpen] = useState(false); // v0.10.1777 kalıcı sil onayı
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkNote, setBulkNote] = useState<string | null>(null);
  const qc = useQueryClient();
  const ownerFilter = searchParams.get('owner') ?? '';
  const sreFilter = searchParams.get('sre') ?? '';
  // v0.9.1246 (operatör: "takımımın exception'ları dediğinde o takım
  // filtreli exceptions açabilir") — TEK EKSENLİ takım süzgeci: takım
  // owner VEYA SRE olarak geçiyorsa satır kalır. owner/sre açılırlarından
  // FARKLI ve bilerek: onlar iki ayrı eksen (AND), bu birleşim.
  //
  // Kendi açılır menüsü YOK — kaynağı sohbetin derin linki ve paylaşılan
  // URL. Karşılığında GÖRÜNÜR bir çip basıyoruz (× ile temizlenir): bir
  // filtre görünmezse operatör eksik listeyi "sistem boş" diye okur.
  const teamFilter = readInboxTeam(searchParams);
  // v0.9.320 — occurrence floor, same semantics and same default as
  // /problems (v0.9.315) so the two surfaces can't disagree about what
  // counts as noise. URL-borne like every other facet here, so a saved view
  // and a shared link carry the threshold too.
  //
  // NOT silent: the strip below states the floor, says how many rows it hid
  // (the server counts them AFTER every other narrow, so the number is the
  // honest one), and "show all" is a single click. A rare exception that
  // fired once can still be the important one.
  // v0.9.417 (operatör kararı 2026-07-30, eski "1-2-3'lük grupları
  // gösterme" direktifini geri alır): varsayılan taban 0 — az sayıda
  // occurrence'lı gruplar da GÖRÜNÜR; öncelik formülü onları zaten
  // P3'e koyar ve priority-sıralı liste dibe iter. ?minOcc= isteyen
  // için aynen duruyor.
  // v0.10.740 (operatör: "1 tane geldiyse dahil etme") — varsayılan taban 2
  // (sunucu inboxDefaultMinOcc ile aynı): tek oluşumlu grup varsayılanda
  // yok, 2-3'lükler görünür. "show all" 0'ı URL'e YAZAR (silinirse
  // varsayılan geri gelir); paylaşılan link gördüğünü taşır.
  // v0.10.949 (operatör 2026-09-26: "tek servisten gelen 5'ten küçük
  // exception'ları göstermeyebiliriz") — varsayılan taban 5; aynı exception
  // aynı anda ≥2 serviste görülüyorsa 5'in altında da görünür. VARSAYILAN
  // KİP = URL'de param YOK: o hâlde istek tabanı göndermez, sunucu etkin
  // tabanı (min(5, P1 eşiği)) ve istisnayı kendisi uygular. Açık ?minOcc=N
  // (eski ?minOcc=5 linki ve 0 = hepsi dahil) istisnasız, bugünkü gibi.
  const floorDefault = searchParams.get('minOcc') === null;
  const minOcc = (() => {
    const raw = searchParams.get('minOcc');
    if (raw === null) return DEFAULT_MIN_OCC;
    const n = Number(raw);
    return Number.isFinite(n) && n >= 0 ? n : DEFAULT_MIN_OCC;
  })();
  const setMinOcc = (v: number) => setSearchParams(prev => {
    const next = new URLSearchParams(prev);
    if (v === DEFAULT_MIN_OCC) next.delete('minOcc'); else next.set('minOcc', String(v));
    return next;
  }, { replace: true });
  // v0.9.525 (operatör: "son 1 gün veya 2 saat seçebilmek isterim") —
  // İLK GÖRÜLME penceresi, URL'de ?since=. Sabit basamaklar (2h/24h/7d/
  // boş=hepsi): sunucu cache anahtarına girdiği için kardinalite sınırlı
  // olmalı (v0.8.270). SON görülme bilerek değil — 35 gündür yanan bir P1
  // "son 2 saat"te de görünürdü ve filtre hiçbir şeyi elemezdi; bu filtre
  // "bu pencerede ORTAYA ÇIKANLAR" sorusunu cevaplıyor.
  // v0.9.954 (UX denetimi F5 / Ö13) — en dar basamak 2h'ten 30m'e indi.
  // Inbox "ne oldu?"nun doğal girişi ama "şu 20 dakikada ortaya
  // çıkanlar" KURULAMIYORDU: bir olayın hemen ardından bakan operatör
  // en dar seçenekte bile 2 saatlik gürültüyü birlikte alıyordu.
  //
  // TAM CUSTOM PENCERE HÂLÂ YOK ve bu bilinçli — değer sunucu cache
  // anahtarına giriyor, serbest pencere kardinaliteyi patlatırdı
  // (v0.8.270). Sabit basamak sayısı 3'ten 5'e çıktı, sözleşme aynı.
  // Küme sunucunun normalizeInboxSince'iyle BİREBİR olmak zorunda:
  // burada var olup orada olmayan bir değer sessizce "hepsi" gibi
  // davranırdı.
  const SINCE_OPTS = [
    { v: '',    label: 'First seen: any' },
    { v: '30m', label: 'Last 30m' },
    { v: '1h',  label: 'Last 1h' },
    { v: '2h',  label: 'Last 2h' },
    { v: '24h', label: 'Last 24h' },
    { v: '7d',  label: 'Last 7d' },
  ] as const;
  const sinceFilter = (() => {
    const raw = searchParams.get('since') ?? '';
    return SINCE_OPTS.some(o => o.v === raw) ? raw : '';
  })();
  // v0.9.1342 — özne şeridi. Kapalı sözlük + varsayılan 'service':
  // sunucunun normalizeInboxSubject'iyle BİREBİR aynı sözleşme, yoksa
  // elle düzenlenmiş bir link burada bir şerit, orada başkasını açardı.
  const subjectLane: SubjectLane = parseSubjectLane(searchParams.get('subject'));
  const setSubjectLane = (s: SubjectLane) =>
    setParam('subject', s === 'service' ? null : s);

  // v0.10.1081 — ?item=<inboxId> artık çekmece AÇMAZ (çekmece kalktı); eski
  // link (bildirim, kayıtlı görünüm, sohbet) aşağıdaki efektte satırın tam
  // sayfasına YÖNLENDİRİLİR (replace — geri tuşu yönlendirmeye takılmaz).
  const legacyItem = searchParams.get('item');
  // v0.9.837 — ?problem=<id> tam-sayfa host'u, /problems'takinin aynısı.
  // Alert-rules bölümünün satır tıkı bu parametreyi yazar, yani detay
  // AYNI sayfada açılır; liste + bölüm MOUNTED kalır (display:none) ki
  // facet/seçim state'i "← Problems"te yerinde dursun.
  // v0.10.1032 — ikinci tam sayfa: ?anomaly=<id> (AnomalyEventHost), aynı
  // desen. İkisi KARŞILIKLI DIŞLAYICI (lib/inboxHref readInboxDetail /
  // withInboxDetail): elle ikisi birden yazılmışsa problem okunur, biri
  // açılınca öteki silinir — iki tam sayfa üst üste çizilmez.
  const openDetailParam = readInboxDetail(searchParams);
  const problemParam = openDetailParam?.param === 'problem' ? openDetailParam.id : null;
  const anomalyParam = openDetailParam?.param === 'anomaly' ? openDetailParam.id : null;
  const isAdmin = user?.role === 'admin' || user?.role === 'editor';

  // Single URL writer — replace:true + copy prev so foreign params (a future
  // ?problem= drawer, range, etc.) survive. Empty/null deletes the key.
  const setParam = (k: string, v: string | null) => {
    setSearchParams(prev => {
      const p = new URLSearchParams(prev);
      if (v === null || v === '') p.delete(k); else p.set(k, v);
      return p;
    }, { replace: true });
  };
  const setStatusFilter = (s: InboxStatus) => setParam('status', s === 'open' ? null : s);
  // v0.10.1015 — öğretme görünümü: ?verdict=noise yalnız "problem değil"
  // işaretli satırları gösterir; parametresiz (varsayılan) onları GİZLER.
  const verdictView = parseVerdictView(searchParams.get('verdict'));
  const setVerdictView = (v: VerdictView) => setParam('verdict', v === 'triage' ? null : v);
  // Multi-select toggles keep the min-1 invariant (can't deselect everything),
  // then serialise the set back to the URL (default selection → param removed).
  const togglePrio = (p: string) => {
    const next = new Set(prioSet);
    if (next.has(p)) { if (next.size === 1) return; next.delete(p); } else next.add(p);
    setParam('prio', encodeCsvSet(next, PRIO_ALL, PRIO_DEFAULT));
  };
  const toggleKind = (k: InboxKind) => {
    const next = new Set(kindSet);
    if (next.has(k)) { if (next.size === 1) return; next.delete(k); } else next.add(k);
    setParam('kind', encodeCsvSet(next, kindAll, kindDefault));
  };
  // v0.9.356 — "sadece" (1 tıkla izole; lejantların isolate jestiyle aynı)
  // ve grup başına "tümü". Solo, toggle'ın aksine tek değere İNER; tümü,
  // grubun tamamını seçer. İkisi de URL'e aynı codec'le yazar.
  const soloPrio = (p: string) => setParam('prio', encodeCsvSet(new Set([p]), PRIO_ALL, PRIO_DEFAULT));
  const allPrio = () => setParam('prio', encodeCsvSet(new Set(PRIO_ALL), PRIO_ALL, PRIO_DEFAULT));
  const soloKind = (k: InboxKind) => setParam('kind', encodeCsvSet(new Set([k]), kindAll, kindDefault));
  const allKind = () => setParam('kind', encodeCsvSet(new Set(kindAll), kindAll, kindDefault));
  // v0.10.706 — kategori çipi; varsayılan tümü (param silinir).
  const toggleCat = (c: string) => {
    const next = new Set(catSet);
    if (next.has(c)) { if (next.size === 1) return; next.delete(c); } else next.add(c);
    setParam(INBOX_CAT_PARAM, encodeCsvSet(next, INBOX_CAT_ALL, INBOX_CAT_ALL));
  };
  const soloCat = (c: string) => setParam(INBOX_CAT_PARAM, encodeCsvSet(new Set([c]), INBOX_CAT_ALL, INBOX_CAT_ALL));
  const allCat = () => setParam(INBOX_CAT_PARAM, null);
  const setServiceFilter = (v: string) => setParam('service', v || null);
  const setSearchFilter = (v: string) => setParam('q', v || null);
  const setOwnerFilter = (v: string) => setParam('owner', v || null);
  const setSreFilter = (v: string) => setParam('sre', v || null);
  // v0.9.1246 — param ADI tek yerden (INBOX_TEAM_PARAM): okuyan taraf
  // (readInboxTeam) ile yazan taraf aynı sabiti kullanmalı, yoksa çipin
  // × düğmesi filtreyi TEMİZLEMEZ ve operatör "kaldıramıyorum" der.
  const setTeamFilter = (v: string) => setParam(INBOX_TEAM_PARAM, v || null);
  // v0.9.341 — an EXCEPTION row opens its full detail page, not the drawer.
  //
  // Operator: "Eskiden occurrences görürdüm, şimdi göremiyorum exception'a
  // tıkladığımda. Ayrı sayfa açılıyordu, hatta onu istiyorum."
  //
  // This is a rejected pattern coming back by the side door. A drawer for
  // exception detail was tried in v0.9.508 and REVERTED in v0.9.510: the
  // standing decision is that exception detail is a FULL PAGE in the classic
  // v0.9.425 layout. The merged triage queue (v0.9.323) then routed every
  // row — exceptions included — through the in-place drawer, which shows a
  // count and three buttons. The occurrences-over-time chart, the stack trace
  // and the sample traces all live on the page, so clicking the row lost
  // exactly what the operator opens an exception to see.
  //
  // Other kinds keep the drawer: it exists so a Problem or an anomaly can be
  // acked without leaving the queue (v0.8.292), and neither has a richer
  // destination that the drawer is hiding.
  //
  // v0.10.1032 — the last sentence above STOPPED BEING TRUE and this is the
  // correction. Operator (prod): "Anomali ve alert rule'lara girdiğimde drawer
  // çıkıyor. Exception gibi detay gözükmüyor."
  //   • Alert-rule problems got a full page in v0.9.837 (AlertProblemHost →
  //     AlertProblemDetail: root cause, metric, offenders, timeline, blast
  //     radius, correlated signals, log evidence, notifications, runbook),
  //     hosted RIGHT HERE via ?problem=. The row click kept opening the 560px
  //     drawer, i.e. it hid an existing richer destination behind a title, a
  //     ribbon and three buttons — exactly the v0.9.341 exception story again.
  //   • Anomaly events had no page at all; now they do (AnomalyEventHost,
  //     ?anomaly=, same in-place pattern).
  // So both kinds open their full page in place, like exceptions do. This
  // supersedes the v0.8.292 / v0.9.341 "drawer for non-exception kinds" call
  // for these two kinds. Nothing the drawer could do is lost: Assign… and
  // "Gerçek problem / Problem değil" are on the alert page (Acknowledge was
  // already there), Mute… and the verdict on the anomaly page.
  // Incident rows and old ?item= links keep the drawer (out of scope).
  // The decision itself is a pure, table-tested helper (lib/inboxHref
  // inboxRowOpen); click and keyboard Enter/o both call openRow.
  //
  // v0.10.1081 (operatör: "Drawer çıkmasın, problem sayfasında direkt
  // içeriğine girebileyim, Exceptions sayfası gibi.") — the "incident rows
  // keep the drawer" exception above is GONE and so is the drawer: an incident
  // row navigates to its full page (/incident?id=, readable top since this
  // release), carrying the queue URL so its back link returns here. Bulk
  // checkboxes and keyboard j/k + Enter are unchanged.
  // Tam sayfa detayı aç / kapat — tek yazıcı, replace:true, yabancı
  // parametreler (süzgeçler, ?env=) korunur; ?item= ve diğer detay silinir.
  const openDetail = (param: InboxDetailParam, id: string | null) =>
    setSearchParams(prev => withInboxDetail(prev, param, id), { replace: true });
  // v0.10.1414 (tablo satırları gerçek link) — kararın BAĞLANTI hâli
  // (lib/inboxHref inboxRowLink): başlık hücresi <Link to replace state>,
  // satırın boş yeri rowClickHandlers, klavye Enter/o (onOpen) aynı hedef.
  // Adresler değişmedi: tam sayfa push (incident'te kuyruğa geri dönüş
  // durumu), yerinde detay withInboxDetail'in kuyruk adresi (replace).
  const rowLinkOf = (it: InboxItem) => inboxRowLink(inboxRowOpen(it), location.pathname, location.search);
  const openRow = (it: InboxItem) => {
    const link = rowLinkOf(it);
    if (link) navigate(link.to, { replace: link.replace, state: link.state });
  };
  // Eski ?item= linki → satırın tam sayfası (çözülemeyen kimlikte yalnız
  // param silinir; kuyruk açık kalır). Yükü değil KİMLİĞİ okur: liste
  // yüklenmeden ve satır süzgeç dışında olsa da çalışır.
  // Elle yazılmış ?problem= / ?anomaly= zaten açıksa o kazanır: ?item= yalnız
  // silinir (iki hedef arasında sıçrama yok).
  const detailOpen = !!openDetailParam;
  useEffect(() => {
    if (!legacyItem) return;
    const o = detailOpen ? ({ to: 'none' } as const) : inboxItemIdTarget(legacyItem);
    if (o.to === 'detail') {
      setSearchParams(prev => withInboxDetail(prev, o.param, o.id), { replace: true });
    } else if (o.to === 'page') {
      navigate(o.href, {
        replace: true,
        state: o.href.startsWith('/incident?') ? incidentBackState('/inbox') : undefined,
      });
    } else {
      setSearchParams(prev => { const p = new URLSearchParams(prev); p.delete('item'); return p; }, { replace: true });
    }
  }, [legacyItem, detailOpen, navigate, setSearchParams]);

  // Global env picker (v0.8.387) — service-scoped: the server keeps
  // rows whose service ran in the env in the last hour (+ service-
  // less global alerts), same semantics as /problems. Hint chip in
  // the facet bar spells it out.
  const [env] = useUrlEnv();
  const oraLabel = useBranding().oracleGroupLabel; // v0.10.1109
  // v0.9.319 — the sort now runs on the SERVER, before the cap. Seeded from
  // the same precedence useDataTable restores with (resolveInitialSort), so
  // the first fetch and the header arrow agree at mount instead of the page
  // showing a lastSeen arrow over a priority-ranked response. onSortChange
  // below keeps them in step afterwards.
  // v0.10.1081 — varsayılan ilk görülme (SORT_DEFAULT), kişisel kayıt
  // okunmaz (persist=false) ve kimlik sunucu kümesine karşı doğrulanır —
  // useDataTable'ın kendi başlangıcıyla AYNI argümanlar, ilk istek ile başlık
  // oku montajda ayrışmasın. URL useSearchParams'tan (window.location değil).
  const [srvSort, setSrvSort] = useState(() =>
    resolveInitialSort('inbox', searchParams.get('s_inbox'), SORT_DEFAULT, undefined, false, INBOX_SORT_IDS));

  const inboxQ = useInbox({
    status: statusFilter,
    service: serviceFilter || undefined,
    q: searchFilter || undefined,
    ownerTeam: ownerFilter || undefined,
    sreTeam: sreFilter || undefined,
    team: teamFilter || undefined,
    env: env || undefined,
    limit: 300,
    sort: srvSort.id ?? SORT_DEFAULT.id,
    dir: srvSort.dir,
    // v0.10.949 — param yoksa GÖNDERME (sunucu varsayılan kipi: taban +
    // çoklu-servis istisnası); açık değer (0 dahil) aynen gider.
    minOcc: searchParams.get('minOcc') === null ? undefined : minOcc,
    since: sinceFilter || undefined,
    // v0.9.330 — facets go to the SERVER now. As client-side filters they ran
    // over a page the server had already capped at 300 by priority; on prod
    // those 300 were all Incidents, so the exception-first default rendered
    // "Queue clear" over 2,144 real items.
    kind: [...kindSet].join(','),
    prio: [...prioSet].join(','),
    // v0.9.1342 — özne şeridi de SUNUCU filtresi (SQL'de, LIMIT'ten
    // önce). db şeridinde sunucu `kind`i ["problem"]e ZORLAR: db özneli
    // satır yalnız problems kaynağında var ve sayfanın tür varsayılanı
    // ['exception'] — zorlanmasaydı şerit boş açılırdı.
    subject: subjectLane,
  });

  // Şerit çipinin sayısı SUNUCUDAN gelir (tek COUNT, cap'ten ÖNCE).
  // Dönen sayfadan saymak "Exceptions 0" yalanının aynısı olurdu: servis
  // şeridindeyken db satırları o sayfada zaten YOK, yani sayfadan
  // türetilen her sayı 0 çıkardı. undefined = sunucu henüz cevap
  // vermedi; 0 = ölçüldü ve yok — ikisi farklı ve çip öyle gösteriyor.
  const dbLaneCount = inboxQ.data?.dbSubjectCount;
  const externalLaneCount = inboxQ.data?.externalSubjectCount; // v0.10.1017
  const data: InboxItem[] | null | undefined =
    inboxQ.isPending ? undefined : inboxQ.isError ? null : inboxQ.data?.items ?? [];
  // v0.9.221 — the server caps the queue; say so rather than letting 300 rows
  // pass for the whole thing. Sorted priority-desc, so what fell off is the
  // low-priority tail — exactly the part an operator would assume was empty.
  const capped = !inboxQ.isPending && !inboxQ.isError && inboxQ.data?.truncated
    ? { shown: inboxQ.data.items.length, total: inboxQ.data.total }
    : null;
  // v0.9.318 — the scan itself hit its ceiling, so the filters above ran over
  // a slice of the candidates rather than all of them. Worth saying ONLY when
  // a filter is actually active: unfiltered, the priority-ranked page is the
  // honest top of the queue and `capped` already covers it. Filtered, silence
  // would let "no results" mean "none exist".
  const anyFilter = !!(serviceFilter || searchFilter || ownerFilter || sreFilter || teamFilter || env);
  const scanCapped = !inboxQ.isPending && !inboxQ.isError
    && !!inboxQ.data?.scanCapped && anyFilter;
  const hiddenByMinOcc = inboxQ.data?.hiddenByMinOcc ?? 0;
  // v0.10.949 — varsayılan kipin sunucu değerleri: etkin taban (P1 eşiği 5'in
  // altına çekildiyse o), istisnayla tutulan satırlar ve "aynı anda" penceresi.
  // keepPreviousData: kip değişiminin hemen ardından data ÖNCEKİ (açık kip)
  // yanıtıdır — sunucu taban alanlarına yalnız yanıtın kendisi varsayılan
  // kipteyse güven; minOcc opsiyonel, yine sabite düş.
  const floorLive = inboxQ.data?.minOccDefault === true;
  const effFloor = floorLive ? (inboxQ.data?.minOcc ?? DEFAULT_MIN_OCC) : DEFAULT_MIN_OCC;
  const keptBySpread = floorLive ? (inboxQ.data?.keptBySpread ?? 0) : 0;
  // v0.10.949 (operatör kararı 2026-09-26) — regressed gruplar tabanın altında
  // ve tek serviste de olsa varsayılan kipte gösterilir; yayılımdan bağımsız.
  const keptRegressed = floorLive ? (inboxQ.data?.keptRegressed ?? 0) : 0;
  const spreadWin = inboxQ.data?.spreadWindowMin;
  // v0.10.949 — yayılım okunamadı (soft-fail): istisna uygulanmadı, gizli
  // sayı çoklu-servis gruplarını da içerir. undefined (eski gövde) = var.
  const spreadOff = inboxQ.data?.spreadAvailable === false;
  // v0.9.353 (operator-reported) — the rows on screen belong to the PREVIOUS
  // filter while the new one fetches. useInbox keeps previous data
  // (placeholderData: keepPreviousData) so a filter change doesn't blank the
  // page — but nothing SAID so. On prod the owner-filtered request was slow
  // enough that the old, unfiltered rows just sat there and the operator
  // reasonably concluded "the owner filter does not work". Stale data may be
  // shown; it may not masquerade as the answer.
  const showingStale = inboxQ.isPlaceholderData && inboxQ.isFetching;
  // v0.10.2122 (S2) — üst sayım cümlesi; "şimdi" yanıt geldiğinde sabitlenir
  // (render başına değişmez, yalnız metin — sorgu anahtarına girmez).
  const countSentence = useMemo(() => (inboxQ.isPending || inboxQ.isError ? null
    : inboxCountSentence(inboxQ.data?.counts, inboxQ.data?.oldestFirstSeen, inboxQ.dataUpdatedAt * 1e6, lang)),
  [inboxQ.isPending, inboxQ.isError, inboxQ.data, inboxQ.dataUpdatedAt, lang]);
  // v0.9.487 (operatör kararı) — v0.9.328/330 "bakmadığın türler" ve v0.9.424
  // "öncelik filtresi dışında" şeritleri KALDIRILDI: "kafa karıştırıyor".
  // Görünürlük sözleşmesi chip sayılarına devrolur (facet chip'leri seçili
  // OLMAYAN tür/önceliklerin sayısını her zaman gösterir); exception-dışı
  // türler sunucuda artık HEP P3 (inbox.go forceNonExceptionP3), yani
  // varsayılan P1 görünümüne hiçbir zaman karışmazlar.

  // v0.9.330 — the server already applied these (before the cap, which is the
  // whole point). Kept as a belt-and-braces pass so a stale cached page from a
  // pre-upgrade pod can't render rows the operator deselected — it must never
  // be the ONLY place the facet is applied again.
  // v0.10.1015 — öğretme kararları (imza → gerçek / problem değil). Kararlar
  // yüklenemezse dizin boş kalır: hiçbir satır gizlenmez (güvenli taraf).
  // v0.10.1626 (perf §10 i) — sorgu, dizin ve mutation BURADA bir kez; satır
  // hücreleri (ProblemVerdictRow) aynı bağlamdan yalnız bakar.
  const verdictCtx = useVerdictRowContext();
  const verdicts = verdictCtx.index;
  // v0.10.2279 (entity grafı S2) — "Ne bozuk" alt satırı (köken · varlık
  // sayısı); gölge özet, satırlar katlanmaz / sıralanmaz / süzülmez.
  const graphSum = useProblemGraphSummary();
  // Düz hesap (memo değil): satır başına bir harita bakışı; `data`ya bağlı bir
  // memo daha, bu dosyadaki "data conditional" lint sınıfını büyütürdü.
  const noiseCount = countNoise(Array.isArray(data) ? data : null, verdicts);
  const filtered = useMemo(() => {
    if (!data) return data;
    const facet = data.filter(it =>
      prioSet.has(it.priority) &&
      rowPassesKindFacet(it, kindSet) &&
      catSet.has(it.category ?? 'CUSTOM')); // v0.10.706
    return filterByVerdictView(facet, verdicts, verdictView);
  }, [data, prioSet, kindSet, catSet, verdicts, verdictView]);

  // Shared sortable + resizable table. Pre-sort by lastSeen desc so the
  // primitive's stable priority-desc sort reproduces the prior fixed
  // ordering (P1 first, newest within priority). Hook is unconditional.
  // onOpen + searchRef wire the app-wide keyboard nav (j/k select,
  // Enter/o open the source surface, "/" focuses the filter).
  // v0.9.319 — the lastSeen pre-sort is GONE. It existed to make the
  // primitive's client-side stable sort reproduce "P1 first, newest within
  // priority"; the server now ranks the whole candidate set before capping,
  // so re-ordering here would corrupt the order the cap was applied in.
  const inboxRows = filtered ?? [];
  const cols = useMemo(() => inboxCols(lang), [lang]); // v0.10.2283
  const dt = useDataTable<InboxItem>({
    // v0.10.2121 — 'inbox2': eski kolon genişlikleri yeni düzene taşınmasın;
    // sıralama URL'i paylaşılan linkler için `s_inbox` kalır.
    storageKey: 'inbox2',
    sortUrlKey: 's_inbox',
    columns: cols,
    rows: inboxRows,
    // v0.10.1081 — varsayılan ilk görülme (en yeni önce); kişisel kayıt yok,
    // URL `s_inbox` kazanır, bayat kimlik varsayılana düşer.
    initialSort: SORT_DEFAULT,
    persistSort: false,
    sortIds: INBOX_SORT_IDS,
    // serverSort: the ORDER BY happens in the handler over the FULL candidate
    // set, so sortedRows is the response verbatim. Client-side sorting would
    // rank the page — the same lie the filters told before v0.9.318.
    serverSort: true,
    onSortChange: setSrvSort,
    // v0.10.1032 (inceleme) — tam sayfa detay açıkken GİZLİ kuyruğun klavye
    // gezinmesi KAPALI: yoksa detay sayfasında `j` + Enter görünmeyen bir
    // satırı açıyordu. onOpen yokken useDataTable gezinmeyi hiç kurmaz.
    onOpen: problemParam || anomalyParam ? undefined : openRow,
    searchRef: problemParam || anomalyParam ? undefined : searchRef,
  });

  // Selection resolves against the rows actually on screen, so a row
  // that got resolved out from under the operator stops being counted
  // as acknowledgeable.
  const pickedRows = useMemo(
    () => (filtered ?? []).filter((r: InboxItem) => picked.has(r.id)),
    [filtered, picked]);
  const selCount = pickedRows.length;
  const ackProblemIds = pickedRows
    .filter((r: InboxItem) => r.kind === 'problem' && r.problem)
    .map((r: InboxItem) => r.problem!.id);
  const ackExceptionFps = pickedRows
    .filter((r: InboxItem) => isExcFamily(r))
    .map((r: InboxItem) => r.exception!.fingerprint);
  // v0.10.2075 — Incidents emekli: /api/incidents/* her metotta 410. Eski
  // incident satırı toplu onaylanmaz; sayaçta "atlanacak" olarak görünür.
  const ackable = ackProblemIds.length + ackExceptionFps.length;
  const skipped = selCount - ackable;
  const allPickedOnScreen = (filtered?.length ?? 0) > 0
    && (filtered ?? []).every((r: InboxItem) => picked.has(r.id));

  // v0.10.967 — tablo standardı T12 (dilim 5): yükleniyor / hata / eşleşme
  // yok / boş tablonun İÇİNDE; başlık, taban şeridi, kırpma rozetleri yerinde.
  // `data` hata anında null (önbellekte bayat sayfa olsa da) → bayat satır
  // hatayla yan yana kalmaz. Eski "Queue clear" metni öncelik/tür süzgecine
  // göre dallanıyordu: o dal eşleşme-yok yüklemidir (varsayılan görünüm
  // P1+P2 Exception, yani dar). İsteğin taşıdığı diğer süzgeçler (arama,
  // servis, takım, ilk görülme) sunucuda daraltır → eşleşme yok. Kategori
  // yalnız istemcide süzülür (istekte yok): sunucu satır döndürüp kategori
  // hepsini elediyse `data.length > 0` zaten eşleşme-yok der; kaynak boşsa
  // kuyruk gerçekten boştur ("Kuyruk boş"), kategori onu daraltmadı.
  // Ortam seçicisi pencere gibi genel kapsamdır, süzgeç sayılmaz. Tek bir
  // "hepsini temizle" eylemi yok → onClearFilters yok. Karşılaştırma sözlük
  // BOYUTUNA karşı (v0.9.323: tür listesi büyürken `< 3` literali kalmıştı).
  const facetNarrowed = prioSet.size < PRIO_ALL.length || kindSet.size < kindAll.length;
  const otherNarrowed = !!(serviceFilter || searchFilter || ownerFilter || sreFilter || teamFilter || sinceFilter);
  const inboxState: Omit<DataTableStateProps<InboxItem>, 'dt'> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error' }
    : verdictView === 'noise' ? { kind: 'no-match', message: tl('inbox.empty.noise', lang) } // v0.10.1015
    : facetNarrowed ? { kind: 'no-match', message: tl('inbox.empty.facet', lang) }
    : otherNarrowed || data.length > 0 ? { kind: 'no-match' }
    : { kind: 'empty', message: tl('inbox.empty.none', lang) };

  const toggleRow = (id: string) => setPicked(prev => {
    const next = new Set(prev);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });

  // v0.10.1777 — toplu kapat (editor+) / kalıcı sil (admin, onay + audit).
  const bulkIds = { ids: ackProblemIds, fingerprints: ackExceptionFps };
  const bulkCloseable = ackProblemIds.length + ackExceptionFps.length;
  const bulkResolve = async () => {
    setBulkBusy(true); setBulkNote(null);
    try {
      const r = await api.resolveProblemsBulk(bulkIds);
      setBulkNote(tf('inbox.note.resolved', lang, { c: r.resolvedProblems + r.resolvedExceptions }));
      setPicked(new Set());
      qc.invalidateQueries({ queryKey: ['inbox'] });
    } catch (e) {
      setBulkNote(e instanceof Error ? e.message : String(e));
    } finally {
      setBulkBusy(false);
    }
  };
  const bulkDelete = async () => {
    setDelOpen(false);
    setBulkBusy(true); setBulkNote(null);
    try {
      const r = await api.deleteProblemsBulk(bulkIds);
      setBulkNote(tf('inbox.note.deleted', lang, { c: r.deletedProblems + r.deletedExceptions }));
      setPicked(new Set());
      qc.invalidateQueries({ queryKey: ['inbox'] });
    } catch (e) {
      setBulkNote(e instanceof Error ? e.message : String(e));
    } finally {
      setBulkBusy(false);
    }
  };

  const bulkAcknowledge = async () => {
    setBulkBusy(true); setBulkNote(null);
    // Both calls go out before either result is inspected: they hit
    // different stores, and a failure in one must not cancel the other
    // half of the operator's gesture.
    const [pRes, eRes] = await Promise.allSettled([
      ackProblemIds.length ? api.acknowledgeProblems(ackProblemIds) : Promise.resolve(null),
      ackExceptionFps.length ? api.setExceptionGroupStatesBulk(ackExceptionFps, 'acknowledged') : Promise.resolve(null),
    ]);
    const okProblems = pRes.status === 'fulfilled' ? ackProblemIds.length : 0;
    const okExceptions = eRes.status === 'fulfilled'
      ? ((eRes.value as { applied: number } | null)?.applied ?? 0)
      : 0;
    const errs = [pRes, eRes].filter(r => r.status === 'rejected').length;
    const ok = okProblems + okExceptions;
    setBulkNote(errs
      ? tf('inbox.note.ackErr', lang, { c: ok, e: errs })
      : tf('inbox.note.ack', lang, { c: ok }));
    setPicked(new Set());
    // The server already dropped the cached list + badge; refetch so
    // the rows leave the screen instead of lingering a poll cycle.
    qc.invalidateQueries({ queryKey: ['inbox'] });
    setBulkBusy(false);
  };

  // v0.10.1531 (Problem standardı R3, D1) — toplu triyaj: seçili satırların
  // imzalarına aynı karar. Toplu uç yok (karar imza başına tek satır,
  // saved_views); istekler birlikte gider, biri düşerse kalanlar yazılır.
  // İmzası olmayan satır (incident) sayılır ve atlanır. Son cevap tam liste
  // olduğundan önbellek doğrudan tazelenir.
  const verdictSubjects = pickedRows.map(inboxVerdictSubject).filter((x): x is NonNullable<typeof x> => !!x);
  const bulkVerdict = async (verdict: 'real' | 'noise', noise?: NoiseReasonChoice) => {
    setBulkBusy(true); setBulkNote(null);
    const res = await Promise.allSettled(verdictSubjects.map(subj => api.putProblemVerdict({
      signature: subj.signature, verdict, label: subj.label, kind: subj.kind, service: subj.service,
      priority: subj.priority, magnitude: subj.magnitude,
      ...(verdict === 'noise' && noise ? { reason: noise.reason, note: noise.note || undefined, permanent: noise.permanent || undefined } : {}),
    })));
    const ok = res.filter(r => r.status === 'fulfilled').length;
    const label = tl(verdict === 'real' ? 'inbox.verdict.real' : 'inbox.verdict.noise', lang);
    setBulkNote(ok === res.length ? tf('inbox.note.verdict', lang, { c: ok, label }) : tf('inbox.note.verdictErr', lang, { c: ok, e: res.length - ok }));
    setPicked(new Set());
    void qc.invalidateQueries({ queryKey: ['problem-verdicts'] });
    setBulkBusy(false);
  };

  // v0.9.330 — chips render SERVER counts, computed over the pre-facet,
  // pre-cap set. Counting the returned page is exactly how prod displayed
  // "Exceptions 0" on a queue that held thousands: the page was 300 incidents,
  // so every other chip read zero and the numbers argued the queue was empty.
  // Falls back to page-derived counts only while no server payload exists yet.
  // v0.10.706 — kategori sayıları sayfadan (kind/prio süzgeci sonrası değil,
  // ham veriden: çip "ne var"ı söylesin).
  // Memo'suz: ≤300 satır, her render'da ucuz; `data` koşullu olduğu için
  // useMemo bağımlılık uyarısı üretirdi (sayfadaki mevcut sınıf).
  const catCounts: Record<string, number> = {};
  for (const it of data ?? []) {
    const c = it.category ?? 'CUSTOM';
    catCounts[c] = (catCounts[c] ?? 0) + 1;
  }
  const counts = useMemo(() => {
    const fromServer = inboxQ.data?.counts;
    if (fromServer) return fromServer;
    const out: Record<string, number> = { P1: 0, P2: 0, P3: 0,
      problem: 0, exception: 0, httperror: 0, anomaly: 0, incident: 0 };
    for (const it of data ?? []) {
      out[it.priority] = (out[it.priority] ?? 0) + 1;
      out[it.kind] = (out[it.kind] ?? 0) + 1;
    }
    return out;
  }, [data, inboxQ.data]);

  // v0.9.355 (operator-reported) — dropdown options come from the CATALOG,
  // not from the filtered rows.
  //
  // They used to be derived from `data` — the response of a request that
  // already carried the team filter. Pick Looki and the response holds only
  // Looki's rows, so the dropdown collapsed to "all + Looki": switching to
  // another team meant going back to "all" first, waiting for the (big)
  // unfiltered list, then picking again. The old comment here claimed the
  // opposite ("opening the dropdown again still shows the remaining teams"),
  // which stopped being true the moment the filter moved server-side — and
  // v0.9.353's SQL narrow made the collapse total.
  //
  // useServicesMetadata is the same catalog read the /services and
  // exceptions pages feed their team dropdowns from (React Query dedups it,
  // 60s staleTime) — no new endpoint, and all three surfaces now agree on
  // what teams exist.
  const catalogQ = useServicesMetadata();
  const { ownerOptions, sreOptions } = useMemo(() => {
    const mds = Object.values(catalogQ.data ?? {}) as import('@/lib/types').ServiceMetadata[];
    return {
      // v0.8.330 — case-insensitive dedup, same rule everywhere.
      ownerOptions: teamOptionsCI(mds.map(md => md.ownerTeam)),
      sreOptions:   teamOptionsCI(mds.map(md => md.sreTeam)),
    };
  }, [catalogQ.data]);
  // v0.10.2124 (S4) — Ekip seçenekleri: owner ∪ SRE (team paramı ikisini de
  // eşler); seçili değer katalogda yoksa da listede.
  const teamOptions = useMemo(() => {
    const opts = teamOptionsCI([...ownerOptions, ...sreOptions]);
    return teamFilter && !opts.includes(teamFilter) ? [teamFilter, ...opts] : opts;
  }, [ownerOptions, sreOptions, teamFilter]);
  const moreActive = inboxMoreFiltersActive(searchParams);

  return (
    <>
      {/* v0.9.323 — triage merge: this page IS the queue now, so it carries
          the name. The route stays /inbox so saved views, notification deep
          links and dashboard markdown keep resolving — only the label moved. */}
      {/* v0.9.837 — ?problem= tam-sayfa detayı açıkken kuyruk GİZLENİR
          (unmount DEĞİL): facet/seçim/scroll state'i "← Problems"te
          yerinde duruyor. /problems'taki v0.8.426/428 deseninin aynısı. */}
      {/* v0.10.1032 — ?anomaly= tam sayfası da aynı kuralla (kendi Topbar'ı var). */}
      {!problemParam && !anomalyParam && <Topbar title="Problems" showEnv envApplies />}
      {/* v0.9.981 (D3.1) — `id="content"` DEĞİL `.page-body`: detay açıkken
          bu kap gizli ama MOUNT'lu kalıyor, dolayısıyla id'li olsaydı aynı
          DOM'da iki `#content` bulunurdu (geçersiz HTML + `getElementById`
          gizli olanı döndürür). Stil `#content` ile birebir aynı. */}
      <div className="page-body" style={problemParam || anomalyParam ? { display: 'none' } : undefined}>
        {/* v0.10.1081 — varsayılan metni sabitlerden türer (elle yazılmaz). */}
        <p style={{ color: 'var(--text2)', fontSize: 13, marginBottom: 14 }}>
          Everything needing a human — Problems (alert rules), open Exception
          groups, and active Anomaly detections. Default view:{' '}
          <b>{PRIO_DEFAULT.length === PRIO_ALL.length ? 'all priorities' : `${PRIO_DEFAULT.join(' + ')} only`}</b>,{' '}
          {kindDefault.map(k => KIND_LABEL[k]).join(' + ')}, P1 first. Click any row to open its detail page.
        </p>

        {/* One grouped facet bar (v0.8.38) — status pivot + priority + kind
            chips share the shared .facet primitive (repo equivalent of the
            design's .logbar/.facet). Handlers + state are unchanged: status
            pivot single-select via setStatusFilter, priority/kind multi-
            select via togglePrio/toggleKind. Team selects + service filter
            stay pushed right with margin-left:auto. */}
        <div className="facetbar">
          {/* v0.10.2124 (S4) — Ekip önde: `team` (owner VEYA SRE, v0.9.1246 /
              DECISIONS:12118 kararı değişmez). Eski çipin yerini alır: seçili
              takım açılırda GÖRÜNÜR; katalogda olmayan takım (sohbet derin
              linki) seçenek olarak eklenir, sessizce düşmez. */}
          <NativeSelect size="sm" value={teamFilter}
            onChange={e => setTeamFilter(e.target.value)}
            aria-label={tl('inbox.team.aria', lang)}
            title={tl('inbox.team.title', lang)}
            className={cn('ib-team-select', FIELD_SURFACE)}>
            <NativeSelectOption value="">{tl('inbox.team.all', lang)}</NativeSelectOption>
            {teamOptions.map(o => <NativeSelectOption key={o} value={o}>{o}</NativeSelectOption>)}
          </NativeSelect>
          {/* v0.9.357 (mockup C, operatör: "C daha uygun bizim kuruma") —
              çip grupları sayılı multi-select dropdown'lara döndü; Owner/SRE
              select'leriyle tek dil. v0.9.356'nın "sadece"/"tümü" jestleri
              panelin içinde yaşıyor; sayılar da orada — yoğunluk bir tık
              arkaya taşındı, kaybolmadı. */}
          <FacetMultiSelect label={tl('inbox.f.priority', lang)}
            options={(['P1', 'P2', 'P3'] as const).map(pp => ({
              value: pp, label: pp, count: counts[pp] ?? 0 }))}
            selected={prioSet}
            onToggle={togglePrio} onSolo={soloPrio} onAll={allPrio} />
          {/* v0.10.1081 — varsayılan yalnız P1; dışarıda kalan öncelikler TEK
              tıkla açılır (açılır menüye girmeden), sayılarıyla. */}
          {prioSet.size < PRIO_ALL.length && (
            <Button variant="ghost" size="sm" onClick={allPrio}
              title={tl('inbox.allPrio.title', lang)}>
              {tf('inbox.allPrio', lang, { c: PRIO_ALL.reduce((n, pp) => n + (counts[pp] ?? 0), 0) })}
            </Button>
          )}

          {/* v0.9.1342 — db şeridinde Tür facet'i GİZLİ ve bu bir
              gizleme değil dürüstlük: o şeritte sunucu `kind`i
              ["problem"]e zorluyor (db özneli satır yalnız problems
              kaynağında var). Açık bırakmak, tıklandığında hiçbir şey
              değiştirmeyen bir kontrol göstermek olurdu. */}
          {subjectLane === 'service' && (
            <FacetMultiSelect label={tl('inbox.f.kind', lang)}
              options={kindAll.map(k => ({
                value: k, label: KIND_LABEL[k], count: counts[k] ?? 0 }))}
              selected={kindSet as Set<string>}
              onToggle={k => toggleKind(k as InboxKind)}
              onSolo={k => soloKind(k as InboxKind)} onAll={allKind} />
          )}

          {/* Env hint chip (v0.8.387) — non-interactive; the pick lives
              in the Topbar EnvPicker. Surfaces the service-scoped
              semantics so the operator doesn't read rows as per-env. */}
          {/* v0.10.922 (sade palet adım 1) — bilgi çipi üst veri: nötr. */}
          {env && (
            <span className="badge b-gray" style={{ cursor: 'help' }}
              title={`Showing items on services seen in "${env}" during the last hour (global environment picker). Triage rows carry no environment of their own — a row on a multi-env service still shows, and service-less (global) alerts always show. External-source problems (Oracle / Influx) follow the service they were mapped to; those with no mapped service have no environment and show only when no environment is selected — the lane and sidebar counts use the same rule.`}>
              env: {env} — service-scoped
            </span>
          )}

          <span style={{ marginLeft: 'auto' }} />

          {/* v0.9.251 — one box, matched SERVER-side across service +
              title + source. Title was the gap: with thousands of open
              items an operator could narrow to a service but never to
              "timeout" or "OOMKilled". Client-side filtering wouldn't
              have worked either — the list is a server-capped top slice,
              so a term present only on row 900 would never arrive. */}
          <SearchField ref={searchRef} value={searchFilter}
            onChange={setSearchFilter}
            placeholder="Search service, title, source…"
            aria-label="Search service, title, source"
            hint="/"
            title="Case-insensitive substring match across the service, the title and the source label."
            style={{ minWidth: 220 }} />
          {serviceFilter && (
            <Chip onRemove={() => setServiceFilter('')}
              removeLabel={`Remove the ${serviceFilter} service filter`}
              title="Service filter carried in the URL (?service=). Narrows on top of the search box.">
              service: {serviceFilter}
            </Chip>
          )}
          {/* v0.9.1246 — takım süzgeci GÖRÜNÜR çip (× ile temizlenir); v0.10.2124'ten
              beri Ekip açılırı da aynı paramı gösterir, çip derin linkte kalır. */}
          {teamFilter && (
            <Chip onRemove={() => setTeamFilter('')}
              removeLabel={tf('inbox.teamChip.remove', lang, { t: teamFilter })}
              title={tl('inbox.teamChip.title', lang)}>
              {tl('inbox.teamChip', lang)}{teamFilter}
            </Chip>
          )}
        </div>

        {/* v0.10.2124 (S4) — ikincil süzgeçler kapalı bölümde; varsayılan-dışı
            bir değer etkinse açık gelir (inboxMoreFiltersActive). Hiçbir URL
            paramı silinmedi: Durum, Öğrenilen, Özne, Kategori, First seen,
            Owner/SRE, occurrence tabanı ve kayıtlı görünümler burada. */}
        <MoreFilters activeCount={moreActive.length}>
          {/* v0.9.255 — kayıtlı görünümler (yalnız queryString saklar);
              v0.10.2124'ten beri bu bölümde. */}
          <SavedViewsBar page="inbox" />
          <div className="facetbar">
            {/* Status pivot — single-select. v0.9.356: 'ignored' "All" diye
                etiketleniyordu (ternary yalnız 'open'ı ayırıyordu), yani
                çubukta İKİ "All" vardı ve biri susturulmuş exception'ları
                açıyordu. Operatörün "iki anlamı belirsiz All" gözlemi düpedüz
                etiket hatasıydı. */}
            <span className="facet-grp">
              <span className="gl">{tl('inbox.f.status', lang)}</span>
              {STATUS_PIVOTS.map(s => (
                <span key={s} onClick={() => setStatusFilter(s)}
                  className={`facet${statusFilter === s ? ' on' : ''}`}>
                  {s === 'open' ? 'Open / Active' : s === 'all' ? 'All' : 'Ignored'}
                </span>
              ))}
            </span>

            {/* v0.10.1015 (operatör: "hangisi gerçek problem hangisi değil zamanla
                öğretelim") — öğretme görünümü. Tek-seçim, URL'de ?verdict=.
                "Problem değil" işaretli imzalar varsayılanda gizlenir; sayısı
                çipte hep görünür ve tek tıkla açılır (gizlenen şey saklanmaz). */}
            <span className="facet-grp">
              <span className="gl">{tl('inbox.f.learned', lang)}</span>
              <span onClick={() => setVerdictView('triage')} className={`facet${verdictView === 'triage' ? ' on' : ''}`}
                title={tl('inbox.v.triage.title', lang)}>
                Triage
              </span>
              <span onClick={() => setVerdictView('noise')} className={`facet${verdictView === 'noise' ? ' on' : ''}`}
                title={tl('inbox.v.noise.title', lang)}>
                {tf('inbox.v.noise', lang, { c: noiseCount })}
              </span>
            </span>

            {/* v0.9.1342 (operatör kararı) — ÖZNE şeridi. Durum pivotuyla
                aynı dil: tek-seçim çip grubu, URL'de ?subject=.

                Neden ayrı bir ŞERİT ve bir facet DEĞİL: bir facet seçimi
                satırları aynı listede birleştirir ve db problemleri servis
                problemleriyle öncelik sırasında YARIŞIRDI. Operatörün
                istemediği tam olarak bu.

                Sayı yalnız Veritabanları çipinde: o şerit TEK kaynaklı
                (problems), yani sayı TAM. Servis şeridi dört kaynaklı ve
                tek bir COUNT ile dürüstçe ifade edilemez — sunucu onu
                iddia etmiyor, ekran da uydurmuyor.

                v0.10.1026 — varsayılan (Servisler) şeridi dış kaynak
                satırlarını da içeriyor; Dış kaynaklar çipi onların
                daraltılmış görünümü. İkisi de title ile söyleniyor. */}
            <span className="facet-grp">
              <span className="gl">{tl('inbox.f.subject', lang)}</span>
              {SUBJECT_LANES.map(l => (
                <span key={l} onClick={() => setSubjectLane(l)}
                  title={SUBJECT_TITLE[l] ? tl(SUBJECT_TITLE[l], lang) : undefined}
                  className={`facet${subjectLane === l ? ' on' : ''}`}>
                  {tl(SUBJECT_LABEL[l], lang)}
                  {l === 'db' && dbLaneCount !== undefined && ` (${dbLaneCount})`}
                  {l === 'external' && externalLaneCount !== undefined && ` (${externalLaneCount})`}
                </span>
              ))}
            </span>
            {/* v0.10.706 — kategori (Davis sınıfı). Sayılar sayfadaki
                satırlardan (sunucu sayımı yok; sözlük küçük, ucuz). */}
            <FacetMultiSelect label={tl('inbox.f.category', lang)}
              options={INBOX_CAT_ALL.map(c => ({
                value: c, label: INBOX_CAT_LABEL[c], count: catCounts[c] ?? 0 }))}
              selected={catSet as Set<string>}
              onToggle={toggleCat} onSolo={soloCat} onAll={allCat} />

            {/* v0.9.525 — first-seen penceresi. Küçük sabit küme → düz
                <select> (frontend-design-system, eski frontend-conventions: ≤10 değer picker istemez). */}
            <select
              value={sinceFilter}
              onChange={e => setParam('since', e.target.value || null)}
              title={tl('inbox.since.title', lang)}
              style={{
                background: 'var(--field-bg)', color: 'var(--text)',
                border: '1px solid var(--border)', borderRadius: 6,
                fontSize: 12, padding: '4px 8px',
              }}
            >
              {SINCE_OPTS.map(o => <option key={o.v} value={o.v}>{o.label}</option>)}
            </select>
            {/* Team filters (v0.5.234). Distinct values come from
                the current result set so an operator can stack
                owner + SRE narrows without losing the option list.
                Empty option = "all". Server-side filter (no client
                re-fetch shaping) so the result count drops
                accurately as the operator narrows. */}
            <select value={ownerFilter}
              onChange={e => setOwnerFilter(e.target.value)}
              title="Filter by service.ownerTeam"
              style={{ fontSize: 12, padding: '4px 8px', minWidth: 130 }}>
              <option value="">Owner: all</option>
              {ownerOptions.map(o => <option key={o} value={o}>{o}</option>)}
            </select>
            <select value={sreFilter}
              onChange={e => setSreFilter(e.target.value)}
              title="Filter by service.sreTeam"
              style={{ fontSize: 12, padding: '4px 8px', minWidth: 130 }}>
              <option value="">SRE: all</option>
              {sreOptions.map(o => <option key={o} value={o}>{o}</option>)}
            </select>
          </div>
          {!inboxQ.isPending && !inboxQ.isError && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 8,
              fontSize: 11.5, color: 'var(--text3)', marginBottom: 8,
            }}>
              {minOcc > 0 && floorDefault ? (
                // v0.10.949 — varsayılan kip: taban + çoklu-servis istisnası.
                // Gizlenen = "tabanın altında, tek serviste"; istisnayla
                // tutulanlar ayrıca sayılır (rastlantısal eşleşme görünür kalsın).
                // Yayılım kapalıysa (spreadOff) düz taban dili: "Showing N+ groups · X hidden".
                // v0.10.949 — regressed istisnası her iki dilde de (yayılımdan bağımsız).
                <>
                  <span title={spreadOff ? spreadOffFloorTitle(effFloor, lang) : defaultFloorTitle(effFloor, spreadWin, lang)}>
                    Showing <b style={{ color: 'var(--text2)' }}>{effFloor}+</b> groups{spreadOff ? ' and regressed groups' : <>, regressed groups and groups (2+ occurrences) seen in ≥2 services at the same time{spreadWin ? ` (±${spreadWin} min)` : ''}</>}
                    {floorLive && hiddenByMinOcc > 0 && <> · <b style={{ color: 'var(--text2)' }}>{hiddenByMinOcc}</b> {spreadOff ? 'hidden' : <>groups below {effFloor}, single service, hidden</>}</>}
                    {!spreadOff && keptBySpread > 0 && <> · <b style={{ color: 'var(--text2)' }}>{keptBySpread}</b> multi-service groups shown although below {effFloor}</>}
                    {keptRegressed > 0 && <> · <b style={{ color: 'var(--text2)' }}>{keptRegressed}</b> regressed groups shown although below {effFloor}</>}
                  </span>
                  <Button variant="ghost" size="sm" onClick={() => setMinOcc(0)}>show all</Button>
                  <Button variant="ghost" size="sm" onClick={() => setMinOcc(10)}>10+</Button>
                </>
              ) : minOcc > 0 ? (
                <>
                  <span title={tl('inbox.minOcc.title', lang)}>
                    Showing groups with <b style={{ color: 'var(--text2)' }}>{minOcc}+</b> occurrences
                    {hiddenByMinOcc > 0 && <> — <b style={{ color: 'var(--text2)' }}>{hiddenByMinOcc}</b> hidden</>}
                  </span>
                  <Button variant="ghost" size="sm" onClick={() => setMinOcc(0)}>show all</Button>
                  {minOcc !== 10 && (
                    <Button variant="ghost" size="sm" onClick={() => setMinOcc(10)}>10+</Button>
                  )}
                </>
              ) : (
                <>
                  <span>Showing <b style={{ color: 'var(--text2)' }}>every</b> row, including one-off exceptions</span>
                  <Button variant="ghost" size="sm" onClick={() => setMinOcc(DEFAULT_MIN_OCC)}>{tf('inbox.minOcc.default', lang, { n: DEFAULT_MIN_OCC })}</Button>
                </>
              )}
              {/* v0.9.1332 — sürüklenmiş kolon genişliklerinin çıkmazı (operatör:
                  "neden sayfa yatayda kayıyor"). v0.10.939 (tablo standardı S8):
                  "Kolonları sıfırla" artık başlık satırının ⋯ menüsünde
                  (DataTableHead), sayfa başına düğme yok. */}
            </div>
          )}
        </MoreFilters>

        {/* v0.9.252 — bulk acknowledge. Appears only with a selection so
            the toolbar above stays quiet during normal triage.

            Scope is explicit rather than silent: problems and exception
            groups both have an "acknowledged" state and are sent to
            their respective BULK endpoints (one request, one audit row
            each); anomalies have no ack — they are silenced, which is a
            different decision — so they are counted out loud instead of
            being dropped without a word. */}
        {selCount > 0 && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
            padding: '8px 12px', marginBottom: 8, borderRadius: 6,
            background: 'var(--bg2)', border: '1px solid var(--accent)',
          }}>
            <b style={{ fontSize: 12 }}>{tf('inbox.bulk.selected', lang, { c: selCount })}</b>
            <span style={{ fontSize: 11, color: 'var(--text3)' }}>
              {tf('inbox.bulk.ackable', lang, { c: ackable })}
              {/* Onay yolu yalnız problem + exception grubunda; anomali
                  susturma ister, eski incident emekli (v0.10.2075). */}
              {skipped > 0 && tf('inbox.bulk.skipped', lang, { c: skipped })}
            </span>
            <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center' }}>
              {bulkNote && <span style={{ fontSize: 11, color: 'var(--text2)' }}>{bulkNote}</span>}
              <Button variant="primary" size="sm" disabled={ackable === 0}
                onClick={bulkAcknowledge} loading={bulkBusy}>
                {tf('inbox.bulk.ack', lang, { c: ackable })}
              </Button>
              {/* v0.10.1531 (R3, D1) — toplu triyaj; yalnız editor/admin (sunucu da kapalı). */}
              {isAdmin && (
                <>
                  <Button variant="secondary" size="sm" disabled={verdictSubjects.length === 0 || bulkBusy}
                    onClick={() => { void bulkVerdict('real'); }}>
                    {tf('inbox.bulk.real', lang, { c: verdictSubjects.length })}
                  </Button>
                  <NoiseReasonButton label={tf('inbox.bulk.noise', lang, { c: verdictSubjects.length })} isAdmin={user?.role === 'admin'}
                    disabled={verdictSubjects.length === 0 || bulkBusy}
                    onConfirm={c => { void bulkVerdict('noise', c); }} />
                </>
              )}
              {isAdmin && (
                <Button variant="secondary" size="sm" disabled={bulkCloseable === 0 || bulkBusy}
                  onClick={() => { void bulkResolve(); }} data-testid="bulk-resolve">
                  {tf('inbox.bulk.close', lang, { c: bulkCloseable })}
                </Button>
              )}
              {user?.role === 'admin' && (
                <Button variant="danger" size="sm" disabled={bulkCloseable === 0 || bulkBusy}
                  onClick={() => setDelOpen(true)} data-testid="bulk-delete">
                  {tf('inbox.bulk.delete', lang, { c: bulkCloseable })}
                </Button>
              )}
              <AlertDialog open={delOpen} onOpenChange={setDelOpen}>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>{tf('inbox.del.title', lang, { c: bulkCloseable })}</AlertDialogTitle>
                    <AlertDialogDescription>{tl('inbox.del.body', lang)}</AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{tl('inbox.del.cancel', lang)}</AlertDialogCancel>
                    <AlertDialogAction onClick={() => { void bulkDelete(); }} data-testid="bulk-delete-confirm">{tl('inbox.del.confirm', lang)}</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
              <Button variant="secondary" size="sm" disabled={bulkBusy}
                onClick={() => { setPicked(new Set()); setBulkNote(null); }}>
                {tl('inbox.bulk.clear', lang)}
              </Button>
            </span>
          </div>
        )}

        {showingStale && (
          <div style={{ marginBottom: 8 }}>
            <span className="badge b-warn">
              {tl('inbox.stale', lang)}
            </span>
          </div>
        )}
        {/* v0.9.487 — "bakmadığın türlerde" + "öncelik filtresi dışında"
            şeritleri operatör kararıyla kaldırıldı; sayılar facet
            chip'lerinde yaşıyor. */}
        {/* v0.10.2122 (S2) — üst sayım cümlesi: tek satır, akışta (yapışkan değil). */}
        {countSentence && <p className="ib-summary" data-testid="inbox-count-sentence">{countSentence}</p>}
        {scanCapped && (
          <div style={{ marginBottom: 8 }}>
            <span className="badge b-warn"
              title={tl('inbox.scanCapped.title', lang)}>
              {tl('inbox.scanCapped', lang)}
            </span>
          </div>
        )}
        {capped && (
          <div style={{ marginBottom: 8 }}>
            {/* v0.10.1081 — kırpma SEÇİLİ sıralamaya göre (varsayılan ilk
                görülme); öncelik süzgeci tavandan önce uygulanır. */}
            <span className="badge b-warn"
              title="The server filters by the chosen priorities, sorts the whole matching queue by the chosen column and returns the top slice. Narrow by service, team or environment to reach the rest.">
              {tf('inbox.capped', lang, { total: capped.total, shown: capped.shown, sort: capSortLabel(srvSort.id, srvSort.dir, lang) })}
            </span>
          </div>
        )}
        {/* v0.10.1016 — "Problem değil" görünümü: bu işaret bildirimi de
            susturuyor mu (durum herkese; anahtar yalnız admin). */}
        {verdictView === 'noise' && <ProblemVerdictPolicyBar />}
        {/* NOT VirtualTable: rows are variable-height (DetailLine renders a
            multi-line exception message + team chips), which breaks the
            VirtualTable uniform-row assumption. content-visibility keeps the
            >100-row paint cheap while letting each row size to its content. */}
        <div className="table-wrap"
          style={{ opacity: showingStale ? 0.45 : 1, transition: 'opacity 120ms' }}
          aria-busy={showingStale}>
          <table {...dt.tableProps}>
            {/* leading={[34]} — the primitive owns the <colgroup>; a
                second one alongside it is invalid markup and the
                browser silently keeps only the first. */}
            <DataTableColgroup dt={dt} leading={[34]} />
            <DataTableHead dt={dt} leading={
              <th style={{ width: 34, textAlign: 'center' }}>
                {/* Select-all covers the rows CURRENTLY on screen, not the
                    whole server-side queue — the list is a capped top
                    slice, so "all" can only honestly mean what is
                    visible. */}
                <input type="checkbox" aria-label={tl('inbox.selectAll', lang)}
                  title={tl('inbox.selectAll', lang)}
                  checked={allPickedOnScreen}
                  disabled={(filtered?.length ?? 0) === 0}
                  ref={el => { if (el) el.indeterminate = selCount > 0 && !allPickedOnScreen; }}
                  onChange={() => setPicked(prev => {
                    if (allPickedOnScreen) return new Set();
                    const next = new Set(prev);
                    for (const r of (filtered ?? [])) next.add(r.id);
                    return next;
                  })} />
              </th>
            } />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} leading={[34]} {...inboxState} /> : dt.sortedRows.map((it, i) => {
                const rp = dt.rowProps(i);
                const link = rowLinkOf(it); // v0.10.1414 — null: satır açılmaz (işaret yok)
                const spread = spreadOf(it.exception); // v0.10.949
                const reqImpact = stdRequestImpact(it.std, lang); // v0.10.2380
                // v0.10.1109 — Oracle hata tablosu grubu: Exceptions'taki etiket.
                const ora = isExcFamily(it) && isOracleGroup(it.exception!.fingerprint);
                return (
                <tr key={it.id}
                  {...rp}
                  {...(link
                    ? rowClickHandlers(link.to, () => navigate(link.to, { replace: link.replace, state: link.state }))
                    : { 'data-row-action': undefined })}
                  onMouseEnter={() => dt.nav.setSelected(i)}
                  className={[rp.className, 'cv-row'].filter(Boolean).join(' ')}>
                  <td style={{ textAlign: 'center' }}
                    onClick={e => { e.stopPropagation(); }}>
                    {/* stopPropagation: the row itself opens the detail
                        page, and ticking a box must not also navigate. */}
                    <input type="checkbox" checked={picked.has(it.id)}
                      aria-label={tf('inbox.selectRow', lang, { t: it.title })}
                      onChange={() => toggleRow(it.id)} />
                  </td>
                  <td>
                    {/* v0.10.2300 (S3) — gerekçe EN'de anahtardan (TR aynen). */}
                    <PriorityBadge p={it.priority} reason={prioReasonView(it.priorityReason, it.priorityReasonMsg, lang).text} />
                  </td>
                  <td>
                    {/* v0.10.1084 — Exceptions listesiyle ORTAK başlık hücresi
                        (TriageTitleCell). v0.10.1529 (Problem standardı R2) —
                        std varsa: 1. satır özne (mono) · standart manşet, 2. satır
                        kaynak çipi + kanıt cümlesi (exception ailesinde mesaj),
                        3. satır bağlam + takım çipleri + yayılım. std yoksa
                        v0.10.1084 biçimi aynen. */}
                    {(() => {
                      const head = it.std ? splitStdHeadline(it.std, lang) : null;
                      const headV = it.std ? stdHeadlineView(it.std, lang) : null;
                      const evV = it.std ? stdEvidenceView(it.std, lang) : null;
                      const titleText = headV ? headV.text : inboxRowHeadline(it);
                      const titleInner = head
                        ? <>{head.subject && <><span className="std-subject">{head.subject}</span> · </>}{headV?.orig ? <OrigLang>{head.rest}</OrigLang> : head.rest}</>
                        : titleText;
                      const detail = it.std
                        ? <><span className="std-src" title={tf('inbox.srcTitle', lang, { name: stdSourceName(it.std.evidence.source, lang), note: stdSamplingNote(it.std, lang) })}>{stdSourceChip(it.std, lang)}</span>
                            {ora ? oracleInboxDetail(it.exception!.message, oraLabel)
                              : isExcFamily(it) ? <span className="mono">{it.exception?.message}</span>
                                : evV?.orig ? <OrigLang>{evV.text}</OrigLang> : evV?.text}</>
                        : ora ? oracleInboxDetail(it.exception!.message, oraLabel) : <DetailLine it={it} />;
                      return (
                        <TriageTitleCell
                          title={titleText}
                          titleNode={link
                            ? <Link to={link.to} replace={link.replace} state={link.state} onClick={stopRowClick} className="cell-link">{titleInner}</Link>
                            : (head ? titleInner : undefined)}
                          code={!it.std && isExcFamily(it)}
                          chips={<><RowChips it={it} real={verdictOf(it, verdicts) === 'real'} oraLabel={ora ? oraLabel : undefined} frequentEnforce={graphSum.data?.frequentEnforce} />
                            {it.problem && <GraphFrequentChip row={graphSum.data?.rows?.[it.problem.id]} enforce={graphSum.data?.frequentEnforce} />}</>}
                          detail={detail}
                          detailMono={!it.std && isExcFamily(it)}
                          detailTitle={isExcFamily(it) ? it.exception?.message : (evV?.text ?? inboxRowLabel(it) ?? undefined)}
                          ai={it.aiSummary ? stripMarkdown(it.aiSummary) : undefined}
                          aiAge={it.aiSummaryAt ? fmtAgoNs(it.aiSummaryAt) : undefined} />
                      );
                    })()}
                    {it.problem && <GraphRowSub row={graphSum.data?.rows?.[it.problem.id]} folded={!!it.cluster} />}
                    {it.cluster && <GraphClusterMembers cluster={it.cluster} hrefOf={id => inboxRowLink({ to: 'detail', param: 'problem', id }, location.pathname, location.search)} />}
                    <div className="std-ctx">
                      {/* v0.10.2121 — özne Servis, ekip Ekip kolonunda; oluşum sayısı burada. */}
                      {occLine(it, lang) && <span className="ib-occ">{occLine(it, lang)}</span>}
                      {/* v0.10.2380 — hata oranı Problem'inde etki istek sayısıyla (kısa biçim; standardın etki cümlesi). */}
                      {reqImpact && (reqImpact.orig
                        ? <OrigLang><span data-testid="ib-req">{reqImpact.main}</span></OrigLang>
                        : <span data-testid="ib-req">{reqImpact.main}</span>)}
                      {it.std?.context?.map(c => <span key={c}>{c}</span>)}
                      {/* v0.10.949 — aynı exception aynı anda N serviste. */}
                      {spread && (
                        <span title={spreadTitle(spread.n, spread.partners, spreadWin, lang)}
                          style={{ display: 'inline-flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
                          <span className="dot" aria-hidden="true"
                            style={{ width: 6, height: 6, borderRadius: '50%', background: 'currentColor', flex: 'none' }} />
                          {tf('inbox.spread', lang, { n: spread.n })}
                        </span>
                      )}
                    </div>
                  </td>
                  <td>
                    {ora && isSyntheticOracleService(it.service) ? (
                      <span className="mono cell-faint" title={tl('inbox.oraNoSvc', lang)}>{it.service}</span>
                    ) : (
                      <SubjectLink service={it.service} subjectKind={it.subjectKind}
                        href={serviceHref(it.service, { range: inboxItemWindow(it) })}
                        onClick={e => e.stopPropagation()}
                        emptyFallback={<span className="cell-faint">—</span>} />
                    )}
                  </td>
                  <td>
                    <TeamCell it={it} active={teamFilter} onPick={setTeamFilter} />
                  </td>
                  <td>{inboxKindLabel(it, lang)}</td>
                  {/* v0.10.736 — 13 px mono damga sınıfta (ib-when). v0.10.1529 —
                      "Ne zamandan beri": başlangıç + süre · sürüyor; tam damga title'da. */}
                  <td className="ib-when" title={it.startedAt ? tsLong(it.startedAt) : undefined}>
                    {it.std && it.std.since.startedAt
                      ? <>
                          {stdSinceLine(it.std, Date.now(), lang)}
                          <div className="ib-when__ago">{stdDurationLine(it.std, lang)}</div>
                        </>
                      : it.startedAt
                        ? <>
                            <span className="mono">{tsCompact(it.startedAt)}</span>
                            <div className="ib-when__ago">{fmtAgoNs(it.startedAt)}</div>
                          </>
                        : <span style={{ color: 'var(--text3)' }}>—</span>}
                  </td>
                  <td>
                    <div className="ib-owner">
                      {/* v0.10.2125 (S5) — alt "Alert rules" tablosunun "Take it"i
                          buraya taşındı: alarm probleminde editor+ kendine alır /
                          yeniden atar (PATCH /assignee, sunucu editor kapılı, audit
                          var). Viewer ve diğer türler salt okunur. */}
                      {it.kind === 'problem' && it.problem?.id && isAdmin
                        ? <span onClick={stopRowClick}>
                            <AssigneeCell problem={{ id: it.problem.id, assignee: it.assignee }}
                              currentUserEmail={user?.email ?? ''}
                              onChanged={() => { void qc.invalidateQueries({ queryKey: ['inbox'] }); }} />
                          </span>
                        : it.assignee
                          ? <AssigneePill v={it.assignee} />
                          : <span className="cell-faint">—</span>}
                      {/* v0.10.1529 (D1) — satır triyajı hover/focus'ta (pv-row). */}
                      <ProblemVerdictRow item={it} ctx={verdictCtx} />
                    </div>
                  </td>
                </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {/* v0.10.2125 (S5) — alt "Alert rules" tablosu (v0.9.837) KALDIRILDI:
            aynı alarm problemleri yukarıdaki listede. "Take it" İlgilenen
            hücresinde; ROOT CAUSE rozeti ve "Ne oldu?" detayda (?problem=). */}
        {/* v0.10.2124 (S4) — sayfa altı bağlantıları (mockup): durum
            görünümleri mevcut süzgeçleri korur; kural yönetimi /alerts. */}
        <nav className="ib-foot" aria-label={tl('inbox.foot', lang)}>
          <Link to={inboxStatusHref(searchParams, 'all')}>{tl('inbox.foot.resolved', lang)}</Link>
          <span aria-hidden="true">·</span>
          <Link to={inboxStatusHref(searchParams, 'ignored')}>{tl('inbox.foot.ignored', lang)}</Link>
          <span aria-hidden="true">·</span>
          <Link to="/alerts">{tl('inbox.foot.rules', lang)}</Link>
        </nav>
      </div>

      {/* v0.10.1081 — yerinde triyaj çekmecesi (v0.8.292) KALDIRILDI; her
          satır tam sayfa açar (openRow), eski ?item= yönlendirilir. */}
      {problemParam && (
        <>
          {/* Singular: this is ONE problem, reached from a notification deep
              link. Plural "Problems" is the merged queue (v0.9.323). v0.10.2170
              — Topbar host'tan buraya taşındı (görünüm aynı). */}
          <Topbar title="Problem" showEnv envApplies />
          {/* v0.10.1032 (inceleme) — kimlikle anahtarlı: A'dan B'ye geçişte
              açılır bölümlerin ve triyajın meşgul/hata durumu taşınmaz. */}
          <AlertProblemHost
            key={problemParam}
            id={problemParam}
            isAdmin={isAdmin}
            backLabel="← Problems"
            onBack={() => openDetail('problem', null)}
          />
        </>
      )}
      {anomalyParam && <Navigate to={anomalyEventHref(anomalyParam)} replace />}
    </>
  );
}
