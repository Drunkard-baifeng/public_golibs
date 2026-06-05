package websocket

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
)

func TestTextRoundTrip(t *testing.T) {
	url, closeServer := startEchoServer(t)
	defer closeServer()

	client, err := Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	reply, err := client.RequestText("hello", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "hello" {
		t.Fatalf("reply = %q, want hello", reply)
	}
}

func TestHexRoundTrip(t *testing.T) {
	url, closeServer := startEchoServer(t)
	defer closeServer()

	client, err := Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	reply, err := client.RequestHexWithOptions("0x0a ff 12", " ", false, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "0a ff 12" {
		t.Fatalf("reply = %q, want 0a ff 12", reply)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	url, closeServer := startEchoServer(t)
	defer closeServer()

	client, err := Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var reply map[string]any
	err = client.RequestJSON(map[string]any{"op": "ping"}, &reply, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply["op"] != "ping" {
		t.Fatalf("reply = %#v, want op=ping", reply)
	}
}

func TestApplicationHeartbeat(t *testing.T) {
	upgrader := gws.Upgrader{}
	messages := make(chan string, 4)

	server := http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				messages <- string(data)
			}
		}),
	}

	ln, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	defer server.Close()

	client, err := Dial(
		"ws://"+ln.Addr().String(),
		WithAppHeartbeat(map[string]string{"type": "ping"}, 20*time.Millisecond, AppHeartbeatJSON, true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	deadline := time.After(2 * time.Second)
	count := 0
	for count < 2 {
		select {
		case raw := <-messages:
			var data map[string]string
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				t.Fatal(err)
			}
			if data["type"] == "ping" {
				count++
			}
		case <-deadline:
			t.Fatalf("did not receive enough heartbeat messages, got %d", count)
		}
	}
}

func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	upgrader := gws.Upgrader{}
	server := http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				messageType, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err = conn.WriteMessage(messageType, data); err != nil {
					return
				}
			}
		}),
	}

	ln, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)

	return "ws://" + ln.Addr().String(), func() { _ = server.Close() }
}
