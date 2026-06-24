// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package async

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
)

// Operation is a single long-running unit of work. It MUST be context-aware and
// idempotent: it may run again after a controller restart or a retry.
type Operation func(ctx context.Context) error

// DoneEvent reports the submitted key and terminal error (nil on success) when an operation finishes.
type DoneEvent struct {
	Key string
	Err error
}

// Listener is notified when an operation finishes.
type Listener interface {
	HandleDone(evt DoneEvent)
}

// ListenerFuncs is an adapter to use ordinary functions as a Listener.
type ListenerFuncs struct {
	HandleDoneFunc func(evt DoneEvent)
}

// HandleDone implements Listener.
func (l ListenerFuncs) HandleDone(evt DoneEvent) {
	if l.HandleDoneFunc != nil {
		l.HandleDoneFunc(evt)
	}
}

var (
	// ErrInProgress is returned by Submit when an operation for the same key is
	// already running. The caller should do nothing and wait for the completion
	// notification to requeue the object.
	ErrInProgress = errors.New("async: operation for key already in progress")
	// ErrAtCapacity is returned by Submit when the runner is at its maxWorkers cap.
	// The caller should requeue (with backoff) and try again when a slot frees.
	ErrAtCapacity = errors.New("async: at max worker capacity")
	// ErrNotRunning is returned by Submit before Start has been called. The caller
	// should requeue and try again once the runner is started.
	ErrNotRunning = errors.New("async: runner not started")
)

// submitRequest is sent from Submit to the dispatcher loop.
type submitRequest struct {
	key    string
	op     Operation
	result chan error // receives nil (accepted), ErrInProgress, or ErrAtCapacity
}

// Runner executes submitted operations with bounded concurrency, de-duplicating by
// key and notifying listeners on completion. A Runner must be created with New and
// run with Start before operations are submitted.
type Runner struct {
	name       string
	log        logr.Logger
	maxWorkers int

	mu        sync.Mutex
	running   bool
	listeners []Listener

	submitCh chan submitRequest
	doneCh   chan DoneEvent
}

func New(log logr.Logger, name string, maxWorkers int) *Runner {
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	return &Runner{
		name:       name,
		log:        log.WithName("async-runner").WithValues("runner", name),
		maxWorkers: maxWorkers,
		submitCh:   make(chan submitRequest),
		doneCh:     make(chan DoneEvent),
	}
}

// AddListener registers a listener. It may be called before or after Start.
func (r *Runner) AddListener(l Listener) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = append(r.listeners, l)
}

// Start runs the dispatcher loop until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return fmt.Errorf("async runner %q already started", r.name)
	}
	r.running = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	r.log.V(1).Info("Starting async runner", "maxWorkers", r.maxWorkers)
	r.loop(ctx)
	r.log.V(1).Info("Stopped async runner")
	return nil
}

// loop is the single dispatcher goroutine. It exclusively owns inFlight and active,
// so they require no locking. The mutex only guards the lifecycle/listener fields.
func (r *Runner) loop(ctx context.Context) {
	inFlight := make(map[string]struct{})
	active := 0

	for {
		select {
		case <-ctx.Done():
			return

		case evt := <-r.doneCh:
			delete(inFlight, evt.Key)
			if active > 0 {
				active--
			} else {
				r.log.Info("unexpected done event with active counter already zero",
					"key", evt.Key, "err", evt.Err)
			}
			r.notify(evt)

		case req := <-r.submitCh:
			switch {
			case func() bool { _, ok := inFlight[req.key]; return ok }():
				req.result <- ErrInProgress
			case active >= r.maxWorkers:
				req.result <- ErrAtCapacity
			default:
				inFlight[req.key] = struct{}{}
				active++
				go r.run(ctx, req.key, req.op)
				req.result <- nil
			}
		}
	}
}

func (r *Runner) run(ctx context.Context, key string, op Operation) {
	var err error
	defer func() {
		select {
		case r.doneCh <- DoneEvent{Key: key, Err: err}:
		case <-ctx.Done():
		}
	}()
	err = op(ctx)
}

// notify invokes listeners; HandleDone must not block.
func (r *Runner) notify(evt DoneEvent) {
	r.mu.Lock()
	listeners := make([]Listener, len(r.listeners))
	copy(listeners, r.listeners)
	r.mu.Unlock()

	for _, l := range listeners {
		l.HandleDone(evt)
	}
}

// Submit hands an operation to the dispatcher loop, keyed by key. It returns:
//   - nil:           accepted; a worker goroutine was spawned.
//   - ErrInProgress: an operation for key is already running.
//   - ErrAtCapacity: the runner is at its maxWorkers cap.
//   - ErrNotRunning: Start has not been called yet.
//   - ctx.Err():     ctx was cancelled while submitting.
//
// Submit does not block on the operation itself; it only waits for the loop to accept
// or reject the request.
//
// ctx must cancel when the runner stops (typically the same one passed to Start);
// a longer-lived ctx may block on submitCh after the loop exits.
func (r *Runner) Submit(ctx context.Context, key string, op Operation) error {
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if !running {
		return ErrNotRunning
	}

	result := make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.submitCh <- submitRequest{key: key, op: op, result: result}:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}
