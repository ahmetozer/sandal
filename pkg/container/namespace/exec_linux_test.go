//go:build linux

package namespace

import (
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

// ownNS is a container run with -ns-net cont:B (stored as B's pid at the
// time), -ns-ipc host and -ns-user "" (its own user namespace); every other
// kind was created for it.
func ownNS() Namespaces {
	return Namespaces{
		"mnt":    {UserValue: strp("")},
		"pid":    {UserValue: strp("")},
		"uts":    {UserValue: strp("")},
		"cgroup": {UserValue: strp("")},
		"net":    {UserValue: strp("pid:777"), IsUserDefined: true},
		"ipc":    {UserValue: strp("host"), IsHost: true},
		"user":   {UserValue: strp("")},
	}
}

func TestForExec(t *testing.T) {
	const cont = 4242
	contTarget := "pid:4242"
	type want struct {
		host   bool
		target string
	}
	cases := []struct {
		name      string
		own       Namespaces
		overrides map[string]string
		want      map[Name]want
	}{
		{
			name: "no overrides",
			own:  ownNS(),
			want: map[Name]want{
				"mnt": {target: contTarget}, "pid": {target: contTarget},
				"uts": {target: contTarget}, "cgroup": {target: contTarget},
				// The stale pid:777 stored at run time is replaced.
				"net": {target: contTarget},
				// Shared with the host at run time: stays on the host.
				"ipc": {host: true},
			},
		},
		{
			name: "empty own config joins every kind",
			own:  nil,
			want: map[Name]want{
				"mnt": {target: contTarget}, "pid": {target: contTarget},
				"uts": {target: contTarget}, "cgroup": {target: contTarget},
				"net": {target: contTarget}, "ipc": {target: contTarget},
			},
		},
		{
			name: "each override form",
			own:  ownNS(),
			overrides: map[string]string{
				"mnt":    "host",
				"pid":    "99",
				"uts":    "pid:98",
				"net":    "file:/var/run/netns/blue",
				"ipc":    "cont:db",
				"cgroup": "",
			},
			want: map[Name]want{
				"mnt": {host: true}, "pid": {target: "99"},
				"uts": {target: "pid:98"}, "net": {target: "file:/var/run/netns/blue"},
				"ipc": {target: "cont:db"}, "cgroup": {target: contTarget},
			},
		},
		{
			name:      "mnt join through a process",
			own:       ownNS(),
			overrides: map[string]string{"mnt": "cont:b"},
			want: map[Name]want{
				"mnt": {target: "cont:b"}, "pid": {target: contTarget},
				"uts": {target: contTarget}, "cgroup": {target: contTarget},
				"net": {target: contTarget}, "ipc": {host: true},
			},
		},
		{
			name:      "user host is accepted and still not joined",
			own:       ownNS(),
			overrides: map[string]string{"user": "host"},
			want: map[Name]want{
				"mnt": {target: contTarget}, "pid": {target: contTarget},
				"uts": {target: contTarget}, "cgroup": {target: contTarget},
				"net": {target: contTarget}, "ipc": {host: true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ForExec(cont, tc.own, tc.overrides)
			if err != nil {
				t.Fatalf("ForExec = %v", err)
			}
			if _, ok := got["user"]; ok {
				t.Fatal("user namespace in the join set; exec must never join it")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("join set has %d kinds, want %d: %v", len(got), len(tc.want), got)
			}
			for kind, w := range tc.want {
				conf, ok := got[kind]
				if !ok {
					t.Fatalf("%s missing from the join set", kind)
				}
				if w.host {
					if !conf.IsHost || conf.IsUserDefined {
						t.Errorf("%s = %+v (%q), want host", kind, conf, conf.String())
					}
					continue
				}
				if conf.IsHost || !conf.IsUserDefined || conf.String() != w.target {
					t.Errorf("%s = %q (host=%v userDefined=%v), want join %q", kind, conf.String(), conf.IsHost, conf.IsUserDefined, w.target)
				}
			}
		})
	}
}

func TestForExecErrors(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		wantInErr string
	}{
		{"unknown kind", map[string]string{"foo": "host"}, `unknown namespace kind "foo"`},
		{"time is not offered", map[string]string{"time": "1"}, `unknown namespace kind "time"`},
		{"user target", map[string]string{"user": "cont:b"}, "-ns-user"},
		{"user new", map[string]string{"user": "pid:1"}, "never joins a user namespace"},
		{"mnt file", map[string]string{"mnt": "file:/x"}, "-ns-mnt"},
		{"bad syntax", map[string]string{"net": "abc"}, `-ns-net "abc"`},
		{"empty pid", map[string]string{"uts": "pid:"}, "-ns-uts"},
		{"zero pid", map[string]string{"pid": "0"}, "-ns-pid"},
		{"empty cont", map[string]string{"ipc": "cont:"}, "-ns-ipc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ForExec(4242, ownNS(), tc.overrides)
			if err == nil {
				t.Fatalf("ForExec(%v) = %v, want error", tc.overrides, got)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantInErr)
			}
		})
	}
	if _, err := ForExec(0, ownNS(), nil); err == nil {
		t.Fatal("ForExec(pid 0) = nil error, want error")
	}
}

