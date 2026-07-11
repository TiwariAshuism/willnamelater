package storage

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeDoer is a stub Doer that records the last request and returns a canned
// response or error, so tests never touch the network.
type fakeDoer struct {
	got  *http.Request
	resp *http.Response
	err  error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func newResp(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testClient(t *testing.T, d Doer) *Client {
	t.Helper()
	c, err := New(Config{
		Endpoint:  "http://localhost:4566",
		Region:    "us-east-1",
		Bucket:    "reports",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		HTTP:      d,
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.now = func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }
	return c
}

// TestSigV4_KnownAnswer_SigningKey asserts the HMAC signing-key chain against
// the AWS General Reference documented example
// (https://docs.aws.amazon.com/general/latest/gr/signature-v4-examples.html):
// secret wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY, 20150830, us-east-1, iam.
func TestSigV4_KnownAnswer_SigningKey(t *testing.T) {
	const want = "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	got := hex.EncodeToString(deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20150830", "us-east-1", "iam"))
	if got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

// TestSigV4_KnownAnswer_GetObject asserts the canonical-request hash and the
// final signature against the AWS-documented "GET Object" example from
// https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
// (secret wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY, 20130524, us-east-1, s3).
// The canonical-request hash equals AWS's published value; the signature is the
// deterministic HMAC of the string-to-sign under the verified signing key.
func TestSigV4_KnownAnswer_GetObject(t *testing.T) {
	const (
		secret     = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		dateStamp  = "20130524"
		region     = "us-east-1"
		wantReqSHA = "7344ae5b7ee6c3e7e6b0fe0640412a37625d1fbfff95c48bbb2dc43964946972"
		wantSig    = "67fe34c8530db585abddc51067328adfedb6e42487d2566dc7d927d6e2722900"
	)

	canonicalRequest := strings.Join([]string{
		"GET",
		"/test.txt",
		"",
		"host:examplebucket.s3.amazonaws.com",
		"range:bytes=0-9",
		"x-amz-content-sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"x-amz-date:20130524T000000Z",
		"",
		"host;range;x-amz-content-sha256;x-amz-date",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}, "\n")

	if got := sha256Hex([]byte(canonicalRequest)); got != wantReqSHA {
		t.Fatalf("canonical request hash = %s, want %s", got, wantReqSHA)
	}

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		"20130524T000000Z",
		"20130524/us-east-1/s3/aws4_request",
		wantReqSHA,
	}, "\n")

	c := &Client{region: region, secretKey: secret}
	if got := c.sign(dateStamp, stringToSign); got != wantSig {
		t.Fatalf("signature = %s, want %s", got, wantSig)
	}
}

