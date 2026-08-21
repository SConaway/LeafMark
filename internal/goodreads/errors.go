package goodreads

import "errors"

// ErrSessionInvalid means Goodreads no longer accepts the current cookie
// jar (redirect to sign-in, or an auth-shaped HTML response instead of the
// expected JSON/RSS). Callers should not retry with the same cookies — the
// poll loop treats this as "trigger a re-login" (see login.go), falling
// through to the ntfy loud-failure path if re-login itself fails.
var ErrSessionInvalid = errors.New("goodreads: session is no longer valid")

// ErrNotImplemented marks a method whose live contract hasn't been wired up
// yet.
var ErrNotImplemented = errors.New("goodreads: not implemented")

// errCSRFRejected is an internal signal (never returned to callers) meaning
// a request was rejected specifically for a stale/invalid CSRF token (a 422,
// or — confirmed against production logs — a 404 "Page not found" on
// /user_status.json), as opposed to an auth-shaped failure. UpdateProgress
// uses it to decide whether to invalidate-and-retry once versus fail
// immediately.
var errCSRFRejected = errors.New("goodreads: csrf token rejected")
