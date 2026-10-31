package chstore

import (
	"strings"
	"testing"
)

// user_cols_test.go — v0.10.1731 (kullanıcı detay sayfası, rol kilidi).
// users tablosunun probe'lu kolonları üç yerde yaşar: SELECT projeksiyonu,
// scanUserRow hedefleri ve UpsertUser INSERT listesi. Biri kolonu adlayıp
// diğeri adlamazsa ya Scan hata verir (her giriş kırılır) ya da whole-row
// replace kolonu sessizce sıfırlar (kilit/dizin bilgisi silinir). Her probe
// birleşimi için üçünün aynı uzunlukta büyüdüğünü ve RoleLocked'ın gidiş-
// dönüşte korunduğunu pinler.

type countScanner struct {
	n    int
	vals []any
}

func (c *countScanner) Scan(dst ...any) error {
	c.n = len(dst)
	for i, d := range dst {
		if i >= len(c.vals) {
			break
		}
		if p, ok := d.(*uint8); ok {
			if v, ok := c.vals[i].(uint8); ok {
				*p = v
			}
		}
	}
	return nil
}

func TestUserColsAlignAcrossProbes(t *testing.T) {
	combos := []userCols{
		{}, {ldap: true}, {roleLocked: true}, {ldap: true, roleLocked: true},
	}
	for _, c := range combos {
		ins := strings.Split(userInsertColumns(c), ",")
		args := userInsertArgs(User{ID: "u1", Role: "viewer", RoleLocked: true}, c)
		if len(ins) != len(args) {
			t.Errorf("%+v: INSERT %d kolon, %d değer", c, len(ins), len(args))
		}
		sel := strings.Split(userSelectExprFor(c), ",")
		sc := &countScanner{}
		if _, err := scanUserRow(sc, c); err != nil {
			t.Fatal(err)
		}
		if len(sel) != sc.n {
			t.Errorf("%+v: SELECT %d ifade, Scan %d hedef", c, len(sel), sc.n)
		}
		hasRL := strings.Contains(userInsertColumns(c), "role_locked")
		if hasRL != c.roleLocked {
			t.Errorf("%+v: role_locked INSERT'te=%v", c, hasRL)
		}
		if c.roleLocked && args[len(args)-1] != uint8(1) {
			t.Errorf("%+v: RoleLocked=true 1 yazılmalı, %v", c, args[len(args)-1])
		}
	}
}

func TestScanUserRowRoleLocked(t *testing.T) {
	c := userCols{ldap: true, roleLocked: true}
	vals := make([]any, 15)
	vals[14] = uint8(1)
	u, err := scanUserRow(&countScanner{vals: vals}, c)
	if err != nil {
		t.Fatal(err)
	}
	if !u.RoleLocked {
		t.Fatal("role_locked=1 RoleLocked=true okunmalı")
	}
}
