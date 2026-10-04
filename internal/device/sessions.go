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

// Package device — direct-runtime persistent sessions (the executor v2).
//
// Replaces the old warm-container executor: no Docker anywhere. Each session
// (a step/loop/node coordinate) maps to ONE long-running local interpreter
// process — `python3 -i`, `node -i`, or an interactive `bash` — with a
// per-session working directory. Code blocks are fed on stdin between sentinel
// markers; the sentinel's arrival on stdout marks the end of the block, so
// imports/variables/files persist across a tool loop's turns (the same
// property the warm containers had, without the container).
//
// Direct execution is the accepted trust model: code runs with the user's
// privileges on the user's machine (the ladder author's own machine for local
// ladr runs; the granting user's machine for federated runs, where the device
// client prompts per-ladder consent before any block runs).
//
// Known runtime quirks (accepted, v1): a bare `exit N` in bash code terminates
// the persistent shell — the block errors with "session process exited
// mid-block" and the next call starts fresh; node's REPL echoes completion
// values into captured output (see cleanLine).
package device

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout is used when ExecParams.TimeoutMs is zero.
const DefaultTimeout = 30 * time.Second

// maxOutput caps captured stdout/stderr per call to protect the prompt from
// runaway output.
const maxOutput = 1 << 20 // 1 MiB

// DefaultSweepInterval / DefaultMaxIdle govern the idle reaper started by
// NewSessionRunner.
const (
	DefaultSweepInterval = 5 * time.Minute
	DefaultMaxIdle       = 15 * time.Minute
)

// sentinelBase — each runner randomizes its sentinel so user code printing the
// literal marker can't falsely end a block.
const sentinelBase = "__LADR_BLOCK_DONE_"

// SessionRunner runs code in persistent local interpreter processes keyed by
// session ID. It implements Backend. Safe for concurrent use.
type SessionRunner struct {
	mu       sync.Mutex
	sessions map[string]*sessionProc
	baseDir  string // workspace root; each session gets a subdirectory as cwd
	sentinel string
}

var _ Backend = (*SessionRunner)(nil)

// NewSessionRunner returns a runner with a background idle reaper (processes
// unused for DefaultMaxIdle are killed). baseDir is the workspace root under
// which per-session directories are created; "" → a temp dir. The reaper uses
// context.Background and lives for the process.
func NewSessionRunner(baseDir string) *SessionRunner {
	return newSessionRunner(context.Background(), baseDir, DefaultSweepInterval, DefaultMaxIdle)
}

func newSessionRunner(ctx context.Context, baseDir string, sweep, maxIdle time.Duration) *SessionRunner {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	r := &SessionRunner{
		sessions: map[string]*sessionProc{},
		baseDir:  baseDir,
		sentinel: sentinelBase + hex.EncodeToString(b) + "__",
	}
	if sweep > 0 && maxIdle > 0 {
		go r.reaperLoop(ctx, sweep, maxIdle)
	}
	return r
}

func (r *SessionRunner) reaperLoop(ctx context.Context, sweep, maxIdle time.Duration) {
	t := time.NewTicker(sweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.reapIdle(maxIdle)
		}
	}
}

func (r *SessionRunner) reapIdle(maxIdle time.Duration) {
	now := time.Now()
	r.mu.Lock()
	var kill []*sessionProc
	for k, s := range r.sessions {
		s.mu.Lock()
		idle := now.Sub(s.lastUsed)
		s.mu.Unlock()
		if idle > maxIdle {
			kill = append(kill, s)
			delete(r.sessions, k)
		}
	}
	r.mu.Unlock()
	for _, s := range kill {
		s.kill()
	}
}

// Shutdown kills every session process. Best-effort.
func (r *SessionRunner) Shutdown() {
	r.mu.Lock()
	procs := make([]*sessionProc, 0, len(r.sessions))
	for k, s := range r.sessions {
		procs = append(procs, s)
		delete(r.sessions, k)
	}
	r.mu.Unlock()
	for _, s := range procs {
		s.kill()
	}
}

