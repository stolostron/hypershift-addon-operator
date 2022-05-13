package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	installcmd "github.com/openshift/hypershift/cmd/install"
)

type HypershiftInstallExecutorInterface interface {
	Execute(ctx context.Context, args []string) ([]byte, error)
}

type HypershiftCliExecutor struct {
}

var _ HypershiftInstallExecutorInterface = &HypershiftCliExecutor{}

type HypershiftLibExecutor struct {
}

var _ HypershiftInstallExecutorInterface = &HypershiftLibExecutor{}

func (c *HypershiftCliExecutor) Execute(ctx context.Context, args []string) ([]byte, error) {
	//hypershiftInstall will get the inClusterConfig and use it to apply resources
	//
	//skip the GoSec since we intent to run the hypershift binary
	cmd := exec.Command("hypershift", append([]string{"install"}, args...)...) //#nosec G204

	renderTemplate, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run the hypershift install render command, err: %w", err)
	}

	return renderTemplate, nil
}

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
