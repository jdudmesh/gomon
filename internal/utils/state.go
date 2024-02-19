package utils

import (
	"sync/atomic"
)

type State[T ~int] struct {
	innerState atomic.Int64
}

func NewState[T ~int](initialState T) *State[T] {
	s := &State[T]{
		innerState: atomic.Int64{},
	}
	s.innerState.Store(int64(initialState))
	return s
}

func (s *State[T]) Get() T {
	return T(s.innerState.Load())
}

func (s *State[T]) Set(newState T) {
	s.innerState.Store(int64(newState))
}
