package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
)

func (a *Agent) runCommandLoop(ctx context.Context) {
	intv := time.Duration(a.cfg.Intervals.StateSec) * time.Second
	if intv <= 0 {
		intv = 15 * time.Second
	}

	ticker := time.NewTicker(intv)
	defer ticker.Stop()

	for {
		if err := a.executeNextCommand(ctx); err != nil {
			a.log.Warn("command-sync", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) executeNextCommand(ctx context.Context) error {
	command, err := a.ctrl.GetNextCommand(ctx)
	if err != nil {
		return err
	}
	if command == nil {
		return nil
	}

	startedAt := time.Now().UTC()
	a.log.Info("executing agent command", "command_id", command.ID, "type", command.Type)

	execErr := a.executeAgentCommand(ctx, command.Type)
	ack := &model.AgentCommandAck{
		Status: model.AgentCommandAckSucceeded,
		Result: map[string]any{
			"executed_at": startedAt.Format(time.RFC3339),
			"type":        string(command.Type),
		},
	}
	if execErr != nil {
		ack.Status = model.AgentCommandAckFailed
		ack.ErrorMessage = execErr.Error()
	}

	if ackErr := a.ctrl.AckCommand(ctx, command.ID, ack); ackErr != nil {
		return fmt.Errorf("ack command %s: %w", command.ID, ackErr)
	}

	if execErr != nil {
		a.log.Warn(
			"agent command failed",
			"command_id",
			command.ID,
			"type",
			command.Type,
			"err",
			execErr,
		)
		return nil
	}

	a.log.Info("agent command completed", "command_id", command.ID, "type", command.Type)
	return nil
}

func (a *Agent) executeAgentCommand(ctx context.Context, commandType model.AgentCommandType) error {
	switch commandType {
	case model.AgentCommandTypeRestartCore:
		return runSystemctl(ctx, "restart", "xray")
	case model.AgentCommandTypeRestartAgent:
		return runSystemctl(ctx, "restart", "--no-block", "xray-agent")
	default:
		return fmt.Errorf("unsupported command type: %s", commandType)
	}
}

func runSystemctl(ctx context.Context, args ...string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "systemctl", args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	if message != "" {
		return fmt.Errorf("systemctl %s failed: %s", strings.Join(args, " "), message)
	}

	return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
}
