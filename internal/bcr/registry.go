// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package bcr

import (
	"fmt"
	"io"
	"os"

	"k8s.io/apimachinery/pkg/util/yaml"

	iri "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
)

func LoadBucketClasses(reader io.Reader) ([]*iri.BucketClass, error) {
	var classList []*iri.BucketClass
	if err := yaml.NewYAMLOrJSONDecoder(reader, 4096).Decode(&classList); err != nil {
		return nil, fmt.Errorf("unable to unmarshal bucket classes: %w", err)
	}

	return classList, nil
}

func LoadBucketClassesFile(filename string) ([]*iri.BucketClass, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to open bucket class file (%s): %w", filename, err)
	}

	defer file.Close()
	return LoadBucketClasses(file)
}

func NewBucketClassRegistry(classes []*iri.BucketClass) (*Bcr, error) {
	registry := Bcr{
		classes: map[string]*iri.BucketClass{},
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
	classes map[string]*iri.BucketClass
}

func (v *Bcr) Get(bucketClassName string) (*iri.BucketClass, bool) {
	class, found := v.classes[bucketClassName]
	return class, found
}

func (v *Bcr) List() []*iri.BucketClass {
	var classes []*iri.BucketClass
	for name := range v.classes {
		class := v.classes[name]
		classes = append(classes, class)
	}
	return classes
}
