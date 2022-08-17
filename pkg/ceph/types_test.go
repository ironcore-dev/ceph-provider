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
	"testing"

	assert "github.com/stretchr/testify/require"
)

func Test_cephClusterList(t *testing.T) {
	t.Run("Add", func(t *testing.T) {
		t.Run("in nil list", func(t *testing.T) {
			// arrange
			var list ClusterList = nil

			// act
			list.Add(ClusterEntry{ClusterID: "1", Monitors: []string{"addr1", "addr2"}})

			// assert
			assert.Exactly(t, ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}, list)
		})

		t.Run("new entry", func(t *testing.T) {
			// arrange
			list := ClusterList{}

			// act
			list.Add(ClusterEntry{ClusterID: "1", Monitors: []string{"addr1", "addr2"}})

			// assert
			assert.Exactly(t, ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}, list)
		})

		t.Run("update existing entry", func(t *testing.T) {
			// arrange
			list := ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}

			// act
			list.Add(ClusterEntry{ClusterID: "1", Monitors: []string{"addr3"}, RadosNamespace: "rds-ns"})

			// assert
			assert.Exactly(t, ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr3"}, RadosNamespace: "rds-ns"},
			}, list)
		})
	})

	t.Run("Remove", func(t *testing.T) {
		t.Run("in nil list", func(t *testing.T) {
			// arrange
			var list ClusterList = nil

			// act
			list.Remove("id")

			// assert
			assert.Nil(t, list)
		})

		t.Run("existing entry", func(t *testing.T) {
			// arrange
			list := ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}

			// act
			list.Remove("1")

			// assert
			assert.Exactly(t, ClusterList{}, list)
		})

		t.Run("non existing entry", func(t *testing.T) {
			// arrange
			list := ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}

			// act
			list.Remove("2")

			// assert
			assert.Exactly(t, ClusterList{
				"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			}, list)
		})

		// arrange
		var list ClusterList
		list.Add(ClusterEntry{ClusterID: "1", Monitors: []string{"addr1", "addr2"}})
		list.Add(ClusterEntry{ClusterID: "2", Monitors: []string{"addr3", "addr4"}, RadosNamespace: "rds-ns"})

		// act
		list.Remove("1")

		//
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		// arrange
		var list ClusterList
		list.Add(ClusterEntry{ClusterID: "1", Monitors: []string{"addr1", "addr2"}})
		list.Add(ClusterEntry{ClusterID: "2", Monitors: []string{"addr3", "addr4"}, RadosNamespace: "rds-ns"})

		// act
		b, err := json.Marshal(list)

		// assert
		assert.NoError(t, err)
		assert.JSONEq(t, `[
			{"clusterID": "1", "monitors": ["addr1", "addr2"]},
			{"clusterID": "2", "monitors": ["addr3", "addr4"], "radosNamespace": "rds-ns"}
		]`, string(b))
	})

	t.Run("UnmarshalJSON", func(t *testing.T) {
		// arrange
		var list ClusterList
		jsonText := `[
			{"clusterID": "1", "monitors": ["addr1", "addr2"]},
			{"clusterID": "2", "monitors": ["addr3", "addr4"], "radosNamespace": "rds-ns"}
		]`

		// act
		err := json.Unmarshal([]byte(jsonText), &list)

		// assert
		assert.NoError(t, err)
		assert.Exactly(t, ClusterList{
			"1": {ClusterID: "1", Monitors: []string{"addr1", "addr2"}},
			"2": {ClusterID: "2", Monitors: []string{"addr3", "addr4"}, RadosNamespace: "rds-ns"},
		}, list)
	})
}
