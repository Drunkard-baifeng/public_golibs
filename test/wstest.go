package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/websocket"
	gorillawebsocket "github.com/gorilla/websocket"
)

var (
	neaxWSPrivateKey = ""
	neaxWSID         = ""
	neaxAddress      = "127.0.0.1:18089/server/APPServer"
	neaxSecure       = false
	neaxInsecureTLS  = true
	neaxRunSeconds   = 0

	// 代理默认关闭。填 host:port 后启用，类型支持 http / https / socks5 / socks5h。
	neaxProxyAddress  = ""
	neaxProxyType     = ""
	neaxProxyUsername = ""
	neaxProxyPassword = ""
)

const (
	neaxPingCode          = "999999"
	neaxHeartbeatInterval = 5 * time.Second
	neaxOpenTimeout       = 15 * time.Second
)

func WSTest() {
	codec, err := NewNeaxWSCodec(neaxWSPrivateKey)
	if err != nil {
		fmt.Println("new codec failed:", err)
		return
	}

	wsURL := codec.BuildSocketURL(neaxAddress, neaxWSID, neaxSecure, "xxx")
	fmt.Println("连接:", wsURL)

	runSeconds := neaxRunSeconds
	ctx, cancel := context.WithCancel(context.Background())
	if runSeconds > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(runSeconds)*time.Second)
	}
	defer cancel()

	heartbeatPayload, err := codec.Encrypt(neaxPayload{Code: neaxPingCode, Data: map[string]any{}})
	if err != nil {
		fmt.Println("build heartbeat failed:", err)
		return
	}

	options := []websocket.Option{
		websocket.WithHeader("User-Agent", "okhttp/4.12.0"),
		websocket.WithOrigin(neaxOrigin(neaxAddress, neaxSecure)),
		websocket.WithMaxMessageSize(0),
		websocket.WithTimeout(neaxOpenTimeout, 0, 10*time.Second),
		websocket.WithAppHeartbeat(heartbeatPayload, neaxHeartbeatInterval, websocket.AppHeartbeatBinary, true),
	}
	options = appendNeaxProxyOptions(options)
	if neaxSecure && neaxInsecureTLS {
		options = append(options, websocket.WithTLSConfig(&tls.Config{InsecureSkipVerify: true}))
	}

	client, err := websocket.DialContext(ctx, wsURL, options...)
	if err != nil {
		fmt.Println("连接异常:", err)
		return
	}
	defer func() { _ = client.Close() }()
	fmt.Println("[+] 已连接!")

	if err = neaxSendMsg(client, codec, "001001", nil); err != nil {
		fmt.Println("发送失败:", err)
		return
	}

	recvDone := make(chan error, 1)
	go func() {
		recvDone <- neaxReceiver(ctx, client, codec)
	}()

	if runSeconds > 0 {
		select {
		case <-ctx.Done():
			fmt.Printf("[x] 结束: 已运行 %ds\n", runSeconds)
		case err = <-recvDone:
			printNeaxEnd("接收结束", err)
		}
		return
	}

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- neaxConsoleSender(ctx, client, codec)
	}()

	select {
	case err = <-recvDone:
		printNeaxEnd("接收结束", err)
	case err = <-sendDone:
		printNeaxEnd("发送结束", err)
	case <-ctx.Done():
		fmt.Println("[x] 结束:", ctx.Err())
	}
}

func printNeaxEnd(label string, err error) {
	if err == nil {
		fmt.Println("[x] " + label)
		return
	}
	if gorillawebsocket.IsCloseError(err, gorillawebsocket.CloseNormalClosure) {
		fmt.Println("[x] " + label + ": 服务端正常关闭")
		return
	}
	fmt.Println("[x] "+label+":", err)
}

func appendNeaxProxyOptions(options []websocket.Option) []websocket.Option {
	proxyAddress := strings.TrimSpace(neaxProxyAddress)
	if proxyAddress == "" {
		return options
	}

	proxyType := strings.TrimSpace(neaxProxyType)
	proxyUsername := strings.TrimSpace(neaxProxyUsername)
	proxyPassword := strings.TrimSpace(neaxProxyPassword)

	fmt.Printf("[*] 使用代理: type=%s address=%s\n", proxyType, proxyAddress)
	return append(options, websocket.WithProxyAuth(
		proxyAddress,
		websocket.ProxyType(proxyType),
		proxyUsername,
		proxyPassword,
	))
}

func neaxReceiver(ctx context.Context, client *websocket.Client, codec *NeaxWSCodec) error {
	return client.Listen(ctx, func(msg websocket.Message) error {
		jsonText, err := codec.DecryptText(msg.Data)
		if err != nil {
			fmt.Printf("<<< [解析失败] %v 原始(%dB): %s\n", err, len(msg.Data), hexPrefix(msg.Data, 60))
			return nil
		}

		var decoded map[string]any
		_ = json.Unmarshal([]byte(jsonText), &decoded)
		fmt.Printf("<<< 接收 code=%v len=%d: %s\n", decoded["code"], len(jsonText), jsonText)
		return nil
	})
}

func neaxConsoleSender(ctx context.Context, client *websocket.Client, codec *NeaxWSCodec) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println(`提示: 输入 "<code> [JSON]" 发送 (如 001001); 输入 q 退出`)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Hour):
					continue
				}
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch strings.ToLower(line) {
		case "q", "quit", "exit":
			_ = client.Close()
			return nil
		}

		parts := strings.SplitN(line, " ", 2)
		data := map[string]any{}
		if len(parts) > 1 {
			if err = json.Unmarshal([]byte(parts[1]), &data); err != nil {
				fmt.Println("[!] JSON 解析失败:", err)
				continue
			}
		}
		if err = neaxSendMsg(client, codec, parts[0], data); err != nil {
			return err
		}
	}
}

func neaxSendMsg(client *websocket.Client, codec *NeaxWSCodec, code string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	payload, err := codec.Encrypt(neaxPayload{Code: code, Data: data})
	if err != nil {
		return err
	}
	if err = client.SendBinary(payload); err != nil {
		return err
	}
	fmt.Printf(">>> 发送 code=%s %v\n", code, data)
	return nil
}

func neaxOrigin(address string, secure bool) string {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	host := strings.Split(strings.TrimPrefix(strings.TrimPrefix(address, "ws://"), "wss://"), "/")[0]
	return scheme + "://" + host
}

func hexPrefix(data []byte, max int) string {
	if len(data) > max {
		data = data[:max]
	}
	return hex.EncodeToString(data)
}
