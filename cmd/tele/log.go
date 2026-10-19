package main

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/sorokin-vladimir/tele/internal/version"
)

// openLog opens the log in dir, file-only since the TUI owns the terminal.
// The host logs to tele.log. A tele that only attaches to another one's host
// logs to tele-client.log, and tags every line with its pid: two windows on
// one host are two processes, and one file rotated by both would lose lines
// (#208). Two such windows still share that file; rotating it from both is
// rare enough to accept.
func openLog(dir string, hosting, verbose bool) (*zap.Logger, string) {
	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	if verbose {
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	name := "tele.log"
	if !hosting {
		name = "tele-client.log"
	}
	path := filepath.Join(dir, name)
	w := zapcore.AddSync(&lumberjack.Logger{
		Filename:   path,
		MaxSize:    10,
		MaxBackups: 3,
		Compress:   false,
	})
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		w,
		level,
	)
	// Every line is tagged with the release it came from: a log excerpt from a
	// user is otherwise impossible to attribute to a build.
	log := zap.New(core).With(zap.String("v", version.Version))
	if !hosting {
		log = log.With(zap.Int("pid", os.Getpid()))
	}
	return log, path
}