// sessionProc is one live interpreter process.
type sessionProc struct {
	runtime Runtime
	cwd     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser

	execMu sync.Mutex // serializes blocks on this session (one stdin, one sentinel stream)

	mu       sync.Mutex
	cond     *sync.Cond
	lines    []string // buffered stdout lines (consumed per block)
	eof      bool     // stdout reader closed
	dead     bool     // process exited / killed
	lastUsed time.Time

	stderrMu     sync.Mutex
	stderrCur    *bytes.Buffer // current block's stderr capture
	stderrDropped bool         // hit the cap
}

// launch starts the interpreter process for a runtime.
func launchRuntime(rt Runtime, cwd string) (*sessionProc, error) {
	var cmd *exec.Cmd
	switch rt {
	case RuntimePython:
		if _, err := exec.LookPath("python3"); err != nil {
			return nil, errors.New("runtime python not found on this host (install python3)")
		}
		cmd = exec.Command("python3", "-i", "-q", "-u")
	case RuntimeNode:
		if _, err := exec.LookPath("node"); err != nil {
			return nil, errors.New("runtime node not found on this host (install node)")
		}
		cmd = exec.Command("node", "-i")
	case RuntimeBash:
		if _, err := exec.LookPath("bash"); err != nil {
			return nil, errors.New("runtime bash not found on this host (install bash)")
		}
		cmd = exec.Command("bash", "--noprofile", "--norc", "-i")
	default:
		return nil, fmt.Errorf("unsupported runtime %q", rt)
	}
	cmd.Dir = cwd
	// Prompt hygiene: keep interactive prompts empty/quiet so stdout stays parseable.
	cmd.Env = append(os.Environ(), "PS1=", "PS2=", "PROMPT_COMMAND=", "NODE_NO_READLINE=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s session: %w", rt, err)
	}

	s := &sessionProc{
		runtime:  rt,
		cwd:      cwd,
		cmd:      cmd,
		stdin:    stdin,
		lastUsed: time.Now(),
		stderrCur: &bytes.Buffer{},
	}
	s.cond = sync.NewCond(&s.mu)

	go s.readStdout(stdout)
	go s.readStderr(stderr)

	// Bash: silence job-control noise + make the sentinel carry exit codes.
	if rt == RuntimeBash {
		_, _ = io.WriteString(stdin, "set +m\n")
	}
	return s, nil
}

// readStdout feeds the line buffer until the pipe closes. REPL prompt lines
// and banners are filtered so the block reader only sees program output.
func (s *sessionProc) readStdout(rd io.Reader) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line, ok := s.cleanLine(sc.Text()); ok {
			s.mu.Lock()
			s.lines = append(s.lines, line)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	s.eof = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// cleanLine filters REPL prompts/banners from a stdout line. Node's REPL
// writes its "> " prompt without a trailing newline, so the prompt fuses with
// the program output that follows on the same line — strip the prefix (node
// only; python/bash prompts never reach stdout here). Returns ok=false for
// pure prompt/banner lines.
//
// Node quirk (accepted, v1): the REPL also echoes each statement's completion
// value ("undefined" for var/console.log), which lands in the captured output.
// Python (prompts suppressed when stdin is a pipe) and bash (prompts go to
// stderr) stay clean.
func (s *sessionProc) cleanLine(line string) (string, bool) {
	t := strings.TrimSpace(line)
	switch t {
	case ">>>", "...", ">", "$":
		return "", false
	}
	if strings.HasPrefix(t, "Welcome to Node.js") {
		return "", false
	}
	if s.runtime == RuntimeNode {
		for strings.HasPrefix(t, "> ") {
			t = t[2:]
		}
		if t == "" {
			return "", false
		}
		return t, true
	}
	return line, true
}

// readStderr continuously captures stderr into the current block buffer.
func (s *sessionProc) readStderr(rd io.Reader) {
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		s.stderrMu.Lock()
		if s.stderrCur.Len() < maxOutput {
			s.stderrCur.WriteString(sc.Text())
			s.stderrCur.WriteByte('\n')
		} else {
			s.stderrDropped = true
		}
		s.stderrMu.Unlock()
	}
}

