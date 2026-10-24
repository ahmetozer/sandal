package config

import "testing"

func TestWithPanicReboot(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "panic=-1"},
		{"added", "console=ttyAMA0 init=/init", "panic=-1 console=ttyAMA0 init=/init"},
		{"user value kept", "console=ttyS0 panic=30", "console=ttyS0 panic=30"},
		{"user zero kept", "panic=0 console=ttyS0", "panic=0 console=ttyS0"},
		{"other panic params do not count", "panic_on_warn=1 oops=panic", "panic=-1 panic_on_warn=1 oops=panic"},
		{"init args do not count", "console=ttyS0 init=/bin/sh -- panic=5", "panic=-1 console=ttyS0 init=/bin/sh -- panic=5"},
		{"quoted value does not count", `console=ttyS0 foo="a panic=5"`, `panic=-1 console=ttyS0 foo="a panic=5"`},
		{"quoted user value kept", `"panic=10" console=ttyS0`, `"panic=10" console=ttyS0`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithPanicReboot(tt.in); got != tt.want {
				t.Errorf("WithPanicReboot(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
