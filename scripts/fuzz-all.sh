#!/bin/sh
set -eu

fuzzTime=${1:-10}

files=$(grep -r --include='*_test.go' --files-with-matches 'func Fuzz' .)

echo "Fuzz time: ${fuzzTime}s"

# Go 1.26 can very occasionally report a coordinator shutdown race just after
# -fuzztime expires. Buffer one attempt at a time, print its complete output,
# and retry once only when the transcript is exactly the known deadline tail.
tmpDir=$(mktemp -d "${TMPDIR:-/tmp}/zerodecimal-fuzz-all.XXXXXX")
trap 'rm -rf "$tmpDir"' 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

attemptNumber=0
lastFuzzLog=

runFuzzAttempt() {
	attemptNumber=$((attemptNumber + 1))
	lastFuzzLog=${tmpDir}/attempt.${attemptNumber}.log
	if go "$@" >"$lastFuzzLog" 2>&1; then
		status=0
	else
		status=$?
	fi
	if ! cat "$lastFuzzLog"; then
		echo "could not print the complete fuzz output" >&2
		return 125
	fi
	return "$status"
}

isDeadlineOnlyFailure() {
	case "$fuzzTime" in
		''|*[!0-9]*|0) return 1 ;;
	esac

	# Fail closed when the attempt contains any non-fuzz prelude, including
	# cold-cache `go: downloading ...` output. The preceding tagged-test CI step
	# warms the module cache; broadening this allowlist would risk retrying an
	# unrecognized diagnostic along with the deadline tail.
	awk -v target="$1" -v seconds="$fuzzTime" '
		{ lines[NR] = $0 }
		END {
			if (NR < 5) exit 1
			for (i = 1; i <= NR - 5; i++)
				if (lines[i] !~ /^fuzz: /) exit 1

			prefix = "--- FAIL: " target " ("
			header = lines[NR - 4]
			if (index(header, prefix) != 1 || substr(header, length(header), 1) != ")") exit 1
			duration = substr(header, length(prefix) + 1, length(header) - length(prefix) - 1)
			if (duration !~ /^[0-9]+([.][0-9]+)?s$/) exit 1
			sub(/s$/, "", duration)
			if (duration + 0 < seconds + 0 || duration + 0 >= seconds + 1) exit 1

			if (lines[NR - 3] != "    context deadline exceeded" ||
				lines[NR - 2] != "FAIL" || lines[NR - 1] != "exit status 1" ||
				lines[NR] !~ /^FAIL[[:space:]]+[^[:space:]]+[[:space:]]+[0-9]+([.][0-9]+)?s$/) exit 1
		}
	' "$lastFuzzLog"
}

runFuzzTarget() {
	target=$1
	shift

	if runFuzzAttempt "$@"; then
		return 0
	else
		firstStatus=$?
	fi

	if [ "$firstStatus" -eq 1 ] && isDeadlineOnlyFailure "$target"; then
		echo "Retrying ${target} once after an exact deadline-only fuzz shutdown failure"
		if runFuzzAttempt "$@"; then
			return 0
		else
			return $?
		fi
	fi

	return "$firstStatus"
}

for file in ${files}; do
	funcs=$(grep -E -o 'func Fuzz[A-Za-z0-9_]+' "$file" | cut -d' ' -f2)
	for func in ${funcs}; do
		echo "Fuzzing ${func} in ${file}"
		runFuzzTarget "$func" test -tags=fuzz "$(dirname "$file")" -run='^$' -fuzz="^${func}\$" -fuzztime="${fuzzTime}s" -timeout=10m
	done
done

# The default build intentionally compiles the string cache out, so the main
# loop can only exercise FuzzStringCacheMode's cache-miss branch. Run that
# target once more with the opt-in cache to keep hit/index/value-cache behavior
# under automated fuzz coverage too.
echo "Fuzzing FuzzStringCacheMode with zerodecimal_strcache"
runFuzzTarget FuzzStringCacheMode test -tags=fuzz,zerodecimal_strcache . -run='^$' -fuzz='^FuzzStringCacheMode$' -fuzztime="${fuzzTime}s" -timeout=10m
