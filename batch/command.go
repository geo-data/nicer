package batch

import (
	"context"
	"os/exec"
)

// ShellCommand provides a Commander that will cover most purposes.
var ShellCommand *CommandContext = NewCommandContext("sh", "-c")

// CommandContext is a Commander that wraps the standard exec.CommandContext
// function.
type CommandContext struct {
	// Name is the path of the shell binary to call.
	Name string
	// Args are the arguments to pass to the shell, excluding the command to execute.
	Args []string
}

// NewCommandContext instantiates a CommandContext.
func NewCommandContext(name string, args ...string) *CommandContext {
	return &CommandContext{name, args}
}

// CommandContext implements the Commander interface.  It wraps the standard
// exec.CommandContext function, passing it the Name and Args after appending
// cmdStr to Args.
func (c *CommandContext) CommandContext(ctx context.Context, cmdStr string) (cmd *exec.Cmd, err error) {
	args := make([]string, len(c.Args))
	copy(args, c.Args)
	args = append(args, cmdStr)
	cmd = exec.CommandContext(ctx, c.Name, args...)
	return
}
