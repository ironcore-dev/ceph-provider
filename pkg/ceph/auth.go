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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

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

func GetKeyFromKeyring(keyringFile string) (string, error) {
	data, err := os.ReadFile(keyringFile)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	var key string

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if line := sc.Text(); strings.Contains(line, "key") {
			line = strings.Trim(line, "\t")
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "key")
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "=")
			line = strings.TrimSpace(line)
			key = line
			break
		}
	}

	if key == "" {
		return "", fmt.Errorf("failed to extract key from keyring")
	}

	return key, nil

}

func CheckIfPoolExists(conn *rados.Conn, pool string) error {
	pools, err := conn.ListPools()
	if err != nil {
		return fmt.Errorf("failed to list pools: %w", err)
	}

	var found bool
	for _, p := range pools {
		if p == pool {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("pool %s not found", pool)
	}

	return nil
}
