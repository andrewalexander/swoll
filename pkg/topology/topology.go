package topology

import (
	"context"
	"sync"

	"github.com/criticalstack/swoll/pkg/types"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type OnEventCallback func(t EventType, container *types.Container)

// These are the two states in which an observer event can be in.
type EventType int

const (
	EventTypeStart EventType = iota // container started
	EventTypeStop                   // container stopped
)

// ErrNilEvent is the error returned to indicate the observer sent an empty
// message
var ErrNilEvent = errors.New("nil event")

// ErrNilContainer is the error returned to indicate the observer sent an empty
// container message
var ErrNilContainer = errors.New("nil container")

// ErrUnknownType is the error returned to indicate a malformed observer event
var ErrUnknownType = errors.New("unknown event-type")

// ErrBadNamespace is the error returned to indicate the observer was unable to
// resolve the PID-Namespace of the container
var ErrBadNamespace = errors.New("invalid kernel pid-namespace")

// ErrContainerNotFound is the error returned to indicate the container was
// unable to be resolved
var ErrContainerNotFound = errors.New("container not found")

type ObservationEvent struct {
	Type      EventType
	Container *types.Container
}

type Observer interface {
	Connect(ctx context.Context) error
	Containers(ctx context.Context) ([]*types.Container, error)
	Run(ctx context.Context, out chan<- *ObservationEvent)
	Copy(opts ...interface{}) (Observer, error)
	Close() error
}

type Topology struct {
	sync.RWMutex
	observer Observer
	cache    map[int]*types.Container
}

func NewTopology(obs Observer) *Topology {
	return &Topology{
		observer: obs,
		cache:    make(map[int]*types.Container),
	}
}

func (t *Topology) Close() error {
	if t != nil && t.observer != nil {
		return t.observer.Close()
	}

	return nil
}

func (t *Topology) Connect(ctx context.Context) error {
	return t.observer.Connect(ctx)
}

func (t *Topology) Containers(ctx context.Context) ([]*types.Container, error) {
	return t.observer.Containers(ctx)
}

func (t *Topology) Run(ctx context.Context, cb OnEventCallback) {
	ch := make(chan *ObservationEvent)
	go t.observer.Run(ctx, ch)

	for {
		select {
		case ev := <-ch:
			t.Lock()
			if err := t.processEvent(ctx, ev, cb); err != nil {
				log.Warnf("error processing event: %v", err)
			}

			t.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (t *Topology) LookupContainer(ctx context.Context, pidns int) (*types.Container, error) {
	t.RLock()
	defer t.RUnlock()

	if container, ok := t.cache[pidns]; ok {
		ret := container.Copy()
		return ret, nil
	}

	return nil, ErrContainerNotFound
}

func (t *Topology) processEvent(ctx context.Context, ev *ObservationEvent, cb OnEventCallback) error {
	if ev == nil {
		return ErrNilEvent
	}

	container := ev.Container
	if container == nil {
		return ErrNilContainer
	}

	if container.PidNamespace <= 0 {
		return ErrBadNamespace
	}

	switch ev.Type {
	case EventTypeStart:
		t.cache[container.PidNamespace] = container
		if cb != nil {
			t.Unlock()
			cb(EventTypeStart, container)
			t.Lock()
		}
	case EventTypeStop:
		delete(t.cache, container.PidNamespace)
		if cb != nil {
			t.Unlock()
			cb(EventTypeStop, container)
			t.Lock()
		}
	default:
		return ErrUnknownType
	}

	return nil
}
