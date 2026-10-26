// cmd/mockjira runs the Jira-like test double standalone. Deliveries queue
// up until the flush interval fires or POST /_admin/flush?mode=... is called
// (modes: in_order, duplicate, reverse, drop); POST /_admin/fail?n=&status=&retry_after=
// makes the next n REST calls fail.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"fieldsync/internal/mockjira"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	webhookURL := flag.String("webhook-url", "http://localhost:8081/webhooks/mockjira", "where signed events are POSTed")
	secret := flag.String("secret", "dev-webhook-secret", "HMAC secret shared with the receiver")
	flushEvery := flag.Duration("flush-interval", 2*time.Second, "auto-flush interval; 0 = manual flush only")
	seed := flag.Int("seed", 3, "issues to create at startup")
	flag.Parse()

	srv := mockjira.New(mockjira.Options{WebhookURL: *webhookURL, WebhookSecret: *secret})
	for i := 1; i <= *seed; i++ {
		srv.Create("Seed issue", "Open", "")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *flushEvery > 0 {
		go func() {
			ticker := time.NewTicker(*flushEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if n, err := srv.Flush(mockjira.FlushInOrder); err != nil {
						log.Printf("mockjira: flush after %d post(s): %v", n, err)
					}
				}
			}
		}()
	}

	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		httpSrv.Shutdown(context.Background())
	}()
	log.Printf("mockjira: listening on %s, webhooks -> %s", *addr, *webhookURL)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("mockjira: %v", err)
	}
}
