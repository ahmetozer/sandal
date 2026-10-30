//go:build linux || darwin

package cmd

import (
	"errors"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func TestStopProcess(t *testing.T) {
	type call struct{ signal, timeout int }
	tests := []struct {
		name      string
		errs      []error
		wantCalls []call
		wantErr   bool
	}{
		{"graceful stop", []error{nil}, []call{{15, 30}}, false},
		{"escalates to SIGKILL", []error{errors.New("timeout"), nil}, []call{{15, 30}, {9, 5}}, false},
		{"SIGKILL fails", []error{errors.New("timeout"), errors.New("stuck")}, []call{{15, 30}, {9, 5}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			old := killContainer
			t.Cleanup(func() { killContainer = old })
			killContainer = func(c *config.Config, signal, timeout int) error {
				err := tt.errs[len(calls)]
				calls = append(calls, call{signal, timeout})
				return err
			}

			err := stopProcess(&config.Config{Name: "c"}, 15, 30)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(calls) != len(tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, tt.wantCalls)
			}
			for i := range calls {
				if calls[i] != tt.wantCalls[i] {
					t.Errorf("calls = %v, want %v", calls, tt.wantCalls)
				}
			}
		})
	}
}
