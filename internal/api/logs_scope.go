package api

// logs_scope.go — v0.10.1855 (Log Search Faz 2 dilim 2.4; audit §5 risk #2,
// §9-4 kararı — operatör 2026-11-05: varsayılan kapsam `scope=mine`).
//
//	GET /api/logs/scope   → {available, scoped, reason, team?, services[{name, health…}], trimmed?}
//
// İki parça:
//
//  1. `scope=mine` — /logs okumalarının (`/api/logs`, `/api/logs/search`,
//     `/api/logs/timeseries`, `/api/logs/fieldstats`, `/api/logs/patterns`,
//     `/api/logs/stream`) parametresi. Sunucu kullanıcının servislerini çözer
//     (users.team → team_aliases → service_metadata ownerTeam VEYA sreTeam;
//     CoSRE v2 `user_scope` ile AYNI okuma — chat_user_scope.go
//     readUserScopeTeam/readUserScopeServices + resolveUserScopeCore, ≤100
//     servis) ve logstore.Filter.Services'e koyar (ES: aday alanlar × terms).
//     Açık `service=` kazanır (kapsam yok sayılır). Kapsam çözülemezse 400 —
//     sessizce kapsamsız `app-*` sorgusu ATILMAZ (risk #2). Önbellek anahtarı
//     servis kümesinin sıralı FNV özetini taşır (logsScopeKeys — v0.5.187).
//  2. `/api/logs/scope` — /logs'un varsayılan kararı + servis seçicinin
//     "Servislerim · sağlık son 5 dk" grubu. Sağlık service_summary_5m'den
//     (MV, tek IN okuması) + açık Problem sayıları; kural /api/services'in
//     scoreHealth'i. Rol kapısı YOK (viewer da kendi kapsamını görür);
//     kimliksiz → kapsamsız.
//
// Yalnız Elasticsearch log yolunda (operatör 2026-11-06: prod logları ES'te).
// CH log yolunda `available:false` döner ve `scope=` YOK SAYILIR — sayfa ve
// istekler bugünkü gibi (yeni özellik orada sessizce kapalı).
//
// Neden ayrı uç, /api/logs/my-team değil: o uç takım ADINI döner (team=
// süzgeci ES takım alanına bakar); kapsam servis KÜMESİDİR (katalog sahipliği).
// İki mekanizma farklı sonuç verebilir (§9-4); team= elle süzgeç olarak kalır.
// api.go BÜYÜMEZ: rota bu dosyada, kayıt init()'te.

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func init() { registerRoutesExtra("logs_scope", (*Server).registerLogsScopeRoutes) }

func (s *Server) registerLogsScopeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs/scope", s.getLogsScope)
}

const (
	// logsScopeTTL — kullanıcı → servis kümesi çözümünün süreç-içi ömrü ve
	// /api/logs/scope önbelleği (sahiplik dakikalar içinde değişmez).
	logsScopeTTL = 60 * time.Second
	// logsScopeHealthWindow — seçicideki sağlık penceresi (mockup "son 5 dk").
	logsScopeHealthWindow = 5 * time.Minute
	// logsScopeCacheMax — süreç-içi kapsam haritasının tavanı (aşınca boşalır).
	logsScopeCacheMax       = 2000
	logsScopeResolveTimeout = 3 * time.Second
)

// logsScopeEnabled — v0.10.1877: "Servislerim" kapsamı (scope=mine) KAPALI.
//
// Operatör bildirimi (prod, 2026-11-06): 1876'da Elasticsearch'te aşırı CPU ve
// bellek; 1851'e dönünce düzeldi. İki sürüm arasında /logs'un HER açılışta
// kendiliğinden attığı tek yeni ES yükü bu kapsamdı (v0.10.1855): takımı
// tanımlı kullanıcıda liste + histogram + seviye sayımı + desen sorgularının
// hepsi, 100 servise kadar bir kümeyi 5 aday alan × 2 biçim `terms` yan
// tümcesiyle (yan tümce başına ~200 değer) TÜM `app-*` indekslerinde arıyordu.
// Tek servis seçiliyken sorgu o servisin TEK indeksine gider; kapsamda bu
// daraltma yoktu (1855'in bilinen sapması). Per-servis indeks düzeninde
// (binlerce indeks, 10B+ log/gün) çok değerli `terms` yan tümcesi her
// segmentte çalışır.
//
// Kök neden ES görev çıktısıyla KANITLANMADI (operatör geri aldı, yük gitti);
// bu en olası neden ve kapatmak sayfayı prod'da sağlam olduğu bilinen 1851
// davranışına döndürür. Yetenek kapalıyken uç `available:false` döner ve
// istemci bugünkü "yetenek yok" yolunu izler (CH log yoluyla aynı): kapsam
// parametresi gitmez, çip ve "Servislerim" grubu çizilmez, eski `scope=mine`
// linkleri kapsamsız açılır.
//
// Yeniden açma koşulu: kapsam, servisleri İNDEKSLERE çözmeli (tek servis
// yolundaki şablon çözümü gibi) ve çok değerli `terms` tüm indekslere
// gitmemeli; prod ölçümüyle. Testler bu değişkeni açarak kapsam mantığını
// sınamaya devam eder.
var logsScopeEnabled = false

