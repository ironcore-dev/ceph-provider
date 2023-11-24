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

	"k8s.io/apimachinery/pkg/util/yaml"

	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

func LoadVolumeClasses(reader io.Reader) ([]iri.VolumeClass, error) {
	var classList []iri.VolumeClass
	if err := yaml.NewYAMLOrJSONDecoder(reader, 4096).Decode(&classList); err != nil {
		return nil, fmt.Errorf("unable to unmarshal volume classes: %w", err)
	}

	return classList, nil
}

func LoadVolumeClassesFile(filename string) ([]iri.VolumeClass, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to open volume class file (%s): %w", filename, err)
	}

	defer file.Close()
	return LoadVolumeClasses(file)
}

func NewVolumeClassRegistry(classes []iri.VolumeClass) (*Vcr, error) {
	registry := Vcr{
		classes: map[string]iri.VolumeClass{},
	}

	for _, class := range classes {
		if _, ok := registry.classes[class.Name]; ok {
			return nil, fmt.Errorf("multiple classes with same name (%s) found", class.Name)
		}
		registry.classes[class.Name] = class
	}

	return &registry, nil
}

type Vcr struct {
	classes map[string]iri.VolumeClass
}

func (v *Vcr) Get(volumeClassName string) (*iri.VolumeClass, bool) {
	class, found := v.classes[volumeClassName]
	return &class, found
}

func (v *Vcr) List() []*iri.VolumeClass {
	var classes []*iri.VolumeClass
	for name := range v.classes {
		class := v.classes[name]
		classes = append(classes, &class)
	}
	return classes
}
