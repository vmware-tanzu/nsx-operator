// Package framework provides bounded recovery helpers for end-to-end tests.
//
// Recovery applies to temporary API transport failures and API responses that
// explicitly request another attempt. It does not turn failed assertions into
// successful results or retry validation and authorization errors. Callers own
// the context deadline and the safety of repeating an operation; Poll must not
// be used to replay arbitrary writes.
package framework
