package ws

import (
	"testing"
	"time"
)

// roomExists checks the hub's room map under its lock.
func roomExists(h *Hub, roomID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.Rooms[roomID]
	return ok
}

// waitForRoomGone polls until the room disappears from the hub or times out.
func waitForRoomGone(t *testing.T, h *Hub, roomID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for roomExists(h, roomID) {
		if time.Now().After(deadline) {
			t.Fatalf("room %q was not removed from the hub", roomID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHubBroadcast_RoomIsolation(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	inRoom := NewClient(hub, nil, "room-a", 1)
	otherRoom := NewClient(hub, nil, "room-b", 2)

	hub.Register <- inRoom
	hub.Register <- otherRoom

	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-a",
		Message: []byte(`{"type":"hello"}`),
	}

	select {
	case <-inRoom.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("client in the target room did not receive the broadcast")
	}

	select {
	case msg := <-otherRoom.Send:
		t.Fatalf("client in a different room received the broadcast: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// OK — message stayed within room-a.
	}
}

func TestHub_RegisterMultipleClients_SystemBroadcastReachesAll(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	first := NewClient(hub, nil, "room-1", 1)
	second := NewClient(hub, nil, "room-1", 2)

	hub.Register <- first
	hub.Register <- second

	// Sender nil marks a system message: nobody is skipped.
	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"system"}`),
	}

	for _, c := range []*Client{first, second} {
		select {
		case <-c.Send:
		case <-time.After(2 * time.Second):
			t.Fatalf("client %d did not receive the system broadcast", c.UserID)
		}
	}
}

func TestHub_UnregisterLastClientRemovesRoom(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	first := NewClient(hub, nil, "room-1", 1)
	second := NewClient(hub, nil, "room-1", 2)

	hub.Register <- first
	hub.Register <- second

	hub.Unregister <- first
	select {
	case <-first.done:
	case <-time.After(2 * time.Second):
		t.Fatal("first client was not closed on unregister")
	}
	// One client remains, so the room must still exist.
	if !roomExists(hub, "room-1") {
		t.Fatal("room was removed while a client was still registered")
	}

	hub.Unregister <- second
	select {
	case <-second.done:
	case <-time.After(2 * time.Second):
		t.Fatal("second client was not closed on unregister")
	}
	waitForRoomGone(t, hub, "room-1")
}

func TestHubBroadcast_UnknownRoomIsNoOp(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	client := NewClient(hub, nil, "room-1", 1)
	hub.Register <- client

	// Broadcasting to a room nobody joined must not panic or wedge the hub.
	hub.Broadcast <- &RoomMessage{
		RoomID:  "ghost-room",
		Message: []byte(`{"type":"into the void"}`),
	}

	// The hub still services real rooms afterwards.
	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"alive"}`),
	}
	select {
	case <-client.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("hub stopped delivering after a broadcast to an unknown room")
	}
}

func TestHub_UnregisterUnknownClientIsNoOp(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	registered := NewClient(hub, nil, "room-1", 1)
	hub.Register <- registered

	// Unregistering a client that never registered (e.g. handshake failed
	// before Register) must not disturb the room or panic.
	stranger := NewClient(hub, nil, "room-1", 99)
	hub.Unregister <- stranger

	select {
	case <-stranger.done:
		// The hub still marks the stray client closed so its pumps exit.
	case <-time.After(2 * time.Second):
		t.Fatal("stray client was not marked closed")
	}

	if !roomExists(hub, "room-1") {
		t.Fatal("room was removed by unregistering a non-member")
	}

	hub.Broadcast <- &RoomMessage{
		RoomID:  "room-1",
		Message: []byte(`{"type":"still here"}`),
	}
	select {
	case <-registered.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("registered client did not receive broadcast after stray unregister")
	}
}
