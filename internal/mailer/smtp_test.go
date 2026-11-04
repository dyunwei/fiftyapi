package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestSMTPDeliveryAndNoCleartextRemote(t *testing.T) {
	c := SMTP{Host: "mail.example", Port: "25", From: "sender@example.test", TLSMode: "none"}
	if c.Validate() == nil {
		t.Fatal("remote cleartext SMTP allowed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	c.Host = host
	c.Port = port
	messages := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		io.WriteString(conn, "220 localhost test SMTP\r\n")
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				io.WriteString(conn, "250 localhost\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				io.WriteString(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				io.WriteString(conn, "354 send message\r\n")
				var message strings.Builder
				for {
					part, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					if part == ".\r\n" {
						break
					}
					message.WriteString(part)
				}
				messages <- message.String()
				io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(conn, "221 bye\r\n")
				return
			default:
				io.WriteString(conn, "500 unsupported\r\n")
			}
		}
	}()
	url := "https://gateway.example/#invite=" + strings.Repeat("a", 43)
	if err = c.SendInvitation(context.Background(), Message{Email: "member@example.test", Name: "成员", URL: url}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-messages:
		parsed, e := mail.ReadMessage(strings.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, parsed.Body))
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(body), url) || !strings.Contains(string(body), "8 字节") || !strings.Contains(string(body), "FiftyAPI") {
			t.Fatal("invitation contents incomplete")
		}
		if parsed.Header.Get("To") != "<member@example.test>" && parsed.Header.Get("To") != "member@example.test" {
			t.Fatal("wrong recipient")
		}
	case <-time.After(time.Second):
		t.Fatal("local SMTP did not receive mail")
	}
}

func TestFromEnvOptionalAndRequiredFields(t *testing.T) {
	for _, key := range []string{"FIFTYAPI_SMTP_HOST", "FIFTYAPI_SMTP_PORT", "FIFTYAPI_SMTP_FROM", "FIFTYAPI_SMTP_USERNAME", "FIFTYAPI_SMTP_PASSWORD", "FIFTYAPI_SMTP_TLS"} {
		t.Setenv(key, "")
	}
	sender, err := FromEnv()
	if err != nil || sender != nil {
		t.Fatal("unset SMTP should be optional")
	}
	t.Setenv("FIFTYAPI_SMTP_HOST", "mail.example")
	if _, err = FromEnv(); err == nil {
		t.Fatal("missing sender accepted")
	}
	t.Setenv("FIFTYAPI_SMTP_FROM", "fiftyapi <sender@example.test>")
	sender, err = FromEnv()
	if err != nil || sender.TLSMode != "starttls" || sender.Port != "587" {
		t.Fatal("secure SMTP defaults not applied")
	}
}

func TestSMTPRequiresSTARTTLSBeforeAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		io.WriteString(conn, "220 test SMTP\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				return
			}
			if strings.HasPrefix(line, "AUTH") || strings.HasPrefix(line, "MAIL FROM:") {
				t.Error("credentials/mail sent without required TLS")
				return
			}
			io.WriteString(conn, "250 localhost\r\n")
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	c := SMTP{Host: host, Port: port, From: "sender@example.test", Username: "test-user", Password: "test-only-password", TLSMode: "starttls"}
	err = c.SendInvitation(context.Background(), Message{Email: "member@example.test", Name: "Member", URL: "https://gateway.example"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatal("missing STARTTLS did not fail closed")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP fixture did not close")
	}
}
