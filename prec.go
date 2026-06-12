package zerodecimal

// MaxPrec is the maximum number of fractional digits a Decimal can carry.
// It is bounded by the 10^19 < 2^64 invariant the scaling tables rely on.
const MaxPrec uint8 = 19
