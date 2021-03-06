// Copyright 2017 Google Inc.
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

package kube

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"

	"github.com/golang/glog"

	multierror "github.com/hashicorp/go-multierror"

	"istio.io/manager/model"

	meta_v1 "k8s.io/client-go/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/api/errors"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/runtime/schema"
	"k8s.io/client-go/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// IstioAPIGroup defines Kubernetes API group for TPR
	IstioAPIGroup = "istio.io"
	// IstioResourceVersion defined Kubernetes API group version
	IstioResourceVersion = "v1"
)

// Client provides state-less Kubernetes bindings for the manager:
// - configuration objects are stored as third-party resources
// - dynamic REST client is configured to use third-party resources
// - static client exposes Kubernetes API
type Client struct {
	mapping model.KindMap
	client  *kubernetes.Clientset
	dyn     *rest.RESTClient
}

// CreateRESTConfig for cluster API server, pass empty config file for in-cluster
func CreateRESTConfig(kubeconfig string, km model.KindMap) (config *rest.Config, err error) {
	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	if err != nil {
		return
	}

	version := schema.GroupVersion{
		Group:   IstioAPIGroup,
		Version: IstioResourceVersion,
	}

	config.GroupVersion = &version
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: api.Codecs}

	schemeBuilder := runtime.NewSchemeBuilder(
		func(scheme *runtime.Scheme) error {
			scheme.AddKnownTypes(
				version,
				&v1.ListOptions{},
				&v1.DeleteOptions{},
			)
			for kind := range km {
				scheme.AddKnownTypeWithName(schema.GroupVersionKind{
					Group:   IstioAPIGroup,
					Version: IstioResourceVersion,
					Kind:    kind,
				}, &Config{})
				scheme.AddKnownTypeWithName(schema.GroupVersionKind{
					Group:   IstioAPIGroup,
					Version: IstioResourceVersion,
					Kind:    kind + "List",
				}, &ConfigList{})
			}
			return nil
		})
	err = schemeBuilder.AddToScheme(api.Scheme)

	return
}

// NewClient creates a client to Kubernetes API using a kubeconfig file.
// Use an empty value for `kubeconfig` to use the in-cluster config.
func NewClient(kubeconfig string, km model.KindMap) (*Client, error) {
	config, err := CreateRESTConfig(kubeconfig, km)
	if err != nil {
		return nil, err
	}
	cl, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dyn, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, err
	}

	out := &Client{
		mapping: km,
		client:  cl,
		dyn:     dyn,
	}

	return out, nil
}

