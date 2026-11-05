package goodreads

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		// Chromium's crash handler (crashpad) derives its database path
		// from $HOME (well, actually XDG_CONFIG_HOME/XDG_CACHE_HOME on
		// Linux), which doesn't exist for this container's user (created
		// with --no-create-home) — that makes crashpad launch with an
		// empty --database argument and die immediately
		// ("chrome_crashpad_handler: --database is required"), taking
		// Chrome down with it. --crash-dumps-dir looks like the fix but
		// isn't: recent Chromium (128+) ignores it for crashpad's own
		// database path — it's a known upstream regression, widely
		// reported against headless-Chrome tooling running in containers.
		// Pointing $HOME/XDG_*_HOME at the per-run profile dir instead
		// works because it's the actual mechanism crashpad reads; the dir
		// is writable and already cleaned up by the defer above.
		chromedp.Env(
			"HOME="+dir,
			"XDG_CONFIG_HOME="+dir,
			"XDG_CACHE_HOME="+dir,
		),
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
		chromedp.Click(emailSelector, chromedp.ByID),
		chromedp.SendKeys(emailSelector, user, chromedp.ByID),
		logStep("waiting for the password field to render"),
		// SendKeys types into whatever element has focus; if the password
		// field is hidden or not yet laid out, focus stays on the email
		// field and the password gets typed into it. So wait for it to be
		// visible and explicitly click it first.
		chromedp.WaitVisible(passSelector, chromedp.ByID),
		logStep("filling in password"),
		chromedp.Click(passSelector, chromedp.ByID),
		chromedp.SendKeys(passSelector, password, chromedp.ByID),
		logStep("verifying field contents"),
		verifyFields(len(user), len(password)),
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
		dumped := false
		for {
			var loc string
			if err := chromedp.Location(&loc).Do(ctx); err != nil {
				return err
			}
			// Dump what the page is actually showing (CAPTCHA? OTP? error
			// banner?) a few seconds in, while the browser context is still
			// alive, so a stuck login is diagnosable from the logs.
			if !dumped && time.Until(deadline) < 12*time.Second {
				dumped = true
				dumpPage(ctx)
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

// verifyFields fails fast if the email/password inputs don't hold exactly
// what was typed (e.g. the password leaked into the email field).
func verifyFields(userLen, passLen int) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var gotUser, gotPass int
		if err := chromedp.Evaluate(`document.querySelector("#ap_email").value.length`, &gotUser).Do(ctx); err != nil {
			return err
		}
		if err := chromedp.Evaluate(`document.querySelector("#ap_password").value.length`, &gotPass).Do(ctx); err != nil {
			return err
		}
		log.Printf("goodreads: login: field lengths: email=%d (want %d), password=%d (want %d)", gotUser, userLen, gotPass, passLen)
		if gotUser != userLen || gotPass != passLen {
			return fmt.Errorf("sign-in fields hold the wrong contents (email %d/%d chars, password %d/%d chars)", gotUser, userLen, gotPass, passLen)
		}
		return nil
	})
}

// dumpPage logs the page title and visible text, and saves a screenshot to
// the OS temp dir, as best-effort diagnostics. Never returns an error.
func dumpPage(ctx context.Context) {
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var title, text string
	var shot []byte
	err := chromedp.Run(dctx,
		chromedp.Title(&title),
		chromedp.Evaluate(`document.body ? document.body.innerText.slice(0, 1500) : ""`, &text),
		chromedp.FullScreenshot(&shot, 80),
	)
	if err != nil {
		log.Printf("goodreads: login: diagnostics partially failed: %v", err)
	}
	log.Printf("goodreads: login: page title=%q", title)
	log.Printf("goodreads: login: page text=%q", strings.Join(strings.Fields(text), " "))
	if len(shot) > 0 {
		path := filepath.Join(os.TempDir(), "leafmark-login-failure.png")
		if werr := os.WriteFile(path, shot, 0o644); werr != nil {
			log.Printf("goodreads: login: could not save screenshot: %v", werr)
		} else {
			log.Printf("goodreads: login: saved screenshot to %s", path)
		}
	}
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
