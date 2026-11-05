// Command logintest runs only the Goodreads automated sign-in, for local
// debugging. Usage: GOODREADS_USER=... GOODREADS_PASSWORD=... go run ./cmd/logintest
package main

import (
	"context"
	"log"
	"os"

	"leafmark/internal/goodreads"
)

func main() {
	c := goodreads.NewClient("0")
	if err := c.Login(context.Background(), os.Getenv("GOODREADS_USER"), os.Getenv("GOODREADS_PASSWORD")); err != nil {
		log.Fatal(err)
	}
	log.Print("login OK")
}
