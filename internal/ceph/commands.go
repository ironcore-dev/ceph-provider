// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package ceph

import (
	"encoding/json"
	"fmt"

	"github.com/ceph/go-ceph/rados"
)

type CommandRequest struct {
	Prefix string `json:"prefix"`
	Detail string `json:"detail"`
	Format string `json:"format"`
}

type DfCommandResponse struct {
	Stats        Stats        `json:"stats"`
	StatsByClass StatsByClass `json:"stats_by_class"`
	Pools        []Pool       `json:"pools"`
}

type Stats struct {
	TotalBytes         int64   `json:"total_bytes"`
	TotalAvailBytes    int64   `json:"total_avail_bytes"`
	TotalUsedBytes     int     `json:"total_used_bytes"`
	TotalUsedRawBytes  int     `json:"total_used_raw_bytes"`
	TotalUsedRawRatio  float64 `json:"total_used_raw_ratio"`
	NumOsds            int     `json:"num_osds"`
	NumPerPoolOsds     int     `json:"num_per_pool_osds"`
	NumPerPoolOmapOsds int     `json:"num_per_pool_omap_osds"`
}

type StatsByClass map[string]struct {
	TotalBytes        int64   `json:"total_bytes"`
	TotalAvailBytes   int64   `json:"total_avail_bytes"`
	TotalUsedBytes    int     `json:"total_used_bytes"`
	TotalUsedRawBytes int     `json:"total_used_raw_bytes"`
	TotalUsedRawRatio float64 `json:"total_used_raw_ratio"`
}

type Pool struct {
	Name  string    `json:"name"`
	Id    int       `json:"id"`
	Stats PoolStats `json:"stats"`
}

type PoolStats struct {
	Stored      int     `json:"stored"`
	Objects     int     `json:"objects"`
	KbUsed      int     `json:"kb_used"`
	BytesUsed   int     `json:"bytes_used"`
	PercentUsed float64 `json:"percent_used"`
	MaxAvail    int64   `json:"max_avail"`
}

type Command interface {
	PoolStats() (*PoolStats, error)
}

func NewCommandClient(conn *rados.Conn, poolName string) (*CommandClient, error) {
	return &CommandClient{
		conn:     conn,
		poolName: poolName,
	}, nil
}

type CommandClient struct {
	conn     *rados.Conn
	poolName string
}

func (c *CommandClient) PoolStats() (*PoolStats, error) {
	req, err := json.Marshal(CommandRequest{
		Prefix: "df",
		Detail: "",
		Format: "json",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal df command request data: %w", err)
	}

	resp, _, err := c.conn.MonCommand(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do df request: %w", err)
	}

	data := &DfCommandResponse{}
	if err := json.Unmarshal(resp, data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal df command request data: %w", err)
	}

	for _, pool := range data.Pools {
		if pool.Name == c.poolName {
			return &pool.Stats, nil
		}
	}

	return nil, fmt.Errorf("no pool stats with pool name %s found", c.poolName)
}
