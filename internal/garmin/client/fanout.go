package client

import (
	"context"
	"sync"
)

// GetJSON performs req and decodes the response into out.
//
// The payload is returned as well, so a caller can keep the raw bytes for
// diagnostics — bounded, sealed and unloggable — next to the decoded model.
func (c *Client) GetJSON(ctx context.Context, session Session, req Request, out any) (Payload, error) {
	payload, err := c.Do(ctx, session, req)
	if err != nil {
		return payload, err
	}
	if err := DecodeJSON(payload, out); err != nil {
		return payload, err
	}
	return payload, nil
}

// FanOut runs count tasks with at most Limits.MaxConcurrency of them in flight.
//
// The bound exists because a fan-out is the one place a single tool call can turn
// into an unbounded burst against Garmin: one request per day of a wide date range
// would trip the account's rate limit and look like abuse. A non-positive count is
// no work at all.
//
// The first failure cancels the derived context, so tasks that have not started
// never run and the ones in flight can return early. That failure is what FanOut
// reports; later failures are discarded, because one actionable cause is more
// useful than a pile of cancellations.
func (c *Client) FanOut(ctx context.Context, count int, task func(ctx context.Context, index int) error) error {
	if count <= 0 {
		return nil
	}
	if task == nil {
		return validationError("fan-out needs a task")
	}

	workerCount := min(c.limits.MaxConcurrency, count)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	indexes := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error

	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for index := range indexes {
				if err := task(runCtx, index); err != nil {
					once.Do(func() { firstErr = err })
					cancel()
					return
				}
			}
		}()
	}

	feedIndexes(runCtx, indexes, count)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// feedIndexes hands out work until the context is done, then closes the channel so
// every worker can finish.
func feedIndexes(ctx context.Context, indexes chan<- int, count int) {
	defer close(indexes)

	for index := range count {
		select {
		case indexes <- index:
		case <-ctx.Done():
			return
		}
	}
}
