package api

// auth_permissions.go — v0.10.1110 (merkezi login yetki servisi).
//
// api.go BÜYÜMEYECEK kuralı (TestApiGoDoesNotGrow): rota kendi dosyasında,
// kayıt init()'te registerRoutesExtra ile.
//
//	POST /api/auth/permissions   sunucudan-sunucuya; oturum YOK, paylaşılan anahtar
//
// SÖZLEŞME (müşterinin merkezi login standardı): IdP, kullanıcı oturumu başına
// BİR kez (token yenilemede değil) çağırır ve 200 cevabındaki `permissions`
// dizisini access token'a claim olarak gömer.
//
//	istek  {"userId","username","email","registrationNumber"}   (JSON, ≤4 KB)
//	200    {"subject":<registrationNumber AYNEN>,"permissions":["COREMETRY_…"],"ttlSeconds":N}
//	204    gövdesiz — YALNIZ devre dışı bırakılmış hesap
//	400    gövde bozuk / registrationNumber yok
//	401    X-Coremetry-Auth-Key yok ya da yanlış (gövdesiz — ayrıntı yok);
//	       anahtarsız kipte hiç dönmez
//	403    çağıranın IP'si izin listesinde değil (gövdesiz; v0.10.1111)
//	404    servis kapalı ya da anahtar tanımlı değil ve anahtarsız kip kapalı
//
// Müşteri subject ≠ registrationNumber ise cevabı yok sayar; 200/204 dışı her
// durum "cevap yok" → token claim'siz basılır. Başarı/başarısızlık yalnız HTTP
// durum kodu; gövdede sonuç kodu yok.
//
// NEDEN OTURUMSUZ: çağıran bir kullanıcı değil, IdP'nin kendisi; auth.SkipPath
// yalnız `POST /api/auth/permissions`'ı muaf tutar (/api/auth/login ve OIDC
// callback ile aynı yer). SINIR: X-Coremetry-Auth-Key başlığı, kayıtlı
// anahtarla auth.OIDCService.PermissionKeyMatches'te SHA-256 özetleri
// üzerinden crypto/subtle ile karşılaştırılır — uç anahtarı hiç tutmaz,
// loglamaz, yankılamaz. Sıra: 404 (kapalı) → 403 (IP izin listesi) → 401
// (anahtar; anahtarsız kipte atlanır) → 400 (gövde) — kimliksiz çağıran
// gövde doğrulamasından bilgi alamasın.
//
// v0.10.1111 (operatör: "Anahtarsız olmaz mı" — müşterinin merkezi login'i
// özel başlık gönderemiyor): permissionServiceAllowNoKey açıkken başlık HİÇ
// denetlenmez; sınır IP/CIDR izin listesi (permissionServiceAllowedCIDRs, boş
// = kısıt yok). Çağıranın IP'si RemoteAddr; X-Forwarded-For yalnız doğrudan
// eş permissionServiceTrustedProxies'teyse, sağdan ilk güvenilmeyen giriş.
//
// HIZ SINIRI: /api/auth/login'in sınırlayıcısı YOK (repo'da login limiter
// bulunmuyor); bu uç da eklemedi. Gerekçe: yanlış anahtar hiçbir depo
// okumasına ulaşmaz (yalnız bir SHA-256), anahtar ≥16 karakter ve yer
// tutucu reddedilir; IP başına kilit ise paylaşılan ingress arkasında meşru
// IdP'yi kilitleyebilirdi. Sayaç (auth_permission_requests_total{result})
// 401 fırtınasını görünür kılar.
//
// EŞLEME SIRASI (operatör kararı), her adım TEK sınırlı okuma (disabled
// satırları da görür, aktif satır önce):
//  1. email → GetUserByEmailAnyState (küçük harf)
//  2. yoksa ldap_username = username, sonra = registrationNumber (küçük harf)
//  3. yoksa TANIMSIZ kullanıcı → 200 + SSO varsayılan rolü (varsayılan viewer
//     → ["COREMETRY_VIEWER"]). Operatör kararı 2026-10-05: "herkesin viewer
//     rolünde login olabilmesi gerekir — kullanıcı ilk defa login olacaksa da
//     viewer"; 204 merkezi login'de girişi engelleyebilir.
//
// 204 YALNIZ devre dışı hesap (Coremetry girişi de onu reddeder,
// oidcLoginUser). Roller Coremetry'de yönetilir; servis yalnız RAPORLAR.
//
// LOG: çağrı başına tek satır, slog DEBUG düzeyinde (varsayılan logger'da
// görünmez); PII olarak yalnız Coremetry kullanıcı kimliği.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

func init() { registerRoutesExtra("auth-permissions", (*Server).registerAuthPermissionsRoutes) }

