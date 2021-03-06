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

package envoy

// MeshConfig defines proxy mesh variables
type MeshConfig struct {
	// DiscoveryAddress is the DNS address for Envoy discovery service
	DiscoveryAddress string
	// MixerAddress is the DNS address for Istio Mixer service
	MixerAddress string
	// ProxyPort is the Envoy proxy port
	ProxyPort int
	// AdminPort is the administrative interface port
	AdminPort int
	// Envoy binary path
	BinaryPath string
	// Envoy config root path
	ConfigPath string
}

// Config defines the schema for Envoy JSON configuration format
type Config struct {
	Listeners      []Listener     `json:"listeners"`
	Admin          Admin          `json:"admin"`
	ClusterManager ClusterManager `json:"cluster_manager"`
}

// AbortFilter definition
type AbortFilter struct {
	Percent    int `json:"abort_percent,omitempty"`
	HTTPStatus int `json:"http_status,omitempty"`
}

// DelayFilter definition
type DelayFilter struct {
	Type     string `json:"type,omitempty"`
	Percent  int    `json:"fixed_delay_percent,omitempty"`
	Duration int    `json:"fixed_duration_ms,omitempty"`
}

// Header definition
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FilterEndpointsConfig definition
type FilterEndpointsConfig struct {
	ServiceConfig string `json:"service_config,omitempty"`
	ServerConfig  string `json:"server_config,omitempty"`
}

// FilterFaultConfig definition
type FilterFaultConfig struct {
	Abort   *AbortFilter `json:"abort,omitempty"`
	Delay   *DelayFilter `json:"delay,omitempty"`
	Headers []Header     `json:"headers,omitempty"`
}

// FilterRouterConfig definition
type FilterRouterConfig struct {
	// DynamicStats defaults to true
	DynamicStats bool `json:"dynamic_stats,omitempty"`
}

// Filter definition
type Filter struct {
	Type   string      `json:"type"`
	Name   string      `json:"name"`
	Config interface{} `json:"config"`
}

// Runtime definition
type Runtime struct {
	Key     string `json:"key"`
	Default int    `json:"default"`
}

// Route definition
type Route struct {
	Runtime       *Runtime `json:"runtime,omitempty"`
	Prefix        string   `json:"prefix"`
	PrefixRewrite string   `json:"prefix_rewrite,omitempty"`
	Cluster       string   `json:"cluster"`
	Headers       []Header `json:"headers,omitempty"`
}

// VirtualHost definition
type VirtualHost struct {
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	Routes  []Route  `json:"routes"`
}

// RouteConfig definition
type RouteConfig struct {
	VirtualHosts []VirtualHost `json:"virtual_hosts"`
}

// AccessLog definition.
type AccessLog struct {
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
	Filter string `json:"filter,omitempty"`
}

// NetworkFilterConfig definition
type NetworkFilterConfig struct {
	CodecType         string      `json:"codec_type"`
	StatPrefix        string      `json:"stat_prefix"`
	GenerateRequestID bool        `json:"generate_request_id,omitempty"`
	RouteConfig       RouteConfig `json:"route_config"`
	Filters           []Filter    `json:"filters"`
	AccessLog         []AccessLog `json:"access_log"`
	Cluster           string      `json:"cluster,omitempty"`
}

// NetworkFilter definition
type NetworkFilter struct {
	Type   string              `json:"type"`
	Name   string              `json:"name"`
	Config NetworkFilterConfig `json:"config"`
}

// Listener definition
type Listener struct {
	Port           int             `json:"port"`
	Filters        []NetworkFilter `json:"filters"`
	BindToPort     bool            `json:"bind_to_port"`
	UseOriginalDst bool            `json:"use_original_dst,omitempty"`
}

// Admin definition
type Admin struct {
	AccessLogPath string `json:"access_log_path"`
	Port          int    `json:"port"`
}

// Host definition
type Host struct {
	URL string `json:"url"`
}

// Constant values
const (
	LbTypeRoundRobin = "round_robin"
)

// Cluster definition
type Cluster struct {
	Name                     string `json:"name"`
	ServiceName              string `json:"service_name,omitempty"`
	ConnectTimeoutMs         int    `json:"connect_timeout_ms"`
	Type                     string `json:"type"`
	LbType                   string `json:"lb_type"`
	MaxRequestsPerConnection int    `json:"max_requests_per_connection,omitempty"`
	Hosts                    []Host `json:"hosts,omitempty"`
	Features                 string `json:"features,omitempty"`
}

// ListenersByPort sorts listeners by port
type ListenersByPort []Listener

func (l ListenersByPort) Len() int {
	return len(l)
}

func (l ListenersByPort) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

func (l ListenersByPort) Less(i, j int) bool {
	return l[i].Port < l[j].Port
}

// ClustersByName sorts clusters by name
type ClustersByName []Cluster

func (s ClustersByName) Len() int {
	return len(s)
}

func (s ClustersByName) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s ClustersByName) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

// HostsByName sorts clusters by name
type HostsByName []VirtualHost

func (s HostsByName) Len() int {
	return len(s)
}

func (s HostsByName) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s HostsByName) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

// RoutesByCluster sorts routes by name
type RoutesByCluster []Route

func (r RoutesByCluster) Len() int {
	return len(r)
}

func (r RoutesByCluster) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

func (r RoutesByCluster) Less(i, j int) bool {
	return r[i].Cluster < r[j].Cluster
}

// SDS is a service discovery service definition
type SDS struct {
	Cluster        Cluster `json:"cluster"`
	RefreshDelayMs int     `json:"refresh_delay_ms"`
}

// ClusterManager definition
type ClusterManager struct {
	Clusters []Cluster `json:"clusters"`
	SDS      SDS       `json:"sds"`
}

// ByName implements sort
type ByName []Cluster

// Len length
func (a ByName) Len() int {
	return len(a)
}

// Swap elements
func (a ByName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// Less compare
func (a ByName) Less(i, j int) bool {
	return a[i].Name < a[j].Name
}

//ByHost implement sort
type ByHost []Host

// Len length
func (a ByHost) Len() int {
	return len(a)
}

// Swap elements
func (a ByHost) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// Less compare
func (a ByHost) Less(i, j int) bool {
	return a[i].URL < a[j].URL
}
