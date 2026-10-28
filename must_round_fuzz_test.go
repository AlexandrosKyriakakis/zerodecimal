//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mustRoundFuzzRounding is the fixed (places, mode) set the seed corpus meets,
// one entry per seed by rotation: all six modes at zero places, the money scale
// in the tie-breaking mode, full precision truncated, and one rejected value of
// each argument (places 20, mode 6), so every sentinel the twins can panic with
// replays from seeds alone.
var mustRoundFuzzRounding = [...]struct {
	places uint8
	mode   RoundingMode
}{
	{0, ToNearestAway},
	{0, ToNearestEven},
	{0, AwayFromZero},
	{0, TowardZero},
	{0, TowardPositive},
	{0, TowardNegative},
	{2, ToNearestEven},
	{19, TowardZero},
	{MaxPrec + 1, ToNearestAway},
	{2, TowardNegative + 1},
}

// requireMustRoundFuzzContract holds every Must twin that consumes the given
// number of operands to the FuzzMustTwins contract at one fuzzed (places,
// mode): the twin panics exactly when its erroring form returns an error, the
// panic value is that bare sentinel (errors.Is, and in fact the identical
// value), and a success is representation-identical. pc and mc are reduced
// modulo one more than the valid ranges, so every mutated byte stays reachable
// and the rejected values (places 20, mode 6) stay common: the fuzzer cannot
// hide behind validation, and cannot skip it either.
func requireMustRoundFuzzContract(t *testing.T, operands int, a, b, c Decimal, pc, mc uint8) {
	t.Helper()
	places := pc % (MaxPrec + 2)
	mode := RoundingMode(mc % (uint8(TowardNegative) + 2))
	for _, op := range mustRoundOps {
		if op.operands != operands {
			continue
		}
		want, wantErr := op.twin(a, b, c, places, mode)
		var got Decimal
		pv := fuzzPanicValue(func() { got = op.must(a, b, c, places, mode) })
		requireTwin(t, op.name, wantErr, pv, a, b, c, places, mode)
		if wantErr != nil {
			require.Samef(t, wantErr, pv, "%s: panic value must be the bare sentinel %v, got %v: a=%+v b=%+v c=%+v places=%d mode=%d",
				op.name, wantErr, pv, a, b, c, places, mode)
			continue
		}
		require.Equalf(t, want, got, "%s: must twin result: a=%+v b=%+v c=%+v places=%d mode=%d",
			op.name, a, b, c, places, mode)
	}
}

// FuzzMustRoundTwins pins the two-operand twins, MustMulRound and
// MustDivRound, to their erroring forms over operand pairs, as FuzzMustTwins
// does for the legacy twins. Operands are the corpus quadruples; places and
// mode are fuzzed too (see requireMustRoundFuzzContract). The three-operand
// twins are in FuzzMustRoundTwinsTriples.
func FuzzMustRoundTwins(f *testing.F) {
	for i, a := range fuzzCorpus {
		for j, b := range fuzzCorpus {
			r := mustRoundFuzzRounding[(i*len(fuzzCorpus)+j)%len(mustRoundFuzzRounding)]
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, r.places, uint8(r.mode))
		}
	}
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc, mc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		requireMustRoundFuzzContract(t, 2, a, b, Decimal{}, pc, mc)
	})
}

// FuzzMustRoundTwinsTriples pins the three-operand twins, MustMulAddRound,
// MustMulDivRound and MustAvgRound (the mean of a, b and c), to their erroring
// forms over operand triples, which FuzzMustTwins' pairs cannot reach: the
// addend of d*e + f, the divisor of d*e/f, and the third value of a mean. Seeds
// pair every corpus quadruple with every other and with a third one chosen by
// the rotating index of fuzzTriples, and with one of the fixed (places, mode)
// pairs, so each shape appears in every operand slot without cubing the seed
// count.
func FuzzMustRoundTwinsTriples(f *testing.F) {
	for i, a := range fuzzCorpus {
		for j, b := range fuzzCorpus {
			c := fuzzCorpus[(i+j)%len(fuzzCorpus)]
			r := mustRoundFuzzRounding[(i*len(fuzzCorpus)+j)%len(mustRoundFuzzRounding)]
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, c.neg, c.hi, c.lo, c.prec, r.places, uint8(r.mode))
		}
	}
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, cneg bool, chi, clo uint64, cprec uint8, pc, mc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzDecimal(t, cneg, chi, clo, cprec)
		requireMustRoundFuzzContract(t, 3, a, b, c, pc, mc)
	})
}
