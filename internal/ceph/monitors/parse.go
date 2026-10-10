// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	csiClusterConfigJSONKey = "csi-cluster-config-json"
	rookDataKey             = "data"
)

type csiClusterConfigEntry struct {
	ClusterID string   `json:"clusterID"`
	Monitors  []string `json:"monitors"`
}

// ParseCSIClusterConfigJSON unmarshals Rook csi-cluster-config-json.
// If clusterID is empty, the first entry with a non-empty monitors list is used.
func ParseCSIClusterConfigJSON(data []byte, clusterID string) ([]string, error) {
	var entries []csiClusterConfigEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse csi-cluster-config-json: %w", err)
	}

	if clusterID != "" {
		for _, entry := range entries {
			if entry.ClusterID == clusterID {
				return NormalizeEndpoints(entry.Monitors)
			}
		}
		return nil, fmt.Errorf("cluster %q not found in csi-cluster-config-json", clusterID)
	}

	for _, entry := range entries {
		if len(entry.Monitors) == 0 {
			continue
		}
		return NormalizeEndpoints(entry.Monitors)
	}
	return nil, fmt.Errorf("no monitors in csi-cluster-config-json")
}

// ParseRookDataCSV parses Rook `data` values of the form `id=host:port,...`.
// Tokens containing '[' are rejected (msgr2 addrvecs); callers must use the CSI JSON key.
func ParseRookDataCSV(raw string) ([]string, error) {
	tokens := strings.Split(raw, ",")
	endpoints := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.Contains(token, "[") {
			return nil, fmt.Errorf("msgr2 vector not supported; use csi-cluster-config-json")
		}
		if i := strings.Index(token, "="); i >= 0 {
			token = strings.TrimSpace(token[i+1:])
		}
		if token == "" {
			continue
		}
		endpoints = append(endpoints, token)
	}
	return NormalizeEndpoints(endpoints)
}

// NormalizeEndpoints strips trailing /nonce suffixes, validates host:port with net.SplitHostPort,
// deduplicates, and sorts. Empty input is an error.
func NormalizeEndpoints(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no monitor endpoints")
	}

	seen := make(map[string]struct{}, len(raw))
	for _, endpoint := range raw {
		endpoint = strings.TrimSpace(stripNonce(endpoint))
		if endpoint == "" {
			continue
		}
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid monitor endpoint %q: %w", endpoint, err)
		}
		formatted := net.JoinHostPort(host, port)
		seen[formatted] = struct{}{}
	}

	if len(seen) == 0 {
		return nil, fmt.Errorf("no monitor endpoints")
	}

	out := make([]string, 0, len(seen))
	for endpoint := range seen {
		out = append(out, endpoint)
	}
	sort.Strings(out)
	return out, nil
}

// Join returns a comma-separated monitor CSV.
func Join(endpoints []string) string {
	return strings.Join(endpoints, ",")
}

// ParseConfigMapData reads monitor endpoints from a ConfigMap data map.
// If the preferred key is missing or empty, it falls back to the Rook `data` key.
func ParseConfigMapData(data map[string]string, key, clusterID string) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("configmap data is empty")
	}

	usedKey := key
	raw := strings.TrimSpace(data[key])
	if raw == "" {
		usedKey = rookDataKey
		raw = strings.TrimSpace(data[rookDataKey])
	}
	if raw == "" {
		return nil, fmt.Errorf("configmap has no monitor endpoints (key %q or %q)", key, rookDataKey)
	}

	if usedKey == csiClusterConfigJSONKey || strings.HasPrefix(raw, "[") {
		return ParseCSIClusterConfigJSON([]byte(raw), clusterID)
	}
	return ParseRookDataCSV(raw)
}

func stripNonce(endpoint string) string {
	i := strings.LastIndex(endpoint, "/")
	if i < 0 || i == len(endpoint)-1 {
		return endpoint
	}
	suffix := endpoint[i+1:]
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return endpoint
		}
	}
	return endpoint[:i]
}
