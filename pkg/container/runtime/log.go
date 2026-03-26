//go:build linux

package runtime

import "log/slog"

type ContainerLog struct {
	name    string
	logType string
}

func (c ContainerLog) Write(p []byte) (n int, err error) {
	slog.Debug(c.name, slog.Any("msg", p))
	return len(p), nil
}

func NewLogWriter(name, t string) *ContainerLog {
	lw := &ContainerLog{}
	lw.name = name
	lw.logType = t
	return lw
}