// RegisterResources creates third party resources
func (cl *Client) RegisterResources() error {
	var out error
	for kind, v := range cl.mapping {
		apiName := kindToAPIName(kind)
		res, err := cl.client.
			Extensions().
			ThirdPartyResources().
			Get(apiName, meta_v1.GetOptions{})
		if err == nil {
			glog.V(2).Infof("Resource already exists: %q", res.Name)
		} else if errors.IsNotFound(err) {
			glog.V(1).Infof("Creating resource: %q", kind)
			tpr := &v1beta1.ThirdPartyResource{
				ObjectMeta:  v1.ObjectMeta{Name: apiName},
				Versions:    []v1beta1.APIVersion{{Name: IstioResourceVersion}},
				Description: v.Description,
			}
			res, err = cl.client.
				Extensions().
				ThirdPartyResources().
				Create(tpr)
			if err != nil {
				out = multierror.Append(out, err)
			} else {
				glog.V(2).Infof("Created resource: %q", res.Name)
			}
		} else {
			out = multierror.Append(out, err)
		}
	}

	// validate that the resources exist or fail with an error after 30s
	ready := true
	glog.V(2).Infof("Checking for TPR resources")
	for i := 0; i < 30; i++ {
		ready = true
		for kind := range cl.mapping {
			_, err := cl.List(kind, "")
			if err != nil {
				glog.V(2).Infof("TPR %q is not ready. Waiting...", kind)
				ready = false
				break
			}
		}
		if ready {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !ready {
		out = multierror.Append(out, fmt.Errorf("Failed to create all TPRs"))
	}

	return out
}

// DeregisterResources removes third party resources
func (cl *Client) DeregisterResources() error {
	var out error
	for kind := range cl.mapping {
		apiName := kindToAPIName(kind)
		err := cl.client.Extensions().ThirdPartyResources().
			Delete(apiName, &v1.DeleteOptions{})
		if err != nil {
			out = multierror.Append(out, err)
		}
	}
	return out
}

// Get implements registry operation
func (cl *Client) Get(key model.ConfigKey) (*model.Config, bool) {
	if err := cl.mapping.ValidateKey(&key); err != nil {
		glog.Warning(err)
		return nil, false
	}

	config := &Config{}
	err := cl.dyn.Get().
		Namespace(key.Namespace).
		Resource(key.Kind + "s").
		Name(key.Name).
		Do().Into(config)
	if err != nil {
		glog.Warning(err)
		return nil, false
	}
	out, err := kubeToModel(key.Kind, cl.mapping[key.Kind], config)
	if err != nil {
		glog.Warning(err)
		return nil, false
	}
	return out, true
}

// Put implements registry operation
func (cl *Client) Put(obj *model.Config) error {
	out, err := modelToKube(cl.mapping, obj)
	if err != nil {
		return err
	}
	return cl.dyn.Post().
		Namespace(obj.Namespace).
		Resource(obj.Kind + "s").
		Body(out).
		Do().Error()
}

// Delete implements registry operation
func (cl *Client) Delete(key model.ConfigKey) error {
	if err := cl.mapping.ValidateKey(&key); err != nil {
		return err
	}

	return cl.dyn.Delete().
		Namespace(key.Namespace).
		Resource(key.Kind + "s").
		Name(key.Name).
		Do().Error()
}

// List implements registry operation
func (cl *Client) List(kind string, ns string) ([]*model.Config, error) {
	if _, ok := cl.mapping[kind]; !ok {
		return nil, fmt.Errorf("Missing kind %q", kind)
	}

	list := &ConfigList{}
	errs := cl.dyn.Get().
		Namespace(ns).
		Resource(kind + "s").
		Do().Into(list)

	var out []*model.Config
	for _, item := range list.Items {
		elt, err := kubeToModel(kind, cl.mapping[kind], &item)
		if err != nil {
			errs = multierror.Append(errs, err)
		}
		out = append(out, elt)
	}
	return out, errs
}

// camelCaseToKabobCase converts "MyName" to "my-name"
func camelCaseToKabobCase(s string) string {
	var out bytes.Buffer
	for i := range s {
		if 'A' <= s[i] && s[i] <= 'Z' {
			if i > 0 {
				out.WriteByte('-')
			}
			out.WriteByte(s[i] - 'A' + 'a')
		} else {
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

// kindToAPIName converts Kind name to 3rd party API group
func kindToAPIName(s string) string {
	return camelCaseToKabobCase(s) + "." + IstioAPIGroup
}

func kubeToModel(kind string, schema model.ProtoSchema, config *Config) (*model.Config, error) {
	// TODO: validate incoming kube object
	spec, err := mapToProto(schema.MessageName, config.Spec)
	if err != nil {
		return nil, err
	}
	var status proto.Message
	if config.Status != nil {
		status, err = mapToProto(schema.StatusMessageName, config.Status)
		if err != nil {
			return nil, err
		}
	}

	out := model.Config{
		ConfigKey: model.ConfigKey{
			Kind:      kind,
			Name:      config.GetObjectMeta().GetName(),
			Namespace: config.GetObjectMeta().GetNamespace(),
		},
		Spec:   spec,
		Status: status,
	}

	return &out, nil
}

func modelToKube(km model.KindMap, obj *model.Config) (*Config, error) {
	if err := km.ValidateConfig(obj); err != nil {
		return nil, err
	}
	spec, err := protoToMap(obj.Spec.(proto.Message))
	if err != nil {
		return nil, err
	}
	var status map[string]interface{}
	if obj.Status != nil {
		status, err = protoToMap(obj.Status.(proto.Message))
		if err != nil {
			return nil, err
		}
	}

	out := &Config{
		TypeMeta: meta_v1.TypeMeta{
			Kind: obj.Kind,
		},
		Metadata: api.ObjectMeta{
			Name:      obj.ConfigKey.Name,
			Namespace: obj.ConfigKey.Namespace,
		},
		Spec:   spec,
		Status: status,
	}

	return out, nil
}

func protoToMap(msg proto.Message) (map[string]interface{}, error) {
	// Marshal from proto to json bytes
	m := jsonpb.Marshaler{}
	bytes, err := m.MarshalToString(msg)
	if err != nil {
		return nil, err
	}

	// Unmarshal from json bytes to go map
	var data map[string]interface{}
	err = json.Unmarshal([]byte(bytes), &data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func mapToProto(message string, data map[string]interface{}) (proto.Message, error) {
	// Marshal to json bytes
	str, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	// Unmarshal from bytes to proto
	pbt := proto.MessageType(message)
	pb := reflect.New(pbt.Elem()).Interface().(proto.Message)
	err = jsonpb.UnmarshalString(string(str), pb)
	if err != nil {
		return nil, err
	}

	return pb, nil
}
