package util

import "sync"

type Concurrent[T any] struct {
	lock   *sync.RWMutex
	object T
}

func NewConcurrent[T any](obj T) *Concurrent[T] {
	return &Concurrent[T]{
		lock:   &sync.RWMutex{},
		object: obj,
	}
}

func NewConcurrentWithLock[T any](obj T, lock *sync.RWMutex) *Concurrent[T] {
	return &Concurrent[T]{
		lock:   lock,
		object: obj,
	}
}

func (c *Concurrent[T]) RWTransaction(f func(object T)) {
	c.lock.Lock()
	defer c.lock.Unlock()

	f(c.object)
}

func (c *Concurrent[T]) RWTransactionErr(f func(object T) error) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	return f(c.object)
}

func (c *Concurrent[T]) RTransaction(f func(object T)) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	f(c.object)
}

func (c *Concurrent[T]) RTransactionErr(f func(object T) error) error {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return f(c.object)
}
