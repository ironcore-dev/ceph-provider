// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

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

	done := make(chan error, 1)
	go func() {
		done <- conn.Connect()
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("ceph connect timeout. monitors: %s, user: %s: %w", c.Monitors, c.User, ctx.Err())
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("connecting failed: %w", err)
		}
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
