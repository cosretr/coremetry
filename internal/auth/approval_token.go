package auth

// approval_token.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.2):
// CoSRE araç onayı jetonu. Döngü onay isteyen çağrıda durur; operatörün
// onayı YENİ bir istekle gelir ve jeton kendini taşır (pod bağımsız, sunucu
// durum tutmaz). action_token.go ailesi: aynı HMAC anahtarı, aynı şekil
// base64url(JSON) "." base64url(HMAC-SHA256), farklı claim seti.
//
// Tür ayrımı iki yönlü: onay jetonunda `i` alanı YOK → VerifyAction (notify
// ignore ucu) onu ErrActionToken ile reddeder; VerifyApproval da k ≠
// "cosre-approval" olan her jetonu reddeder.
//
// Bağlama (x,s,t,h,u) ve tek-kullanım kontrolü çağıranda (api/chat_approval.go);
// bu dosya yalnız imza + süre.

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ApprovalKind — onay jetonunun türü.
const ApprovalKind = "cosre-approval"

// ApprovalBudget — devam isteğine devredilen döngü bütçesi (sıfırlanmaz).
type ApprovalBudget struct {
	CallsLeft  int `json:"c"`
	RoundsUsed int `json:"r"`
}

// ApprovalClaims — jetonun bağlandığı her şey.
type ApprovalClaims struct {
	Kind       string         `json:"k"`
	Exchange   string         `json:"x"`
	Step       string         `json:"s"`
	Tool       string         `json:"t"`
	ArgsDigest string         `json:"h"`
	User       string         `json:"u"`
	Budget     ApprovalBudget `json:"b"`
	Exp        int64          `json:"e"` // unix saniye
}

// SignApproval — claims (Kind/Exp hariç) + ttl → jeton. Boş bağlama alanı
// ya da ttl ≤ 0 hata: eksik bağlı jeton hiçbir zaman basılmaz.
func (s *Service) SignApproval(c ApprovalClaims, now time.Time, ttl time.Duration) (string, ApprovalClaims, error) {
	if c.Exchange == "" || c.Step == "" || c.Tool == "" || c.ArgsDigest == "" || c.User == "" || ttl <= 0 {
		return "", ApprovalClaims{}, ErrActionToken
	}
	c.Kind = ApprovalKind
	c.Exp = now.Add(ttl).Unix()
	b, err := json.Marshal(c)
	if err != nil {
		return "", ApprovalClaims{}, err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.actionMAC(payload), c, nil
}

// VerifyApproval — imza (sabit zamanlı) + tür + süre. Süresi geçmiş jeton
// ErrActionExpired (çağıran bunu RET sayar: zaman aşımı = ret).
func (s *Service) VerifyApproval(token string, now time.Time) (ApprovalClaims, error) {
	var c ApprovalClaims
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
	if err := json.Unmarshal(b, &c); err != nil || c.Kind != ApprovalKind {
		return ApprovalClaims{}, ErrActionToken
	}
	if now.Unix() > c.Exp {
		return c, ErrActionExpired
	}
	return c, nil
}
