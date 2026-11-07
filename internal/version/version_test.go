// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package version

import "testing"

func TestString(t *testing.T) {
	if got, want := String(), "jaga dev (none)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
