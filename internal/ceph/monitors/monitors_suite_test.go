// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMonitors(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Monitors Suite")
}
