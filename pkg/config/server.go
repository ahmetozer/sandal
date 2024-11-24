package config

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

var (
	configs []Config
)

type Health struct {
	Status string
}

func Server() {

	mux := http.NewServeMux()
	server := http.Server{
		Handler: mux,
	}

	_, err := os.Stat(DaemonSocket)
	switch {
	case err == nil:
		if pingServerSocket() == nil {
			slog.Error("Server", slog.String("action", "ping socket"), slog.String("socket", DaemonSocket), slog.Any("error", "socket already exist and responsive"))
			os.Exit(1)
		}
		err = os.Remove(DaemonSocket)
		if err != nil {
			slog.Error("Server", slog.String("action", "remove old socket"), slog.String("socket", DaemonSocket), slog.Any("error", err))
			os.Exit(1)
		}
	case os.IsNotExist(err):
		socketDir := filepath.Dir(DaemonSocket)
		err = os.MkdirAll(socketDir, 0o600)
		if err != nil {
			slog.Error("Server", slog.String("action", "create socket dir"), slog.String("socket dir", socketDir), slog.Any("error", err))
			os.Exit(1)
		}
		slog.Debug("Server", slog.String("socket", DaemonSocket), slog.Any("message", "socket path allocatable"))
	default:
		slog.Error("Server", slog.Any("error", err))
		os.Exit(1)
	}

	mux.HandleFunc("/AllContainers", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(configs)
	})
	mux.Handle("/ListConfigs", http.FileServer(http.Dir(BaseStateDir)))

	healthResponse, _ := json.Marshal(Health{
		Status: "ok",
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write(healthResponse)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("Server", slog.Any("action", r.URL.Path))
		mux.ServeHTTP(w, r)
	})

	unixListener, err := net.Listen("unix", DaemonSocket)
	if err != nil {
		panic(err)
	}
	configs, err = Containers()
	if err != nil {
		slog.Warn("Server", slog.String("action", "load all containers"), slog.Any("error", err))
	}

	slog.Debug("Server", slog.String("socket", unixListener.Addr().String()))

	server.Serve(unixListener)

}

func pingServerSocket() error {

	httpc := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", DaemonSocket)
			},
		},
	}

	response, err := httpc.Get("http://unix/AllContainers")
	if err != nil {
		return err
	}
	r := &Health{}
	json.NewDecoder(response.Body).Decode(r)

	slog.Debug("Server", slog.String("action", "ping socket"), slog.Any("status", r.Status))

	return nil
}
