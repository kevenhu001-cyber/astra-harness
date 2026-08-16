// Command astra-auth serves the Astra official site and the account/auth API
// used by `astra login`. Run it on a host the CLI can reach; point the CLI at
// it with the `auth_server` config or ASTRA_AUTH_SERVER.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kevenhu001-cyber/astra-harness/internal/authsrv"
)

func main() {
	addr := flag.String("addr", envOr("ASTRA_AUTH_ADDR", ":8080"), "listen address")
	baseURL := flag.String("base-url", envOr("ASTRA_AUTH_BASE_URL", "http://localhost:8080"), "public base URL for verification links")
	dataDir := flag.String("data-dir", envOr("ASTRA_AUTH_DATA_DIR", defaultDataDir()), "directory for auth state")
	flag.Parse()

	store, err := authsrv.OpenStore(filepath.Join(*dataDir, "auth.json"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer store.Close()

	var mailer authsrv.Mailer
	if smtp := authsrv.NewSMTPMailerFromEnv(); smtp.Enabled() {
		mailer = smtp
	} else {
		mailer = authsrv.ConsoleMailer{}
		log.Printf("mailer: console (verification links printed to this log); set SMTP_HOST/SMTP_USER/SMTP_PASS to send real email")
	}

	srv := authsrv.New(store, mailer, authsrv.Options{BaseURL: *baseURL})
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("astra-auth listening on %s (site + API at %s)", *addr, *baseURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".astra-auth"
	}
	return filepath.Join(dir, "astra-auth")
}
