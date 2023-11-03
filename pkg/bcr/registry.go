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

package bcr

import (
	"fmt"
	"io"
	"os"

	"k8s.io/apimachinery/pkg/util/yaml"

	ori "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
)

func LoadBucketClasses(reader io.Reader) ([]ori.BucketClass, error) {
	var classList []ori.BucketClass
	if err := yaml.NewYAMLOrJSONDecoder(reader, 4096).Decode(&classList); err != nil {
		return nil, fmt.Errorf("unable to unmarshal bucket classes: %w", err)
	}

	return classList, nil
}

func LoadBucketClassesFile(filename string) ([]ori.BucketClass, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to open bucket class file (%s): %w", filename, err)
	}

	defer file.Close()
	return LoadBucketClasses(file)
}

func NewBucketClassRegistry(classes []ori.BucketClass) (*Bcr, error) {
	registry := Bcr{
		classes: map[string]ori.BucketClass{},
	}

	for _, class := range classes {
		if _, ok := registry.classes[class.Name]; ok {
			return nil, fmt.Errorf("multiple classes with same name (%s) found", class.Name)
		}
		registry.classes[class.Name] = class
	}

	return &registry, nil
}

type Bcr struct {
	classes map[string]ori.BucketClass
}

func (v *Bcr) Get(bucketClassName string) (*ori.BucketClass, bool) {
	class, found := v.classes[bucketClassName]
	return &class, found
}

func (v *Bcr) List() []*ori.BucketClass {
	var classes []*ori.BucketClass
	for name := range v.classes {
		class := v.classes[name]
		classes = append(classes, &class)
	}
	return classes
}
