package chstore

// notification_ignore_test.go — v0.10.749: işaret yazımı boş girdiyi
// reddeder (CH'ye dokunmadan) ve okuma sorgusu sınır sözleşmesini taşır
// (zaman sınırı + max_execution_time + kimlik/tür yüklemi).

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestMarkNotificationIgnoredRequiresKindAndID(t *testing.T) {
	s := &Store{}
	if err := s.MarkNotificationIgnored(context.Background(), "", "p1", "who", "ip"); err == nil {
		t.Error("boş kind kabul edildi")
	}
	if err := s.MarkNotificationIgnored(context.Background(), "problem", "", "who", "ip"); err == nil {
		t.Error("boş id kabul edildi")
	}
	if ok, err := s.NotificationIgnored(context.Background(), ""); ok || err != nil {
		t.Errorf("boş id: %v %v", ok, err)
	}
}

func TestNotificationIgnoredQueryIsBounded(t *testing.T) {
	src, err := os.ReadFile("notification_ignore.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{"INTERVAL 30 DAY", "max_execution_time", "related_id = ?", "channel_kind = 'ignore'"} {
		if !strings.Contains(s, want) {
			t.Errorf("sorgu %q taşımalı", want)
		}
	}
}
