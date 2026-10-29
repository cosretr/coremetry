package api

// users_block.go — v0.10.1558 (operatör: "kullanıcıları bloklama özelliği de
// olsun. o kullanıcı login olamasın").
//
//   POST   /api/users/{id}/block     → aktif hesabı engeller
//   POST   /api/users/{id}/unblock   → engeli kaldırır
//   DELETE /api/users/{id}           → silme (api.go'dan buraya taşındı; kayıt
//                                      satırı api.go'da kalır)
//
// Engel = users.disabled = 2 (chstore.userBlocked). Silmeden farkı: hesap
// listede "Engelli" rozetiyle KALIR ve geri alınabilir. Blocked ⇒ Disabled
// olduğu için şifre girişi (GetUserByEmail), açık oturumlar (LiveAuthz, ≤10 sn;
// bu pod'da InvalidateAuthz ile anında) ve SSO (oidcLoginUser u.Disabled) ek
// kod olmadan reddeder. Tek açık yol LDAP'tı: GetUserByEmail engelli satırı
// görmediği için loginViaLDAP hesabı "ilk kez gelen" sanıp YENİ viewer satırı
// açardı — refuseBlockedLDAPLogin (engelli satır SAYIMI) bunu kapatır. Silinmiş (engellenmemiş) LDAP
// hesabının yeniden açılması bugünkü gibi sürer (operatör kararı bekliyor).
//
// Kapı yalnız admin; her yazım audit'e (user.block / user.unblock /
// user.delete). Kendini engelleme ve son aktif admini engelleme reddedilir —
// ikisi de kimsenin giremediği bir sistem bırakır.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("users-block", (*Server).registerUserBlockRoutes) }

func (s *Server) registerUserBlockRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/users/{id}/block",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.blockUser)))
	mux.Handle("POST /api/users/{id}/unblock",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.unblockUser)))
}

// errUserBlocked — LDAP girişinde dönen hata; login yalnız loglar, istemciye
// her başarısız giriş gibi "invalid credentials" gider (hesabın varlığı sızmaz).
var errUserBlocked = errors.New("account blocked")

// blockRefusal — SAF: engelleme isteğinin reddi (0 = izin). lastAdmin yalnız
// hedef admin olduğunda anlamlı; çağıran CountAdmins'i yalnız o zaman okur.
func blockRefusal(callerID string, target *chstore.User, lastAdmin bool) (int, string) {
	switch {
	case target == nil || target.Disabled:
		return http.StatusNotFound, "user not found"
	case callerID != "" && callerID == target.ID:
		return http.StatusBadRequest, "cannot block your own account"
	case target.Role == auth.RoleAdmin && lastAdmin:
		return http.StatusBadRequest, "cannot block the last admin"
	}
	return 0, ""
}

// refuseBlockedLDAPLogin — loginViaLDAP'ın provisioning'den ÖNCEKİ kapısı.
// Okuma hatası girişi durdurur (fail-closed): engel kontrolü yapılamadıysa
// hesabı açmak engeli delmek olurdu. Kontrol bir SAYIM (chstore.LoginBlocked),
// tekil satır okuması değil — neden: loginBlockedQuery başlığı.
func (s *Server) refuseBlockedLDAPLogin(ctx context.Context, email, username string) error {
	blocked, err := s.store.LoginBlocked(ctx,
		strings.ToLower(strings.TrimSpace(email)), strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return err
	}
	if blocked {
		return errUserBlocked
	}
	return nil
}

func (s *Server) blockUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	callerID := ""
	if caller := auth.FromContext(r.Context()); caller != nil {
		callerID = caller.UserID
	}
	target, err := s.store.GetUserByIDAnyState(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	lastAdmin := false
	if target != nil && target.Role == auth.RoleAdmin {
		n, err := s.store.CountAdmins(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		lastAdmin = n <= 1
	}
	if code, msg := blockRefusal(callerID, target, lastAdmin); code != 0 {
		writeJSONError(w, code, msg)
		return
	}
	if err := s.store.SetUserBlocked(r.Context(), id, true); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"email": target.Email, "role": target.Role})
	s.audit(r, "user.block", "user", id, string(details))
	s.auth.InvalidateAuthz(id)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) unblockUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.store.GetUserByIDAnyState(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if target == nil || !target.Blocked {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if err := s.store.SetUserBlocked(r.Context(), id, false); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"email": target.Email, "role": target.Role})
	s.audit(r, "user.unblock", "user", id, string(details))
	s.auth.InvalidateAuthz(id)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	caller := auth.FromContext(r.Context())
	if caller != nil && caller.UserID == id {
		http.Error(w, `{"error":"cannot delete your own account"}`, http.StatusBadRequest)
		return
	}
	// AnyState: engelli hesap da silinebilsin (GetUserByID onu görmez).
	target, err := s.store.GetUserByIDAnyState(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if target == nil || (target.Disabled && !target.Blocked) {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	// Refuse to remove the last remaining admin — locks the system. A blocked
	// admin is already outside CountAdmins, so removing one never lowers it.
	if target.Role == auth.RoleAdmin && !target.Blocked {
		n, err := s.store.CountAdmins(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		if n <= 1 {
			http.Error(w, `{"error":"cannot remove the last admin"}`, http.StatusBadRequest)
			return
		}
	}
	if err := s.store.DisableUser(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"email": target.Email, "role": target.Role})
	s.audit(r, "user.delete", "user", id, string(details))
	s.auth.InvalidateAuthz(id)
	writeJSON(w, map[string]string{"status": "ok"})
}
