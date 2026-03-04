package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/najahiiii/xray-agent/internal/model"
)

const (
	coreRestartSyncRetries  = 6
	coreRestartSyncInterval = 1 * time.Second
	coreRestartSyncTimeout  = 5 * time.Second
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

	if command.Type == model.AgentCommandTypeRestartAgent {
		return a.ackThenRestartAgent(command.ID, startedAt)
	}

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

func (a *Agent) ackThenRestartAgent(commandID string, startedAt time.Time) error {
	ack := &model.AgentCommandAck{
		Status: model.AgentCommandAckSucceeded,
		Result: map[string]any{
			"executed_at": startedAt.Format(time.RFC3339),
			"type":        string(model.AgentCommandTypeRestartAgent),
			"mode":        "ack_then_restart",
		},
	}
	if ackErr := a.ctrl.AckCommand(context.Background(), commandID, ack); ackErr != nil {
		return fmt.Errorf("ack command %s: %w", commandID, ackErr)
	}

	a.log.Info("agent restart acknowledged, restarting service", "command_id", commandID)
	if err := runSystemctl(context.Background(), "restart", "--no-block", "xray-agent"); err != nil {
		a.log.Warn("restart agent trigger failed after ack", "command_id", commandID, "err", err)
		return nil
	}

	return nil
}

func (a *Agent) executeAgentCommand(ctx context.Context, commandType model.AgentCommandType) error {
	switch commandType {
	case model.AgentCommandTypeRestartCore:
		if err := runSystemctl(ctx, "restart", "xray"); err != nil {
			return err
		}
		if err := a.syncStateAfterCoreRestart(ctx); err != nil {
			return fmt.Errorf("restart core completed but immediate state sync failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported command type: %s", commandType)
	}
}

func (a *Agent) syncStateAfterCoreRestart(ctx context.Context) error {
	var lastErr error

	for attempt := 1; attempt <= coreRestartSyncRetries; attempt++ {
		syncCtx, cancel := context.WithTimeout(ctx, coreRestartSyncTimeout)
		err := a.syncStateOnce(syncCtx)
		cancel()
		if err == nil {
			a.log.Info("immediate state sync after core restart completed", "attempt", attempt)
			return nil
		}

		lastErr = err
		a.log.Warn("immediate state sync after core restart failed", "attempt", attempt, "err", err)

		if attempt >= coreRestartSyncRetries {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(coreRestartSyncInterval):
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("immediate state sync failed")
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
