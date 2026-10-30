// cosreAsk — v0.10.1648 (CoSRE v2 Faz 2b B2a, plan §10: "v2, v1'in açılış
// ekranını ve composer'ını AYNEN kullanır"). CopilotChat'in soru bağlamı,
// composer girdisi ve karşılama verisi kancaları; gövdeler CopilotChat.tsx'ten
// satır satır taşındı (davranış ve sorgu anahtarları aynı). Tüketiciler:
// v1 CopilotChat + v2 components/cosre/conversation (yalnız cevap yolu farklı).
// Bileşen dosyasında kanca dışa aktarmak react-refresh uyarısıydı → ayrı dosya.
import { useCallback, useMemo, useRef, useState } from 'react';
import { useLocation, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useCopilotStarters } from '@/lib/queries/copilot'; // v0.10.702
import { pageContext } from '@/lib/pageContext';
import { usePageOverlay, withPageOverlay } from '@/lib/pageContextOverlay'; // v0.10.1435
import { legacyFromPinned, readPin, writePin } from '@/lib/pinnedContext';
import type { PageContext } from '@/lib/types';
import { useAuth } from '@/components/AuthProvider';
import { useUrlRange } from '@/lib/useUrlRange';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { timeRangeToNs } from '@/lib/utils';
import { contextStarter, serviceFromRoute } from '@/lib/chatContext';
import { useComposerDraft } from './composerDraft'; // v0.10.1145 — konuşma başına taslak
import { useNameCompletion } from './useNameCompletion'; // v0.10.687 — D4 ad tamamlama
import { applyCompletion, type CompletionItem } from './chatCompletion';
import { useChatProfile } from './chatProfileStore'; // v0.10.1138 — kullanıcı başına seçim
import { useDeepThink } from './deepThink'; // v0.10.1150 — "Derin düşün"
import { useCodeRead } from './codeRead'; // v0.10.1461 — "Kod" hapı (bağımsız sohbet kod turu)
import { hasDrawerContext, parseChatInput, scopeChips, scopeForRequest } from './chatScope'; // v0.10.1138 — @/ kapsamı
import { applyContextExclusions, contextBarItems, type ChatRequestContext, type ContextKey } from './chatContextScope';
import { useCopilotConfig } from './useCopilotEnabled'; // v0.10.483
import { greetErrors, greetTooltip } from './greeting';

