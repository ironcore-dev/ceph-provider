// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package ceph

import "testing"

func TestGetKeyFromKeyring(t *testing.T) {
	key, err := GetKeyFromKeyring("./test.key")
	if err != nil {
		t.Fail()
	}

	if key != "TESTw48ExAAzbCAVAJn3kGVG3HHg0ahDQ==" {
		t.Fail()
	}
}
