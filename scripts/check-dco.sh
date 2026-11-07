#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# Usage: check-dco.sh <base> <head>
# Fails when a non-merge commit in base..head has no Signed-off-by line
# matching its author (see CONTRIBUTING.md).

set -eu

base=$1
head=$2
bad=0

for c in $(git rev-list --no-merges "$base..$head"); do
	author=$(git show -s --format='%an <%ae>' "$c")
	if ! git show -s --format='%(trailers:key=Signed-off-by,valueonly)' "$c" | grep -qxF "$author"; then
		echo "$(git show -s --format='%h %s' "$c"): missing 'Signed-off-by: $author'"
		bad=1
	fi
done

if [ "$bad" -ne 0 ]; then
	echo "sign off with 'git commit -s' (fix existing commits: git rebase --signoff $base)"
	exit 1
fi
echo "all commits signed off"
