// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ceph

import (
	"context"
	"fmt"

	"github.com/ceph/go-ceph/rados"
)

type Credentials struct {
	Monitors string
	User     string
	Keyfile  string
}

func ConnectToRados(ctx context.Context, c Credentials) (*rados.Conn, error) {
	args := []string{"-m", c.Monitors, "--keyfile=" + c.Keyfile}
	conn, err := rados.NewConnWithUser(c.User)
	if err != nil {
		return nil, fmt.Errorf("creating a new connection failed: %w", err)
	}
	err = conn.ParseCmdLineArgs(args)
	if err != nil {
		return nil, fmt.Errorf("parsing cmdline args (%v) failed: %w", args, err)
	}

	err = conn.Connect()
	if err != nil {
		return nil, fmt.Errorf("connecting failed: %w", err)
	}

	return conn, nil
}
