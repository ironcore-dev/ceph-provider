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

package vcr

import (
	"fmt"
	"io"
	"os"

	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func LoadVolumeClasses(reader io.Reader) ([]ori.VolumeClass, error) {
	var classList []ori.VolumeClass
	if err := yaml.NewYAMLOrJSONDecoder(reader, 4096).Decode(&classList); err != nil {
		return nil, fmt.Errorf("unable to unmarshal volume classes: %w", err)
	}

	return classList, nil
}

func LoadVolumeClassesFile(filename string) ([]ori.VolumeClass, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to open volume class file (%s): %w", filename, err)
	}

	return LoadVolumeClasses(file)
}

func NewVolumeClassRegistry(classes []ori.VolumeClass) (*Vcr, error) {
	registry := Vcr{
		classes: map[string]*ori.VolumeClass{},
	}

	for _, class := range classes {
		if _, ok := registry.classes[class.Name]; ok {
			return nil, fmt.Errorf("multiple classes with same name (%s) found", class.Name)
		}
		registry.classes[class.Name] = &class
	}

	return &registry, nil
}

type Vcr struct {
	classes map[string]*ori.VolumeClass
}

func (v *Vcr) Get(volumeClassName string) (*ori.VolumeClass, bool) {
	class, found := v.classes[volumeClassName]
	return class, found
}

func (v *Vcr) List() []*ori.VolumeClass {
	var classes []*ori.VolumeClass
	for _, class := range v.classes {
		classes = append(classes, class)
	}
	return classes
}
