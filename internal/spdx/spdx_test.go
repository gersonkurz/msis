package spdx

import "testing"

// The licence check follows the SPDX grammar (#64's review), not a copy of the SPDX list.
func TestSPDXExpressionSyntax(t *testing.T) {
	for _, ok := range []string{
		"MIT", "GPL-2.0+", "LicenseRef-acme-proprietary", "DocumentRef-spdx-tool-1.2:LicenseRef-MIT-Style-2",
		"GPL-3.0-only WITH Classpath-exception-2.0",
		"(MIT OR Apache-2.0) AND BSD-3-Clause",
		"(MIT)OR(Apache-2.0)", // parentheses delimit tokens
		"MIT AND (LGPL-2.1-or-later OR BSD-3-Clause) AND ((Apache-2.0))",
		"(GPL-2.0-only WITH Classpath-exception-2.0) OR MIT", // a WITH inside a group, combined by OR
	} {
		if err := Valid(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "(MIT", "MIT)", "MIT++", "()", "MIT OR", "OR MIT", "MIT AND AND Apache-2.0",
		"MIT and Apache-2.0", // operators are upper case
		"MIT WITH", "MIT WITH OR", "(MIT OR Apache-2.0", "MIT Apache-2.0", "MIT,Apache-2.0",
		"LicenseRef-", "GPL-2.0+ WITH exception+",
		// WITH's left operand is a single licence (Annex D's simple expression), never a group.
		"(MIT OR Apache-2.0) WITH Classpath-exception-2.0",
		"(GPL-2.0-only WITH Classpath-exception-2.0) WITH LLVM-exception",
		"(MIT) WITH Classpath-exception-2.0",
		"MIT WITH Classpath-exception-2.0 WITH LLVM-exception",
	} {
		if err := Valid(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
