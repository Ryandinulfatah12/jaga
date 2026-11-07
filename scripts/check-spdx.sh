#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# Fails when a tracked source file has no SPDX license header in its first lines.

set -eu

missing=0
for f in $(git ls-files '*.go' '*.sh' '*.js' '*.ts' '*.svelte' '*.css' 'Makefile'); do
	if ! sed -n '1,5p' "$f" | grep -q 'SPDX-License-Identifier:'; then
		echo "missing SPDX header: $f"
		missing=1
	fi
done

if [ "$missing" -ne 0 ]; then
	echo "add '// SPDX-License-Identifier: Apache-2.0' and '// Copyright The Jaga Authors' (see CONTRIBUTING.md)"
	exit 1
fi
