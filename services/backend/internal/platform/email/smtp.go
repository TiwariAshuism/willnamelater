// Package email sends transactional mail over SMTP so consuming modules depend
// on a small interface rather than on a mail provider.
//
// SMTP, and not a provider SDK, on purpose. Every transactional provider worth
// using — Postmark, Resend, SendGrid, Mailgun, SES — speaks it, so switching
// provider is a change of four environment variables. Adopting one provider's
// REST client would buy nothing this package does not already do and would cost a
// rewrite the day the provider changed, which is exactly the coupling the rest of
// this codebase is built to avoid.
//
// The message is assembled by hand rather than through a library for the same
// reason the S3 client signs by hand: the wire format is small, it is stable, and
// keeping it in one reviewable place is worth more than the dependency.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// DefaultTimeout bounds the whole send — dial, handshake, and delivery. Mail is
// never on a request's critical path here (every caller sends best-effort), so
// this is generous enough for a slow relay and short enough that a black-holed
// one cannot pin a worker.
const DefaultTimeout = 15 * time.Second

// TLSMode selects how the connection is protected.
type TLSMode string

const (
	// TLSStartTLS connects in the clear on the submission port (587) and upgrades
	// with STARTTLS before authenticating. This is what every hosted relay wants.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit negotiates TLS from the first byte (port 465).
	TLSImplicit TLSMode = "implicit"
	// TLSNone sends in the clear and refuses to authenticate. It exists for a
	// local capture server (mailpit) and is rejected outside dev by config
	// validation: a password on a plaintext SMTP connection is a password given
	// away.
	TLSNone TLSMode = "none"
)

// Config carries the relay's location and credentials.
type Config struct {
	// Host is the relay hostname, e.g. "smtp.postmarkapp.com". Required.
	Host string
	// Port is the submission port: 587 for STARTTLS, 465 for implicit TLS.
	// Required.
	Port int
	// Username and Password authenticate to the relay. Every hosted relay requires
	// them; a local capture server requires neither.
	Username string
	Password string
	// From is the envelope sender and the From header address. Required.
	From string
	// FromName is the optional display name shown beside From.
	FromName string
	// TLS selects the connection protection. Empty means TLSStartTLS.
	TLS TLSMode
	// Timeout bounds the whole send. Zero means DefaultTimeout.
	Timeout time.Duration
}

// Client sends mail through a single SMTP relay.
type Client struct {
	cfg Config
}

var _ port.Mailer = (*Client)(nil)

// New validates cfg and returns a Client. It performs no I/O: construction is
// pure, so the composition root can build the client at boot without requiring
// the relay to be reachable, exactly as the storage and PDF clients do.
func New(cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, errs.New(errs.KindInvalid, "email.host_required", "an SMTP host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, errs.New(errs.KindInvalid, "email.port_invalid", "the SMTP port must be between 1 and 65535")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, errs.New(errs.KindInvalid, "email.from_invalid", "the SMTP from address is not a valid email address")
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSStartTLS
	}
	switch cfg.TLS {
	case TLSStartTLS, TLSImplicit, TLSNone:
	default:
		return nil, errs.New(errs.KindInvalid, "email.tls_invalid",
			`email.tls must be one of "starttls", "implicit", "none"`)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Client{cfg: cfg}, nil
}

// Send delivers msg through the relay.
//
// Transport failures are KindUnavailable: the relay is a dependency that can be
// down, and a caller that treats mail as best-effort needs to distinguish "could
// not deliver right now" from "this message is malformed", which is KindInvalid.
func (c *Client) Send(ctx context.Context, msg port.Message) error {
	if len(msg.To) == 0 {
		return errs.New(errs.KindInvalid, "email.no_recipient", "a message needs at least one recipient")
	}
	for _, to := range msg.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return errs.New(errs.KindInvalid, "email.recipient_invalid",
				"a recipient address is not a valid email address")
		}
	}
	if msg.Text == "" {
		return errs.New(errs.KindInvalid, "email.empty_body", "a message needs a text body")
	}

	body, err := c.compose(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	return c.deliver(ctx, msg.To, body)
}

