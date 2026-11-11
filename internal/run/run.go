// Package run wraps external command execution with timeouts and clean errors.
package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes commands with a per-call timeout.
type Runner struct {
	DefaultTimeout time.Duration
}

// New returns a Runner with a sensible default timeout.
func New() *Runner {
	return &Runner{DefaultTimeout: 15 * time.Second}
}

// Result is the outcome of one command.
type Result struct {
	Stdout   string
	Stderr   string
	Err      error
	ExitCode int
	Duration time.Duration
}

// Run executes name with args, returning combined stdout/stderr on error.
func (r *Runner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) Result {
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(cmd.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Result{
		Stdout:   strings.TrimSpace(decodeOutput(out.Bytes())),
		Stderr:   strings.TrimSpace(decodeOutput(errb.Bytes())),
		Err:      err,
		Duration: time.Since(start),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		res.Err = fmt.Errorf("命令超时 (%s): %s %s", timeout, name, strings.Join(args, " "))
	}
	return res
}

// Output runs a command and returns stdout, or an error containing stderr.
func (r *Runner) Output(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	res := r.Run(ctx, timeout, name, args...)
	if res.Err != nil {
		return res.Stdout, CommandError(name, args, res)
	}
	return res.Stdout, nil
}

// RunInput executes a command with stdin and returns stdout, or a command error.
func (r *Runner) RunInput(ctx context.Context, timeout time.Duration, input, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = r.DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = append(cmd.Environ(), "LC_ALL=C.UTF-8", "LANG=C.UTF-8")
	cmd.Stdin = strings.NewReader(input)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Result{
		Stdout:   strings.TrimSpace(decodeOutput(out.Bytes())),
		Stderr:   strings.TrimSpace(decodeOutput(errb.Bytes())),
		Err:      err,
		Duration: time.Since(start),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		res.Err = fmt.Errorf("命令超时 (%s): %s", timeout, name)
	}
	if res.Err != nil {
		return res.Stdout, CommandError(name, args, res)
	}
	return res.Stdout, nil
}

// CommandError builds a concise, user-facing error.
func CommandError(name string, args []string, res Result) error {
	msg := res.Stderr
	if msg == "" {
		msg = res.Stdout
	}
	if msg == "" && res.Err != nil {
		msg = res.Err.Error()
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}
	cmdline := name
	if len(args) > 0 {
		cmdline += " " + strings.Join(args, " ")
	}
	if msg == "" {
		return fmt.Errorf("命令失败: %s", cmdline)
	}
	return fmt.Errorf("%s: %s", cmdline, msg)
}

// Available reports whether an executable exists in PATH.
func Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// FirstLine returns the first non-empty line of s.
func FirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// HasAny reports whether s contains one of the substrings (case-insensitive).
func HasAny(s string, subs ...string) bool {
	ls := strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(ls, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
