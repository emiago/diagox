// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/caarlos0/env/v9"
	"github.com/emiago/sipgo/sip"
	"gopkg.in/yaml.v3"
)

// Struct for each Endpoint
type ConfigEndpoint struct {
	Name  string      `yaml:"name"`
	Match ConfigMatch `yaml:"match"`
	Auth  ConfigAuth  `yaml:"auth,omitempty"`
	URI   string      `yaml:"uri"`
	Route string      `yaml:"route"`

	// AOR         ConfigAOR   `yaml:"aor,omitempty"`
	// MatchNext   string `yaml:"match_next,omitempty"`
	TransportID      string              `yaml:"transport,omitempty"`
	Media            ConfigEndpointMedia `yaml:"media"`
	ContactHDR       string              `yaml:"contact_header"`
	contactHDRParsed *sip.ContactHeader
	useRegistry      bool
}

type ConfigEndpointMedia struct {
	Type string `yaml:"type"`
}

// Struct for Transports
type ConfigTransport struct {
	Transport       string `yaml:"transport"`
	Bind            string `yaml:"bind"`
	Port            int    `yaml:"port"`
	ExternalHost    string `yaml:"external_host"`
	ExternalPort    int    `yaml:"external_port"`
	ExternalMediaIP string `yaml:"external_media_ip"`
	RewriteContact  bool   `yaml:"rewrite_contact"`
}

// Struct for Auth details
type ConfigAuth struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

const (
	ConfigAuthTypeDigest = "digest"
	ConfigAuthTypeBearer = "bearer"
)

func (a ConfigAuth) AuthType() string {
	authType := strings.ToLower(strings.TrimSpace(a.Type))
	if authType == "" && (a.Username != "" || a.Password != "") {
		return ConfigAuthTypeDigest
	}
	return authType
}

// Struct for AOR (Address of Record)
type ConfigAOR struct {
	URI      string     `yaml:"uri"`
	Protocol string     `yaml:"protocol"`
	Auth     ConfigAuth `yaml:"auth"`
}

// Struct for Match section in each endpoint
type ConfigMatch struct {
	Type      string   `yaml:"type"`
	Values    []string `yaml:"values,omitempty"`
	Transport string   `yaml:"transport,omitempty"`
}

type ConfigRoute struct {
	ID             string              `yaml:"id"`
	EndpointName   string              `yaml:"endpoint"`
	Fallback       ConfigRouteFallback `yaml:"fallback"`
	Match          string              `yaml:"match"`
	StripPrefix    string              `yaml:"strip_prefix"`
	UseRegistry    bool                `yaml:"use_registry"`
	Recording      bool                `yaml:"recording"`
	Hangup         ConfigRouteHangup   `yaml:"hangup"`
	SipHeaders     map[string]string   `yaml:"sip_headers"`
	SipHeadersPass []string            `yaml:"sip_headers_pass"`
}

type ConfigRouteFallback struct {
	Enabled          bool     `yaml:"enabled"`
	Endpoints        []string `yaml:"endpoints"`
	FallbacksCodes   []int    `yaml:"codes"`
	FallbacksTimeout bool     `yaml:"timeout"`
}

type ConfigRouteHangup struct {
	Code   int    `yaml:"code"`
	Reason string `yaml:"reason"`
}

type ConfigRecordings struct {
	Path string `yaml:"path,omitempty"`
}

// Struct for the full YAML file
type Config struct {
	Version    string                     `yaml:"version"`
	Transports map[string]ConfigTransport `yaml:"transports"`
	Endpoints  map[string]ConfigEndpoint  `yaml:"endpoints"`
	Routes     map[string][]ConfigRoute   `yaml:"routes"`

	endpointOrder []string
	endpointIndex endpointIndex
}

func ConfigLoadYamlFile(filename string, c *Config) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	return ConfigLoad(file, c)
}

func ConfigLoad(r io.Reader, c *Config) error {
	var root yaml.Node
	if err := yaml.NewDecoder(r).Decode(&root); err != nil {
		return err
	}

	if err := root.Decode(c); err != nil {
		return err
	}
	c.endpointOrder = yamlMappingKeys(&root, "endpoints")

	if c.Routes == nil {
		c.Routes = map[string][]ConfigRoute{}
	}
	if c.Endpoints == nil {
		c.Endpoints = map[string]ConfigEndpoint{}
	}

	// Default route will match any and have registry
	if c.Routes["default"] == nil {
		c.Routes["default"] = []ConfigRoute{
			{
				ID:          "",
				Match:       "any",
				UseRegistry: true,
			},
		}
	}

	// Avoid nil pointers
	for k, v := range c.Routes {
		for i, r := range v {
			if r.Fallback.Endpoints == nil {
				v[i].Fallback.Endpoints = []string{}
			}
		}
		c.Routes[k] = v
	}

	// Fill endpoint name
	// Parse contact header
	parser := sip.DefaultHeadersParser()["contact"]
	for name, e := range c.Endpoints {
		if e.Name == "" {
			// If user did not provide endpoint name than use as id of entry
			e.Name = name
		}
		if e.Route == "" {
			e.Route = "default"
		}

		if e.ContactHDR != "" {
			h, err := parser([]byte("Contact"), e.ContactHDR)
			if err != nil {
				slog.Error("Failed to parse endpoint contact value", "error", err)
				return fmt.Errorf("failed to parse contact header on endpoint=%s: %w", name, err)
			}
			e.contactHDRParsed = h.(*sip.ContactHeader)
		}

		c.Endpoints[name] = e
	}

	idx, err := buildEndpointIndex(c.Endpoints, c.endpointOrder)
	if err != nil {
		return err
	}
	c.endpointIndex = idx

	return nil
}