// deliver opens the connection and runs the SMTP conversation.
//
// The connection is dialled through net.Dialer.DialContext rather than
// smtp.Dial, which takes no context: without this, a relay that accepts the TCP
// connection and then stops talking would hang the caller past any deadline it
// set. That is the reason this conversation is driven by hand instead of through
// smtp.SendMail, which offers no seam for it.
func (c *Client) deliver(ctx context.Context, to []string, body []byte) error {
	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprint(c.cfg.Port))

	var conn net.Conn
	var err error
	dialer := &net.Dialer{}

	if c.cfg.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.cfg.Host},
		}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.dial", "the mail relay is unreachable")
	}
	defer func() { _ = conn.Close() }()

	// The context deadline governs the whole conversation, not just the dial: an
	// SMTP server that accepts and then stalls mid-DATA is otherwise unbounded.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.handshake", "the mail relay refused the connection")
	}
	defer func() { _ = client.Close() }()

	if c.cfg.TLS == TLSStartTLS {
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.cfg.Host}); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "email.starttls", "the mail relay does not support STARTTLS")
		}
	}

	// Authenticating over an unencrypted connection would hand the credential to
	// anyone on the path. Refuse rather than downgrade silently.
	if c.cfg.Username != "" && c.cfg.TLS != TLSNone {
		auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "email.auth", "the mail relay rejected our credentials")
		}
	}

	if err := client.Mail(c.cfg.From); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.sender_rejected", "the mail relay rejected the sender")
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "email.recipient_rejected", "the mail relay rejected a recipient")
		}
	}

	w, err := client.Data()
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.data", "the mail relay refused the message body")
	}
	if _, err := w.Write(body); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.write", "the message body could not be sent")
	}
	if err := w.Close(); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "email.commit", "the mail relay did not accept the message")
	}

	// Quit flushes and closes cleanly. A relay that accepted the DATA has taken
	// responsibility for the message, so a failure here is logged by the caller,
	// not treated as a non-delivery.
	_ = client.Quit()
	return nil
}

// compose renders the RFC 5322 message.
//
// A message with HTML is sent as multipart/alternative so a text-only client
// still reads the text part; a message without it is a plain text/plain body
// rather than a single-part multipart, which some clients render badly. Both
// bodies are quoted-printable encoded: report links are long and would otherwise
// break the 998-octet line limit and be silently mangled.
func (c *Client) compose(msg port.Message) ([]byte, error) {
	var buf bytes.Buffer

	from := c.cfg.From
	if c.cfg.FromName != "" {
		from = (&mail.Address{Name: c.cfg.FromName, Address: c.cfg.From}).String()
	}

	header := textproto.MIMEHeader{}
	header.Set("From", from)
	header.Set("To", strings.Join(msg.To, ", "))
	header.Set("Subject", encodeHeader(msg.Subject))
	header.Set("MIME-Version", "1.0")
	header.Set("Date", time.Now().UTC().Format(time.RFC1123Z))

	if msg.HTML == "" {
		header.Set("Content-Type", `text/plain; charset="utf-8"`)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		writeHeader(&buf, header)
		buf.WriteString("\r\n")
		if err := writeQP(&buf, msg.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mp := multipart.NewWriter(&buf)
	header.Set("Content-Type", `multipart/alternative; boundary="`+mp.Boundary()+`"`)
	writeHeader(&buf, header)
	buf.WriteString("\r\n")

	// Order matters: in multipart/alternative the LAST part is the one a capable
	// client prefers, so text comes first and HTML second.
	for _, part := range []struct{ contentType, body string }{
		{`text/plain; charset="utf-8"`, msg.Text},
		{`text/html; charset="utf-8"`, msg.HTML},
	} {
		w, err := mp.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "email.compose", "the message could not be assembled")
		}
		if err := writeQP(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := mp.Close(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "email.compose", "the message could not be assembled")
	}

	return buf.Bytes(), nil
}

func writeHeader(buf *bytes.Buffer, h textproto.MIMEHeader) {
	// Deterministic order so a composed message is byte-stable and testable.
	for _, k := range []string{"From", "To", "Subject", "Date", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"} {
		if v := h.Get(k); v != "" {
			buf.WriteString(k + ": " + v + "\r\n")
		}
	}
}

func writeQP(w interface{ Write([]byte) (int, error) }, s string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(s)); err != nil {
		return errs.Wrap(err, errs.KindInternal, "email.encode", "the message body could not be encoded")
	}
	if err := qp.Close(); err != nil {
		return errs.Wrap(err, errs.KindInternal, "email.encode", "the message body could not be encoded")
	}
	return nil
}

// encodeHeader RFC 2047-encodes a subject that carries non-ASCII and returns an
// ASCII one unchanged. A raw UTF-8 subject line is not legal in a header and
// arrives as mojibake; QEncoding.Encode makes that decision for us.
func encodeHeader(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}
