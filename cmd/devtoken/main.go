// Command devtoken mints a short-lived HS256 JWT for local testing.
// It reads JWT_SECRET and JWT_ISSUER from the environment. Never use it against production.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

func main() {
	sub := flag.String("sub", "demo-alice", "customer id (JWT subject)")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	flag.Parse()

	secret, issuer := os.Getenv("JWT_SECRET"), os.Getenv("JWT_ISSUER")
	if len(secret) < 32 || issuer == "" {
		fmt.Fprintln(os.Stderr, "JWT_SECRET (>= 32 bytes) and JWT_ISSUER must be set")
		os.Exit(1)
	}
	tok, err := auth.NewJWT([]byte(secret), issuer).Issue(*sub, *ttl, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
