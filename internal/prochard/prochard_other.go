//go:build !linux

// Package prochard — süreç sertleştirme (v0.10.966). Linux dışı (darwin
// geliştirme) no-op: /proc yok, release yalnız linux/amd64.
package prochard

// DisableDumpable — v0.10.966 — linux dışında hiçbir şey yapmaz.
func DisableDumpable() error { return nil }

// Supported — false: bu platformda DisableDumpable etkisizdir.
const Supported = false
