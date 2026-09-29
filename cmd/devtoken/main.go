// Command devtoken mints a short-lived HS256 JWT for local testing.
// It reads JWT_SECRET and JWT_ISSUER from the environment. Never use it against production.
//
// With -demo-url it prints the payment demo page URL instead (spec payment-checkout):
// the token, TWOC2P_MERCHANT_ID, and TWOC2P_SECRET_KEY go in the URL fragment,
// which browsers never send to the server.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

func main() {
	sub := flag.String("sub", "demo-customer", "customer id (JWT subject)")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	demoURL := flag.Bool("demo-url", false, "print the /demo page URL carrying the token and 2C2P sandbox credentials")
	flag.Parse()

	secret, issuer := os.Getenv("JWT_SECRET"), os.Getenv("JWT_ISSUER")
	if len(secret) < 32 || issuer == "" {
		fail("JWT_SECRET (>= 32 bytes) and JWT_ISSUER must be set")
	}
	tok, err := auth.NewJWT([]byte(secret), issuer).Issue(*sub, *ttl, time.Now())
	if err != nil {
		fail(err.Error())
	}
	if !*demoURL {
		fmt.Println(tok)
		return
	}
	merchant, key := os.Getenv("TWOC2P_MERCHANT_ID"), os.Getenv("TWOC2P_SECRET_KEY")
	if merchant == "" || key == "" {
		fail("TWOC2P_MERCHANT_ID and TWOC2P_SECRET_KEY must be set")
	}
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}
	fragment := url.Values{"token": {tok}, "merchant": {merchant}, "secret": {key}}.Encode()
	fmt.Printf("http://localhost:%s/demo#%s\n", port, fragment)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