func (c *Config) Validate() error {
	dynamicUserEndpoints := 0
	for endName, e := range c.Endpoints {
		switch authType := e.Auth.AuthType(); authType {
		case "", ConfigAuthTypeDigest, ConfigAuthTypeBearer:
		default:
			return fmt.Errorf("endpoint %q: auth type %q is not supported", endName, e.Auth.Type)
		}

		if e.Match.Type == "user_dynamic" {
			dynamicUserEndpoints++
		}
	}
	if dynamicUserEndpoints > 1 {
		return fmt.Errorf("only one endpoint with match type %q is supported", "user_dynamic")
	}
	if _, err := buildEndpointIndex(c.Endpoints, c.endpointOrder); err != nil {
		return err
	}

	// Check do dids have mapping correct
	for routeName, route := range c.Routes {
		for _, r := range route {
			if r.UseRegistry {
				if r.EndpointName != "" {
					if _, exists := c.Endpoints[r.EndpointName]; !exists {
						return fmt.Errorf("route context %q: Endpoint %q does not exists for DID %q", routeName, r.EndpointName, r.ID)
					}
				}
				continue
			}

			if r.Hangup.Code > 0 {
				if r.EndpointName != "" {
					return fmt.Errorf("route context %q: Endpoint name %q with registry can not be used together with hangup", routeName, r.EndpointName)
				}
				continue
			}

			// Does endpoint exists
			endpoint := r.EndpointName
			_, exists := c.Endpoints[endpoint]
			if !exists {
				return fmt.Errorf("route context %q: Endpoint %q does not exists for DID %q", routeName, endpoint, r.ID)
			}
		}
	}

	// Check does endpoint route exists
	for endName, e := range c.Endpoints {
		_, exists := c.Routes[e.Route]
		if !exists {
			return fmt.Errorf("Route %q does not exists for endpoint %q", e.Route, endName)
		}

	}
	return nil
}

func yamlMappingKeys(root *yaml.Node, key string) []string {
	if root == nil {
		return nil
	}
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		value := node.Content[i+1]
		if value.Kind != yaml.MappingNode {
			return nil
		}
		keys := make([]string, 0, len(value.Content)/2)
		for j := 0; j+1 < len(value.Content); j += 2 {
			keys = append(keys, value.Content[j].Value)
		}
		return keys
	}
	return nil
}

type RedisConfig struct {
	Addr              string `env:"REDIS_ADDR" envDefault:""` // Addr holds the address to connect with Redis. If multiple addresses are provided seperated by comma, then redis cluster is being used
	Username          string `env:"REDIS_USERNAME" envDefault:""`
	Password          string `env:"REDIS_PASSWORD" envDefault:""`
	CacheEnabled      bool   `env:"REDIS_CACHE" envDefault:"true"`
	SentinelMasterSet string `env:"REDIS_SENTINEL_MASTER_SET" envDefault:""`
	CacheConnSizeMB   int    `env:"REDIS_CACHE_CONN_SIZE_MB" envDefault:"32"`
}

type PostgresConfig struct {
	User     string `env:"POSTGRES_USER" envDefault:"diagox"`
	Password string `env:"POSTGRES_PASSWORD"`
	Database string `env:"POSTGRES_DATABASE" envDefault:"diagox"`
	Host     string `env:"POSTGRES_HOST" envDefault:""`
}

type MysqlConfig struct {
	User     string `env:"MYSQL_USER" envDefault:"diagox"`
	Password string `env:"MYSQL_PASSWORD"`
	Database string `env:"MYSQL_DATABASE" envDefault:"diagox"`
	Host     string `env:"MYSQL_HOST" envDefault:""`
}

type RateLimiterIncomingConfig struct {
	Enabled   bool  `env:"RATE_LIMITER_IN_ENABLED" envDefault:"false"`
	DialogRPS int64 `env:"RATE_LIMITER_IN_DIALOG_RPS"`
	DialogMax int64 `env:"RATE_LIMITER_IN_DIALOG_MAX"`
}

