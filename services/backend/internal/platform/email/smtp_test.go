package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// fakeRelay is a minimal SMTP server. It speaks just enough of the protocol to
// accept one message and records the conversation, so the wire format this
// package produces is asserted against a real socket rather than against an
// internal buffer.
type fakeRelay struct {
	addr string

	mu       sync.Mutex
	envelope []string // MAIL FROM / RCPT TO lines, verbatim
	data     string   // the message body after DATA

	// stall makes the relay accept the connection and then never speak, which is
	// the failure smtp.Dial cannot bound and DialContext can.
	stall bool
}

func newFakeRelay(t *testing.T, stall bool) *fakeRelay {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	r := &fakeRelay{addr: ln.Addr().String(), stall: stall}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go r.serve(conn)
		}
	}()

	return r
}

func (r *fakeRelay) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if r.stall {
		// Accept and say nothing. A client without a deadline waits forever.
		select {}
	}

	br := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	write("220 fake.relay ESMTP")

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// No STARTTLS and no AUTH advertised: this relay is the TLSNone case.
			write("250-fake.relay")
			write("250 SIZE 10240000")
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
			r.mu.Lock()
			r.envelope = append(r.envelope, strings.TrimSpace(line))
			r.mu.Unlock()
			write("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				l, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			r.mu.Lock()
			r.data = body.String()
			r.mu.Unlock()
			write("250 OK: queued")
		case strings.HasPrefix(cmd, "QUIT"):
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func (r *fakeRelay) captured() (envelope []string, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.envelope...), r.data
}

// clientTo builds a Client pointed at a fake relay. TLSNone because the fake
// speaks plaintext; the TLS paths are exercised by the config-level tests and by
// the prod validation that forbids "none".
func clientTo(t *testing.T, r *fakeRelay) *Client {
	t.Helper()

	host, portStr, err := net.SplitHostPort(r.addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	c, err := New(Config{
		Host:     host,
		Port:     port,
		From:     "reports@influaudit.com",
		FromName: "InfluAudit",
		TLS:      TLSNone,
		Timeout:  3 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSendDeliversEnvelopeAndBody(t *testing.T) {
	relay := newFakeRelay(t, false)
	c := clientTo(t, relay)

	err := c.Send(context.Background(), port.Message{
		To:      []string{"creator@example.com"},
		Subject: "Your InfluAudit report is ready",
		Text:    "Download the PDF: https://cdn.example/reports/abc.pdf",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	envelope, data := relay.captured()

	// The envelope is what actually routes the mail — the headers are decoration.
	wantEnvelope := []string{
		"MAIL FROM:<reports@influaudit.com>",
		"RCPT TO:<creator@example.com>",
	}
	for _, want := range wantEnvelope {
		found := false
		for _, got := range envelope {
			if strings.HasPrefix(got, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("envelope missing %q; got %v", want, envelope)
		}
	}

	for _, want := range []string{
		`From: "InfluAudit" <reports@influaudit.com>`,
		"To: creator@example.com",
		"Subject: Your InfluAudit report is ready",
		`Content-Type: text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding: quoted-printable",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("message missing %q\n--- got ---\n%s", want, data)
		}
	}

	// Long URLs are exactly what quoted-printable exists to protect: an unencoded
	// share link would breach the 998-octet line limit and be silently mangled.
	if !strings.Contains(data, "https://cdn.example/reports/abc.pdf") {
		t.Errorf("share link did not survive encoding\n--- got ---\n%s", data)
	}
}

func TestSendMultipartWhenHTMLIsPresent(t *testing.T) {
	relay := newFakeRelay(t, false)
	c := clientTo(t, relay)

	err := c.Send(context.Background(), port.Message{
		To:      []string{"creator@example.com"},
		Subject: "Report ready",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, data := relay.captured()

	if !strings.Contains(data, "multipart/alternative") {
		t.Errorf("want multipart/alternative\n--- got ---\n%s", data)
	}
	// Order is load-bearing: in multipart/alternative a capable client renders the
	// LAST part it understands, so HTML must follow text.
	textAt := strings.Index(data, `text/plain`)
	htmlAt := strings.Index(data, `text/html`)
	if textAt < 0 || htmlAt < 0 {
		t.Fatalf("want both parts\n--- got ---\n%s", data)
	}
	if textAt > htmlAt {
		t.Error("text/plain must precede text/html so a text-only client reads the text part")
	}
}

// TestSendIsBoundedByContext is the reason this package drives the SMTP
// conversation by hand: smtp.Dial takes no context, so a relay that accepts the
// connection and then stops talking would hang the caller past any deadline.
func TestSendIsBoundedByContext(t *testing.T) {
	relay := newFakeRelay(t, true) // accepts, then stalls forever
	c := clientTo(t, relay)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Send(ctx, port.Message{
			To:      []string{"creator@example.com"},
			Subject: "s",
			Text:    "t",
		})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded against a stalled relay; want a timeout error")
		}
		// A dead relay is a dependency being unavailable, not a bad message: the
		// caller distinguishes the two to decide whether retrying could ever help.
		if errs.KindOf(err) != errs.KindUnavailable {
			t.Errorf("kind = %v, want KindUnavailable; err = %v", errs.KindOf(err), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return; the context deadline is not bounding the conversation")
	}
}

func TestSendRejectsMalformedMessages(t *testing.T) {
	relay := newFakeRelay(t, false)
	c := clientTo(t, relay)

	tests := []struct {
		name string
		msg  port.Message
	}{
		{"no recipient", port.Message{Subject: "s", Text: "t"}},
		{"invalid recipient", port.Message{To: []string{"not-an-address"}, Subject: "s", Text: "t"}},
		{"empty body", port.Message{To: []string{"a@b.com"}, Subject: "s"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Send(context.Background(), tc.msg)
			if err == nil {
				t.Fatal("Send succeeded; want a validation error")
			}
			// A malformed message is the caller's fault and retrying cannot help —
			// which is precisely what distinguishes it from an unreachable relay.
			if errs.KindOf(err) != errs.KindInvalid {
				t.Errorf("kind = %v, want KindInvalid; err = %v", errs.KindOf(err), err)
			}
		})
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no host", Config{Port: 587, From: "a@b.com"}},
		{"bad port", Config{Host: "h", Port: 0, From: "a@b.com"}},
		{"bad from", Config{Host: "h", Port: 587, From: "not-an-address"}},
		{"unknown tls mode", Config{Host: "h", Port: 587, From: "a@b.com", TLS: "ssl"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("New succeeded; want a validation error")
			} else if errs.KindOf(err) != errs.KindInvalid {
				t.Errorf("kind = %v, want KindInvalid", errs.KindOf(err))
			}
		})
	}
}

func TestNewDefaultsToStartTLS(t *testing.T) {
	c, err := New(Config{Host: "smtp.postmarkapp.com", Port: 587, From: "a@b.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.TLS != TLSStartTLS {
		t.Errorf("TLS = %q, want %q — an unset mode must not mean plaintext", c.cfg.TLS, TLSStartTLS)
	}
	if c.cfg.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.cfg.Timeout, DefaultTimeout)
	}
}

// A Client must satisfy the consumer's port without the consumer importing this
// package's types. If this stops compiling, the seam has been broken.
var _ port.Mailer = (*Client)(nil)