/** Soru bağlamı: model/Derin/Kod seçimleri + sayfa/pin/aralık/env kapsamı. */
export function useCosreAskContext() {
  // v0.10.483 — config TEK kaynaktan (useCopilotConfig: modül cache'i, AIDrawer
  // ile aynı); eskiden CopilotChat kendi effect'iyle ikinci bir istek atıyordu.
  const cfg = useCopilotConfig(true);
  const enabled: boolean | null = cfg ? cfg.enabled : null;
  // v0.10.183 — model profili seçici (>1 profil); boş = sunucu varsayılanı.
  const profiles = useMemo(() => cfg?.profiles ?? [], [cfg]);
  const defaultProfile = cfg?.defaultProfile ?? '';
  // v0.10.461 — başlık meta şeridindeki model çipi (AIDrawer ile aynı anatomi).
  const model = cfg?.model ?? ''; // v0.10.483 — config tek kaynaktan (useCopilotConfig)
  // v0.10.1138 — seçim kullanıcı başına kalıcı (chatProfileStore); rozet etkin modeli gösterir.
  const { user } = useAuth();
  const { profile, setProfile, activeModel } = useChatProfile(user?.id, profiles, defaultProfile, model);
  const [deep, setDeep] = useDeepThink(user?.id); // v0.10.1150 — "Derin düşün" (kullanıcı başına, kalıcı)
  // v0.10.1461 — "Kod" hapı: yalnız DevOps bağlıyken (config.codeRead var) çizilir;
  // sunucu kod okuyamıyorsa (yerel profil yok) devre dışı + gerekçe, istek gövdesine gitmez.
  const [codePill, setCodePill] = useCodeRead(user?.id);
  const codeRead = cfg?.codeRead;
  const code = codePill && !!codeRead?.available;

  // Context-awareness (v0.9.164) — bulunulan sayfanın servisi. Mesaj servis
  // adı taşımıyorsa backend guided router bunu varsayılan alır ("neden yavaş?"
  // servis sayfasında → o servis). Banner scope'u şeffaf gösterir.
  const loc = useLocation();
  // v0.10.539 (Faz 3.2) — sayfa bağlamı: URL'den saf serileştirici; rota
  // değişince yeniden hesaplanır (chat AppShell'de tek mount).
  // v0.10.1435 — sayfanın yayınladığı grafik seçimleri (brush + seçili seri;
  // lib/pageContextOverlay.ts) bağlama katılır: "bu tepe" soruları onları görür.
  const pageOverlay = usePageOverlay();
  const page = useMemo(() => withPageOverlay(pageContext(loc.pathname, loc.search), pageOverlay), [loc.pathname, loc.search, pageOverlay]);
  // v0.10.540 (Faz 3.2b) — PIN: operatör sayfa bağlamını sohbete sabitler;
  // sayfa değişse de özne kalır. Eski düz alanlar pinden türetilir (guided/
  // drawer kademeleri sunucu değişikliği olmadan pini izler); sunucu önsözde
  // pini ÖNCE yazar. Oturuma bağlı (sessionStorage), konuşma temizlenince düşer.
  const [pinned, setPinnedState] = useState<PageContext | null>(() => readPin());
  const setPinned = useCallback((ctx: PageContext | null) => { setPinnedState(ctx); writePin(ctx); }, []);
  const pinnedLegacy = useMemo(() => (pinned ? legacyFromPinned(pinned) : null), [pinned]);
  const [sp, setSp] = useSearchParams();
  // v0.9.653 — ekrandaki özneden türeyen başlangıç çipi. Saf çözümleyici
  // (lib/chatContext.ts); rota değişince kendiliğinden güncelleniyor.
  const ctxStarter = useMemo(
    () => contextStarter(loc.pathname, loc.search),
    [loc.pathname, loc.search]);
  // v0.9.1226 — saf çözümleyiciye taşındı (chatContext.serviceFromRoute):
  // yalnız /service|/pod değil, ?service= taşıyan tüm liste rotaları
  // bağlamı devrediyor; kapsam bandı + guided ctxService bedavaya doldu.
  const currentService = useMemo(
    () => serviceFromRoute(loc.pathname, loc.search),
    [loc.pathname, loc.search]);
  // v0.9.537 (operatör raporu: "ekrandaki trace'i anlamıyor") — trace
  // sayfasındayken adresteki ID bağlam olarak gider; "bu trace neden
  // yavaş" sunucuda guidedTraceByID'ye oturur. Mesaja yapıştırılan
  // açık 32-hex her zaman bundan güçlüdür.
  const currentTrace = useMemo(
    () => (loc.pathname === '/trace' ? (sp.get('id') || '') : ''),
    [loc.pathname, sp]);
  // Operation-awareness (v0.9.184) — servis sayfasında seçili ?op=. "bu
  // operasyonun durumu" gibi bir soru guided router'da RED'i o span-name'e
  // daraltır (resolveGuidedOperation'ın context fallback'i).
  const currentOp = useMemo(() => {
    if (loc.pathname === '/service' || loc.pathname === '/service/backtrace') {
      return sp.get('op') || '';
    }
    return '';
  }, [loc.pathname, sp]);

  // v0.9.529 — ekrandaki zaman aralığı. Ev kuralı: timeRangeToNs bare
  // JSX'te ASLA (v0.5.184 sonsuz refetch); burada useMemo içinde ve
  // yalnız pencere kimliğinden türüyor, `now()` okunmuyor.
  const [range] = useUrlRange();
  // v0.9.1259 — env devri: ekrandaki global env seçimi sohbete bağlam
  // olarak gider (rangeS aynası); kapsam bandı da gösterir.
  const [env] = useUrlEnv();
  const rangeS = useMemo(() => {
    const { from, to } = timeRangeToNs(range);
    const s = Math.round((to - from) / 1e9);
    return s > 0 ? s : undefined;
  }, [range]);
  // v0.10.33 — MUTLAK pencerenin BİTİŞ anı. rangeS pencereyi SÜREYE
  // çökertiyor ve sunucu onu şimdiye yeniden çapalıyordu: dün gece
  // 03:00-04:00'a zoom yapıp soru sorunca cevap aynı UZUNLUKTA ama
  // BUGÜNKÜ pencereden geliyordu. Sayılar gerçek olduğu için hata
  // sessizdi.
  //
  // ⚠ YALNIZ custom/zoom aralıkta gönderiliyor. Göreli aralıkta ("son 1
  // saat") çıpayı sabitlemek, uzun bir soruşturmada cevabı DONDURUR:
  // operatör yirmi dakika sonra "şimdi nasıl" diye sorduğunda hâlâ
  // yirmi dakika önceki pencereyi görürdü.
  const toMs = useMemo(
    () => (range.preset === 'custom' && range.toMs ? range.toMs : undefined),
    [range],
  );

  // v0.10.1274 (CoSRE adım 5) — bağlam şeridi: gönderilecek kapsam composer'ın
  // üstünde görünür; ✕ ile çıkarılan öğe BU KONUŞMA için istekten düşer
  // (chatContextScope.ts: düz alan + sayfa ikizi). Env Topbar'ın global
  // seçicisinden (useUrlEnv, aynı URL/sticky kaynağı) — "Tüm ortamlar" = ''.
  const [ctxExcluded, setCtxExcluded] = useState<ReadonlySet<ContextKey>>(() => new Set());
  const screenReqCtx = useMemo<ChatRequestContext>(() => ({
    // v0.10.540 — pin varken eski alanlar pinden (boş alan = kapsamsız, ekrandan DEĞİL).
    service: pinnedLegacy ? (pinnedLegacy.service ?? '') : currentService,
    operation: pinnedLegacy ? (pinnedLegacy.operation ?? '') : currentOp,
    rangeS: pinnedLegacy ? pinnedLegacy.rangeS : rangeS,
    toMs: pinnedLegacy ? pinnedLegacy.toMs : toMs,
    trace: pinnedLegacy ? (pinnedLegacy.trace ?? '') : currentTrace,
    env: pinnedLegacy ? (pinnedLegacy.env ?? '') : env,
    page, // v0.10.539 — sayfa bağlamı protokolü (lib/pageContext, her turda)
    pinnedPage: pinned ?? undefined, // v0.10.540 — sabitlenmiş bağlam
  }), [pinnedLegacy, currentService, currentOp, rangeS, toMs, currentTrace, env, page, pinned]);
  const reqCtx = useMemo(() => applyContextExclusions(screenReqCtx, ctxExcluded), [screenReqCtx, ctxExcluded]);
  const baseCtxItems = useMemo(() => contextBarItems(reqCtx), [reqCtx]);
  // v0.10.1139 — kapsam çipleri gönderilecek hâli gösterir: trace bağlamında
  // @-kapsamı düşer (useChatThread aynı kuralı uygular), açık /komut kalır.
  const chipTrace = pinnedLegacy ? (pinnedLegacy.trace ?? '') : currentTrace;
  // v0.10.1128 — wiki ipucu/çipi yalnız sohbet bu kullanıcıya wiki'den cevap
  // verebiliyorsa (/api/copilot/config `wiki`; yeni istek yok).
  const wikiOn = cfg?.wiki === true;

  return {
    cfg, enabled, profiles, defaultProfile, user, profile, setProfile, activeModel,
    deep, setDeep, codePill, setCodePill, codeRead, code,
    loc, page, pinned, setPinned, pinnedLegacy, sp, setSp, ctxStarter,
    currentService, currentTrace, env,
    ctxExcluded, setCtxExcluded, reqCtx, baseCtxItems, chipTrace, wikiOn,
  };
}
export type CosreAskContext = ReturnType<typeof useCosreAskContext>;

