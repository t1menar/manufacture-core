package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func minimal() map[string]string {
	return map[string]string{
		EnvDatabaseURL: "postgres://mf:pg-secret@localhost:5432/manufacture",
		EnvAMQPURL:     "amqp://mf:mq-secret@localhost:5672/",
		EnvS3Endpoint:  "localhost:9000",
		EnvS3AccessKey: "manufacture",
		EnvS3SecretKey: "s3-secret",
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(minimal()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != DefaultListenAddr || c.Runtime != DriverDocker || c.S3.Bucket != DefaultS3Bucket ||
		c.LogLevel != slog.LevelInfo || c.LogFormat != "json" || c.S3.TLS {
		t.Errorf("defaults: %+v", c)
	}
	if !slices.Equal(c.Roles, AllRoles) || !c.HasRole(RoleGateway) {
		t.Errorf("roles = %v", c.Roles)
	}
}

func TestLoadOverrides(t *testing.T) {
	m := minimal()
	m[EnvListenAddr] = ":9090"
	m[EnvRuntime] = "kubernetes"
	m[EnvRoles] = "engine, controller,engine"
	m[EnvLogLevel] = "debug"
	m[EnvLogFormat] = "text"
	m[EnvS3TLS] = "true"
	m[EnvAdminToken] = strings.Repeat("t", MinAdminTokenLength)
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9090" || c.Runtime != DriverKubernetes || c.LogLevel != slog.LevelDebug || !c.S3.TLS {
		t.Errorf("overrides: %+v", c)
	}
	if !slices.Equal(c.Roles, []Role{RoleEngine, RoleController}) || c.HasRole(RoleGateway) {
		t.Errorf("roles = %v", c.Roles)
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := Load(env(map[string]string{
		EnvAMQPURL:    "http://broker",
		EnvListenAddr: "8080",
		EnvRuntime:    "podman",
		EnvRoles:      "engine,ui",
		EnvLogLevel:   "loud",
		EnvLogFormat:  "xml",
		EnvS3TLS:      "maybe",
		EnvAdminToken: "short",
	}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		EnvDatabaseURL + " is required", EnvAMQPURL + " must use scheme amqp or amqps",
		EnvS3Endpoint + " is required", EnvS3AccessKey + " is required", EnvS3SecretKey + " is required",
		EnvListenAddr + " must be host:port", EnvRuntime + " must be one of docker, kubernetes, external",
		`unknown role "ui"`, EnvLogLevel + " must be", EnvLogFormat + " must be one of json, text",
		EnvS3TLS + " must be true or false", EnvAdminToken + " must be at least",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestSecretsNeverLeak(t *testing.T) {
	m := minimal()
	m[EnvAdminToken] = strings.Repeat("a", MinAdminTokenLength)
	m[EnvDatabaseURL] = "postgres://mf:pg-secret@host/db\x7f" // invalid URL: the error must not echo it
	c, err := Load(env(m))
	if err == nil || strings.Contains(err.Error(), "pg-secret") {
		t.Errorf("error leaks or is missing: %v", err)
	}
	b, jerr := json.Marshal(c)
	if jerr != nil {
		t.Fatal(jerr)
	}
	printed := string(b) + fmt.Sprintf("%v %+v %#v %s", c, c, c, c.AdminToken)
	for _, secret := range []string{"pg-secret", "mq-secret", "s3-secret", strings.Repeat("a", MinAdminTokenLength)} {
		if strings.Contains(printed, secret) {
			t.Errorf("secret %q leaked: %s", secret, printed)
		}
	}
	if c.AdminToken.Value() != strings.Repeat("a", MinAdminTokenLength) {
		t.Error("Value must return the secret")
	}
	if Secret("").String() != "" {
		t.Error("an empty secret prints as empty, so that 'not set' stays visible")
	}
}
