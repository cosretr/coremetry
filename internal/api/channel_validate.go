package api

// channel_validate.go — bildirim kanalı KAYIT doğrulamaları.
//
// v0.10.747: validateWebhookChannel api.go'dan buraya taşındı (api.go
// büyümez kuralı; tavan 11901 satır) ve olay türü allow-list'i
// (matchRules.kinds) doğrulaması eklendi. İkisi de create/update
// handler'larından çağrılır — ulaşılabilirlik pini channel_validate_test.go.

import (
	"encoding/json"
	"fmt"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/notify"
)

// validateChannelKinds — matchRules.kinds'i normalize eder (kırp,
// küçült, tekrar at) ve bilinmeyen değeri 400'e çevirir. Boş → nil
// (omitempty; süzgeç yok). Kaynak tek: chstore.NormalizeNotifyKinds.
func validateChannelKinds(c *chstore.NotificationChannel) error {
	ks, err := chstore.NormalizeNotifyKinds(c.MatchRules.Kinds)
	if err != nil {
		return fmt.Errorf("matchRules.kinds: %w", err)
	}
	c.MatchRules.Kinds = ks
	return nil
}

// validateWebhookChannel — v0.8.445: webhook kanalının BodyTemplate'i
// KAYIT anında parse + örnek-render'dan geçer; bozuk şablon hiç
// kaydedilmez (runtime'da default gövdeye düşüş yalnız beklenmedik
// veri hataları için kalır).
func validateWebhookChannel(c chstore.NotificationChannel) error {
	if c.Type != "webhook" || len(c.Config) == 0 {
		return nil
	}
	var wc notify.WebhookChannelConfig
	if err := json.Unmarshal(c.Config, &wc); err != nil {
		return fmt.Errorf("webhook config: %w", err)
	}
	if err := notify.ValidateWebhookTemplate(wc.BodyTemplate); err != nil {
		return fmt.Errorf("bodyTemplate: %w", err)
	}
	return nil
}
