package zerodecimal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecimalJSONNullPolicy(t *testing.T) {
	t.Run("nonzero_receiver_is_unchanged", func(t *testing.T) {
		marker := RequireFromString("1234.5678")
		d := marker
		require.ErrorIs(t, d.UnmarshalJSON([]byte(jsonNull)), ErrJSONNull)
		assert.Equal(t, marker, d)
	})

	t.Run("zero_receiver_is_unchanged", func(t *testing.T) {
		var d Decimal
		require.ErrorIs(t, d.UnmarshalJSON([]byte(jsonNull)), ErrJSONNull)
		assert.Equal(t, Decimal{}, d)
	})

	t.Run("encoding_json_propagates_sentinel", func(t *testing.T) {
		marker := RequireFromString("1234.5678")
		d := marker
		require.ErrorIs(t, json.Unmarshal([]byte(jsonNull), &d), ErrJSONNull)
		assert.Equal(t, marker, d)
	})

	t.Run("direct_surrounding_whitespace_preserves_strict_input", func(t *testing.T) {
		marker := RequireFromString("1234.5678")
		d := marker
		require.ErrorIs(t, d.UnmarshalJSON([]byte(" \tnull\r\n")), ErrInvalidFormat)
		assert.Equal(t, marker, d)
	})

	t.Run("encoding_json_normalizes_surrounding_whitespace", func(t *testing.T) {
		marker := RequireFromString("1234.5678")
		d := marker
		require.ErrorIs(t, json.Unmarshal([]byte(" \tnull\r\n"), &d), ErrJSONNull)
		assert.Equal(t, marker, d)
	})
}

type jsonNullPolicyFixture struct {
	Required Decimal     `json:"required"`
	Optional NullDecimal `json:"optional"`
}

func TestJSONNullStructPolicy(t *testing.T) {
	marker := jsonNullPolicyFixture{
		Required: RequireFromString("125.50"),
		Optional: NewNullDecimal(RequireFromString("2.5")),
	}

	t.Run("required_decimal_rejects_null", func(t *testing.T) {
		got := marker
		require.ErrorIs(t, json.Unmarshal([]byte(`{"required":null}`), &got), ErrJSONNull)
		assert.Equal(t, marker, got)
	})

	t.Run("nullable_decimal_clears_on_null", func(t *testing.T) {
		got := marker
		require.NoError(t, json.Unmarshal([]byte(`{"optional":null}`), &got))
		assert.Equal(t, marker.Required, got.Required)
		assert.Equal(t, NullDecimal{}, got.Optional)
	})
}

func TestJSONNullRejectedOnReusedObject(t *testing.T) {
	var row jsonNullPolicyFixture
	require.NoError(t, json.Unmarshal(
		[]byte(`{"required":"125.50","optional":"2.5"}`),
		&row,
	))
	want := row

	err := json.Unmarshal([]byte(`{"required":null}`), &row)
	require.ErrorIs(t, err, ErrJSONNull)
	assert.Equal(t, want, row, "a rejected null must not silently retain a stale amount without an error")
}

var (
	jsonNullAllocInput   = []byte(jsonNull)
	jsonNullAllocMarker  = RequireFromString("1234.5678")
	jsonNullAllocDecimal = jsonNullAllocMarker
	jsonNullAllocNull    = NewNullDecimal(jsonNullAllocMarker)
	errJSONNullAlloc     error
)

// TestAllocsJSONNullPolicy pins the zero-allocation contract for both the
// required-value rejection and nullable-value clearing paths.
func TestAllocsJSONNullPolicy(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}

	t.Run("decimal_reject", func(t *testing.T) {
		requireAllocs(t, 0, func() {
			errJSONNullAlloc = jsonNullAllocDecimal.UnmarshalJSON(jsonNullAllocInput)
		})
		require.ErrorIs(t, errJSONNullAlloc, ErrJSONNull)
		assert.Equal(t, jsonNullAllocMarker, jsonNullAllocDecimal)
	})

	t.Run("null_decimal_clear", func(t *testing.T) {
		requireAllocs(t, 0, func() {
			jsonNullAllocNull = NewNullDecimal(jsonNullAllocMarker)
			errJSONNullAlloc = jsonNullAllocNull.UnmarshalJSON(jsonNullAllocInput)
		})
		require.NoError(t, errJSONNullAlloc)
		assert.Equal(t, NullDecimal{}, jsonNullAllocNull)
	})
}
