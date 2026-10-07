package chstore

import "context"

// GetActiveUsersByLdapUsername — v0.10.1121 (OIDC e-posta çözüm zinciri,
// api/auth_oidc_email_resolve.go): OIDC kullanıcı adıyla eşleşmenin ADANMIŞ
// okuması. Yetki servisinin GetUserByLdapUsername'inden farkı: devre dışı
// satırlar YOK SAYILIR ve LIMIT 2 — çağıran iki satırı belirsiz sayar ve
// keyfî ilkini asla seçmez (güvenlik incelemesi F4). Girdi çağıranda küçük
// harf; sütun lowerUTF8 ile karşılaştırılır. Sütun yoksa ya da girdi boşsa
// (nil, nil). users küçük tablo; max_execution_time sınırı korunur.
func (s *Store) GetActiveUsersByLdapUsername(ctx context.Context, username string) ([]User, error) {
	if username == "" || !s.hasLdapUsernameCol {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT `+s.userSelectExpr()+`
		FROM users FINAL
		WHERE disabled = 0 AND ldap_username != '' AND lowerUTF8(ldap_username) = ?
		ORDER BY created_at DESC
		LIMIT 2
		SETTINGS max_execution_time = 5`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUserRow(rows, s.hasLdapUsernameCol)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}