// The join set must not share pointers with the stored config: Resolve
// rewrites cont: values through UserValue, and Enter used to rewrite empty
// entries the same way.
func TestForExecDoesNotAliasOwn(t *testing.T) {
	own := ownNS()
	before := map[Name]string{}
	for k, v := range own {
		before[k] = v.String()
	}
	join, err := ForExec(4242, own, map[string]string{"net": "cont:peer"})
	if err != nil {
		t.Fatalf("ForExec = %v", err)
	}
	for kind, conf := range join {
		if o, ok := own[kind]; ok && o.UserValue == conf.UserValue {
			t.Fatalf("%s shares its UserValue pointer with the config", kind)
		}
	}
	if err := join.Resolve(func(string) (int, error) { return 55, nil }); err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if got := join["net"].String(); got != "pid:55" {
		t.Fatalf("net after Resolve = %q, want pid:55", got)
	}
	_ = join.SetEmptyToPid(4242)
	for k, v := range own {
		if v.String() != before[k] {
			t.Fatalf("config %s changed from %q to %q", k, before[k], v.String())
		}
	}
}

func TestSetEmptyToPidDoesNotWriteThrough(t *testing.T) {
	empty := ""
	ns := Namespaces{"net": {UserValue: &empty}}
	out := ns.SetEmptyToPid(12)
	if empty != "" {
		t.Fatalf("source value rewritten to %q", empty)
	}
	if got := out["net"].String(); got != "pid:12" || !out["net"].IsUserDefined {
		t.Fatalf("SetEmptyToPid net = %q (userDefined=%v), want pid:12", got, out["net"].IsUserDefined)
	}
}

func TestMntRoot(t *testing.T) {
	cases := []struct {
		name    string
		ns      Namespaces
		want    string
		wantErr bool
	}{
		{"no mnt entry", Namespaces{"net": {UserValue: strp("pid:1"), IsUserDefined: true}}, "", false},
		{"mnt host", Namespaces{"mnt": {UserValue: strp("host"), IsHost: true}}, "", false},
		{"mnt not joined", Namespaces{"mnt": {UserValue: strp("")}}, "", false},
		{"pid form", Namespaces{"mnt": {UserValue: strp("pid:31"), IsUserDefined: true}}, "/proc/31/root", false},
		{"bare pid", Namespaces{"mnt": {UserValue: strp("32"), IsUserDefined: true}}, "/proc/32/root", false},
		{"file", Namespaces{"mnt": {UserValue: strp("file:/x"), IsUserDefined: true}}, "", true},
		{"unresolved cont", Namespaces{"mnt": {UserValue: strp("cont:b"), IsUserDefined: true}}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mntRoot(tc.ns)
			if (err != nil) != tc.wantErr {
				t.Fatalf("mntRoot err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("mntRoot = %q, want %q", got, tc.want)
			}
		})
	}
}
