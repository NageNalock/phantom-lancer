package probes

import "crypto/tls"

// cryptoTLSInsecureConfig returns a tls.Config with InsecureSkipVerify.
// Extracted to this file so the main l3_webapi.go doesn't carry the
// crypto/tls import (keeps import list readable in the common case).
func cryptoTLSInsecureConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}
