package sync

import (
	"context"
	"errors"
	"sync"
)

// Coordinator owns at most one worker per workspace. Attaching or detaching
// a server only replaces runtime transport state; it never migrates or clears
// the local SQLCipher database, outbox, cursor, or materialized collection.
type Coordinator struct {
	lifecycle sync.Mutex
	mu        sync.Mutex
	root      context.Context
	cancel    context.CancelFunc
	worker    *Worker
	trigger   chan struct{}
	done      chan error
}

func NewCoordinator(root context.Context) *Coordinator {
	if root == nil {
		root = context.Background()
	}
	return &Coordinator{root: root}
}

func (c *Coordinator) Attach(worker *Worker) error {
	if worker == nil {
		return errors.New("sync: cannot attach a nil worker")
	}
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	c.stopAndWait()
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithCancel(c.root)
	c.cancel, c.worker = cancel, worker
	c.trigger = make(chan struct{}, 1)
	c.done = make(chan error, 1)
	go func(done chan<- error, triggers <-chan struct{}) { done <- worker.Run(ctx, triggers) }(c.done, c.trigger)
	c.trigger <- struct{}{}
	return nil
}

func (c *Coordinator) Trigger() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trigger == nil {
		return
	}
	select {
	case c.trigger <- struct{}{}:
	default:
	}
}

func (c *Coordinator) Detach() {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	c.stopAndWait()
}

func (c *Coordinator) stopAndWait() {
	c.mu.Lock()
	done := c.done
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel, c.worker, c.trigger, c.done = nil, nil, nil, nil
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (c *Coordinator) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.worker != nil
}

func (c *Coordinator) Close() error { c.Detach(); return nil }