// nextLine waits for the next buffered stdout line (or eof/dead).
func (s *sessionProc) nextLine(ctx context.Context) (string, bool) {
	// Fast path: ctx already done.
	if err := ctx.Err(); err != nil {
		return "", false
	}
	done := make(chan struct{})
	go func() {
		s.mu.Lock()
		for len(s.lines) == 0 && !s.eof && !s.dead {
			s.cond.Wait()
		}
		s.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// Wake the waiter so it can re-check; it will see ctx via a second path.
		s.mu.Lock()
		s.dead = s.dead || true // treat timeout as fatal for reads
		s.cond.Broadcast()
		s.mu.Unlock()
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) > 0 {
		line := s.lines[0]
		s.lines = s.lines[1:]
		return line, true
	}
	return "", false // eof or dead
}

// takeStderr returns the current stderr capture, swapping in a fresh buffer.
func (s *sessionProc) takeStderr() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	out := s.stderrCur.String()
	s.stderrCur = &bytes.Buffer{}
	s.stderrDropped = false
	return out
}

func (s *sessionProc) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// kill terminates the process and marks the session dead.
func (s *sessionProc) kill() {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return
	}
	s.dead = true
	s.cond.Broadcast()
	s.mu.Unlock()
	_ = s.cmd.Process.Kill()
	_, _ = s.cmd.Process.Wait()
}

