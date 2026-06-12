package ws

import (
	"testing"
	"time"
)

func TestHub_UnregisterClosesClientWithoutClosingSend(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	client := NewClient(hub, nil, "room-1", 1)
	hub.Register <- client
	hub.Unregister <- client

	select {
	case <-client.done:
	case <-time.After(2 * time.Second):
		t.Fatal("client done channel was not closed on unregister")
	}

	// The Send channel must remain open: TrySend returns false but never
	// panics with send-on-closed-channel.
	if client.TrySend([]byte("late message")) {
		t.Error("TrySend should return false after the client is closed")
	}
}

func TestHubBroadcast_FullSendBufferEvictsClientSafely(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	slow := NewClient(hub, nil, "room-1", 1)
	slow.Send = make(chan []byte, 1) // tiny buffer so it fills immediately
	fast := NewClient(hub, nil, "room-1", 2)

	hub.Register <- slow
	hub.Register <- fast

	// Fill the slow client's buffer so the broadcast cannot be delivered.
	slow.Send <- []byte("filler")

	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"first"}`),
	}

	// The fast client still receives the message.
	select {
	case <-fast.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("fast client did not receive the first broadcast")
	}

	// The slow client is evicted: done closed, Send left open.
	select {
	case <-slow.done:
	case <-time.After(2 * time.Second):
		t.Fatal("slow client was not evicted after its send buffer filled")
	}
	if slow.TrySend([]byte("x")) {
		t.Error("TrySend to an evicted client should return false")
	}

	// A second broadcast must not panic or deadlock and still reaches the
	// fast client.
	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"second"}`),
	}
	select {
	case <-fast.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("fast client did not receive the second broadcast")
	}

	// Unregistering the already-evicted client must be a no-op (no double
	// close panic).
	hub.Unregister <- slow

	// The hub keeps working afterwards.
	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"third"}`),
	}
	select {
	case <-fast.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("fast client did not receive the third broadcast")
	}
}

func TestHubBroadcast_SkipsSender(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	sender := NewClient(hub, nil, "room-1", 1)
	receiver := NewClient(hub, nil, "room-1", 2)

	hub.Register <- sender
	hub.Register <- receiver

	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"hello"}`),
		Sender:  sender,
	}

	select {
	case <-receiver.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("receiver did not get the broadcast")
	}

	select {
	case msg := <-sender.Send:
		t.Fatalf("sender should not receive its own broadcast, got %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// OK — nothing delivered to the sender
	}
}

func TestTrySend_UnblocksWhenClientCloses(t *testing.T) {
	client := NewClient(nil, nil, "room-1", 1)
	client.Send = make(chan []byte) // unbuffered with no reader: send blocks

	result := make(chan bool, 1)
	go func() {
		result <- client.TrySend([]byte("msg"))
	}()

	// Give the goroutine a moment to block on the send, then close the
	// client. Even if it hasn't blocked yet, TrySend must return false.
	time.Sleep(50 * time.Millisecond)
	client.markClosed()

	select {
	case ok := <-result:
		if ok {
			t.Error("expected TrySend to return false after the client closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TrySend did not unblock after the client closed")
	}
}
