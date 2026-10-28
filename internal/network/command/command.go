// Package command provides an injectable, shell-free system-command executor.
package command

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Executor interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type OS struct{}

func (OS) Run(ctx context.Context, program string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("run %s %s: %w: %s", program, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
