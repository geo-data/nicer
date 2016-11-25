// Package batch provides functionality for executing a batch of commands in
// parallel while ensuring system resources fall within specified tolerances
// before any command is executed.
package batch

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/geo-data/nicer/threshold"
)

type (
	// Events is a set of hooks to be run at various stages of a Batch run.
	Events struct {
		Waiting     func()
		CmdStarted  func(count, pid int, cmd string)
		CmdFinished func(count, pid int, cmd string, err error)
		CmdFailed   func(count int, cmd string, err error)
		CmdStdout   func(count, pid int, cmd string, line []byte)
		CmdStderr   func(count, pid int, cmd string, line []byte)
		StdoutErr   func(count, pid int, cmd string, err error)
		StderrErr   func(count, pid int, cmd string, err error)
	}

	// Batch provides the machinery to run multiple commands in
	// parallel. Thresholds can be set against system metrics to ensure commands
	// are not run when these thresholds are exceeded.  Hooks can also be set to
	// trace and act on these events.
	Batch struct {
		Thresholds *threshold.Group
		Wait       time.Duration
		events     *Events
	}
)

// New instantiates a new Batch with specified thresholds and events.  The wait
// parameter determines how long to wait between running commands when there is
// no threshold wait for system metrics to return to tolerable limits.
func New(t *threshold.Group, wait time.Duration, e *Events) (b *Batch) {
	b = &Batch{
		Thresholds: t,
		Wait:       wait,
	}
	b.SetEvents(e)
	return
}

// SetEvents replaces the Events associated with the Batch.
func (b *Batch) SetEvents(e *Events) {
	// Ensure that all fields are populated to save nil checks during a Run.
	if e == nil {
		e = new(Events)
	}

	if e.Waiting == nil {
		e.Waiting = func() {}
	}

	if e.CmdStarted == nil {
		e.CmdStarted = func(count, pid int, cmd string) {}
	}

	if e.CmdFinished == nil {
		e.CmdFinished = func(count, pid int, cmd string, err error) {}
	}

	if e.CmdFailed == nil {
		e.CmdFailed = func(count int, cmd string, err error) {}
	}

	if e.CmdStdout == nil {
		e.CmdStdout = func(count, pid int, cmd string, line []byte) {}
	}

	if e.CmdStderr == nil {
		e.CmdStderr = func(count, pid int, cmd string, line []byte) {}
	}

	if e.StdoutErr == nil {
		e.StdoutErr = func(count, pid int, cmd string, err error) {}
	}

	if e.StderrErr == nil {
		e.StderrErr = func(count, pid int, cmd string, err error) {}
	}

	b.events = e
}

// Run runs commands received on the cmds channel, waiting between command
// invokations for either the time specified by Batch.Wait or until system
// resources fall below threshold values. Commands can be cancelled (i.e. sent
// the TERM signal) using the context.
func (b *Batch) Run(ctx context.Context, cmds <-chan string) {
	var wg sync.WaitGroup

	i := 0
Process:
	for {
		select {
		case <-ctx.Done(): // Check whether we have been asked to cancel.
			break Process
		case cmd, ok := <-cmds: // Process the incoming commands.
			if !ok {
				break Process
			}

			// Check whether we should wait for system metrics to return to
			// normal.
			i++
			if b.Thresholds != nil {
				if b.Thresholds.Exceeded() {
					b.events.Waiting()
					b.Thresholds.Wait()
				} else if i > 1 {
					time.Sleep(b.Wait)
					if b.Thresholds.Exceeded() {
						b.events.Waiting()
						b.Thresholds.Wait()
					}
				}
			}

			// Run the command.
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				b.runCommand(ctx, cmd, index)
			}(i)
		}
	}

	// Block until all commands have finished.
	wg.Wait()
}

// runCommand executes an individual command.
func (b *Batch) runCommand(ctx context.Context, cmdStr string, count int) {
	// Start the command and get the output streams.
	cmd, stdout, stderr, err := startCommand(ctx, cmdStr)
	if err != nil {
		b.events.CmdFailed(count, cmdStr, err)
		return
	}

	// Notify that the command has started.
	pid := cmd.Process.Pid
	b.events.CmdStarted(count, pid, cmdStr)

	// We need line scanners for the output streams.
	scanOut := bufio.NewScanner(stdout)
	scanErr := bufio.NewScanner(stderr)

	// Process the output streams.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for scanOut.Scan() {
			b.events.CmdStdout(count, pid, cmdStr, scanOut.Bytes())
		}
	}()
	go func() {
		defer wg.Done()
		for scanErr.Scan() {
			b.events.CmdStderr(count, pid, cmdStr, scanErr.Bytes())
		}
	}()
	wg.Wait()

	// Notify of any errors encountered whilst scanning.
	if err := scanOut.Err(); err != nil {
		b.events.StdoutErr(count, pid, cmdStr, err)
	}
	if err := scanErr.Err(); err != nil {
		b.events.StderrErr(count, pid, cmdStr, err)
	}

	// Wait for the command to finish, and notify to that effect.
	err = cmd.Wait()
	b.events.CmdFinished(count, pid, cmdStr, err)
}

// startCommand executes a command string and returns the command handle and
// associated output streams.
func startCommand(ctx context.Context, cmdStr string) (cmd *exec.Cmd, stdout, stderr io.ReadCloser, err error) {
	// Obtain a command.
	cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)

	// Get the output streams.
	if stdout, err = cmd.StdoutPipe(); err != nil {
		return
	}
	if stderr, err = cmd.StderrPipe(); err != nil {
		return
	}

	// Run the command.
	if err = cmd.Start(); err != nil {
		return
	}
	return
}
