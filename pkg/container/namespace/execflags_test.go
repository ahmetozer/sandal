package namespace

import (
	"flag"
	"maps"
	"testing"
)

func TestExecFlagsReturnsOnlySetKinds(t *testing.T) {
	f := flag.NewFlagSet("exec", flag.ContinueOnError)
	get := ExecFlags(f)
	if err := f.Parse([]string{"-ns-net", "cont:b", "-ns-mnt", "host", "a"}); err != nil {
		t.Fatalf("Parse = %v", err)
	}
	want := map[string]string{"net": "cont:b", "mnt": "host"}
	if got := get(); !maps.Equal(got, want) {
		t.Fatalf("ExecFlags() = %v, want %v", got, want)
	}
	if args := f.Args(); len(args) != 1 || args[0] != "a" {
		t.Fatalf("remaining args = %v, want [a]", args)
	}
}

func TestExecFlagsNoneSet(t *testing.T) {
	f := flag.NewFlagSet("exec", flag.ContinueOnError)
	get := ExecFlags(f)
	if err := f.Parse([]string{"-ns-uts", "", "a"}); err != nil {
		t.Fatalf("Parse = %v", err)
	}
	if got := get(); len(got) != 0 {
		t.Fatalf("ExecFlags() = %v, want empty (an empty value means the container's own)", got)
	}
}

func TestExecFlagsRegistersNoUserFlag(t *testing.T) {
	f := flag.NewFlagSet("exec", flag.ContinueOnError)
	ExecFlags(f)
	for _, kind := range ExecKinds {
		if f.Lookup("ns-"+string(kind)) == nil {
			t.Errorf("-ns-%s is not registered", kind)
		}
	}
	if f.Lookup("ns-user") != nil {
		t.Fatal("-ns-user is registered; exec never joins the user namespace")
	}
}
