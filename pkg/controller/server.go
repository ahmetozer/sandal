package controller

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

func Server() {
	mux := http.NewServeMux()
	server := http.Server{
		Handler: mux,
	}

	_, err := os.Stat(env.DaemonSocket)
	switch {
	case err == nil:
		if pingServerSocket() == nil {
			slog.Error("Server", slog.String("action", "ping socket"), slog.String("socket", env.DaemonSocket), slog.Any("error", "socket already exist and responsive"))
			os.Exit(1)
		}
		err = os.Remove(env.DaemonSocket)
		if err != nil {
			slog.Error("Server", slog.String("action", "remove old socket"), slog.String("socket", env.DaemonSocket), slog.Any("error", err))
			os.Exit(1)
		}
	case os.IsNotExist(err):
		socketDir := filepath.Dir(env.DaemonSocket)
		err = os.MkdirAll(socketDir, 0o600)
		if err != nil {
			slog.Error("Server", slog.String("action", "create socket dir"), slog.String("socket dir", socketDir), slog.Any("error", err))
			os.Exit(1)
		}
		slog.Debug("Server", slog.String("socket", env.DaemonSocket), slog.Any("message", "socket path allocatable"))
	default:
		slog.Error("Server", slog.Any("error", err))
		os.Exit(1)
	}

	mux.HandleFunc("/containers", func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("Server", slog.Any("path", r.URL.Path))
		c, err := Containers()
		nilDebug(err)
		json.NewEncoder(w).Encode(c)
	})

	mux.HandleFunc("/containers/{name}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			slog.Debug("Server", slog.Any("path", r.URL.Path))
			c, err := GetContainer(r.PathValue("name"))
			nilDebug(err)
			json.NewEncoder(w).Encode(c)
		case http.MethodPost:
			c := config.Config{}
			err = json.NewDecoder(r.Body).Decode(&c)
			if err != nil {
				mockRespond(r, w, http.StatusBadRequest)
				return
			}
			SetContainer(&c)
		default:
			mockRespond(r, w, http.StatusNotImplemented)

		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		mockRespond(r, w, http.StatusOK)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("Server", slog.Any("action", r.URL.Path))
		mux.ServeHTTP(w, r)
	})

	unixListener, err := net.Listen("unix", env.DaemonSocket)
	if err != nil {
		panic(err)
	}

	slog.Info("Server", slog.String("socket", unixListener.Addr().String()))

	server.Serve(unixListener)

}

func pingServerSocket() error {
	response, err := httpc.Get("http://unix/health")
	if err != nil {
		return err
	}
	r := &respond{}
	json.NewDecoder(response.Body).Decode(r)

	slog.Debug("Server", slog.String("action", "ping socket"), slog.Any("status", r.Status))

	return nil
}

func nilDebug(err error) {
	if err != nil {
		slog.Warn("Server", slog.Any("error", err))
	}
}

type respond struct {
	Status int
	Path   string
	Method string
}

func mockRespond(r *http.Request, w http.ResponseWriter, s int) {

	w.WriteHeader(s)
	json.NewEncoder(w).Encode(respond{
		Path:   r.URL.Path,
		Method: r.Method,
		Status: s,
	})

}