// logsScopeAvailable — yetenek açıksa ve yalnız ES log yolunda.
func (s *Server) logsScopeAvailable() bool {
	return logsScopeEnabled && s != nil && s.logs != nil && s.logs.Backend() == "elasticsearch"
}

// logsScopeParam — SAF: `scope` değeri doğrulaması. "" / "all" → kapsam yok;
// "mine" → kullanıcının servisleri; başka değer → geçersiz (400).
func logsScopeParam(raw string) (mine, ok bool) {
	switch strings.TrimSpace(raw) {
	case "", "all":
		return false, true
	case "mine":
		return true, true
	}
	return false, false
}

// logsServicesKey — SAF: servis kümesinin anahtar parçası; küme yoksa ""
// (kapsamsız anahtarlar BAYT-BAYT aynı kalır).
func logsServicesKey(f logstore.Filter) string {
	if len(f.Services) == 0 || f.Service != "" {
		return ""
	}
	return ":svcs=" + fnvStr(logsTeamSorted(f.Services)...)
}

// logsScopeKeys — /logs okuma anahtarlarının kapsam kuyruğu: takım + servis kümesi.
func logsScopeKeys(f logstore.Filter) string {
	return logsTeamKey(f) + logsServicesKey(f)
}

type logsScopeEntry struct {
	scope userScope
	at    time.Time
}

// logsScopeCache — kullanıcı → çözülmüş kapsam (60 s). Her /logs okuması
// kapsamı ister; users + service_metadata okuması istek başına tekrarlanmasın.
var logsScopeCache = struct {
	sync.Mutex
	m map[string]logsScopeEntry
}{m: map[string]logsScopeEntry{}}

// logsUserScope — kullanıcının kapsamı (önbellekli). Okuma hatası önbelleğe
// GİRMEZ (geçici hata 60 s boyunca "servis seç" demesin).
func (s *Server) logsUserScope(ctx context.Context, uid string) userScope {
	now := time.Now()
	logsScopeCache.Lock()
	if e, ok := logsScopeCache.m[uid]; ok && now.Sub(e.at) < logsScopeTTL {
		logsScopeCache.Unlock()
		return e.scope
	}
	logsScopeCache.Unlock()
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), logsScopeResolveTimeout)
	defer cancel()
	in := userScopeInput{Enabled: true}
	s.readUserScopeTeam(c, &in, uid)
	s.readUserScopeServices(c, &in)
	us := resolveUserScopeCore(in)
	if us.Reason != scopeReasonError {
		logsScopeCache.Lock()
		if len(logsScopeCache.m) >= logsScopeCacheMax {
			logsScopeCache.m = map[string]logsScopeEntry{}
		}
		logsScopeCache.m[uid] = logsScopeEntry{scope: us, at: now}
		logsScopeCache.Unlock()
	}
	return us
}

func requestUserID(r *http.Request) string {
	if c := auth.FromContext(r.Context()); c != nil {
		return c.UserID
	}
	return ""
}

