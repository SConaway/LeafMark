// Command leafmark bridges KOReader reading progress to Goodreads. See
// README.md for setup and the project spec for background.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"leafmark/internal/config"
	"leafmark/internal/db"
	"leafmark/internal/goodreads"
	"leafmark/internal/koreader"
	"leafmark/internal/ntfy"
	"leafmark/internal/poll"
	"leafmark/internal/web"
)

func main() {
	once := flag.Bool("once", false, "run a single poll cycle and exit, instead of starting the server and poll loop")
	dryRun := flag.Bool("dry-run", false, "poll and match as usual, but don't push to Goodreads or send ntfy notifications — logs what would have happened instead")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		// Fail fast and loud on startup, per spec, rather than run with a
		// half-valid config.
		log.Fatalf("leafmark: invalid configuration:\n%v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("leafmark: open database: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		log.Fatalf("leafmark: migrate database: %v", err)
	}

	korClient := koreader.NewClient(cfg.KOReaderSyncURL, cfg.KOReaderSyncUsername, cfg.KOReaderSyncPassword)

	grClient := goodreads.NewClient(cfg.GoodreadsUserID)
	relogin := func(ctx context.Context) error {
		return grClient.Login(ctx, cfg.GoodreadsUser, cfg.GoodreadsPassword)
	}

	ntfyClient, err := ntfy.NewClient(cfg.NtfyURL)
	if err != nil {
		log.Fatalf("leafmark: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var gr goodreads.Goodreads = grClient
	var notifier ntfy.Publisher = ntfyClient
	activeRelogin := relogin
	if *dryRun {
		log.Print("leafmark: --dry-run: Goodreads pushes and ntfy notifications will be logged, not sent")
		gr = dryRunGoodreads{grClient}
		notifier = dryRunNotifier{}
		// Belt-and-suspenders alongside dryRunGoodreads.UpdateProgress
		// never performing the real POST: don't even wire up a live
		// Relogin closure, so there's no path back to a real chromedp
		// login/Goodreads session under --dry-run regardless of what
		// pushAndRecord's error handling does in the future.
		activeRelogin = nil
	} else {
		// Log in once at startup so the first poll cycle doesn't have to eat
		// a failed push first. A failure here doesn't stop LeafMark from
		// coming up — the poll loop's own re-login-on-ErrSessionInvalid, and
		// its throttled ntfy alert, take over from here (fail loud, don't
		// crash-loop the whole container over a transient login hiccup).
		// Skipped under --dry-run: nothing that follows actually needs a
		// real session, so there's no reason to touch live Goodreads at all.
		if err := relogin(ctx); err != nil {
			log.Printf("leafmark: initial Goodreads login failed (will retry on first push): %v", err)
		}
	}

	poller := poll.NewPoller(poll.Deps{
		DB: database, KOReader: korClient, Goodreads: gr, Notifier: notifier,
		MatchThreshold: cfg.MatchThreshold, BaseURL: cfg.LeafMarkBaseURL, Relogin: activeRelogin,
	})

	if *once {
		if err := poller.RunOnce(ctx); err != nil {
			log.Fatalf("leafmark: poll cycle failed: %v", err)
		}
		log.Print("leafmark: poll cycle complete (--once)")
		return
	}

	webServer, err := web.NewServer(database, gr, cfg.LeafMarkBaseURL)
	if err != nil {
		log.Fatalf("leafmark: build web server: %v", err)
	}
	httpServer := &http.Server{Addr: cfg.ListenAddr, Handler: webServer}

	go func() {
		log.Printf("leafmark: listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("leafmark: http server error: %v", err)
		}
	}()

	go runPollLoop(ctx, poller, cfg.PollInterval)

	<-ctx.Done()
	log.Print("leafmark: shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("leafmark: http server shutdown: %v", err)
	}
}

func runPollLoop(ctx context.Context, poller *poll.Poller, interval time.Duration) {
	runAndLog := func() {
		if err := poller.RunOnce(ctx); err != nil {
			log.Printf("leafmark: poll cycle failed: %v", err)
		}
	}

	runAndLog()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runAndLog()
		}
	}
}