func TestPutObject_CanonicalRequestAndETag(t *testing.T) {
	d := &fakeDoer{resp: newResp(http.StatusOK, http.Header{"Etag": {`"abc123"`}}, "")} //nolint:bodyclose // response is closed by the client under test
	c := testClient(t, d)

	etag, err := c.PutObject(context.Background(), "audits/2026/report.pdf", "application/pdf", []byte("PDFDATA"))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if etag != "abc123" {
		t.Errorf("etag = %q, want abc123", etag)
	}

	req := d.got
	if req.Method != http.MethodPut {
		t.Errorf("method = %s, want PUT", req.Method)
	}
	if req.URL.String() != "http://localhost:4566/reports/audits/2026/report.pdf" {
		t.Errorf("url = %s", req.URL.String())
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("x-amz-date = %q", got)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != sha256Hex([]byte("PDFDATA")) {
		t.Errorf("x-amz-content-sha256 = %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content-type = %q", got)
	}
	auth := req.Header.Get("Authorization")
	wantCred := "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request"
	if !strings.Contains(auth, wantCred) {
		t.Errorf("authorization missing credential: %q", auth)
	}
	wantSigned := "SignedHeaders=content-length;content-type;host;x-amz-content-sha256;x-amz-date"
	if !strings.Contains(auth, wantSigned) {
		t.Errorf("authorization signed headers = %q", auth)
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("authorization algorithm = %q", auth)
	}
}

func TestPutObject_ErrorHidesAccessKeyAndMapsUnavailable(t *testing.T) {
	body := `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`
	d := &fakeDoer{resp: newResp(http.StatusForbidden, nil, body)} //nolint:bodyclose // response is closed by the client under test
	c := testClient(t, d)

	_, err := c.PutObject(context.Background(), "k", "text/plain", []byte("x"))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := errs.KindOf(err); got != errs.KindUnavailable {
		t.Errorf("kind = %v, want KindUnavailable", got)
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("error should surface S3 code: %q", err.Error())
	}
	if strings.Contains(err.Error(), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("error leaked access key: %q", err.Error())
	}
}

func TestPutObject_TransportErrorIsUnavailable(t *testing.T) {
	d := &fakeDoer{err: errors.New("dial tcp: connection refused")}
	c := testClient(t, d)
	if _, err := c.PutObject(context.Background(), "k", "text/plain", []byte("x")); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

func TestPresignGetURL_ParamsAndDeterminism(t *testing.T) {
	c := testClient(t, &fakeDoer{})

	url1, err := c.PresignGetURL("audits/report.pdf", 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignGetURL: %v", err)
	}
	url2, _ := c.PresignGetURL("audits/report.pdf", 15*time.Minute)
	if url1 != url2 {
		t.Errorf("presign not deterministic for fixed clock:\n%s\n%s", url1, url2)
	}

	for _, want := range []string{
		"http://localhost:4566/reports/audits/report.pdf?",
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request",
		"X-Amz-Date=20130524T000000Z",
		"X-Amz-Expires=900",
		"X-Amz-SignedHeaders=host",
		"&X-Amz-Signature=",
	} {
		if !strings.Contains(url1, want) {
			t.Errorf("presigned URL missing %q:\n%s", want, url1)
		}
	}
	// Algorithm must be the first canonical param (sorted order) and Signature last.
	if !strings.Contains(url1, "?X-Amz-Algorithm=") {
		t.Errorf("algorithm not first param: %s", url1)
	}
	if strings.Contains(url1, "AKIAIOSFODNN7EXAMPLE/2013") {
		t.Errorf("credential slashes not encoded: %s", url1)
	}
}

func TestPresignGetURL_RejectsNonPositiveTTL(t *testing.T) {
	c := testClient(t, &fakeDoer{})
	if _, err := c.PresignGetURL("k", 0); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

func TestEnsureBucket_CreatedAndAlreadyExists(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		d := &fakeDoer{resp: newResp(status, nil, "")} //nolint:bodyclose // response is closed by the client under test
		c := testClient(t, d)
		if err := c.EnsureBucket(context.Background()); err != nil {
			t.Errorf("status %d: EnsureBucket = %v, want nil", status, err)
		}
		if d.got.URL.String() != "http://localhost:4566/reports" {
			t.Errorf("bucket url = %s", d.got.URL.String())
		}
		if got := d.got.Header.Get("X-Amz-Content-Sha256"); got != emptyBodyHash {
			t.Errorf("empty body hash = %q", got)
		}
	}
}

func TestEnsureBucket_ErrorMapsUnavailable(t *testing.T) {
	d := &fakeDoer{resp: newResp(http.StatusInternalServerError, nil, "boom")} //nolint:bodyclose // response is closed by the client under test
	c := testClient(t, d)
	if err := c.EnsureBucket(context.Background()); errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

func TestNew_ValidatesRequiredFields(t *testing.T) {
	base := Config{
		Endpoint: "http://localhost:4566", Region: "us-east-1", Bucket: "b",
		AccessKey: "a", SecretKey: "s", HTTP: &fakeDoer{},
	}
	if _, err := New(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*Config){
		"endpoint":  func(c *Config) { c.Endpoint = "" },
		"region":    func(c *Config) { c.Region = "" },
		"bucket":    func(c *Config) { c.Bucket = "" },
		"accessKey": func(c *Config) { c.AccessKey = "" },
		"secretKey": func(c *Config) { c.SecretKey = "" },
		"http":      func(c *Config) { c.HTTP = nil },
		"badURL":    func(c *Config) { c.Endpoint = "not-a-url" },
	}
	for name, mutate := range cases {
		cfg := base
		mutate(&cfg)
		if _, err := New(cfg); errs.KindOf(err) != errs.KindInvalid {
			t.Errorf("%s: kind = %v, want KindInvalid", name, errs.KindOf(err))
		}
	}
}

func TestURIEncode_SegmentAndFull(t *testing.T) {
	if got := uriEncode("a/b c+d~e", false); got != "a/b%20c%2Bd~e" {
		t.Errorf("path encode = %q", got)
	}
	if got := uriEncode("a/b", true); got != "a%2Fb" {
		t.Errorf("full encode = %q", got)
	}
}
