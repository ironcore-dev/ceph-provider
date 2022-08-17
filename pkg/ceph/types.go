// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ceph

import (
	"encoding/json"
	"sort"
)

type ClusterList map[string]ClusterEntry

type ClusterSlice []ClusterEntry

type ClusterEntry struct {
	ClusterID      string   `json:"clusterID"`
	Monitors       []string `json:"monitors"`
	RadosNamespace string   `json:"radosNamespace,omitempty"`
}

var _ json.Marshaler = (*ClusterList)(nil)
var _ json.Unmarshaler = (*ClusterList)(nil)

func (l *ClusterList) Add(entry ClusterEntry) {
	if *l == nil {
		*l = ClusterList{}
	}
	(*l)[entry.ClusterID] = entry
}

func (l ClusterList) Remove(clusterID string) {
	delete(l, clusterID)
}

func (l ClusterList) MarshalJSON() ([]byte, error) {
	list := make(ClusterSlice, 0, len(l))
	for _, e := range l {
		list = append(list, e)
	}
	sort.Sort(list)
	return json.Marshal(list)
}

func (l *ClusterList) UnmarshalJSON(bytes []byte) error {
	var list ClusterSlice
	if err := json.Unmarshal(bytes, &list); err != nil {
		return err
	}
	*l = ClusterList{}
	for _, e := range list {
		l.Add(e)
	}
	return nil
}

var _ sort.Interface = ClusterSlice{}

func (s ClusterSlice) Len() int           { return len(s) }
func (s ClusterSlice) Less(i, j int) bool { return s[i].ClusterID < s[j].ClusterID }
func (s ClusterSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
