/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/crossplane/crossplane-runtime/pkg/fieldpath"
)

const (
	errMathNoMultiplier   = "no input is given"
	errMathInputNonNumber = "input is required to be a number for math transformer"
)

var (
	errTransformAtIndex    = func(i int) string { return fmt.Sprintf("transform at index %d returned error", i) }
	errMapNotFound         = func(s string) string { return fmt.Sprintf("given value %s is not found in map", s) }
	errMapTypeNotSupported = func(s string) string { return fmt.Sprintf("type %s is not supported for map transform", s) }
	errTypeNotSupported    = func(s string) string { return fmt.Sprintf("transform type %s is not supported", s) }
	errConfigMissing       = func(s string) string { return fmt.Sprintf("given type %s requires configuration", s) }
	errTransformWithType   = func(s string) string { return fmt.Sprintf("%s transform could not resolve", s) }
)

// CompositionSpec specifies the desired state of the definition.
type CompositionSpec struct {
	// From refers to the type that this composition is compatible. The values
	// for the underlying resources will be fetched from the instances of the
	// From.
	// +immutable
	From TypeReference `json:"from"`

	// To is the list of target resources that make up the composition.
	To []ComposedTemplate `json:"to"`
}

// TypeReference is used to refer to a type for declaring compatibility.
type TypeReference struct {
	// APIVersion of the type.
	APIVersion string `json:"apiVersion"`

	// Kind of the type.
	Kind string `json:"kind"`
}

// ComposedTemplate is used to provide information about how the composed resource
// should be processed.
type ComposedTemplate struct {
	// Base is the target resource that the patches will be applied on.
	Base runtime.RawExtension `json:"base"`

	// Patches will be applied as overlay to the base resource.
	// +optional
	Patches []Patch `json:"patches,omitempty"`

	// ConnectionDetails lists the propagation secret keys from this target
	// resource to the composition instance connection secret.
	// +optional
	ConnectionDetails []ConnectionDetail `json:"connectionDetails,omitempty"`
}

// Patch is used to patch the field on the base resource at ToFieldPath
// after piping the value that is at FromFieldPath of the target resource through
// transformers.
type Patch struct {

	// FromFieldPath is the path of the field on the upstream resource whose value
	// to be used as input.
	FromFieldPath string `json:"fromFieldPath"`

	// ToFieldPath is the path of the field on the base resource whose value will
	// be changed with the result of transforms. Leave empty if you'd like to
	// propagate to the same path on the target resource.
	// +optional
	ToFieldPath string `json:"toFieldPath,omitempty"`

	// Transforms are the list of functions that are used as a FIFO pipe for the
	// input to be transformed.
	// +optional
	Transforms []Transform `json:"transforms,omitempty"`
}

// Apply runs transformers and patches the target resource.
func (c *Patch) Apply(from, to runtime.Object) error {
	fromMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(from)
	if err != nil {
		return err
	}

	in, err := fieldpath.Pave(fromMap).GetValue(c.FromFieldPath)
	if err != nil {
		return err
	}
	out := in
	for i, f := range c.Transforms {
		if out, err = f.Transform(out); err != nil {
			return errors.Wrap(err, errTransformAtIndex(i))
		}
	}

	if u, ok := to.(interface{ UnstructuredContent() map[string]interface{} }); ok {
		return fieldpath.Pave(u.UnstructuredContent()).SetValue(c.ToFieldPath, out)
	}

	toMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(to)
	if err != nil {
		return err
	}
	if err := fieldpath.Pave(toMap).SetValue(c.ToFieldPath, out); err != nil {
		return err
	}
	return runtime.DefaultUnstructuredConverter.FromUnstructured(toMap, to)
}

// TransformType is type of the transform function to be chosen.
type TransformType string

// Accepted TransformTypes.
const (
	TransformTypeMap  TransformType = "map"
	TransformTypeMath TransformType = "math"
)

// Transform is a unit of process whose input is transformed into an output with
// the supplied configuration.
type Transform struct {

	// Type of the transform to be run.
	Type TransformType `json:"type"`

	// Math is used to transform input via mathematical operations such as multiplication.
	// +optional
	Math *MathTransform `json:"math,omitempty"`

	// Map uses input as key in the given map and returns the value.
	// +optional
	Map *MapTransform `json:"map,omitempty"`
}

// Transform calls the appropriate Transformer.
func (t *Transform) Transform(input interface{}) (interface{}, error) {
	var transformer interface {
		Resolve(input interface{}) (interface{}, error)
	}
	switch t.Type {
	case TransformTypeMath:
		transformer = t.Math
	case TransformTypeMap:
		transformer = t.Map
	default:
		return nil, errors.New(errTypeNotSupported(string(t.Type)))
	}
	if transformer == nil {
		return nil, errors.New(errConfigMissing(string(t.Type)))
	}
	out, err := transformer.Resolve(input)
	return out, errors.Wrap(err, errTransformWithType(string(t.Type)))
}

// MathTransform conducts mathematical operations on the input with the given
// configuration in its properties.
type MathTransform struct {
	// Multiply the value.
	// +optional
	Multiply *int64 `json:"multiply,omitempty"`
}

// Resolve runs the Math transform.
func (m *MathTransform) Resolve(input interface{}) (interface{}, error) {
	if m.Multiply == nil {
		return nil, errors.New(errMathNoMultiplier)
	}
	switch i := input.(type) {
	case int64:
		return *m.Multiply * i, nil
	case int:
		return *m.Multiply * int64(i), nil
	default:
		return nil, errors.New(errMathInputNonNumber)
	}
}

// MapTransform returns a value for the input from the given map.
type MapTransform struct {
	// Pairs is the map that will be used for transform.
	// +optional
	Pairs map[string]string `json:",inline"`
}

// Resolve runs the Map transform.
func (m *MapTransform) Resolve(input interface{}) (interface{}, error) {
	switch i := input.(type) {
	case string:
		val, ok := m.Pairs[i]
		if !ok {
			return nil, errors.New(errMapNotFound(i))
		}
		return val, nil
	default:
		return nil, errors.New(errMapTypeNotSupported(reflect.TypeOf(input).String()))
	}
}

// ConnectionDetail includes the information about the propagation of the connection
// information from one secret to another.
type ConnectionDetail struct {
	// Name of the connection secret key that will be propagated to the
	// connection secret of the composition instance. Leave empty if you'd like
	// to use the same key name.
	// +optional
	Name *string `json:"name,omitempty"`

	// FromConnectionSecretKey is the key that will be used to fetch the value
	// from the given target resource.
	FromConnectionSecretKey string `json:"fromConnectionSecretKey"`
}

// CompositionStatus shows the observed state of the composition.
type CompositionStatus struct {
	v1alpha1.ConditionedStatus `json:",inline"`
}

// +kubebuilder:object:root=true

// Composition defines the group of resources to be created when a compatible
// type is created with reference to the composition.
// +kubebuilder:resource:categories={crossplane}
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type Composition struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CompositionSpec   `json:"spec,omitempty"`
	Status CompositionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CompositionList contains a list of Compositions.
type CompositionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Composition `json:"items"`
}
