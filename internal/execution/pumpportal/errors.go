package pumpportal

import "fmt"

// APIError representa uma resposta HTTP de erro da PumpPortal.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pumpportal: HTTP %d: %s", e.StatusCode, e.Body)
}

// IsRateLimit indica se o erro foi um 429.
func IsRateLimit(err error) bool {
	if e, ok := err.(*APIError); ok {
		return e.StatusCode == 429
	}
	return false
}