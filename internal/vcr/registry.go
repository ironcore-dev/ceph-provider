// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package vcr

import (
	"fmt"
	"io"
	"os"

	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/util/yaml"
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
