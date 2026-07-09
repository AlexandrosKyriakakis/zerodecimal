#!/bin/sh
set -e

fuzzTime=${1:-10}

files=$(grep -r --include='*_test.go' --files-with-matches 'func Fuzz' .)

echo "Fuzz time: ${fuzzTime}s"

for file in ${files}; do
	funcs=$(grep -E -o 'func Fuzz[A-Za-z0-9_]+' "$file" | cut -d' ' -f2)
	for func in ${funcs}; do
		echo "Fuzzing ${func} in ${file}"
		go test -tags=fuzz "$(dirname "$file")" -run='^$' -fuzz="^${func}\$" -fuzztime="${fuzzTime}s" -timeout=10m
	done
done

# The default build intentionally compiles the string cache out, so the main
# loop can only exercise FuzzStringCacheMode's cache-miss branch. Run that
# target once more with the opt-in cache to keep hit/index/value-cache behavior
# under automated fuzz coverage too.
echo "Fuzzing FuzzStringCacheMode with zerodecimal_strcache"
go test -tags=fuzz,zerodecimal_strcache . -run='^$' -fuzz='^FuzzStringCacheMode$' -fuzztime="${fuzzTime}s" -timeout=10m
