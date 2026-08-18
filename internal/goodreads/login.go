package goodreads

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

const (
	loginTimeout = 60 * time.Second

	signInURL = defaultBaseURL + "/user/sign_in"

	// /user/sign_in now lands on a provider-choice screen first (Amazon /
	// Apple / Google / "Sign in with email"), not directly on the Amazon
	// sign-in form — confirmed by actually loading the live page, since
	// the originally captured auth-flow.har started recording after this
	// click had already happened and never showed this step. This class
	// name is the one specific to the "Sign in with email" button (as
	// opposed to e.g. .amazonSignInButton for "Continue with Amazon").
	emailProviderSelector = ".authPortalSignInButton"

	// Standard, long-lived Amazon sign-in markup, confirmed against a real
	// captured login flow — clicking "Sign in with email" above lands on
	// exactly this Amazon-hosted OpenID2 form, still under the
	// goodreads.com host.
	emailSelector  = "#ap_email"
	passSelector   = "#ap_password"
	submitSelector = "#signInSubmit"
)

// Login drives a real headless Chromium through Goodreads' sign-in form so
// the page's own JS computes the client-side password encryption and
// device-fingerprint fields Amazon's login POST requires (raw HTTP would
// have to reverse-engineer both — evaluated against a captured login and
// ruled out; see the project plan). On success, every cookie the browser
// ends up holding for goodreads.com is harvested into the Client's cookie
// jar for subsequent plain net/http requests; the browser itself is only
// used for this one step.
func (c *Client) Login(ctx context.Context, user, password string) error {
	start := time.Now()
	log.Printf("goodreads: login: starting automated sign-in for user=%q (password: %d bytes)", user, len(password))

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "leafmark-chromedp-")
	if err != nil {
		log.Printf("goodreads: login: failed to create a chrome profile dir: %v", err)
		return fmt.Errorf("goodreads: login: create chrome profile dir: %w", err)
	}
	defer os.RemoveAll(dir)
	log.Printf("goodreads: login: chrome profile dir = %s", dir)

	// Deliberately NOT chromedp.DefaultExecAllocatorOptions — that list
	// includes chromedp.Headless, and AWS WAF Bot Control's JS challenge
	// (window.gokuProps) blocks headless Chrome from ever reaching
	// Goodreads' real sign-in page. Confirmed by isolated testing: curl
	// and genuinely headed Chrome both pass, every headless variant
	// (legacy --headless and --headless=new, with/without --disable-gpu)
	// is blocked. So this runs a real headed Chrome instead — in
	// production that means headed Chrome under Xvfb (a virtual X11
	// framebuffer, set up in the Dockerfile/entrypoint), which has no
	// visible window but isn't --headless and isn't caught by the same
	// detection. The rest of this list mirrors chromedp's own defaults
	// minus Headless.
	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
		chromedp.Flag("no-sandbox", true),            // needed running as a container's non-root user
		chromedp.Flag("disable-dev-shm-usage", true), // Chrome otherwise commonly crashes against Docker's small default /dev/shm
		chromedp.DisableGPU,                          // no real GPU inside the container either way
		chromedp.WindowSize(1280, 900),
		chromedp.UserDataDir(dir),
		// Without an explicit target, Chromium's crash handler (crashpad)
		// derives its database path from $HOME, which doesn't exist for
		// this container's user (created with --no-create-home) — that
		// makes crashpad launch with an empty --database argument and die
		// immediately ("chrome_crashpad_handler: --database is required"),
		// taking Chrome down with it. Pointing it at the per-run profile
		// dir sidesteps $HOME entirely; it's writable and already cleaned
		// up by the defer above.
		chromedp.Flag("crash-dumps-dir", dir),
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx, chromedp.WithLogf(func(format string, args ...any) {
		log.Printf("goodreads: login: chromedp: "+format, args...)
	}))
	defer cancelBrowser()

	log.Printf("goodreads: login: navigating to %s", signInURL)

	var cookies []*network.Cookie
	err = chromedp.Run(browserCtx,
		chromedp.Navigate(signInURL),
		logStep("waiting for the provider-choice screen to render"),
		chromedp.WaitVisible(emailProviderSelector, chromedp.ByQuery),
		logStep(`clicking "Sign in with email"`),
		chromedp.Click(emailProviderSelector, chromedp.ByQuery),
		logStep("waiting for the email field to render"),
		chromedp.WaitVisible(emailSelector, chromedp.ByID),
		logStep("filling in email"),
		chromedp.SendKeys(emailSelector, user, chromedp.ByID),
		logStep("filling in password"),
		chromedp.SendKeys(passSelector, password, chromedp.ByID),
		logStep("submitting sign-in form"),
		chromedp.Click(submitSelector, chromedp.ByID),
		logStep("waiting for sign-in to complete"),
		waitForSignInOutcome(),
		logStep("harvesting cookies"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().Do(ctx)
			return err
		}),
	)
	if err != nil {
		log.Printf("goodreads: login: failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
		return fmt.Errorf("goodreads: login: %w", err)
	}

	if len(cookies) == 0 {
		log.Print("goodreads: login: sign-in appeared to complete but zero cookies were harvested — something is wrong")
		return fmt.Errorf("goodreads: login: no cookies harvested after sign-in, something went wrong")
	}

	log.Printf("goodreads: login: succeeded in %s, harvested %d cookies", time.Since(start).Round(time.Millisecond), len(cookies))
	c.SetCookie(buildCookieHeader(cookies))
	return nil
}

// logStep is a no-op chromedp.Action that just logs a message before the
// next real action runs, so a hung/slow login is visible mid-flow rather
// than only reporting a final timeout with no indication of which step it
// died on.
func logStep(msg string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		log.Print("goodreads: login: " + msg)
		return nil
	})
}

// waitForSignInOutcome polls the current URL until it's no longer on an
// Amazon /ap/ path (success), or gives up after a few seconds and reports a
// descriptive failure — most likely wrong credentials, or an
// unanticipated CAPTCHA/2FA challenge this flow doesn't handle (see the
// project plan's risk notes: automated login isn't guaranteed to always
// work, and its failure mode must fall through to the ntfy loud-failure
// path rather than hang or crash).
func waitForSignInOutcome() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(20 * time.Second)
		var lastLoc string
		for {
			var loc string
			if err := chromedp.Location(&loc).Do(ctx); err != nil {
				return err
			}
			if loc != lastLoc {
				log.Printf("goodreads: login: current location: %s", loc)
				lastLoc = loc
			}
			if !strings.Contains(loc, "/ap/signin") && !strings.Contains(loc, "/ap-handler/") {
				log.Printf("goodreads: login: left the Amazon sign-in flow, landed on %s", loc)
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for sign-in to complete, still on %q (wrong credentials, or an unhandled CAPTCHA/2FA challenge)", loc)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	})
}

// buildCookieHeader joins harvested browser cookies into the raw "Cookie:"
// header value net/http requests need — verbatim name=value pairs, exactly
// as the browser stored them (no re-encoding; some Goodreads/Amazon cookie
// values are themselves quoted strings).
func buildCookieHeader(cookies []*network.Cookie) string {
	names := make([]string, 0, len(cookies))
	pairs := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		names = append(names, ck.Name)
		pairs = append(pairs, ck.Name+"="+ck.Value)
	}
	log.Printf("goodreads: login: harvested cookies: %s", strings.Join(names, ", "))
	return strings.Join(pairs, "; ")
}
