package agent

import (
	"bytes"
	"context"
	"fmt"

	installcmd "github.com/openshift/hypershift/cmd/install"
)

type HypershiftInstallExecutorInterface interface {
	Execute(ctx context.Context, args []string) ([]byte, error)
}

type HypershiftLibExecutor struct {
}

var _ HypershiftInstallExecutorInterface = &HypershiftLibExecutor{}

func (c *HypershiftLibExecutor) Execute(ctx context.Context, args []string) ([]byte, error) {
	cmd := installcmd.NewCommand()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to run the hypershift install render command, err: %w", err)
	}

	return buf.Bytes(), nil
}
