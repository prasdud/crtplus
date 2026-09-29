package crt

import (
	"fmt"
	"strings"
)

// ValidateApex performs a lightweight sanity check before spending an API call.
// crt.name requires an eTLD+1; the server rejects anything else with HTTP 400.
func ValidateApex(apex string) error {
	if apex == "" {
		return fmt.Errorf("apex is empty")
	}
	if strings.ContainsAny(apex, " \t/_") {
		return fmt.Errorf("invalid apex %q", apex)
	}
	if strings.Contains(apex, "://") {
		return fmt.Errorf("apex must be a bare domain, got %q", apex)
	}
	if !strings.Contains(apex, ".") {
		return fmt.Errorf("apex %q is not a registrable domain", apex)
	}
	return nil
}
