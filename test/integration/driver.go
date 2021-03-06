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

// Basic template engine using go templates

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"text/template"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	flag "github.com/spf13/pflag"
)

const (
	yaml = "echo.yaml"

	// budget is the maximum number of retries with 1s delays
	budget = 30
)

var (
	kubeconfig string
	hub        string
	tag        string
	namespace  string
	client     *kubernetes.Clientset
)

func cleanup() {
	deleteNamespace(client, namespace)
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	log.Printf("Test failure: %v\n", msg)
	cleanup()
	os.Exit(1)
}

func init() {
	flag.StringVarP(&kubeconfig, "config", "c", "platform/kube/config",
		"kube config file or empty for in-cluster")
	flag.StringVarP(&hub, "hub", "h", "gcr.io/istio-testing",
		"Docker hub")
	flag.StringVarP(&tag, "tag", "t", "test",
		"Docker tag")
	flag.StringVarP(&namespace, "namespace", "n", "",
		"Namespace to use for testing (empty to create/delete temporary one)")
}

func main() {
	flag.Parse()
	build := true

	// write template
	f, err := os.Create(yaml)
	check(err)
	w := bufio.NewWriter(f)

	check(write("test/integration/manager.yaml.tmpl", map[string]string{
		"hub": hub,
		"tag": tag,
	}, w))

	check(write("test/integration/http-service.yaml.tmpl", map[string]string{
		"hub":   hub,
		"tag":   tag,
		"name":  "a",
		"port1": "8080",
		"port2": "80",
	}, w))

	check(write("test/integration/http-service.yaml.tmpl", map[string]string{
		"hub":   hub,
		"tag":   tag,
		"name":  "b",
		"port1": "80",
		"port2": "8000",
	}, w))

	check(w.Flush())
	check(f.Close())

	// push docker images
	if build {
		run("gcloud docker --authorize-only")
		for _, image := range []string{"app", "init", "runtime"} {
			run(fmt.Sprintf("bazel run //docker:%s", image))
			run(fmt.Sprintf("docker tag istio/docker:%s %s/%s:%s", image, hub, image, tag))
			run(fmt.Sprintf("docker push %s/%s:%s", hub, image, tag))
		}
	}

	client = connect()
	if namespace == "" {
		namespace = generateNamespace(client)
	}
	run("kubectl apply -f " + yaml + " -n " + namespace)
	pods := getPods()
	log.Println("pods:", pods)
	ids := makeRequests(pods)
	log.Println("requests:", ids)
	checkAccessLogs(pods, ids)
	log.Println("Success!")
	cleanup()
}

func write(in string, data map[string]string, out io.Writer) error {
	tmpl, err := template.ParseFiles(in)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(out, data); err != nil {
		return err
	}
	return nil
}

func run(command string) {
	log.Println(command)
	parts := strings.Split(command, " ")
	/* #nosec */
	c := exec.Command(parts[0], parts[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	check(c.Run())
}

func shell(command string) string {
	log.Println(command)
	parts := strings.Split(command, " ")
	/* #nosec */
	c := exec.Command(parts[0], parts[1:]...)
	bytes, err := c.CombinedOutput()
	if err != nil {
		log.Println(string(bytes))
		fail(err.Error())
	}
	return string(bytes)
}

func connect() *kubernetes.Clientset {
	var err error
	var config *rest.Config
	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	check(err)
	cl, err := kubernetes.NewForConfig(config)
	check(err)
	return cl
}

// pods returns pod names by app label as soon as all pods are ready
func getPods() map[string]string {
	pods := make([]v1.Pod, 0)
	out := make(map[string]string)
	for n := 0; ; n++ {
		log.Println("Checking all pods are running...")
		list, err := client.Pods(namespace).List(v1.ListOptions{})
		check(err)
		pods = list.Items
		ready := true

		for _, pod := range pods {
			if pod.Status.Phase != "Running" {
				log.Printf("Pod %s has status %s\n", pod.Name, pod.Status.Phase)
				ready = false
				break
			}
		}

		if ready {
			break
		}

		if n > budget {
			fail("Exceeded budget for checking pod status")
		}

		time.Sleep(time.Second)
	}

	for _, pod := range pods {
		if app, exists := pod.Labels["app"]; exists {
			out[app] = pod.Name
		}
	}

	return out
}

// makeRequests executes requests in each pod and collects request ids per pod
func makeRequests(pods map[string]string) map[string][]string {
	out := make(map[string][]string)
	for app := range pods {
		out[app] = make([]string, 0)
	}

	for src := range pods {
		for dst := range pods {
			for _, port := range []string{"", ":80", ":8080"} {
				for _, domain := range []string{"", "." + namespace} {
					for n := 0; ; n++ {
						url := fmt.Sprintf("http://%s%s%s/%s", dst, domain, port, src)
						log.Printf("Making a request %s from %s (attempt %d)...\n", url, src, n)
						request := shell(fmt.Sprintf("kubectl exec %s -n %s -c app client %s",
							pods[src], namespace, url))
						log.Println(request)
						match := regexp.MustCompile("X-Request-Id=(.*)").FindStringSubmatch(request)
						if len(match) > 1 {
							id := match[1]
							log.Printf("id=%s\n", id)
							out[src] = append(out[src], id)
							out[dst] = append(out[dst], id)
							break
						}

						if src == "t" && dst == "t" {
							log.Println("Expected no match")
							break
						}

						if n > budget {
							fail(fmt.Sprintf("Failed to inject proxy from %s to %s (url %s)", src, dst, url))
						}

						time.Sleep(1 * time.Second)
					}
				}
			}
		}
	}

	return out
}

// checkAccessLogs searches for request ids in the access logs
func checkAccessLogs(pods map[string]string, ids map[string][]string) {
	for n := 0; ; n++ {
		found := true
		for _, pod := range []string{"a", "b"} {
			log.Printf("Checking access log of %s\n", pod)
			access := shell(fmt.Sprintf("kubectl logs %s -n %s -c proxy", pods[pod], namespace))
			for _, id := range ids[pod] {
				if !strings.Contains(access, id) {
					log.Printf("Failed to find request id %s in log of %s\n", id, pod)
					found = false
					break
				}
			}
			if !found {
				break
			}
		}

		if found {
			break
		}

		if n > budget {
			fail("Exceeded budget for checking access logs")
		}

		time.Sleep(time.Second)
	}
}

func generateNamespace(cl *kubernetes.Clientset) string {
	ns, err := cl.Core().Namespaces().Create(&v1.Namespace{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "istio-integration-",
		},
	})
	check(err)
	log.Printf("Created namespace %s\n", ns.Name)
	return ns.Name
}

func deleteNamespace(cl *kubernetes.Clientset, ns string) {
	if cl != nil && ns != "" && ns != "default" {
		if err := cl.Core().Namespaces().Delete(ns, &v1.DeleteOptions{}); err != nil {
			log.Printf("Error deleting namespace: %v\n", err)
		}
		log.Printf("Deleted namespace %s\n", ns)
	}
}
