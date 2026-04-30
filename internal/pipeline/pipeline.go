package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
)

// Pipeline orchestrates stage execution for a single agent run.
type Pipeline struct {
	setup     []Stage // runs once before iteration loop
	iteration []Stage // runs per iteration
	finalize  []Stage // runs once after loop

	Deps PipelineDeps
}

// NewPipeline creates a pipeline from explicit stage lists.
func NewPipeline(setup, iteration, finalize []Stage, deps PipelineDeps) *Pipeline {
	return &Pipeline{
		setup:     setup,
		iteration: iteration,
		finalize:  finalize,
		Deps:      deps,
	}
}

// NewDefaultPipeline creates the standard 8-stage pipeline.
// Setup: [ContextStage]. Iteration: [ThinkStage, PruneStage, ToolStage, ObserveStage, CheckpointStage].
// Finalize: [FinalizeStage].
func NewDefaultPipeline(deps PipelineDeps) *Pipeline {
	d := &deps
	memFlush := NewMemoryFlushStage(d)

	setup := []Stage{
		NewContextStage(d),
	}
	iteration := []Stage{
		NewThinkStage(d),
		NewPruneStage(d, memFlush),
		NewToolStage(d),
		NewObserveStage(d),
		NewCheckpointStage(d),
	}
	finalize := []Stage{
		NewFinalizeStage(d),
	}
	return NewPipeline(setup, iteration, finalize, deps)
}

// Run executes the full pipeline for a single agent run.
func (p *Pipeline) Run(ctx context.Context, state *RunState) (*RunResult, error) {
	start := time.Now()

	// 1. Setup (once)
	for _, stage := range p.setup {
		if err := stage.Execute(ctx, state); err != nil {
			return nil, fmt.Errorf("setup %s: %w", stage.Name(), err)
		}
	}
	// Propagate enriched context from setup stages (ContextStage injects agent/user/workspace values).
	if state.Ctx != nil {
		ctx = state.Ctx
	}

	// 2. Iteration loop
	// BreakLoop: complete all remaining stages in this iteration (ObserveStage must
	// capture FinalContent), then exit the outer loop.
	// AbortRun: exit inner loop immediately (unrecoverable, e.g. over budget after compaction).
	for state.Iteration = 0; state.Iteration < p.Deps.Config.MaxIterations; state.Iteration++ {
		for _, stage := range p.iteration {
			if err := stage.Execute(ctx, state); err != nil {
				runErr := fmt.Errorf("iter %d %s: %w", state.Iteration, stage.Name(), err)
				p.rollbackFailedTurn(context.WithoutCancel(ctx), state, runErr)
				return nil, runErr
			}
			// AbortRun exits inner loop immediately — skip remaining stages.
			if swr, ok := stage.(StageWithResult); ok && swr.Result() == AbortRun {
				state.ExitCode = AbortRun
				break
			}
		}

		// Check exit after all stages (or after AbortRun broke early).
		if state.ExitCode == AbortRun {
			break
		}
		for _, stage := range p.iteration {
			if swr, ok := stage.(StageWithResult); ok && swr.Result() == BreakLoop {
				state.ExitCode = BreakLoop
				break
			}
		}
		if state.ExitCode == BreakLoop {
			break
		}
		if ctx.Err() != nil {
			state.ExitCode = AbortRun
			break
		}
	}

	// 3. Finalize (once, errors logged not fatal).
	// Use background context so finalize stages can persist state even after cancellation.
	finalizeCtx := context.WithoutCancel(ctx)
	for _, stage := range p.finalize {
		if err := stage.Execute(finalizeCtx, state); err != nil {
			slog.Warn("finalize stage error", "stage", stage.Name(), "err", err)
		}
	}

	result := state.BuildResult()
	result.Duration = time.Since(start)
	return result, nil
}

func (p *Pipeline) rollbackFailedTurn(ctx context.Context, state *RunState, runErr error) {
	if p == nil || p.Deps.RollbackMessages == nil || state == nil || state.Input == nil || state.Input.SessionKey == "" || !state.BaselineSet {
		return
	}
	if !shouldRollbackFailedTurn(runErr) {
		return
	}
	baseline := append([]providers.Message(nil), state.BaselineHistory...)
	if err := p.Deps.RollbackMessages(ctx, state.Input.SessionKey, baseline); err != nil {
		slog.Warn("failed-turn rollback failed", "session", state.Input.SessionKey, "err", err, "run_err", runErr)
		return
	}
	state.Messages.SetHistory(baseline)
	slog.Info("failed-turn rolled back", "session", state.Input.SessionKey, "messages", len(baseline), "run_err", runErr)
}

func shouldRollbackFailedTurn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "llm call") ||
		strings.Contains(msg, "stream read error") ||
		strings.Contains(msg, "stream error") ||
		strings.Contains(msg, "internal_error") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "token too long")
}