var unsafeNameChar = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sessionKey combines session ID + runtime (a session may switch runtimes;
// each runtime is its own process but shares the workspace dir).
func sessionKey(sessionID string, rt Runtime) string {
	return string(rt) + "|" + sessionID
}

// sessionDir returns (and creates) the per-session workspace directory.
func (r *SessionRunner) sessionDir(sessionID string) (string, error) {
	root := r.baseDir
	if root == "" {
		root = os.TempDir() + "/ladr-sessions"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dir := root + "/" + unsafeNameChar.ReplaceAllString(sessionID, "-")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// SessionDir returns (and creates) the per-session workspace directory — the
// same directory the session's interpreter processes use as cwd. Hosts that
// implement a local files: scope share it, so files written via files: are
// visible to code run via Exec within one adaptive loop, and vice versa.
func (r *SessionRunner) SessionDir(sessionID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionDir(sessionID)
}

// Exec runs p.Code in the session's persistent interpreter process, starting
// one lazily. The sentinel protocol delimits the block; state (imports,
// variables, files in the session cwd) persists across calls.
func (r *SessionRunner) Exec(ctx context.Context, p ExecParams) (ExecResult, error) {
	if p.Code == "" {
		return ExecResult{}, errors.New("executor: empty code")
	}
	if _, ok := map[Runtime]bool{RuntimePython: true, RuntimeNode: true, RuntimeBash: true}[p.Runtime]; !ok {
		return ExecResult{}, fmt.Errorf("executor: unsupported runtime %q", p.Runtime)
	}

	s, err := r.ensureSession(p.SessionID, p.Runtime)
	if err != nil {
		return ExecResult{}, err
	}

	timeout := DefaultTimeout
	if p.TimeoutMs > 0 {
		timeout = time.Duration(p.TimeoutMs) * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := s.runBlock(cctx, p.Code, r.sentinel, timeout)
	s.touch()
	if err != nil {
		// The session is in an unknown state after a timeout — drop it so the
		// next call starts fresh rather than wedging on a half-read block.
		r.dropSession(p.SessionID, p.Runtime)
		return res, err
	}
	return res, nil
}

// runBlock writes the code + sentinel, then reads stdout until the sentinel
// line. For bash the sentinel line carries the block's exit code
// ("sentinel:<n>"). execMu serializes blocks on one session — concurrent
// writes to the same stdin would interleave and corrupt the protocol.
func (s *sessionProc) runBlock(ctx context.Context, code, sentinel string, timeout time.Duration) (ExecResult, error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()

	var tail string
	writeCode := code
	switch s.runtime {
	case RuntimePython:
		// Feed the block via exec(open(file)) instead of pasting it onto
		// stdin: the interactive interpreter reading a pipe cannot close a
		// compound statement on dedent — a block ending inside a def/class
		// SyntaxErrors. exec() compiles the block as a unit (defs/loops/
		// classes work) and still runs in the REPL's globals so state
		// persists across blocks. (Backport of the TS client's fix.)
		blockFile := filepath.Join(s.cwd, ".ladr_block.py")
		if err := os.WriteFile(blockFile, []byte(code), 0o600); err != nil {
			return ExecResult{}, fmt.Errorf("executor: write block file: %w", err)
		}
		writeCode = "exec(open(" + strconv.Quote(blockFile) + ").read())"
		tail = "\nprint(\"" + sentinel + "\")\n"
	case RuntimeNode:
		tail = "\nconsole.log(\"" + sentinel + "\")\n"
	case RuntimeBash:
		// Group so $? is the block's exit status, echoed as sentinel:<code> on
		// stdout (the block reader waits for the sentinel there).
		tail = "\n{ echo \"" + sentinel + ":$?\"; }\n"
	default:
		return ExecResult{}, fmt.Errorf("unsupported runtime %q", s.runtime)
	}

	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return ExecResult{}, errors.New("executor: session process exited")
	}
	s.mu.Unlock()

	if _, err := io.WriteString(s.stdin, writeCode+"\n"+tail); err != nil {
		return ExecResult{}, fmt.Errorf("executor: write to session stdin: %w", err)
	}

	var out bytes.Buffer
	exit := 0
	for {
		line, ok := s.nextLine(ctx)
		if !ok {
			if ctxErr := ctx.Err(); ctxErr != nil {
				s.kill()
				return ExecResult{Stdout: out.String(), Stderr: s.takeStderr()},
					fmt.Errorf("executor: code timed out after %s", timeout)
			}
			return ExecResult{Stdout: out.String(), Stderr: s.takeStderr()},
				errors.New("executor: session process exited mid-block")
		}
		if strings.Contains(line, sentinel) {
			// Bash stamps the block's exit status after the sentinel
			// ("sentinel:<n>"); parse it off the line before breaking (the
			// sentinel line itself never reaches the captured output).
			if s.runtime == RuntimeBash {
				rest := line[strings.Index(line, sentinel)+len(sentinel):]
				rest = strings.TrimPrefix(rest, ":")
				if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
					exit = n
				}
			}
			break
		}
		if out.Len() < maxOutput {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return ExecResult{Stdout: out.String(), Stderr: s.takeStderr(), ExitCode: exit}, nil
}

// ensureSession returns the live session process for (sessionID, runtime),
// launching one lazily.
func (r *SessionRunner) ensureSession(sessionID string, rt Runtime) (*sessionProc, error) {
	key := sessionKey(sessionID, rt)
	r.mu.Lock()
	if s, ok := r.sessions[key]; ok {
		s.mu.Lock()
		dead := s.dead
		s.mu.Unlock()
		if !dead {
			r.mu.Unlock()
			return s, nil
		}
		// dead → replace below
		delete(r.sessions, key)
	}
	r.mu.Unlock()

	cwd, err := r.sessionDir(sessionID)
	if err != nil {
		return nil, err
	}
	s, err := launchRuntime(rt, cwd)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	// Latest-wins if two goroutines raced to launch.
	if prev, ok := r.sessions[key]; ok {
		r.mu.Unlock()
		s.kill()
		return prev, nil
	}
	r.sessions[key] = s
	r.mu.Unlock()
	return s, nil
}

func (r *SessionRunner) dropSession(sessionID string, rt Runtime) {
	key := sessionKey(sessionID, rt)
	r.mu.Lock()
	s, ok := r.sessions[key]
	if ok {
		delete(r.sessions, key)
	}
	r.mu.Unlock()
	if ok {
		s.kill()
	}
}
