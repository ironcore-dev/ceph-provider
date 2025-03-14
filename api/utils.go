// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"fmt"
	//"github.com/davecgh/go-spew/spew"

	"github.com/ironcore-dev/controller-utils/metautils"
	irimeta "github.com/ironcore-dev/ironcore/iri/apis/meta/v1alpha1"
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetObjectMetadata(o metav1.Object) (*irimeta.ObjectMetadata, error) {
	annotations, err := GetAnnotationsAnnotation(o)
	if err != nil {
		return nil, err
	}

	labels, err := GetLabelsAnnotation(o)
	if err != nil {
		return nil, err
	}

	var deletedAt int64
	if !o.GetDeletionTimestamp().IsZero() {
		deletedAt = o.GetDeletionTimestamp().UnixNano()
	}

	return &irimeta.ObjectMetadata{
		Id:          o.GetName(),
		Annotations: annotations,
		Labels:      labels,
		Generation: o.GetGeneration(),
		CreatedAt:  o.GetCreationTimestamp().UnixNano(),
		DeletedAt:  deletedAt,
	}, nil
}

func SetObjectMetadata(o metav1.Object, metadata *irimeta.ObjectMetadata) error {
	if err := SetAnnotationsAnnotation(o, metadata.Annotations); err != nil {
		return err
	}
	if err := SetLabelsAnnotation(o, metadata.Labels); err != nil {
		return err
	}
	return nil
}

func SetClassLabel(o metav1.Object, class string) {
	metautils.SetLabel(o, ClassLabel, class)
}

func GetClassLabel(o metav1.Object) (string, bool) {
	class, found := o.GetLabels()[ClassLabel]
	return class, found
}

/////////////////////////////////////////////////////////

func GetFilesQuota(m map[string]string) string {
	value, exists := m["bucketMaxObjects"]
	if !exists {
		//fmt.Println("notSet")
		return fmt.Sprintf("notSet")
	}
	if len(value) == 0 {
		//fmt.Println("notSet")
		return fmt.Sprintf("notSet")
	}
	return fmt.Sprint(value)

}

func GetSizeQuota(m map[string]string) string {
	//	if m[bucketMaxSize] {
	value, exists := m["bucketMaxSize"]
	if !exists {
		//fmt.Println("notSet")
		return fmt.Sprintf("notSet")
	}
	if len(value) == 0 {
		//fmt.Println("notSet")
		return fmt.Sprintf("notSet")
	}
	return fmt.Sprint(value)
}

////////////////////////////////////////////////////////

func SetLabelsAnnotation(o metav1.Object, labels map[string]string) error {
	data, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("error marshalling labels: %w", err)
	}
	metautils.SetAnnotation(o, LabelsAnnotation, string(data))
	return nil
}

func GetLabelsAnnotation(o metav1.Object) (map[string]string, error) {
	data, ok := o.GetAnnotations()[LabelsAnnotation]
	if !ok {
		return nil, fmt.Errorf("object has no labels at %s", LabelsAnnotation)
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(data), &labels); err != nil {
		return nil, err
	}

	return labels, nil
}

func SetAnnotationsAnnotation(o metav1.Object, annotations map[string]string) error {
	data, err := json.Marshal(annotations)
	if err != nil {
		return fmt.Errorf("error marshalling annotations: %w", err)
	}
	metautils.SetAnnotation(o, AnnotationsAnnotation, string(data))
	return nil
}

func GetAnnotationsAnnotation(o metav1.Object) (map[string]string, error) {
	data, ok := o.GetAnnotations()[AnnotationsAnnotation]
	if !ok {
		return nil, fmt.Errorf("object has no annotations at %s", AnnotationsAnnotation)
	}

	var annotations map[string]string
	if err := json.Unmarshal([]byte(data), &annotations); err != nil {
		return nil, err
	}

	return annotations, nil
}

func SetBucketManagerLabel(bucket *objectbucketv1alpha1.ObjectBucketClaim, manager string) {
	metautils.SetLabel(bucket, ManagerLabel, manager)
}

func IsManagedBy(o metav1.Object, manager string) bool {
	actual, ok := o.GetLabels()[ManagerLabel]
	return ok && actual == manager
}

func GetObjectMetadataFromObjectID(o Metadata) (*irimeta.ObjectMetadata, error) {
	annotations, err := GetAnnotationsAnnotationForMetadata(o)
	if err != nil {
		return nil, err
	}

	labels, err := GetLabelsAnnotationForMetadata(o)
	if err != nil {
		return nil, err
	}

	var deletedAt int64
	if o.DeletedAt != nil && !o.DeletedAt.IsZero() {
		deletedAt = o.DeletedAt.UnixNano()
	}

	return &irimeta.ObjectMetadata{
		Id:          o.ID,
		Annotations: annotations,
		Labels:      labels,
		Generation:  o.GetGeneration(),
		CreatedAt:   o.CreatedAt.UnixNano(),
		DeletedAt:   deletedAt,
	}, nil
}

func GetAnnotationsAnnotationForMetadata(o Metadata) (map[string]string, error) {
	data, ok := o.GetAnnotations()[AnnotationsAnnotation]
	if !ok {
		return nil, fmt.Errorf("object has no annotations at %s", AnnotationsAnnotation)
	}

	var annotations map[string]string
	if err := json.Unmarshal([]byte(data), &annotations); err != nil {
		return nil, err
	}

	return annotations, nil
}

func GetLabelsAnnotationForMetadata(o Metadata) (map[string]string, error) {
	data, ok := o.GetAnnotations()[LabelsAnnotation]
	if !ok {
		return nil, fmt.Errorf("object has no labels at %s", LabelsAnnotation)
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(data), &labels); err != nil {
		return nil, err
	}

	return labels, nil
}

func GetClassLabelFromObject(o Object) (string, bool) {
	class, found := o.GetLabels()[ClassLabel]
	return class, found
}

func SetObjectMetadataFromMetadata(o Object, metadata *irimeta.ObjectMetadata) error {
	if err := SetAnnotationsAnnotationForObject(o, metadata.Annotations); err != nil {
		return err
	}
	if err := SetLabelsAnnotationForOject(o, metadata.Labels); err != nil {
		return err
	}
	return nil
}

func SetLabelsAnnotationForOject(o Object, labels map[string]string) error {
	data, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("error marshalling labels: %w", err)
	}
	metautils.SetAnnotation(o, LabelsAnnotation, string(data))
	return nil
}

func SetAnnotationsAnnotationForObject(o Object, annotations map[string]string) error {
	data, err := json.Marshal(annotations)
	if err != nil {
		return fmt.Errorf("error marshalling annotations: %w", err)
	}
	metautils.SetAnnotation(o, AnnotationsAnnotation, string(data))

	return nil
}

func SetClassLabelForObject(o Object, class string) {
	metautils.SetLabel(o, ClassLabel, class)
}

func IsObjectManagedBy(o Object, manager string) bool {
	actual, ok := o.GetLabels()[ManagerLabel]
	return ok && actual == manager
}

func SetManagerLabel(o Object, manager string) {
	metautils.SetLabel(o, ManagerLabel, manager)
}
