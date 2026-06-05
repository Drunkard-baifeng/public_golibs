package main

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
)

func WebSocketCall() {
	url, closeServer, err := startWebSocketEchoServer()
	if err != nil {
		fmt.Println("start echo server failed:", err)
		return
	}
	defer closeServer()

	ws, err := websocket.Dial(
		url,
		websocket.WithTimeout(5*time.Second, 5*time.Second, 5*time.Second),
	)
	if err != nil {
		fmt.Println("connect failed:", err)
		return
	}
	defer func() { _ = ws.Close() }()

	textReply, err := ws.RequestText("hello websocket", 5*time.Second)
	if err != nil {
		fmt.Println("request text failed:", err)
		return
	}
	fmt.Println("text reply:", textReply)

	var jsonReply map[string]any
	err = ws.RequestJSON(map[string]any{"op": "ping", "id": 1}, &jsonReply, 5*time.Second)
	if err != nil {
		fmt.Println("request json failed:", err)
		return
	}
	fmt.Println("json reply:", jsonReply)

	hexReply, err := ws.RequestHexWithOptions("0x0a ff 12 34", " ", true, 5*time.Second)
	if err != nil {
		fmt.Println("request hex failed:", err)
		return
	}
	fmt.Println("hex reply:", hexReply)
}

func startWebSocketEchoServer() (string, func(), error) {
	upgrader := gorillawebsocket.Upgrader{}
	server := &http.Server{
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	go func() { _ = server.Serve(ln) }()

	closeServer := func() {
		_ = server.Close()
		_ = ln.Close()
	}
	return "ws://" + ln.Addr().String(), closeServer, nil
}
