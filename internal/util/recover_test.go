package util

import (
	"errors"
	"testing"
	"time"
)

func TestRecoverPanic_SwallowsPanicAndAllowsReturn(t *testing.T) {
	reached := false
	func() {
		defer RecoverPanic("unit test scope")
		reached = true
		panic(errors.New("kaboom"))
	}()

	if !reached {
		t.Fatal("function body did not run")
	}
	// Reaching this line at all proves the panic was recovered.
}

func TestRecoverPanic_NoPanicIsNoOp(t *testing.T) {
	func() {
		defer RecoverPanic("calm scope")
	}()
	// Nothing to assert: RecoverPanic must not itself panic or log spuriously
	// when there is no panic in flight.
}

func TestRecoverPanic_ProtectsBackgroundGoroutine(t *testing.T) {
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer RecoverPanic("background goroutine")
		panic("goroutine panic")
	}()

	// If RecoverPanic failed to recover, the unrecovered panic would crash the
	// whole test process, so completing this wait is the assertion.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not finish")
	}
}

func TestRecoverPanic_NonErrorPanicValue(t *testing.T) {
	func() {
		defer RecoverPanic("string panic scope")
		panic("plain string panic")
	}()

	func() {
		defer RecoverPanic("int panic scope")
		panic(42)
	}()
}
