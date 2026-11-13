package guest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	cruntime "github.com/ahmetozer/sandal/pkg/container/runtime"
	"golang.org/x/sys/unix"
)

// readyChildEnv makes the test binary act as a container init: it takes the
// ready pipe and execs a long-running command, as ContainerInitProc does.
const readyChildEnv = "SANDAL_TEST_READY_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(readyChildEnv) != "" {
		holdReadyPipe()
		err := unix.Exec("/bin/sleep", []string{"sleep", "30"}, os.Environ())
		fmt.Fprintf(os.Stderr, "exec sleep: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// crun learns that the container is ready from EOF on the ready pipe. The
// EOF must come when the init execs the container's command, not only when
// that command exits, and the command must inherit neither the pipe nor
// its env var.
func TestReadyPipeClosesWhenInitExecs(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), readyChildEnv+"=1", cruntime.ReadyFdEnv+"=3")
	cmd.ExtraFiles = []*os.File{w} // fd 3 in the child
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	eof := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, r)
		eof <- err
	}()
	select {
	case err := <-eof:
		if err != nil {
			t.Fatalf("reading the ready pipe: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ready pipe still open after the init exec'd its command: crun would wait until the container exits")
	}

	proc := fmt.Sprintf("/proc/%d", cmd.Process.Pid)
	exe, err := os.Readlink(proc + "/exe")
	if err != nil {
		t.Fatalf("init gone at EOF, want it running its command: %v", err)
	}
	if exe == self {
		t.Fatalf("EOF came before the init exec'd its command (exe still %s)", exe)
	}
	environ, err := os.ReadFile(proc + "/environ")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(environ, []byte(cruntime.ReadyFdEnv+"=")) {
		t.Errorf("container command inherited %s", cruntime.ReadyFdEnv)
	}
}
