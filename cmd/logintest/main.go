// Command logintest runs only the Goodreads automated sign-in, for local
// debugging. Usage: GOODREADS_USER=... GOODREADS_PASSWORD=... GOODREADS_USER_ID=... go run ./cmd/logintest
package main

import (
	"context"
	"log"
	"os"

	"leafmark/internal/goodreads"
)

func main() {
	c := goodreads.NewClient(os.Getenv("GOODREADS_USER_ID"))
	if err := c.Login(context.Background(), os.Getenv("GOODREADS_USER"), os.Getenv("GOODREADS_PASSWORD")); err != nil {
		log.Fatal(err)
	}
	log.Print("login OK")
	for _, shelf := range []goodreads.ShelfName{goodreads.ShelfWantToRead, goodreads.ShelfCurrentlyReading} {
		books, err := c.FetchShelf(context.Background(), shelf)
		if err != nil {
			log.Fatalf("shelf %s: %v", shelf, err)
		}
		log.Printf("shelf %s: %d books", shelf, len(books))
		for i, b := range books {
			if i >= 3 {
				break
			}
			log.Printf("  %s | %q | %q", b.GoodreadsBookID, b.Title, b.Author)
		}
	}
}
