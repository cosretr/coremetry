package chstore

// notify_sre_followup.go — CoSRE Faz C1: Problem bildirimi giderken AI SRE
// araştırması henüz bitmemişse, gönderilen mesajların künyesi (kanal, Zoom
// mesaj kimliği, gönderilen metin, e-posta konusu) burada bekler; notify
// paketinin lider kilitli tiki araştırma bitince Zoom mesajını DÜZENLER
// (düzenlenemeyen kanala konuya tek kısa yanıt) ve satırı siler.
//
// Depolama: ortak durum tablosu saved_views (mimari değişmez 5).
//
//	id            "nsf:" + problemId
//	owner_id      ""                     (sistem satırı)
//	page          "notify-sre-followup"  (sistem sayfası — genel /api/views yazamaz)
//	name          "pending"
//	query_string  JSON NotifySREFollowUp
//	created_at    ilk gönderim anı

import (
	"context"
	"encoding/json"
	"time"
)

// NotifySREFollowUpPage — saved_views page değeri.
const NotifySREFollowUpPage = "notify-sre-followup"

const notifySREFollowUpPrefix = "nsf:"

// NotifySREFollowUpItem — bekleyen tek kanal gönderimi.
type NotifySREFollowUpItem struct {
	ChannelID string `json:"channelId"`
	Type      string `json:"type"`
	// MessageID — Zoom Chat mesaj kimliği (düzenleme ucu için); diğer
	// kanallarda boş.
	MessageID string `json:"messageId,omitempty"`
	// Text — Zoom'a giden ilk metin (düzenlemede bloğun önüne konur).
	Text string `json:"text,omitempty"`
}

// NotifySREFollowUp — bir problemin bekleyen bildirim künyeleri.
type NotifySREFollowUp struct {
	ProblemID  string                  `json:"problemId"`
	Lang       string                  `json:"lang"` // tr | en (gönderimdeki şablon)
	Title      string                  `json:"title,omitempty"`
	Subject    string                  `json:"subject,omitempty"`
	ThreadRoot string                  `json:"threadRoot,omitempty"` // e-posta konu kökü (Message-ID)
	CreatedAt  int64                   `json:"createdAt"`
	Items      []NotifySREFollowUpItem `json:"items"`
}

// notifySREFollowUpFromRow — SAF: satır → kayıt; kimlik/sayfa/sahip tutmazsa ok=false.
func notifySREFollowUpFromRow(v SavedView) (NotifySREFollowUp, bool) {
	if v.Page != NotifySREFollowUpPage || v.OwnerID != "" || v.Name == "" {
		return NotifySREFollowUp{}, false
	}
	var f NotifySREFollowUp
	if err := json.Unmarshal([]byte(v.QueryString), &f); err != nil || f.ProblemID == "" {
		return NotifySREFollowUp{}, false
	}
	if v.ID != notifySREFollowUpPrefix+f.ProblemID {
		return NotifySREFollowUp{}, false
	}
	return f, true
}

// GetNotifySREFollowUp — problemin bekleyen kaydı (yoksa nil, nil).
func (s *Store) GetNotifySREFollowUp(ctx context.Context, problemID string) (*NotifySREFollowUp, error) {
	v, err := s.GetSavedView(ctx, notifySREFollowUpPrefix+problemID)
	if err != nil || v == nil {
		return nil, err
	}
	f, ok := notifySREFollowUpFromRow(*v)
	if !ok {
		return nil, nil
	}
	return &f, nil
}

// PutNotifySREFollowUp — kaydı yazar (aynı problem → aynı satır).
func (s *Store) PutNotifySREFollowUp(ctx context.Context, f NotifySREFollowUp) error {
	if f.CreatedAt == 0 {
		f.CreatedAt = time.Now().UnixNano()
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.UpsertSavedView(ctx, SavedView{ID: notifySREFollowUpPrefix + f.ProblemID, Name: "pending",
		Page: NotifySREFollowUpPage, QueryString: string(raw), CreatedAt: f.CreatedAt})
}

// ListNotifySREFollowUps — bekleyen kayıtlar (≤200; ListSavedViews tavanı).
func (s *Store) ListNotifySREFollowUps(ctx context.Context) ([]NotifySREFollowUp, error) {
	rows, err := s.ListSavedViews(ctx, "", NotifySREFollowUpPage)
	if err != nil {
		return nil, err
	}
	out := make([]NotifySREFollowUp, 0, len(rows))
	for _, r := range rows {
		if f, ok := notifySREFollowUpFromRow(r); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// DeleteNotifySREFollowUp — kaydı siler.
func (s *Store) DeleteNotifySREFollowUp(ctx context.Context, problemID string) error {
	return s.DeleteSavedView(ctx, notifySREFollowUpPrefix+problemID)
}