func (s *Server) registerAuthPermissionsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/permissions", s.authPermissions)
}

const (
	// PermissionAuthHeader — müşteri IdP'sinin anahtarı taşıdığı başlık.
	PermissionAuthHeader = "X-Coremetry-Auth-Key"
	permissionsBodyMax   = 4 << 10
	permissionsFieldMax  = 256
)

// permissionsRequest — müşteri sözleşmesi; bilinmeyen alanlar yok sayılır.
type permissionsRequest struct {
	UserID             string `json:"userId"`
	Username           string `json:"username"`
	Email              string `json:"email"`
	RegistrationNumber string `json:"registrationNumber"`
}

// permissionsResponse — alan sırası sözleşmedeki gibi.
type permissionsResponse struct {
	Subject     string   `json:"subject"`
	Permissions []string `json:"permissions"`
	TTLSeconds  int      `json:"ttlSeconds"`
}

// permUserLookup — iki sınırlı okuma (*chstore.Store karşılar).
type permUserLookup interface {
	GetUserByEmailAnyState(ctx context.Context, email string) (*chstore.User, error)
	GetUserByLdapUsername(ctx context.Context, username string) (*chstore.User, error)
}

// permissionsLookup — YALNIZ test dikişi; üretimde s.store.
var permissionsLookup = func(s *Server) permUserLookup { return s.store }

// ── Sayaç (Self-observability) ───────────────────────────────────────────────

var (
	permCounterOnce sync.Once
	permCounter     metric.Int64Counter
)

