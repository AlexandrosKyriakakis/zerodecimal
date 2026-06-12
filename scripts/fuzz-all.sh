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
