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
	"fmt"
	"os"
	"os/user"
	"testing"
	"time"

	"github.com/golang/glog"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"

	"istio.io/manager/model"
	"istio.io/manager/test/mock"
)

var (
	camelKabobs = []struct{ in, out string }{
		{"ExampleNameX", "example-name-x"},
		{"example1", "example1"},
		{"exampleXY", "example-x-y"},
	}

	protocols = []struct {
		name  string
		proto v1.Protocol
		out   model.Protocol
	}{
		{"", v1.ProtocolTCP, model.ProtocolTCP},
		{"http", v1.ProtocolTCP, model.ProtocolHTTP},
		{"http-test", v1.ProtocolTCP, model.ProtocolHTTP},
		{"http", v1.ProtocolUDP, model.ProtocolUDP},
		{"httptest", v1.ProtocolTCP, model.ProtocolTCP},
		{"https", v1.ProtocolTCP, model.ProtocolHTTPS},
		{"https-test", v1.ProtocolTCP, model.ProtocolHTTPS},
		{"http2", v1.ProtocolTCP, model.ProtocolHTTP2},
		{"http2-test", v1.ProtocolTCP, model.ProtocolHTTP2},
		{"grpc", v1.ProtocolTCP, model.ProtocolGRPC},
		{"grpc-test", v1.ProtocolTCP, model.ProtocolGRPC},
	}

	testService = "test"
)

func TestCamelKabob(t *testing.T) {
	for _, tt := range camelKabobs {
		s := camelCaseToKabobCase(tt.in)
		if s != tt.out {
			t.Errorf("camelCaseToKabobCase(%q) => %q, want %q", tt.in, s, tt.out)
		}
	}
}

func TestConvertProtocol(t *testing.T) {
	for _, tt := range protocols {
		out := convertProtocol(tt.name, tt.proto)
		if out != tt.out {
			t.Errorf("convertProtocol(%q, %q) => %q, want %q", tt.name, tt.proto, out, tt.out)
		}
	}
}

func TestThirdPartyResourcesClient(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	cl := makeClient(t)
	ns := makeNamespace(cl.client, t)
	defer deleteNamespace(cl.client, ns)

	mock.CheckMapInvariant(cl, t, ns, 5)

	// TODO(kuat) initial watch always fails, takes time to register TPR, keep
	// around as a work-around
	// kr.DeregisterResources()
}

func TestController(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	cl := makeClient(t)
	ns := makeNamespace(cl.client, t)
	defer deleteNamespace(cl.client, ns)

	stop := make(chan struct{})
	defer close(stop)

	ctl := NewController(cl, ns, 256*time.Millisecond)
	added, deleted := 0, 0
	n := 5
	err := ctl.AppendConfigHandler(mock.Kind, func(c *model.Config, ev model.Event) {
		switch ev {
		case model.EventAdd:
			if deleted != 0 {
				t.Errorf("Events are not serialized (add)")
			}
			added++
		case model.EventDelete:
			if added != n {
				t.Errorf("Events are not serialized (delete)")
			}
			deleted++
		}
		glog.Infof("Added %d, deleted %d", added, deleted)
	})
	if err != nil {
		t.Error(err)
	}
	go ctl.Run(stop)

	mock.CheckMapInvariant(cl, t, ns, n)
	eventually(func() bool { return added == n && deleted == n }, t)
}

func TestControllerCacheFreshness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	cl := makeClient(t)
	ns := makeNamespace(cl.client, t)
	defer deleteNamespace(cl.client, ns)
	stop := make(chan struct{})
	ctl := NewController(cl, ns, 256*time.Millisecond)

	// test interface implementation
	var _ model.Controller = ctl

	// validate cache consistency
	err := ctl.AppendConfigHandler(mock.Kind, func(c *model.Config, ev model.Event) {
		elts, _ := ctl.List(mock.Kind, ns)
		switch ev {
		case model.EventAdd:
			if len(elts) != 1 {
				t.Errorf("Got %#v, expected %d element(s) on ADD event", elts, 1)
			}
			err := ctl.Delete(c.ConfigKey)
			if err != nil {
				t.Error(err)
			}
		case model.EventDelete:
			if len(elts) != 0 {
				t.Errorf("Got %#v, expected zero elements on DELETE event", elts)
			}
			close(stop)
		}
	})
	if err != nil {
		t.Error(err)
	}

	go ctl.Run(stop)
	o := mock.Make(0, ns)

	// put followed by delete
	if err := ctl.Put(o); err != nil {
		t.Error(err)
	}
}

func TestControllerClientSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	cl := makeClient(t)
	ns := makeNamespace(cl.client, t)
	n := 5
	defer deleteNamespace(cl.client, ns)
	stop := make(chan struct{})
	defer close(stop)

	// add elements directly through client
	for i := 0; i < n; i++ {
		if err := cl.Put(mock.Make(i, ns)); err != nil {
			t.Error(err)
		}
	}

	// check in the controller cache
	ctl := NewController(cl, ns, 256*time.Millisecond)
	go ctl.Run(stop)
	eventually(func() bool { return ctl.HasSynced() }, t)
	os, _ := ctl.List(mock.Kind, ns)
	if len(os) != n {
		t.Errorf("ctl.List => Got %d, expected %d", len(os), n)
	}

	// remove elements directly through client
	for i := 0; i < n; i++ {
		if err := cl.Delete(mock.Make(i, ns).ConfigKey); err != nil {
			t.Error(err)
		}
	}

	// check again in the controller cache
	eventually(func() bool {
		os, _ = ctl.List(mock.Kind, ns)
		glog.Infof("ctl.List => Got %d, expected %d", len(os), 0)
		return len(os) == 0
	}, t)

	// now add through the controller
	for i := 0; i < n; i++ {
		if err := ctl.Put(mock.Make(i, ns)); err != nil {
			t.Error(err)
		}
	}

	// check directly through the client
	eventually(func() bool {
		cs, _ := ctl.List(mock.Kind, ns)
		os, _ := cl.List(mock.Kind, ns)
		glog.Infof("ctl.List => Got %d, expected %d", len(cs), n)
		glog.Infof("cl.List => Got %d, expected %d", len(os), n)
		return len(os) == n && len(cs) == n
	}, t)

	// remove elements directly through the client
	for i := 0; i < n; i++ {
		if err := cl.Delete(mock.Make(i, ns).ConfigKey); err != nil {
			t.Error(err)
		}
	}
}

func TestServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	cl := makeClient(t)
	ns := makeNamespace(cl.client, t)
	defer deleteNamespace(cl.client, ns)

	stop := make(chan struct{})
	defer close(stop)

	ctl := NewController(cl, ns, 256*time.Millisecond)
	go ctl.Run(stop)

	var sds model.ServiceDiscovery = ctl
	createService(testService, ns, cl.client, t)
	eventually(func() bool {
		out := sds.Services()
		glog.Info("Services: %#v", out)
		return len(out) == 1 &&
			out[0].Hostname == fmt.Sprintf("%s.%s.%s", testService, ns, ServiceSuffix) &&
			out[0].Tags == nil &&
			len(out[0].Ports) == 1 &&
			out[0].Ports[0].Protocol == model.ProtocolHTTP
	}, t)
}

func eventually(f func() bool, t *testing.T) {
	interval := 64 * time.Millisecond
	for i := 0; i < 10; i++ {
		if f() {
			return
		}
		glog.Infof("Sleeping %v", interval)
		time.Sleep(interval)
		interval = 2 * interval
	}
	t.Errorf("Failed to satisfy function")
}

func makeClient(t *testing.T) *Client {
	usr, err := user.Current()
	if err != nil {
		t.Fatalf(err.Error())
	}

	kubeconfig := usr.HomeDir + "/.kube/config"
	// For Bazel sandbox we search a different location:
	if _, err = os.Stat(kubeconfig); err != nil {
		kubeconfig, _ = os.Getwd()
		kubeconfig = kubeconfig + "/config"
		info, exists := os.Stat(kubeconfig)
		if exists != nil {
			t.Fatalf("Cannot find .kube/config file")
		}

		// if it's an empty file, switch to in-cluster config
		if info.Size() == 0 {
			glog.Info("Using in-cluster configuration")
			kubeconfig = ""
		}
	}

	cl, err := NewClient(kubeconfig, mock.Mapping)
	if err != nil {
		t.Fatalf(err.Error())
	}

	err = cl.RegisterResources()
	if err != nil {
		t.Fatalf(err.Error())
	}

	return cl
}

func makeNamespace(cl *kubernetes.Clientset, t *testing.T) string {
	ns, err := cl.Core().Namespaces().Create(&v1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "istio-test-",
		},
	})
	if err != nil {
		t.Fatalf(err.Error())
	}
	glog.Infof("Created namespace %s", ns.Name)
	return ns.Name
}

func createService(n, ns string, cl *kubernetes.Clientset, t *testing.T) {
	_, err := cl.Core().Services(ns).Create(&v1.Service{
		ObjectMeta: v1.ObjectMeta{Name: n},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: 80,
					Name: "http-example",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf(err.Error())
	}
	glog.Infof("Created service %s", n)
}

func deleteNamespace(cl *kubernetes.Clientset, ns string) {
	if ns != "" && ns != "default" {
		if err := cl.Core().Namespaces().Delete(ns, &v1.DeleteOptions{}); err != nil {
			glog.Warningf("Error deleting namespace: %v", err)
		}
		glog.Infof("Deleted namespace %s", ns)
	}
}