/** Composer girdisi: konuşma başına taslak, ad tamamlama, @/ kapsam çipleri. */
export function useCosreComposerInput(conversationId: string | null, page: PageContext, chipTrace: string) {
  // v0.10.1145 — composer taslağı konuşma başına sessionStorage'da (composerDraft):
  // konuşma değişince / yenilemede kaybolmaz, gönderimde silinir. (Konuşma
  // kimliği useChatThread'den geldiği için durum onun ALTINDA kurulur.)
  const draft = useComposerDraft('chat', conversationId);
  const input = draft.value;
  const setInput = draft.setValue;
  // v0.10.687 — D4 ad tamamlama: imleç konumu + aday listesi (sunucu araması).
  const [caret, setCaret] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const completion = useNameCompletion(input, caret);
  // v0.10.1138 — tamamlamadan SEÇİLEN servis anmaları (biçimden bağımsız kapsam olur).
  const [chosen, setChosen] = useState<string[]>([]);
  const parsedInput = useMemo(() => parseChatInput(input, chosen), [input, chosen]);
  const composerChips = useMemo(
    () => scopeChips(scopeForRequest(parsedInput, hasDrawerContext({ trace: chipTrace, page }))),
    [parsedInput, chipTrace, page]);
  // v0.10.1145 — operatörün "yeni konuşma"sı AÇIK geçiştir: yeni konuşmanın kayıtlı
  // taslağı yüklenir, ekrandaki taslak eski konuşmasında kalır (composerDraft.expect).
  const expectDraft = draft.expect;
  // v0.10.702 — şablon çipi: composer'ı doldurur, GÖNDERMEZ; imleç başta,
  // operatör yolu yazar ("/api/x hatalı trace'lerini getir" → endpoint_traces).
  // v0.10.1128 — genelleştirildi: yetenek satırları ve wiki çipi de aynı
  // "doldur + imleci koy + odakla, GÖNDERME" yolunu kullanır.
  const prefill = (text: string, at: number) => {
    setInput(text);
    setCaret(at);
    requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(at, at);
    });
  };
  const acceptCompletion = (item: CompletionItem | undefined) => {
    if (!completion.cq || !item) return;
    const r = applyCompletion(input, completion.cq, item);
    const svc = item.service;
    if (svc) setChosen(c => (c.includes(svc) ? c : [...c, svc]));
    setInput(r.text);
    completion.dismiss();
    requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(r.caret, r.caret);
      setCaret(r.caret);
    });
  };
  return { input, setInput, caret, setCaret, textareaRef, completion, chosen, setChosen, composerChips, expectDraft, prefill, acceptCompletion };
}
export type CosreComposerInput = ReturnType<typeof useCosreComposerInput>;

