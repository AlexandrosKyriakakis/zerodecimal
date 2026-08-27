#!/bin/sh
set -eu

scriptDir=$(CDPATH= cd -P "$(dirname "$0")" && pwd)
fuzzScript=${scriptDir}/fuzz-all.sh

testBase=${TMPDIR:-/tmp}/zerodecimal-fuzz-all-test.$$
testSuffix=0
while :; do
	testDir=${testBase}.${testSuffix}
	if (umask 077 && mkdir "$testDir") 2>/dev/null; then
		break
	fi
	testSuffix=$((testSuffix + 1))
	if [ "$testSuffix" -gt 100 ]; then
		echo "could not create test directory" >&2
		exit 1
	fi
done
trap 'rm -rf "$testDir"' 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "fuzz-all_test: $*" >&2
	exit 1
}

assertContains() {
	file=$1
	text=$2
	if ! grep -F "$text" "$file" >/dev/null; then
		echo "missing output: ${text}" >&2
		sed 's/^/  | /' "$file" >&2
		fail "output assertion failed"
	fi
}

assertNotContains() {
	file=$1
	text=$2
	if grep -F "$text" "$file" >/dev/null; then
		echo "unexpected output: ${text}" >&2
		sed 's/^/  | /' "$file" >&2
		fail "output assertion failed"
	fi
}

readCount() {
	file=$1
	if [ ! -f "$file" ]; then
		printf '0\n'
		return
	fi
	count=
	IFS= read -r count <"$file"
	printf '%s\n' "$count"
}

assertCount() {
	file=$1
	want=$2
	got=$(readCount "$file")
	if [ "$got" -ne "$want" ]; then
		fail "$(basename "$file") invocation count = ${got}, want ${want}"
	fi
}

assertNoTempLogs() {
	tmp=$1
	for path in "$tmp"/zerodecimal-fuzz-all.*; do
		if [ -e "$path" ]; then
			fail "temporary fuzz log was not cleaned: ${path}"
		fi
	done
}

fakeBin=${testDir}/bin
mkdir "$fakeBin"
fakeGo=${fakeBin}/go

cat >"$fakeGo" <<'EOF'
#!/bin/sh
set -eu

target=unknown
tags=unknown
for arg in "$@"; do
	case "$arg" in
		-fuzz=*FuzzRetry*) target=retry ;;
		-fuzz=*FuzzStringCacheMode*) target=cache ;;
		-tags=*) tags=${arg#-tags=} ;;
	esac
done

countFile=${FUZZ_FAKE_STATE}/${target}.count
count=0
if [ -f "$countFile" ]; then
	IFS= read -r count <"$countFile"
fi
count=$((count + 1))
printf '%s\n' "$count" >"$countFile"
printf '%s|%s|%s\n' "$target" "$tags" "$*" >>"${FUZZ_FAKE_STATE}/trace"

if [ "$target" = cache ]; then
	printf 'cache rerun stdout\n'
	printf 'cache rerun stderr\n' >&2
	exit 0
fi

emitDeadline() {
	duration=${1:-20.09}
	printf 'fuzz: elapsed: 0s, gathering baseline coverage: 1/1 completed\n'
	printf 'fuzz: elapsed: 20s, execs: 500000 (25000/sec), new interesting: 0 (total: 1)\n'
	printf '%s\n' "--- FAIL: FuzzRetry (${duration}s)"
	printf '    context deadline exceeded\n'
	printf 'FAIL\n'
	printf 'exit status 1\n'
	printf 'FAIL\texample.test/fuzzfixture\t20.123s\n'
}

case "$FUZZ_FAKE_SCENARIO" in
	success)
		printf 'normal success stdout\n'
		printf 'normal success stderr\n' >&2
		exit 0
		;;
	deadline-once)
		if [ "$count" -eq 1 ]; then
			emitDeadline
			exit 1
		fi
		printf 'retry success\n'
		exit 0
		;;
	deadline-twice)
		emitDeadline
		exit 1
		;;
	crash)
		printf 'fuzz: elapsed: 1s, execs: 10 (10/sec), new interesting: 1 (total: 2)\n'
		printf '%s\n' '--- FAIL: FuzzRetry (1.00s)'
		printf 'panic: deterministic crash [recovered]\n'
		printf 'FAIL\nexit status 2\n'
		printf 'FAIL\texample.test/fuzzfixture\t1.123s\n'
		exit 2
		;;
	deadline-with-diagnostic)
		printf 'fuzz: elapsed: 20s, execs: 500000 (25000/sec), new interesting: 0 (total: 1)\n'
		printf '%s\n' '--- FAIL: FuzzRetry (20.09s)'
		printf '    fixture_test.go:10: deterministic assertion failure\n'
		printf '    context deadline exceeded\n'
		printf 'FAIL\nexit status 1\n'
		printf 'FAIL\texample.test/fuzzfixture\t20.123s\n'
		exit 1
		;;
	deadline-with-reproducer)
		printf 'fuzz: elapsed: 20s, execs: 500000 (25000/sec), new interesting: 1 (total: 2)\n'
		printf '%s\n' '--- FAIL: FuzzRetry (20.09s)'
		printf '    context deadline exceeded\n'
		printf '    Failing input written to testdata/fuzz/FuzzRetry/deadbeef\n'
		printf 'FAIL\nexit status 1\n'
		printf 'FAIL\texample.test/fuzzfixture\t20.123s\n'
		exit 1
		;;
	deadline-with-cold-cache-prelude)
		printf 'go: downloading example.test/dependency v1.2.3\n'
		emitDeadline
		exit 1
		;;
	wrong-duration)
		emitDeadline 19.99
		exit 1
		;;
	wrong-duration-high)
		emitDeadline 21.00
		exit 1
		;;
	wrong-status)
		emitDeadline
		exit 2
		;;
	*)
		printf 'unknown fake scenario: %s\n' "$FUZZ_FAKE_SCENARIO" >&2
		exit 99
		;;
