package chstore

import "testing"

func TestParseCosreConversation(t *testing.T) {
	cases := []struct {
		raw       string
		ok, state bool
		verify    string
	}{
		{"", true, true, CosreVerifyShadow},
		{`{}`, true, true, CosreVerifyShadow},
		{`{"state":false}`, true, false, CosreVerifyShadow},
		{`{"verification":"off"}`, true, true, CosreVerifyOff},
		{`{"state":true,"verification":"shadow"}`, true, true, CosreVerifyShadow},
		{`{"verification":"loud"}`, true, true, CosreVerifyShadow},
		{`{bozuk`, false, false, ""},
	}
	for _, c := range cases {
		got, ok := parseCosreConversation([]byte(c.raw))
		if ok != c.ok {
			t.Fatalf("%q ok=%v", c.raw, ok)
		}
		if !ok {
			continue
		}
		if got.State != c.state || got.Verification != c.verify {
			t.Fatalf("%q → %+v", c.raw, got)
		}
	}
}

func TestCosreConversationDefaultStateOn(t *testing.T) {
	if d := DefaultCosreConversation(); !d.State || d.Style || d.Verification != CosreVerifyShadow {
		t.Fatalf("varsayılan %+v", d)
	}
}