// applyLogsScopeParam — `scope=mine` → Filter.Services. Geçersiz değer ya da
// çözülemeyen kapsam → 400 JSON ve false. Açık servis varsa ya da log yolu CH
// ise no-op. Önbellek anahtarından ÖNCE çağrılır.
func (s *Server) applyLogsScopeParam(w http.ResponseWriter, r *http.Request, q url.Values, f *logstore.Filter) bool {
	mine, ok := logsScopeParam(q.Get("scope"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "geçersiz kapsam (scope=mine | all)")
		return false
	}
	if !mine || f.Service != "" || !s.logsScopeAvailable() {
		return true
	}
	us := s.logsUserScope(r.Context(), requestUserID(r))
	if us.Unscoped || len(us.Services) == 0 {
		writeJSONError(w, http.StatusBadRequest, "kapsam çözülemedi ("+us.Reason+") — bir servis seçin")
		return false
	}
	f.Services = logsTeamSorted(us.Services)
	return true
}

// logsScopeService — seçicideki "Servislerim" satırı.
type logsScopeService struct {
	Name         string  `json:"name"`
	Health       string  `json:"health,omitempty"` // green | yellow | red | "" (pencerede span yok)
	HealthReason string  `json:"healthReason,omitempty"`
	ErrorRate    float64 `json:"errorRate,omitempty"`
	OpenProblems int     `json:"openProblems,omitempty"`
}

type logsScopePayload struct {
	Available bool               `json:"available"`
	Scoped    bool               `json:"scoped"`
	Reason    string             `json:"reason"`
	Team      string             `json:"team,omitempty"`
	Services  []logsScopeService `json:"services"`
	Trimmed   int                `json:"trimmed,omitempty"`
}

// logsScopeKey — SAF: kullanıcı + yetenek (hash-all-inputs; sağlık penceresi
// "şimdi"ye göreli ve TTL'le sınırlı).
func logsScopeKey(uid string, available bool) string {
	a := "0"
	if available {
		a = "1"
	}
	return "logs-scope:v1:" + fnvStr(uid, a)
}

// logsScopeHealth — SAF: MV satırları + açık Problem sayıları → seçici satırları
// (kapsam sırasıyla; pencerede span yoksa sağlık boş — "veri yok", uydurulmaz).
func logsScopeHealth(services []string, rows []chstore.ServiceSummary, open map[string]chstore.OpenProblemCounts) []logsScopeService {
	byName := make(map[string]*chstore.ServiceSummary, len(rows))
	for i := range rows {
		byName[rows[i].Name] = &rows[i]
	}
	out := make([]logsScopeService, 0, len(services))
	for _, name := range services {
		c := open[name]
		row := logsScopeService{Name: name, OpenProblems: c.Critical + c.Warning + c.Info}
		if sv := byName[name]; sv != nil && sv.SpanCount > 0 {
			row.Health, row.HealthReason = scoreHealth(sv, c)
			row.ErrorRate = sv.ErrorRate
		} else if c.Critical > 0 || c.Warning > 0 {
			zero := chstore.ServiceSummary{}
			row.Health, row.HealthReason = scoreHealth(&zero, c)
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) getLogsScope(w http.ResponseWriter, r *http.Request) {
	available := s.logsScopeAvailable()
	uid := requestUserID(r)
	if !available || uid == "" || s.store == nil {
		reason := scopeReasonNoIdentity
		if !available {
			reason = "unavailable"
		}
		writeJSON(w, logsScopePayload{Available: available, Reason: reason, Services: []logsScopeService{}})
		return
	}
	s.serveCached(w, r, logsScopeKey(uid, available), logsScopeTTL, func(ctx context.Context) (any, error) {
		us := s.logsUserScope(ctx, uid)
		out := logsScopePayload{Available: true, Scoped: !us.Unscoped, Reason: us.Reason, Team: us.Team, Trimmed: us.Trimmed, Services: []logsScopeService{}}
		if us.Unscoped || len(us.Services) == 0 {
			return out, nil
		}
		svcs := append([]string(nil), us.Services...)
		sort.Strings(svcs)
		to := time.Now()
		rows, err := s.store.GetServicesAggFilteredIn(ctx, to.Add(-logsScopeHealthWindow), to, "", svcs, "", "", len(svcs), 0)
		if err != nil {
			rows = nil // sağlık okunamadı: kapsam yine döner, sağlık boş (uydurulmaz)
		}
		open, err := s.store.GetOpenProblemCountsByService(ctx)
		if err != nil {
			open = nil
		}
		out.Services = logsScopeHealth(svcs, rows, open)
		return out, nil
	})
}
