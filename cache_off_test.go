//go:build !zerodecimal_strcache || zerodecimal_nostrcache

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStringCacheBuildModeDisabled makes default-off behavior a compile-time
// contract. It also verifies that zerodecimal_nostrcache wins if both cache
// tags are supplied, preserving the legacy explicit-disable path.
func TestStringCacheBuildModeDisabled(t *testing.T) {
	assert.False(t, strCacheEnabled)

	if s, ok := cachedString(Zero); ok || s != "" {
		t.Fatalf("cachedString must miss in a cache-free build: got (%q, %t)", s, ok)
	}
	if v, ok := cachedValue(Zero); ok || v != nil {
		t.Fatalf("cachedValue must miss in a cache-free build: got (%v, %t)", v, ok)
	}
}