esac
EOF
chmod +x "$fakeGo"

runCase() {
	name=$1
	scenario=$2
	wantStatus=$3
	wantRetryCount=$4
	wantCacheCount=$5

	caseDir=${testDir}/${name}
	workDir=${caseDir}/work
	stateDir=${caseDir}/state
	tmpDir=${caseDir}/tmp
	mkdir -p "$workDir" "$stateDir" "$tmpDir"
	cat >"${workDir}/fixture_test.go" <<'EOF'
package fuzzfixture

func FuzzRetry() {}
EOF

	if (
		cd "$workDir"
		PATH=${fakeBin}:$PATH \
			TMPDIR=$tmpDir \
			FUZZ_FAKE_SCENARIO=$scenario \
			FUZZ_FAKE_STATE=$stateDir \
			sh "$fuzzScript" 20
	) >"${caseDir}/output" 2>&1; then
		status=0
	else
		status=$?
	fi

	if [ "$status" -ne "$wantStatus" ]; then
		sed 's/^/  | /' "${caseDir}/output" >&2
		fail "${name} status = ${status}, want ${wantStatus}"
	fi
	assertCount "${stateDir}/retry.count" "$wantRetryCount"
	assertCount "${stateDir}/cache.count" "$wantCacheCount"
	assertNoTempLogs "$tmpDir"
}

retryMessage='Retrying FuzzRetry once after an exact deadline-only fuzz shutdown failure'

runCase success success 0 1 1
assertContains "${testDir}/success/output" 'normal success stdout'
assertContains "${testDir}/success/output" 'normal success stderr'
assertContains "${testDir}/success/output" 'cache rerun stdout'
assertContains "${testDir}/success/output" 'cache rerun stderr'
assertContains "${testDir}/success/state/trace" 'cache|fuzz,zerodecimal_strcache|'
assertNotContains "${testDir}/success/output" "$retryMessage"

runCase deadline_once deadline-once 0 2 1
assertContains "${testDir}/deadline_once/output" 'context deadline exceeded'
assertContains "${testDir}/deadline_once/output" "$retryMessage"
assertContains "${testDir}/deadline_once/output" 'retry success'

runCase deadline_twice deadline-twice 1 2 0
assertContains "${testDir}/deadline_twice/output" "$retryMessage"

runCase crash crash 2 1 0
assertContains "${testDir}/crash/output" 'panic: deterministic crash'
assertNotContains "${testDir}/crash/output" "$retryMessage"

runCase diagnostic deadline-with-diagnostic 1 1 0
assertContains "${testDir}/diagnostic/output" 'deterministic assertion failure'
assertNotContains "${testDir}/diagnostic/output" "$retryMessage"

runCase reproducer deadline-with-reproducer 1 1 0
assertContains "${testDir}/reproducer/output" 'Failing input written to testdata/fuzz/FuzzRetry/deadbeef'
assertNotContains "${testDir}/reproducer/output" "$retryMessage"

runCase cold_cache_prelude deadline-with-cold-cache-prelude 1 1 0
assertContains "${testDir}/cold_cache_prelude/output" 'go: downloading example.test/dependency v1.2.3'
assertNotContains "${testDir}/cold_cache_prelude/output" "$retryMessage"

runCase wrong_duration wrong-duration 1 1 0
assertNotContains "${testDir}/wrong_duration/output" "$retryMessage"

runCase wrong_duration_high wrong-duration-high 1 1 0
assertNotContains "${testDir}/wrong_duration_high/output" "$retryMessage"

runCase wrong_status wrong-status 2 1 0
assertNotContains "${testDir}/wrong_status/output" "$retryMessage"

echo 'fuzz-all retry tests: PASS'
