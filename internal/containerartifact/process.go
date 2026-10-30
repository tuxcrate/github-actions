package containerartifact

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// The workflow runs on Linux. Signal the entire process group so hook children
// cannot outlive a timeout, even when Bash is waiting for a foreground command.
func runProcess(ctx context.Context, cmd *exec.Cmd, grace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = grace
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return errors.Join(ctx.Err(), err)
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return errors.Join(ctx.Err(), <-done)
	}
}
