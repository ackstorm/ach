// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

// Package register links every Enterprise package into the ach binary.
package register

import (
	_ "github.com/ackstorm/ach/ee/cmd"
	_ "github.com/ackstorm/ach/ee/platformapi/obsapi"
)
