//go:build unix

package capability

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalSystemProviderTimeoutKillsChildProcessGroup(t *testing.T) {
	pidPath := t.TempDir() + "/child.pid"
	result, err := (LocalSystemProvider{}).RunCommand(t.Context(), RunCommandRequest{
		Command:   "sh",
		Args:      []string{"-c", `sleep 30 & child=$!; printf '%s' "$child" > "$1"; wait "$child"`, "sh", pidPath},
		TimeoutMS: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut {
		t.Fatalf("expected command timeout: %#v", result)
	}
	encodedPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(encodedPID)))
	if err != nil {
		t.Fatalf("decode child pid: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("child process %d survived command timeout", pid)
}
