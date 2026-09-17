package auth

// action_token.go — v0.10.749: bildirimden tek tıkla eylem ("Sustur")
// için imzalı, KİMLİKSİZ bağlantı jetonu (operatör: "alarm geldiğinde bu
// ignore edilebilsin; audit'e kimin ignore ettiği yazsın").
//
// Bir e-posta/Slack alıcısının Coremetry oturumu yoktur; bağlantı
// GET /api/public/notify/ignore/{token} public'tir (SkipPath) ve sınır
// yalnız jeton sahipliği + süredir (public trace paylaşımıyla aynı
// duruş). Kimlik jetonun İÇİNDE taşınır: e-posta kanalında alıcı adresi
// (alıcı başına ayrı jeton), paylaşımlı kanallarda "channel:<tür>/<ad>".
// Tıklayanın oturum çerezi varsa handler onu jetonun üstüne yazar.
//
// Şekil: base64url(JSON{k,i,w,e}) "." base64url(HMAC-SHA256). Anahtar
// JWT secret; COREMETRY_NOTIFY_ACTION_SECRET verilirse o (SetActionSecret)
// — JWT anahtarı zayıfsa bağlantı sahte üretilebilir, tek etkisi bir
// alarmı susturmaktır. Saklama yok: jeton kendini taşır, eylem idempotent
// (ack), tekrar oynatma zararsız.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ActionClaims — jetonun taşıdığı eylem.
type ActionClaims struct {
	Kind string `json:"k"` // problem | incident
	ID   string `json:"i"`
	Who  string `json:"w"` // alıcı e-postası ya da "channel:<tür>/<ad>"
	Exp  int64  `json:"e"` // unix saniye
}

var (
	ErrActionToken   = errors.New("invalid action token")
	ErrActionExpired = errors.New("action token expired")
)

const actionTokenMaxLen = 2048

// SetActionSecret — ayrı anahtar; boş/boşluk → JWT secret kalır.
func (s *Service) SetActionSecret(secret string) {
	if v := strings.TrimSpace(secret); v != "" {
		s.actionSecret = []byte(v)
	}
}

func (s *Service) actionKey() []byte {
	if len(s.actionSecret) > 0 {
		return s.actionSecret
	}
	return s.secret
}

func (s *Service) actionMAC(payload string) string {
	m := hmac.New(sha256.New, s.actionKey())
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SignAction — kind/id/who + ttl → jeton. Boş kind/id ya da ttl ≤ 0 hata.
func (s *Service) SignAction(kind, id, who string, ttl time.Duration) (string, error) {
	if kind == "" || id == "" || ttl <= 0 {
		return "", ErrActionToken
	}
	b, err := json.Marshal(ActionClaims{Kind: kind, ID: id, Who: who, Exp: time.Now().Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.actionMAC(payload), nil
}

// VerifyAction — imza (sabit zamanlı) + süre. now parametre: test edilebilir.
func (s *Service) VerifyAction(token string, now time.Time) (ActionClaims, error) {
	var c ActionClaims
	if len(token) == 0 || len(token) > actionTokenMaxLen {
		return c, ErrActionToken
	}
	i := strings.LastIndexByte(token, '.')
	if i <= 0 || i == len(token)-1 {
		return c, ErrActionToken
	}
	payload, mac := token[:i], token[i+1:]
	if !hmac.Equal([]byte(mac), []byte(s.actionMAC(payload))) {
		return c, ErrActionToken
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return c, ErrActionToken
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Kind == "" || c.ID == "" {
		return ActionClaims{}, ErrActionToken
	}
	if now.Unix() > c.Exp {
		return c, ErrActionExpired
	}
	return c, nil
}
