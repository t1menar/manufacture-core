// Package config loads the settings of the core process from the environment
// (twelve-factor). Pipelines and modules are not configuration: they are resources
// applied through the API.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Role is a part of the core that can run in its own process.
type Role string

// Roles.
const (
	RoleEngine     Role = "engine"
	RoleController Role = "controller"
	RoleGateway    Role = "gateway"
)

// AllRoles lists the roles in start order.
var AllRoles = []Role{RoleEngine, RoleController, RoleGateway}

// Driver is the runtime that starts module containers.
type Driver string

// Runtime drivers.
const (
	DriverDocker     Driver = "docker"
	DriverKubernetes Driver = "kubernetes"
	DriverExternal   Driver = "external"
)

var drivers = []Driver{DriverDocker, DriverKubernetes, DriverExternal}

// Secret hides its value from logs, fmt and JSON.
type Secret string

const redacted = "[redacted]"

// String never reveals the value.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// GoString keeps %#v from printing the value.
func (s Secret) GoString() string { return s.String() }

// LogValue keeps slog from printing the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON never reveals the value.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// Value returns the secret itself; call it only where the secret is used.
func (s Secret) Value() string { return string(s) }

// Config is the process configuration.
type Config struct {
	ListenAddr  string     `json:"listen_addr"`
	DatabaseURL Secret     `json:"database_url"` // carries a password
	AMQPURL     Secret     `json:"amqp_url"`     // carries a password
	S3          S3         `json:"s3"`
	Runtime     Driver     `json:"runtime"`
	Roles       []Role     `json:"roles"`
	AdminToken  Secret     `json:"admin_token"`
	LogLevel    slog.Level `json:"log_level"`
	LogFormat   string     `json:"log_format"`
}

// S3 is the object storage for large payloads.
type S3 struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey Secret `json:"secret_key"`
	TLS       bool   `json:"tls"`
}

// Environment variable names.
const (
	EnvListenAddr  = "MF_LISTEN_ADDR"
	EnvDatabaseURL = "MF_DATABASE_URL"
	EnvAMQPURL     = "MF_AMQP_URL"
	EnvS3Endpoint  = "MF_S3_ENDPOINT"
	EnvS3Bucket    = "MF_S3_BUCKET"
	EnvS3AccessKey = "MF_S3_ACCESS_KEY"
	EnvS3SecretKey = "MF_S3_SECRET_KEY" //nolint:gosec // name of a variable, not a credential.
	EnvS3TLS       = "MF_S3_TLS"
	EnvRuntime     = "MF_RUNTIME"
	EnvRoles       = "MF_ROLES"
	EnvAdminToken  = "MF_ADMIN_TOKEN" //nolint:gosec // name of a variable, not a credential.
	EnvLogLevel    = "MF_LOG_LEVEL"
	EnvLogFormat   = "MF_LOG_FORMAT"
)

// Defaults. The listen address is loopback so that a local start never exposes the API.
const (
	DefaultListenAddr = "127.0.0.1:8080"
	DefaultS3Bucket   = "mf-blobs"
	DefaultRuntime    = DriverDocker
	DefaultLogFormat  = "json"
)

// MinAdminTokenLength guards against trivially guessable tokens.
const MinAdminTokenLength = 32

// Load reads the configuration through getenv (os.Getenv in production, a map in
// tests) and reports every problem at once.
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}
	c := Config{
		ListenAddr:  l.str(EnvListenAddr, DefaultListenAddr),
		DatabaseURL: Secret(l.url(EnvDatabaseURL, "postgres", "postgresql")),
		AMQPURL:     Secret(l.url(EnvAMQPURL, "amqp", "amqps")),
		S3: S3{
			Endpoint:  l.required(EnvS3Endpoint),
			Bucket:    l.str(EnvS3Bucket, DefaultS3Bucket),
			AccessKey: l.required(EnvS3AccessKey),
			SecretKey: Secret(l.required(EnvS3SecretKey)),
			TLS:       l.boolean(EnvS3TLS, false),
		},
		Runtime:    oneOf(&l, EnvRuntime, DefaultRuntime, drivers),
		Roles:      l.roles(EnvRoles),
		AdminToken: Secret(l.str(EnvAdminToken, "")),
		LogLevel:   l.level(EnvLogLevel),
		LogFormat:  oneOf(&l, EnvLogFormat, DefaultLogFormat, []string{"json", "text"}),
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		l.fail(EnvListenAddr, "must be host:port: %v", err)
	}
	if t := c.AdminToken.Value(); t != "" && len(t) < MinAdminTokenLength {
		l.fail(EnvAdminToken, "must be at least %d characters", MinAdminTokenLength)
	}
	return c, errors.Join(l.errs...)
}

// HasRole reports whether the process runs the role.
func (c Config) HasRole(r Role) bool { return slices.Contains(c.Roles, r) }

type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) fail(name, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s %s", name, fmt.Sprintf(format, args...)))
}

func (l *loader) str(name, def string) string {
	if v := strings.TrimSpace(l.getenv(name)); v != "" {
		return v
	}
	return def
}

func (l *loader) required(name string) string {
	v := l.str(name, "")
	if v == "" {
		l.fail(name, "is required")
	}
	return v
}

func (l *loader) url(name string, schemes ...string) string {
	v := l.required(name)
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil {
		// url.Parse echoes the input, which may contain a password.
		l.fail(name, "is not a valid URL")
		return v
	}
	if !slices.Contains(schemes, u.Scheme) {
		l.fail(name, "must use scheme %s", strings.Join(schemes, " or "))
	}
	return v
}

func (l *loader) boolean(name string, def bool) bool {
	v := l.str(name, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(name, "must be true or false")
	}
	return b
}

// oneOf is a function because methods cannot have type parameters.
func oneOf[T ~string](l *loader, name string, def T, allowed []T) T {
	v := T(l.str(name, string(def)))
	if !slices.Contains(allowed, v) {
		names := make([]string, len(allowed))
		for i, a := range allowed {
			names[i] = string(a)
		}
		l.fail(name, "must be one of %s, got %q", strings.Join(names, ", "), v)
	}
	return v
}

func (l *loader) roles(name string) []Role {
	v := l.str(name, "all")
	if v == "all" {
		return slices.Clone(AllRoles)
	}
	var roles []Role
	for part := range strings.SplitSeq(v, ",") {
		r := Role(strings.TrimSpace(part))
		switch {
		case !slices.Contains(AllRoles, r):
			l.fail(name, "has unknown role %q (known: all, engine, controller, gateway)", r)
		case !slices.Contains(roles, r):
			roles = append(roles, r)
		}
	}
	return roles
}

func (l *loader) level(name string) slog.Level {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(l.str(name, "info"))); err != nil {
		l.fail(name, "must be debug, info, warn or error")
	}
	return lvl
}
