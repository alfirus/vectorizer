package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/alfirus/vectorizer/internal/security"
)

func main() {
	var workspace, peer, expiresStr string
	var admin bool
	flag.StringVar(&workspace, "workspace", "", "workspace id (w claim)")
	flag.StringVar(&peer, "peer", "", "peer id (p claim)")
	flag.BoolVar(&admin, "admin", false, "admin token (ad=true)")
	flag.StringVar(&expiresStr, "expires", "", "expiry e.g. 24h, 30d")
	flag.Parse()
	secret := os.Getenv("AUTH_JWT_SECRET")
	if secret == "" { fmt.Fprintln(os.Stderr, "AUTH_JWT_SECRET required"); os.Exit(1) }
	var exp time.Duration
	if expiresStr != "" {
		// support d/w
		if expiresStr[len(expiresStr)-1]=='d' { var n int; fmt.Sscanf(expiresStr, "%dd", &n); exp = time.Duration(n)*24*time.Hour
		} else if expiresStr[len(expiresStr)-1]=='w' { var n int; fmt.Sscanf(expiresStr, "%dw", &n); exp = time.Duration(n)*7*24*time.Hour
		} else { exp, _ = time.ParseDuration(expiresStr) }
	}
	tok, err := security.GenerateToken(secret, workspace, peer, admin, exp)
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	fmt.Println(tok)
}
