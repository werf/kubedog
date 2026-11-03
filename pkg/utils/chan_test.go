package utils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSendOrDone_whenReceiverReads(t *testing.T) {
	ch := make(chan int)
	returned := make(chan struct{})
	go func() {
		SendOrDone(context.Background(), ch, 42)
		close(returned)
	}()

	select {
	case v := <-ch:
		assert.Equal(t, 42, v)
	case <-time.After(10 * time.Second):
		t.Fatal("value was not delivered")
	}

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("send did not return after the value was delivered")
	}
}

func TestSendOrDone_whenContextDoneAndNobodyReads(t *testing.T) {
	ch := make(chan int)
	ctx, cancel := context.WithCancel(context.Background())

	returned := make(chan struct{})
	go func() {
		SendOrDone(ctx, ch, 42)
		close(returned)
	}()

	cancel()

	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("send did not give up after the context was canceled")
	}
}
