//go:build linux || darwin

package sandal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Attach connects to a container's console and relays I/O.
// The caller is responsible for terminal setup (raw mode), signal handling,
// and creating the done channel. This keeps Attach reusable from CLI,
// web endpoints, daemons, etc.
//
// stdin/stdout/stderr: I/O handles for the attach session.
// done: closed when the caller wants to detach (e.g., container exited).
func Attach(c *config.Config, stdin io.Reader, stdout, stderr io.Writer, done <-chan struct{}) error {
	if c.VM != "" {
		return attachViaMgmt(c.Name, stdin, stdout)
	}
	return attachNative(c, stdin, stdout, stderr, done)
}

// attachViaMgmt sends an attach request to the VM's embedded controller
// via the management socket and relays I/O.
func attachViaMgmt(contName string, stdin io.Reader, stdout io.Writer) error {
	conn, err := mgmt.DialRaw(contName)
	if err != nil {
		return fmt.Errorf("management socket for %q: %w", contName, err)
	}
	defer conn.Close()

	reqBody, _ := json.Marshal(map[string]interface{}{"container": contName})

	fmt.Fprintf(conn, "POST /attach HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: Upgrade\r\n\r\n%s", len(reqBody), reqBody)

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("attach request failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("attach failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Relay I/O. The stdin goroutine runs in the background — return
	// when the output direction finishes (container closed its end).
	go io.Copy(conn, stdin)
	io.Copy(stdout, br)

	return nil
}

// ConsoleMode returns the console mode for a container.
// Exported so the cmd layer can decide terminal setup before calling Attach.
func ConsoleMode(name string) (string, error) {
	modePath := fmt.Sprintf("%s/console/%s/mode", os.Getenv("SANDAL_RUN_DIR"), name)
	// Fall back to the standard path construction
	if modePath == "/console/"+name+"/mode" {
		// env not set, use default
		return "", fmt.Errorf("console mode not available")
	}
	data, err := os.ReadFile(modePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
