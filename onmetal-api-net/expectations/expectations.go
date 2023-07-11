package expectations

import (
	"time"

	"github.com/onmetal/onmetal-api/broker/common/sync"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type expectation struct {
	timestamp time.Time
	delete    sets.Set[client.ObjectKey]
	create    sets.Set[client.ObjectKey]
}

type Expectations struct {
	timeout time.Duration

	entriesMu *sync.MutexMap[client.ObjectKey]
	entries   map[client.ObjectKey]*expectation
}

type NewOptions struct {
	Timeout time.Duration
}

func setNewOptionsDefaults(o *NewOptions) {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Minute
	}
}

func (o *NewOptions) ApplyOptions(opts []NewOption) {
	for _, opt := range opts {
		opt.ApplyToNew(o)
	}
}

func (o *NewOptions) ApplyToNew(o2 *NewOptions) {
	if o.Timeout > 0 {
		o2.Timeout = o.Timeout
	}
}

type NewOption interface {
	ApplyToNew(o *NewOptions)
}

type WithTimeout time.Duration

func (w WithTimeout) ApplyToNew(o *NewOptions) {
	o.Timeout = time.Duration(w)
}

func New(opts ...NewOption) *Expectations {
	o := &NewOptions{}
	o.ApplyOptions(opts)
	setNewOptionsDefaults(o)

	return &Expectations{
		timeout:   o.Timeout,
		entriesMu: sync.NewMutexMap[client.ObjectKey](),
		entries:   make(map[client.ObjectKey]*expectation),
	}
}

func (e *Expectations) Delete(ctrlKey client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	delete(e.entries, ctrlKey)
}

func (e *Expectations) ExpectDeletions(ctrlKey client.ObjectKey, deletedKeys []client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	e.entries[ctrlKey] = &expectation{
		timestamp: time.Now(),
		delete:    sets.New(deletedKeys...),
	}
}

func (e *Expectations) ExpectCreations(ctrlKey client.ObjectKey, createdKeys []client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	e.entries[ctrlKey] = &expectation{
		timestamp: time.Now(),
		create:    sets.New(createdKeys...),
	}
}

func (e *Expectations) ExpectCreationsAndDeletions(ctrlKey client.ObjectKey, createdKeys, deletedKeys []client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	e.entries[ctrlKey] = &expectation{
		timestamp: time.Now(),
		create:    sets.New(createdKeys...),
		delete:    sets.New(deletedKeys...),
	}
}

func (e *Expectations) CreationObserved(ctrlKey, createdKey client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	exp, ok := e.entries[ctrlKey]
	if !ok {
		return
	}

	exp.create.Delete(createdKey)
}

func (e *Expectations) DeletionObserved(ctrlKey, deletedKey client.ObjectKey) {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	exp, ok := e.entries[ctrlKey]
	if !ok {
		return
	}

	exp.delete.Delete(deletedKey)
}

func (e *Expectations) Satisfied(ctrlKey client.ObjectKey) bool {
	e.entriesMu.Lock(ctrlKey)
	defer e.entriesMu.Unlock(ctrlKey)

	exp, ok := e.entries[ctrlKey]
	if !ok {
		// We didn't record any expectation and are good to go.
		return true
	}
	if time.Since(exp.timestamp) > e.timeout {
		// Expectations timed out, release.
		return true
	}
	if exp.create.Len() == 0 && exp.delete.Len() == 0 {
		// All expectations satisfied
		return true
	}

	// There are still some pending expectations.
	return false
}
