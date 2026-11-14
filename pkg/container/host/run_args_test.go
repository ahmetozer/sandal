//go:build linux

package host

import (
	"slices"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// The daemon restarts a -startup container from its state record, so every
// start resolves the argv from what the previous start persisted. It must
// come out the same each time: a record that fed its resolved argv back in
// gained one more entrypoint per recovery ('/bin/sh /bin/sh -c ...').
func TestResolveArgsIsStableAcrossRestarts(t *testing.T) {
	const script = "OLLAMA_KEEP_ALIVE=-1 exec /bin/ollama serve"
	aiHostArgs := []string{"/usr/bin/sandal", "run", "-d", "-startup", "-name", "ai", "-lw", "ollama/ollama:latest",
		"-entrypoint", "/bin/sh", "--", "-c", script}
	shArgv := []string{"/bin/sh", "-c", script}

	cases := []struct {
		name          string
		rec           config.Config
		imgEntrypoint []string
		imgCmd        []string
		want          []string
	}{
		{
			name:          "-entrypoint with a command",
			rec:           config.Config{Entrypoint: "/bin/sh", UserArgs: []string{"-c", script}, HostArgs: aiHostArgs},
			imgEntrypoint: []string{"/bin/ollama"},
			imgCmd:        []string{"serve"},
			want:          shArgv,
		},
		{
			name:          "image ENTRYPOINT with a command",
			rec:           config.Config{UserArgs: []string{"-port", "8081"}},
			imgEntrypoint: []string{"/whoami"},
			want:          []string{"/whoami", "-port", "8081"},
		},
		{
			name:          "image ENTRYPOINT and CMD",
			rec:           config.Config{},
			imgEntrypoint: []string{"/bin/ollama"},
			imgCmd:        []string{"serve"},
			want:          []string{"/bin/ollama", "serve"},
		},
		{
			name:   "no entrypoint",
			rec:    config.Config{UserArgs: []string{"sleep", "60"}},
			imgCmd: []string{"/bin/sh"},
			want:   []string{"sleep", "60"},
		},
		// Records written before UserArgs existed hold only the resolved
		// argv; the command comes from their original command line.
		{
			name:          "old record, never recovered",
			rec:           config.Config{Entrypoint: "/bin/sh", ContArgs: shArgv, HostArgs: aiHostArgs},
			imgEntrypoint: []string{"/bin/ollama"},
			imgCmd:        []string{"serve"},
			want:          shArgv,
		},
		{
			name:          "old record grown by recoveries",
			rec:           config.Config{Entrypoint: "/bin/sh", ContArgs: append(slices.Repeat([]string{"/bin/sh"}, 95), "-c", script), HostArgs: aiHostArgs},
			imgEntrypoint: []string{"/bin/ollama"},
			imgCmd:        []string{"serve"},
			want:          shArgv,
		},
		{
			name:          "old record repaired by hand",
			rec:           config.Config{Entrypoint: "/bin/sh", ContArgs: []string{"-c", script}, HostArgs: aiHostArgs},
			imgEntrypoint: []string{"/bin/ollama"},
			imgCmd:        []string{"serve"},
			want:          shArgv,
		},
		{
			name:          "old record of an image ENTRYPOINT",
			rec:           config.Config{ContArgs: []string{"/whoami", "/whoami"}, HostArgs: []string{"/usr/bin/sandal", "run", "-d", "-startup", "-lw", "traefik/whoami"}},
			imgEntrypoint: []string{"/whoami"},
			want:          []string{"/whoami"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.rec.Clone()
			for start := 1; start <= 3; start++ {
				if err := resolveArgs(c, tc.imgEntrypoint, tc.imgCmd); err != nil {
					t.Fatalf("start %d: %v", start, err)
				}
				if !slices.Equal(c.ContArgs, tc.want) {
					t.Fatalf("start %d: argv %q, want %q", start, c.ContArgs, tc.want)
				}
				c = c.Clone() // the record the next start reads back
			}
		})
	}
}

func TestResolveArgsWithoutCommand(t *testing.T) {
	c := &config.Config{HostArgs: []string{"/usr/bin/sandal", "run", "-lw", "/"}}
	if err := resolveArgs(c, nil, nil); err == nil {
		t.Fatalf("resolveArgs = nil, argv %q; want an error for no command", c.ContArgs)
	}
}
