package pop3client

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestPOP3ClientCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		if _, err := fmt.Fprint(conn, "+OK ready\r\n"); err != nil {
			done <- err
			return
		}

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			cmd := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(cmd, "USER "):
				_, err = fmt.Fprint(conn, "+OK user accepted\r\n")
			case strings.HasPrefix(cmd, "PASS "):
				_, err = fmt.Fprint(conn, "+OK pass accepted\r\n")
			case cmd == "STAT":
				_, err = fmt.Fprint(conn, "+OK 2 320\r\n")
			case cmd == "LIST":
				_, err = fmt.Fprint(conn, "+OK scan listing follows\r\n1 120\r\n2 200\r\n.\r\n")
			case cmd == "RETR 2":
				_, err = fmt.Fprint(conn, "+OK 200 octets\r\nSubject: test\r\n\r\nbody\r\n.\r\n")
			case cmd == "QUIT":
				_, err = fmt.Fprint(conn, "+OK bye\r\n")
				done <- err
				return
			default:
				_, err = fmt.Fprint(conn, "-ERR unknown command\r\n")
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr failed: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse port failed: %v", err)
	}

	client, err := NewPOP3Client(host, port)
	if err != nil {
		t.Fatalf("NewPOP3Client failed: %v", err)
	}

	if got := client.GetWelcome(); got != "+OK ready" {
		t.Fatalf("welcome mismatch, got=%q", got)
	}
	if _, err := client.User("demo"); err != nil {
		t.Fatalf("USER failed: %v", err)
	}
	if _, err := client.Pass("secret"); err != nil {
		t.Fatalf("PASS failed: %v", err)
	}

	count, size, err := client.Stat()
	if err != nil {
		t.Fatalf("STAT failed: %v", err)
	}
	if count != 2 || size != 320 {
		t.Fatalf("STAT mismatch, got=(%d,%d)", count, size)
	}

	_, lines, _, err := client.List()
	if err != nil {
		t.Fatalf("LIST failed: %v", err)
	}
	if len(lines) != 2 || lines[0] != "1 120" || lines[1] != "2 200" {
		t.Fatalf("LIST mismatch: %+v", lines)
	}

	_, msg, _, err := client.Retr(2)
	if err != nil {
		t.Fatalf("RETR failed: %v", err)
	}
	if !strings.Contains(string(joinLines(msg)), "Subject: test") {
		t.Fatalf("RETR mismatch: %q", joinLines(msg))
	}

	if _, err := client.Quit(); err != nil {
		t.Fatalf("QUIT failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server failed: %v", err)
	}
}

func TestParseEmail(t *testing.T) {
	raw := strings.Join([]string{
		"From: =?UTF-8?B?5rWL6K+V?= <test@example.com>",
		"To: demo@example.com",
		"Subject: =?UTF-8?B?5L2g5aW9?=",
		"Date: Sat, 30 May 2026 01:00:00 +0800",
		"Content-Type: multipart/alternative; boundary=\"b\"",
		"",
		"--b",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"hello=0Aworld",
		"--b",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: base64",
		"",
		"PGI+aGVsbG88L2I+",
		"--b--",
		"",
	}, "\r\n")

	info, err := ParseEmail([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEmail failed: %v", err)
	}
	if info.From != "测试 <test@example.com>" {
		t.Fatalf("From mismatch, got=%q", info.From)
	}
	if info.Subject != "你好" {
		t.Fatalf("Subject mismatch, got=%q", info.Subject)
	}
	if info.Body != "hello\nworld" {
		t.Fatalf("Body mismatch, got=%q", info.Body)
	}
	if info.HtmlBody != "<b>hello</b>" {
		t.Fatalf("HtmlBody mismatch, got=%q", info.HtmlBody)
	}
}

func joinLines(lines [][]byte) []byte {
	return []byte(strings.Join(bytesLinesToStrings(lines), "\r\n"))
}
