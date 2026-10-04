// LADR - the programming layer for inference-time reasoning.
// Copyright (C) 2026  Redeo Labs Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package device

// sessions_test.go — the direct-runtime persistent-session executor.
//
// Each test skips when its runtime is absent from the host, so the suite runs
// (reduced) on bare containers and fully on dev machines / images with the
// runtimes installed.

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// newTestRunner builds a runner with the reaper disabled (deterministic tests).
func newTestRunner(t *testing.T) *SessionRunner {
	t.Helper()
	r := newSessionRunner(context.Background(), t.TempDir(), 0, 0)
	t.Cleanup(r.Shutdown)
	return r
}

func runtimeAvailable(rt string) bool {
	_, err := exec.LookPath(rt)
	return err == nil
}

func TestSessionRunner_PythonStatePersists(t *testing.T) {
	if !runtimeAvailable("python3") {
		t.Skip("python3 not available on this host")
	}
	r := newTestRunner(t)
	ctx := context.Background()

	if _, err := r.Exec(ctx, ExecParams{SessionID: "s1", Runtime: RuntimePython, Code: "x = 6"}); err != nil {
		t.Fatalf("first block: %v", err)
	}
	res, err := r.Exec(ctx, ExecParams{SessionID: "s1", Runtime: RuntimePython, Code: "print(x * 7)"})
	if err != nil {
		t.Fatalf("second block: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "42" {
		t.Errorf("stdout = %q, want 42 — REPL state did not persist", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestSessionRunner_PythonTracebackIsResultNotError(t *testing.T) {
	if !runtimeAvailable("python3") {
		t.Skip("python3 not available on this host")
	}
	r := newTestRunner(t)
	res, err := r.Exec(context.Background(), ExecParams{
		SessionID: "s2", Runtime: RuntimePython, Code: "raise ValueError('boom')",
	})
	if err != nil {
		t.Fatalf("a traceback must be a result, not a Go error: %v", err)
	}
	if !strings.Contains(res.Stderr, "ValueError") && !strings.Contains(res.Stdout, "ValueError") {
		t.Errorf("output should carry the traceback, got stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestSessionRunner_NodeStatePersists(t *testing.T) {
	if !runtimeAvailable("node") {
		t.Skip("node not available on this host")
	}
	r := newTestRunner(t)
	ctx := context.Background()

	if _, err := r.Exec(ctx, ExecParams{SessionID: "s1", Runtime: RuntimeNode, Code: "var x = 6"}); err != nil {
		t.Fatalf("first block: %v", err)
	}
	res, err := r.Exec(ctx, ExecParams{SessionID: "s1", Runtime: RuntimeNode, Code: "console.log(x * 7)"})
	if err != nil {
		t.Fatalf("second block: %v", err)
	}
	// Contains, not exact-equality: node's REPL echoes completion values
	// ("undefined") into the captured output (documented quirk).
	if !strings.Contains(res.Stdout, "42") {
		t.Errorf("stdout = %q, want it to contain 42 — REPL state did not persist", res.Stdout)
	}
	if strings.Contains(res.Stdout, ">") {
		t.Errorf("stdout = %q, REPL prompt leaked into output", res.Stdout)
	}
}

func TestSessionRunner_BashStateAndExitCode(t *testing.T) {
	if !runtimeAvailable("bash") {
		t.Skip("bash not available on this host")
	}
	r := newTestRunner(t)
	ctx := context.Background()

	// State: a shell variable set in block 1 is visible in block 2.
	if _, err := r.Exec(ctx, ExecParams{SessionID: "sb", Runtime: RuntimeBash, Code: "X=6"}); err != nil {
		t.Fatalf("first block: %v", err)
	}
	res, err := r.Exec(ctx, ExecParams{SessionID: "sb", Runtime: RuntimeBash, Code: "echo $((X * 7))"})
	if err != nil {
		t.Fatalf("second block: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "42" {
		t.Errorf("stdout = %q, want 42 — shell state did not persist", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}

	// Exit code: the sentinel carries the block's status; the sentinel line
	// itself must never appear in captured output. Note: a bare `exit N` would
	// terminate the persistent shell itself — per-block status comes from the
	// last command, so code that wants a specific status uses a subshell.
	fail, err := r.Exec(ctx, ExecParams{SessionID: "sb2", Runtime: RuntimeBash, Code: "echo nope >&2\n(exit 3)"})
	if err != nil {
		t.Fatalf("failing block: %v", err)
	}
	if fail.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", fail.ExitCode)
	}
	if strings.Contains(fail.Stdout, "LADR_BLOCK_DONE") {
		t.Errorf("sentinel leaked into stdout: %q", fail.Stdout)
	}
	if !strings.Contains(fail.Stderr, "nope") {
		t.Errorf("stderr = %q, want it to contain 'nope'", fail.Stderr)
	}
}

func TestSessionRunner_TimeoutDropsSessionNextCallFresh(t *testing.T) {
	if !runtimeAvailable("python3") {
		t.Skip("python3 not available on this host")
	}
	r := newTestRunner(t)
	ctx := context.Background()

	_, err := r.Exec(ctx, ExecParams{
		SessionID: "st", Runtime: RuntimePython, Code: "import time\ntime.sleep(5)", TimeoutMs: 400,
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}

	// The session was dropped after the timeout; the next call must start a
	// fresh process and succeed promptly.
	done := make(chan ExecResult, 1)
	go func() {
		res, err := r.Exec(ctx, ExecParams{SessionID: "st", Runtime: RuntimePython, Code: "print('fresh')"})
		if err != nil {
			t.Errorf("post-timeout block: %v", err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		if strings.TrimSpace(res.Stdout) != "fresh" {
			t.Errorf("stdout = %q, want fresh", res.Stdout)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("post-timeout block did not return — session wedged")
	}
}

func TestSessionRunner_UnsupportedRuntime(t *testing.T) {
	r := newTestRunner(t)
	_, err := r.Exec(context.Background(), ExecParams{SessionID: "sx", Runtime: "cobol", Code: "DISPLAY 1"})
	if err == nil || !strings.Contains(err.Error(), "unsupported runtime") {
		t.Fatalf("expected unsupported-runtime error, got %v", err)
	}
}

func TestSessionRunner_EmptyCodeRejected(t *testing.T) {
	r := newTestRunner(t)
	if _, err := r.Exec(context.Background(), ExecParams{SessionID: "se", Runtime: RuntimePython}); err == nil {
		t.Fatal("expected empty-code error")
	}
}

func TestSessionRunner_PythonCompoundStatement(t *testing.T) {
	if !runtimeAvailable("python3") {
		t.Skip("python3 not available on this host")
	}
	r := newTestRunner(t)
	ctx := context.Background()

	// A block that ENDS inside a compound statement (dedent-closed function
	// call follows) — the regression the exec(open(file)) fix covers: the
	// interactive interpreter cannot close a compound statement fed via pipe.
	code := "def add(a, b):\n    return a + b\n\nresult = add(19, 23)\n"
	res, err := r.Exec(ctx, ExecParams{SessionID: "s-cmpd", Runtime: RuntimePython, Code: code})
	if err != nil {
		t.Fatalf("compound block: %v", err)
	}
	if strings.Contains(res.Stderr, "SyntaxError") {
		t.Errorf("compound statement SyntaxError'd — exec(open(file)) fix regressed: stderr=%q", res.Stderr)
	}

	res2, err := r.Exec(ctx, ExecParams{SessionID: "s-cmpd", Runtime: RuntimePython, Code: "print(result)"})
	if err != nil {
		t.Fatalf("follow-up block: %v", err)
	}
	if strings.TrimSpace(res2.Stdout) != "42" {
		t.Errorf("stdout = %q, want 42 — compound def did not land in REPL globals", res2.Stdout)
	}
}
