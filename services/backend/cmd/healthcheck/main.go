// Command healthcheck probes a running api process and exits 0 when it is ready.
//
// It exists because the runtime image is distroless: there is no shell, no curl,
// and no wget for a container healthcheck to call. Weakening the image to a
// debug variant just to get a shell would trade real attack surface for
// convenience, so the probe ships as a static binary instead.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/readyz", "readiness endpoint to probe")
	timeout := flag.Duration("timeout", 3*time.Second, "probe timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		fail(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fail(fmt.Errorf("status %d", resp.StatusCode))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "healthcheck:", err)
	os.Exit(1)
}
