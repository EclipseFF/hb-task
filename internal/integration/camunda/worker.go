package camunda

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"hb-task/internal/core"
)

type Worker struct {
	Client *Client
	Repo   core.Repository
	ID     string
}

func (w Worker) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, topic := range []string{"check-balance", "record-decision"} {
		workers.Go(func() { w.runTopic(ctx, topic) })
	}
	workers.Wait()
}

func (w Worker) runTopic(ctx context.Context, topic string) {
	for ctx.Err() == nil {
		tasks, err := w.Client.Fetch(ctx, w.ID, topic)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("fetch jobs", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}

		for _, task := range tasks {
			taskCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			err := w.handle(taskCtx, task)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Error("job failed", "task_id", task.ID, "topic", task.Topic, "error", err)
				if failureErr := w.Client.Failure(ctx, task, err); failureErr != nil {
					slog.Error("report job failure", "error", failureErr)
				}
			}
		}
	}
}

func (w Worker) handle(ctx context.Context, task Task) error {
	id, err := task.String("requestId")
	if err != nil {
		return err
	}
	vars := map[string]any{}

	switch task.Topic {
	case "check-balance":
		result, err := w.Repo.Reserve(ctx, id)
		if err != nil {
			return err
		}
		vars["balanceSufficient"] = result.Sufficient
		vars["needsManager"] = result.NeedsManager

	case "record-decision":
		result, err := task.String("result")
		if err != nil {
			return err
		}
		if err := w.Repo.Finalize(ctx, id, core.Status(result)); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown job type %s", task.Topic)
	}
	return w.Client.Complete(ctx, task, vars)
}