type RateLimiterOutgoingConfig struct {
	Enabled   bool  `env:"RATE_LIMITER_OUT_ENABLED" envDefault:"false"`
	DialogRPS int64 `env:"RATE_LIMITER_OUT_DIALOG_RPS"`
}

type SIPRegisterBearerAuthConfig struct {
	URL           string        `env:"SIP_REGISTER_BEARER_AUTH_URL" envDefault:""`
	Timeout       time.Duration `env:"SIP_REGISTER_BEARER_AUTH_TIMEOUT"`
	IdentityField string        `env:"SIP_REGISTER_BEARER_AUTH_IDENTITY_FIELD"`
	ActiveField   string        `env:"SIP_REGISTER_BEARER_AUTH_ACTIVE_FIELD"`
	Header        string        `env:"SIP_REGISTER_BEARER_AUTH_HEADER" envDefault:""`
}

type EnvConfig struct {
	// Internal
	Cluster     bool `env:"GOPBX_CLUSTER"`
	TestMode    bool `env:"TEST_MODE"`
	SIPDebug    bool `env:"SIP_DEBUG" envDefault:"false"`
	SIPCDRTrace bool `env:"SIP_CDR_TRACE" envDefault:"true"`
	RTPDebug    bool `env:"RTP_DEBUG" envDefault:"false"`
	RTCPDebug   bool `env:"RTCP_DEBUG" envDefault:"false"`

	OutboundDialUri  string `env:"OUTBOUND_DIAL_URI" envDefault:""`
	ConfFile         string `env:"CONF_FILE" envDefault:"diagox.yaml"`
	CDREnable        bool   `env:"CDR_ENABLE" envDefault:"true"`
	RecordingsPath   string `env:"RECORDINGS_PATH" envDefault:"recordings"`
	FrontendEnable   bool   `env:"FRONTEND_ENABLE" envDefault:"false"`
	FrontendDevMode  bool   `env:"FRONTEND_DEV_MODE" envDefault:"false"`
	HTTPAddr         string `env:"HTTP_ADDR" envDefault:":6060"`
	HTTPDebug        bool   `env:"HTTP_DEBUG" envDefault:"false"`
	MediaCodecs      string `env:"MEDIA_CODECS" envDefault:""`
	WebrtcICEServers string `env:"WEBRTC_ICE_SERVERS" envDefault:""`
	WebrtcSTUN       WebrtcSTUNConfig
	Redis            RedisConfig
	Mysql            MysqlConfig
	Postgres         PostgresConfig
	// SIP global transport overrides
	// if you have dedicated interface like POD ip, set SIP_BIND_IP
	SIPBindIP          string `env:"SIP_BIND_IP" envDefault:""`
	SIPExternalHost    string `env:"SIP_EXTERNAL_HOST" envDefault:""`
	SIPExternalMediaIP string `env:"SIP_EXTERNAL_MEDIA_IP" envDefault:""`
	// SIP_HOSTNAME identifies that message are matching this hostname like REGISTER
	SIPHostname           string `env:"SIP_HOSTNAME" envDefault:""`
	RateLimiterIn         RateLimiterIncomingConfig
	RateLimiterOut        RateLimiterOutgoingConfig
	SIPRegisterBearerAuth SIPRegisterBearerAuthConfig

	// TLS
	ServerTLSKeyPath string `env:"SERVER_TLS_KEY_PATH" envDefault:""`
	ServerTLSCrtPath string `env:"SERVER_TLS_CRT_PATH" envDefault:""`
	ServerTLSKey     string `env:"SERVER_TLS_KEY" envDefault:""` // Base64 encoded key
	ServerTLSCrt     string `env:"SERVER_TLS_CRT" envDefault:""` // Base64 encoded CRT

	// Flow RPC
	FlowRPCAddr string `env:"FLOW_RPC_HTTP_ADDR" envDefault:":6000"`
}

func EnvConfigLoad(c *EnvConfig) error {
	err := env.Parse(c)
	if err != nil {
		return err
	}
	if c.SIPRegisterBearerAuth.Timeout == 0 {
		c.SIPRegisterBearerAuth.Timeout = 2 * time.Second
	}
	if _, exists := os.LookupEnv("SIP_REGISTER_BEARER_AUTH_IDENTITY_FIELD"); !exists && c.SIPRegisterBearerAuth.IdentityField == "" {
		c.SIPRegisterBearerAuth.IdentityField = ".sub"
	}
	if _, exists := os.LookupEnv("SIP_REGISTER_BEARER_AUTH_ACTIVE_FIELD"); !exists && c.SIPRegisterBearerAuth.ActiveField == "" {
		c.SIPRegisterBearerAuth.ActiveField = ".active"
	}

	return err
}

func (e *EnvConfig) recordingPath(recordingID string) string {
	return path.Join(e.RecordingsPath, recordingID+".wav")
}