/** Karşılamanın canlı yarısı: hata özeti + takımın varsayılanı. `active` =
 *  copilot açık · pencere açık · henüz konuşma yok (yalnız karşılama ekrandayken). */
export function useCosreWelcomeData(active: boolean, env: string, { greeting = true }: { greeting?: boolean } = {}) {
  // Karşılamanın canlı yarısı (v0.9.528). `enabled` ÜÇ koşulu birden
  // taşır: copilot açık, pencere açık, ve henüz konuşma başlamamış —
  // yani sorgu SADECE karşılama gerçekten ekrandayken koşar. Kapalı
  // pencerede ya da sohbet ortasında tek istek bile gitmez.
  // v0.10.1255 (operatör: "P1 yerine son 1 saatteki exception / hata sayısı")
  // — filo hata (MV) + aktif exception grubu; yalnız karşılama ekrandayken.
  // v0.10.1278 (operatör 2026-10-17: sayı tüm hatalı span'lerdi) — hatalı
  // GİRİŞ isteği + açık/yeni exception grubu; global env (Topbar'la aynı
  // useUrlEnv kaynağı) sorguya VE anahtara girer.
  const greetQ = useQuery({
    queryKey: ['cosre-greeting', env],
    queryFn: () => api.cosreGreeting(env),
    // v2 karşılaması hata hapını çizmez (yerini brifing aldı) → istek de gitmez.
    enabled: active && greeting,
    staleTime: 60_000,
  });
  const greetLine = greetErrors(greetQ.data ?? undefined);
  const greetTitle = greetTooltip(greetQ.data ?? undefined);
  const greetAlert = !!greetQ.data && (greetQ.data.failedRequests1h > 0 || greetQ.data.openExceptionGroups1h > 0);
  // v0.10.702 — VERİ çipleri: takımın en kötü servisi + o servisin en çok
  // hata alan yolu (sunucu, 60 s). Yalnız çekmece açık + sohbet boşken.
  // v0.10.1382 — gruplu seçenekler (servis / operasyon / P1 / deploy) yetenek
  // satırlarının altında (CosreCapabilities); env yalnız P1'e uygulanır.
  // v0.10.1402 — `team`: sunucunun çözdüğü kullanıcı takımı (Takımım kartının varsayılanı).
  const starters = useCopilotStarters(active, env);
  return { greetLine, greetTitle, greetAlert, team: starters.team, teamSettled: starters.settled };
}
export type CosreWelcomeData = ReturnType<typeof useCosreWelcomeData>;