// countPermissionResult — result ∈ {ok, default_role, disabled_user,
// unauthorized, ip_denied, service_off, bad_request, error}; kardinalite sabit. Sayaç ilk çağrıda kurulur
// (selfobs.Init'ten SONRA — paket init'inde kurulsa noop meter'a bağlanırdı).
func countPermissionResult(ctx context.Context, result string) {
	permCounterOnce.Do(func() {
		c, err := selfobs.Meter().Int64Counter("auth_permission_requests_total",
			metric.WithDescription("POST /api/auth/permissions çağrıları, sonuca göre"))
		if err != nil {
			log.Printf("[auth-permissions] metric: %v", err)
			return
		}
		permCounter = c
	})
	if permCounter != nil {
		permCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// ── Uç ───────────────────────────────────────────────────────────────────────

func (s *Server) authPermissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cfg := s.oidc.PermissionService() // nil alıcıda güvenli → kapalı
	if !cfg.Usable() {
		countPermissionResult(r.Context(), "service_off")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// v0.10.1111 — ağ sınırı anahtardan ve gövdeden ÖNCE. Çağıranın IP'si
	// clientIP(r) DEĞİL (o XFF'in ilk girişine koşulsuz güvenir): XFF yalnız
	// doğrudan eş güvenilen vekilse okunur (auth/oidc_permission_net.go).
	if ok, ip := cfg.CallerAllowed(r.RemoteAddr, r.Header.Values("X-Forwarded-For")); !ok {
		countPermissionResult(r.Context(), "ip_denied")
		slog.Debug("[auth-permissions] call", "result", "ip_denied", "ip", ip)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// Anahtarsız kip: başlık HİÇ denetlenmez (kayıtlı anahtar olsa da).
	if !cfg.AllowNoKey && !s.oidc.PermissionKeyMatches(r.Header.Get(PermissionAuthHeader)) {
		countPermissionResult(r.Context(), "unauthorized")
		slog.Debug("[auth-permissions] call", "result", "unauthorized")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	in, err := decodePermissionsRequest(w, r)
	if err != nil {
		countPermissionResult(r.Context(), "bad_request")
		slog.Debug("[auth-permissions] call", "result", "bad_request")
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := matchPermissionUser(r.Context(), permissionsLookup(s), in)
	if err != nil {
		countPermissionResult(r.Context(), "error")
		// Depo hata metni çağırana gitmez (dış sistem); log'a yalnız sınıf.
		log.Printf("[auth-permissions] user lookup failed")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if u != nil && u.Disabled {
		countPermissionResult(r.Context(), "disabled_user")
		slog.Debug("[auth-permissions] call", "result", "disabled_user", "user", u.ID)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var perms []string
	result, uid := "ok", ""
	if u != nil {
		uid = u.ID
		perms = auth.PermissionsForRole(u.Role, u.CustomRole, s.customRoleExists(u.CustomRole))
	}
	if len(perms) == 0 {
		// Tanımsız kullanıcı (ya da tanınmayan kayıtlı rol): SSO varsayılan rolü.
		result = "default_role"
		perms = auth.PermissionsForRole(cfg.DefaultRole, "", false)
	}
	countPermissionResult(r.Context(), result)
	slog.Debug("[auth-permissions] call", "result", result, "user", uid)
	writeJSON(w, permissionsResponse{
		Subject:     in.RegistrationNumber, // ALDIĞIMIZ gibi — müşteri birebir karşılaştırır
		Permissions: perms,
		TTLSeconds:  cfg.TTLSeconds,
	})
}

func (s *Server) customRoleExists(name string) bool {
	if name == "" || s.auth == nil {
		return false
	}
	return s.auth.CustomRolePages(name) != nil // nil = katalogda yok
}

var errPermissionsBody = errors.New("geçersiz gövde")

// decodePermissionsRequest — ≤4 KB JSON; registrationNumber zorunlu; her
// alan ≤256. Hata metni girdiyi yankılamaz.
func decodePermissionsRequest(w http.ResponseWriter, r *http.Request) (permissionsRequest, error) {
	var in permissionsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, permissionsBodyMax)).Decode(&in); err != nil {
		return in, errPermissionsBody
	}
	if strings.TrimSpace(in.RegistrationNumber) == "" {
		return in, fmt.Errorf("%w: registrationNumber zorunlu", errPermissionsBody)
	}
	for _, v := range []string{in.UserID, in.Username, in.Email, in.RegistrationNumber} {
		if len(v) > permissionsFieldMax {
			return in, fmt.Errorf("%w: alan çok uzun", errPermissionsBody)
		}
	}
	return in, nil
}

// matchPermissionUser — eşleme sırası (dosya başlığı). (nil, nil) = eşleşme yok.
func matchPermissionUser(ctx context.Context, lk permUserLookup, in permissionsRequest) (*chstore.User, error) {
	if email := strings.ToLower(strings.TrimSpace(in.Email)); email != "" {
		u, err := lk.GetUserByEmailAnyState(ctx, email)
		if err != nil {
			return nil, err
		}
		if u != nil {
			return u, nil
		}
	}
	seen := map[string]bool{}
	for _, cand := range []string{in.Username, in.RegistrationNumber} {
		c := strings.ToLower(strings.TrimSpace(cand))
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		u, err := lk.GetUserByLdapUsername(ctx, c)
		if err != nil {
			return nil, err
		}
		if u != nil {
			return u, nil
		}
	}
	return nil, nil
}

// ── OIDC callback: kullanıcı çözümü + token claim'inden rol ─────────────────

// oidcUserStore — callback yardımcısının dar yüzü (*chstore.Store karşılar).
type oidcUserStore interface {
	GetUserByEmailAnyState(ctx context.Context, email string) (*chstore.User, error)
	UpsertUser(ctx context.Context, u chstore.User) error
	CountAdmins(ctx context.Context) (int64, error)
}

// oidcUserStoreFor — YALNIZ test dikişi; üretimde s.store.
var oidcUserStoreFor = func(s *Server) oidcUserStore { return s.store }

// errOIDCAccountDisabled — giriş sayfasında görünen metin (callback
// oidcFail(err.Error()) ile basar).
var errOIDCAccountDisabled = errors.New("hesap devre dışı — yöneticinize başvurun")

// v0.10.1120 (güvenlik incelemesi) — trustUnverifiedEmail ile gelen giriş
// (claims.ViaTrust, email_verified=false) yerel/LDAP ya da admin hesabına
// BAĞLANMAZ: IdP e-postayı doğrulamadığı için e-posta çarpışması hesap ele
// geçirmeye dönüşebilir. Yeni kullanıcı (varsayılan rol) ve mevcut oidc
// viewer/editor etkilenmez. Loga yalnız kullanıcı id'si (e-posta değil).
const oidcClassEmailUnverifiedPrivileged = "email_unverified_privileged"

// oidcLoginPageError — giriş sayfasında AYNEN görünen cümle (büyük harfle
// başlayan kullanıcı metni; errors.New'ün Go hata dizgisi kuralına girmez).
type oidcLoginPageError struct{ Class, Msg string }

func (e *oidcLoginPageError) Error() string { return e.Msg }

var errOIDCEmailUnverifiedPrivileged error = &oidcLoginPageError{
	Class: oidcClassEmailUnverifiedPrivileged,
	Msg: "Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz; parola/LDAP ile girin veya IdP'de Trust Email açılsın " +
		"(this account cannot be opened via SSO with an unverified email; sign in with password/LDAP or ask for Trust Email on the IdP)",
}

// oidcTrustLinkAllowed — doğrulanmamış (yalnız güven anahtarıyla kabul) bir
// SSO girişi bu kayıtlı kullanıcıya bağlanabilir mi: yalnız oidc kaynaklı ve
// admin olmayan hesap. Boş AuthProvider depoda "local" sayılır. SAF.
func oidcTrustLinkAllowed(u chstore.User) bool {
	return u.AuthProvider == "oidc" && u.Role != auth.RoleAdmin
}

// oidcLoginUser — api.go oidcCallback'in kullanıcı okuması (eski
// s.store.GetUserByEmail çağrısının yerine, AYNI satır; api.go büyümez).
//
// Güvenlik incelemesi F1b: GetUserByEmail disabled=0 süzdüğü için callback
// devre dışı bırakılmış kullanıcıyı "yok" sayıp YENİ bir viewer satırıyla
// yeniden açıyordu (users ORDER BY id). Şimdi disabled satırı da gören okuma;
// aktif satır yoksa ve disabled satır varsa giriş REDDEDİLİR. (nil, nil) =
// gerçekten yeni kullanıcı → callback bugünkü gibi defaultRole ile açar
// (operatör kararı 2026-10-05: "oidc ile kullanıcılar login olduğunda yine
// default viewer olsun").
//
// Ardından roleFromClaim açık ve claim tanınan bir rol taşıyorsa (Exchange
// doldurur) KAYITLI kullanıcının rolü yalnız DÜŞÜRÜLÜR (syncClaimRole). Rol
// yazımı başarısız olursa giriş kayıtlı rolle sürer (log).
func (s *Server) oidcLoginUser(r *http.Request, email string, claims *auth.OIDCClaims) (*chstore.User, error) {
	st := oidcUserStoreFor(s)
	u, err := st.GetUserByEmailAnyState(r.Context(), email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	if u.Disabled {
		log.Printf("[oidc] login refused: user id=%s is disabled", u.ID)
		return nil, errOIDCAccountDisabled
	}
	if claims != nil && claims.ViaTrust && !oidcTrustLinkAllowed(*u) {
		log.Printf("[oidc] login refused: class=%s user id=%s", oidcClassEmailUnverifiedPrivileged, u.ID)
		return nil, errOIDCEmailUnverifiedPrivileged
	}
	if claims != nil && claims.ClaimRole != "" {
		nu, err := s.syncClaimRole(r, st, *u, claims.ClaimRole)
		if err != nil {
			log.Printf("[oidc] role-from-claim sync failed (source=%s): %v", claims.ClaimRoleSource, err)
			return u, nil
		}
		return &nu, nil
	}
	return u, nil
}

// syncClaimRole — claim'den rol YALNIZ DÜŞÜRÜR (güvenlik incelemesi F1a):
// müşteri cevabı önbellekliyor ve token IdP oturumu boyunca yaşıyor; bayat
// bir claim, düşürülmüş (ya da ele geçirildiği için düşürülmüş) bir admin'i
// yeniden yükseltemesin. Yükseltme yalnız Kullanıcılar sayfasından. Aynı ya
// da daha yüksek rol → yazma yok. Son admin düşürülmez (setUserRole ile aynı
// kapı). Değişim audit'lenir: aktör kullanıcının kendisi, aktör rolü ÖNCEKİ
// rol, IP isteğin IP'si, ayrıntıda from→to.
func (s *Server) syncClaimRole(r *http.Request, st oidcUserStore, u chstore.User, role string) (chstore.User, error) {
	// roleRank (mcp_gate.go): admin 2 > editor 1 > viewer 0.
	if !auth.IsValidRole(role) {
		return u, nil
	}
	cur, want := roleRank(u.Role), roleRank(role)
	if want >= cur {
		if want > cur {
			log.Printf("[oidc] role-from-claim: user id=%s claim would raise %s → %s — ignored (raise only via Users page)", u.ID, u.Role, role)
		}
		return u, nil
	}
	if u.Role == auth.RoleAdmin {
		n, err := st.CountAdmins(r.Context())
		if err != nil {
			return u, err
		}
		if n <= 1 {
			log.Printf("[oidc] role-from-claim: last admin id=%s not demoted", u.ID)
			return u, nil
		}
	}
	prev := u.Role
	nu := u
	nu.Role = role
	if err := st.UpsertUser(r.Context(), nu); err != nil {
		return u, err
	}
	if s.meUsers != nil {
		s.meUsers.clear()
	}
	if s.auth != nil {
		s.auth.InvalidateAuthz(nu.ID)
	}
	details, _ := json.Marshal(map[string]any{"email": nu.Email, "from": prev, "to": role, "source": "oidc_claim"})
	s.auditAs(&auth.Claims{UserID: nu.ID, Email: nu.Email, Role: prev}, clientIP(r),
		"user.set_role_from_claim", "user", nu.ID, string(details))
	log.Printf("[oidc] role-from-claim: user id=%s %s → %s", nu.ID, prev, role)
	return nu, nil
}
