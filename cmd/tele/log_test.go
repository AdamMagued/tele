package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tele that only attaches to another one's host logs to a file of its own,
// and says which process each line came from: two windows on one host must not
// rotate one file from two processes, and a line in it must be traceable to
// the window that wrote it (#208).
func TestOpenLog_AClientLogsApartAndSaysWhoItIs(t *testing.T) {
	dir := t.TempDir()

	log, path := openLog(dir, false, false)
	log.Info("hello")
	_ = log.Sync()

	if filepath.Base(path) != "tele-client.log" {
		t.Fatalf("a client logs to %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), fmt.Sprintf(`"pid":%d`, os.Getpid())) {
		t.Fatalf("no pid in %s", raw)
	}
}

// The host logs where tele always has.
func TestOpenLog_TheHostLogsWhereItAlwaysHas(t *testing.T) {
	dir := t.TempDir()

	_, path := openLog(dir, true, false)

	if path != filepath.Join(dir, "tele.log") {
		t.Fatalf("the host logs to %s", path)
	}
}
