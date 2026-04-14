package build

import (
	"strings"
	"testing"
)

func TestParseDockerfile_Basic(t *testing.T) {
	src := `# comment
FROM alpine:3.19
RUN apk add --no-cache curl
COPY ./hello /hello
CMD ["/hello"]
`
	instrs, err := ParseDockerfile(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(instrs) != 4 {
		t.Fatalf("expected 4 instructions, got %d: %v", len(instrs), instrs)
	}

	cases := []struct {
		kind InstrKind
		line int
	}{
		{InstrFrom, 2},
		{InstrRun, 3},
		{InstrCopy, 4},
		{InstrCmd, 5},
	}
	for i, c := range cases {
		if instrs[i].Kind != c.kind {
			t.Errorf("instr %d: want kind %s, got %s", i, c.kind, instrs[i].Kind)
		}
		if instrs[i].Line != c.line {
			t.Errorf("instr %d: want line %d, got %d", i, c.line, instrs[i].Line)
		}
	}

	// CMD should be JSON exec-form.
	if !instrs[3].JSON {
		t.Errorf("CMD should be JSON form")
	}
	if got := instrs[3].Args; len(got) != 1 || got[0] != "/hello" {
		t.Errorf("CMD args = %v", got)
	}
}

func TestParseDockerfile_LineContinuation(t *testing.T) {
	src := `FROM alpine
RUN apk add --no-cache \
    curl \
    wget
`
	instrs, err := ParseDockerfile(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(instrs) != 2 {
		t.Fatalf("expected 2 instructions, got %d", len(instrs))
	}
	// RUN's single-string arg should contain both curl and wget.
	if got := instrs[1].Args[0]; !strings.Contains(got, "curl") || !strings.Contains(got, "wget") {
		t.Errorf("RUN continuation merge failed: %q", got)
	}
}

func TestParseDockerfile_Stages(t *testing.T) {
	src := `ARG VER=3.19
FROM alpine:${VER} AS build
RUN echo hi
FROM alpine:${VER}
COPY --from=build /x /x
`
	instrs, err := ParseDockerfile(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	globalArgs, stages, err := SplitStages(instrs)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(globalArgs) != 1 {
		t.Errorf("want 1 global ARG, got %d", len(globalArgs))
	}
	if len(stages) != 2 {
		t.Fatalf("want 2 stages, got %d", len(stages))
	}
	if stages[0].Name != "build" {
		t.Errorf("stage 0 name = %q", stages[0].Name)
	}
	if stages[1].Name != "" {
		t.Errorf("stage 1 name = %q (want empty)", stages[1].Name)
	}
	// The COPY in stage 1 should retain its --from flag.
	var copyInstr *Instruction
	for i := range stages[1].Instrs {
		if stages[1].Instrs[i].Kind == InstrCopy {
			copyInstr = &stages[1].Instrs[i]
			break
		}
	}
	if copyInstr == nil {
		t.Fatal("COPY not found in stage 1")
	}
	if copyInstr.Flags["from"] != "build" {
		t.Errorf("--from flag = %q", copyInstr.Flags["from"])
	}
}

func TestParseDockerfile_EnvForms(t *testing.T) {
	src := `FROM alpine
ENV FOO bar baz
ENV A=1 B="two words" C=3
`
	instrs, err := ParseDockerfile(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Legacy form: "ENV FOO bar baz" → single pair FOO=bar baz
	if got := instrs[1].Args; len(got) != 1 || got[0] != "FOO=bar baz" {
		t.Errorf("legacy ENV = %v", got)
	}
	// Multi-pair form
	if got := instrs[2].Args; len(got) != 3 || got[0] != "A=1" || got[1] != "B=two words" || got[2] != "C=3" {
		t.Errorf("multi-pair ENV = %v", got)
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"FOO": "bar", "EMPTY": ""}
	cases := []struct{ in, want string }{
		{"$FOO", "bar"},
		{"${FOO}", "bar"},
		{"prefix-$FOO-suffix", "prefix-bar-suffix"},
		{"${MISSING:-default}", "default"},
		{"${FOO:-default}", "bar"},
		{"${EMPTY:-default}", "default"},
		{"${FOO:+alt}", "alt"},
		{"${MISSING:+alt}", ""},
		{"\\$FOO", "$FOO"}, // escaped
	}
	for _, c := range cases {
		if got := Expand(c.in, vars); got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIgnoreMatcher(t *testing.T) {
	rules := `# comment
node_modules
*.log
!important.log
build/**/*.tmp
`
	m, err := LoadIgnore(strings.NewReader(rules))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cases := []struct {
		path     string
		excluded bool
	}{
		{"node_modules", true},
		{"node_modules/foo", true},
		{"src/main.go", false},
		{"app.log", true},
		{"important.log", false},   // negated
		{"build/a/b/x.tmp", true},  // double-star
		{"build/a/b/x.js", false},  // not a tmp
	}
	for _, c := range cases {
		if got := m.Excluded(c.path); got != c.excluded {
			t.Errorf("Excluded(%q) = %v, want %v", c.path, got, c.excluded)
		}
	}
}
