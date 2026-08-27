package zerodecimal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfiguredDefaultPrecision is shared by every precision build. The
// build-tagged companion files define configuredDefaultPrec, so a constraint
// regression cannot silently test one configuration with another's oracle.
func TestConfiguredDefaultPrecision(t *testing.T) {
	assert.Equal(t, configuredDefaultPrec, DefaultPrec)
	assert.LessOrEqual(t, DefaultPrec, MaxPrec)

	third, err := NewFromInt(1).Div(NewFromInt(3))
	require.NoError(t, err)
	assert.Equal(t, DefaultPrec, third.Prec())
	assert.Equal(t, "0."+strings.Repeat("3", int(DefaultPrec)), third.String())

	parsed := RequireFromString("0.0000000000000000001")
	assert.Equal(t, MaxPrec, parsed.Prec(), "strict parsing remains independent of DefaultPrec")
}
